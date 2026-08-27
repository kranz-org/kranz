package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	kranzlog "github.com/kranz-org/kranz/internal/log"
)

// LogQuery is the delivery-neutral address and window for bounded runtime
// logs. Since is an absolute cutoff; adapters may parse relative durations
// before calling the application layer.
type LogQuery struct {
	Selectors   []string  `json:"selectors,omitempty"`
	Tail        int       `json:"tail,omitempty"`
	Since       time.Time `json:"since,omitempty"`
	Run         int       `json:"run,omitempty"`
	Runs        int       `json:"runs,omitempty"`
	Sources     []string  `json:"sources,omitempty"`
	WithActions bool      `json:"with_actions,omitempty"`
	Cursor      string    `json:"cursor,omitempty"`
	// DefaultTail asks the application layer to apply CLI-style defaults only
	// when no explicit time/run/tail window was supplied. Both adapters use it;
	// they differ only in the limit they pass.
	DefaultTail int `json:"default_tail,omitempty"`
}

// LogEvent is one normalized captured line. Text is the semantic text used by
// structured clients; Raw preserves the exact captured line for CLI display.
type LogEvent struct {
	Sequence  uint64    `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Stream    string    `json:"stream"`
	Kind      string    `json:"kind"`
	Owner     string    `json:"owner"`
	Action    string    `json:"action,omitempty"`
	Run       uint32    `json:"run,omitempty"`
	Source    string    `json:"source"`
	Level     string    `json:"level"`
	Text      string    `json:"text"`
	Raw       string    `json:"raw,omitempty"`
}

type LogStreamWindow struct {
	Stream         string `json:"stream"`
	OldestSequence uint64 `json:"oldest_sequence,omitempty"`
	LatestSequence uint64 `json:"latest_sequence,omitempty"`
}

type LogWindow struct {
	Count   int               `json:"count"`
	First   time.Time         `json:"first,omitempty"`
	Last    time.Time         `json:"last,omitempty"`
	Streams []LogStreamWindow `json:"streams,omitempty"`
}

type LogResult struct {
	Events     []LogEvent        `json:"events"`
	Retention  []RunOutputNotice `json:"retention,omitempty"`
	Truncated  bool              `json:"truncated"`
	NextCursor string            `json:"next_cursor,omitempty"`
	Generation uint64            `json:"generation"`
	Window     LogWindow         `json:"window"`
}

// RunOutputNotice makes a known retention gap observable without fabricating
// log events. It is returned for every selected run whose captured prefix is
// no longer available.
type RunOutputNotice struct {
	Stream    string           `json:"stream"`
	Run       uint32           `json:"run"`
	Output    RunOutputSummary `json:"output"`
	OldestRun uint32           `json:"oldest_retained_run,omitempty"`
}

// LogQueryError provides a stable causal code without importing a delivery
// package into the application layer.
type LogQueryError struct {
	Code     string
	Message  string
	Hint     string
	Selector string
}

func (e *LogQueryError) Error() string { return e.Message }

type logTarget struct {
	address string
	service string
	action  config.ActionID
}

func (t logTarget) isAction() bool { return t.action.Name != "" }

type logCursor struct {
	SessionID  string            `json:"session_id"`
	Generation uint64            `json:"generation"`
	Signature  string            `json:"signature"`
	After      map[string]uint64 `json:"after"`
}

var knownLogSources = []string{"stdout", "stderr", "kranz"}

func queryLogs(local *Local, query LogQuery) (LogResult, error) {
	project := local.Project()
	result := LogResult{Generation: project.Generation, Events: []LogEvent{}}
	if query.Tail < 0 || query.Runs < 0 || query.Run != 0 && query.Runs != 0 {
		return result, &LogQueryError{Code: "invalid_log_query", Message: "tail and runs must be non-negative, and run and runs cannot be combined"}
	}
	for index, source := range query.Sources {
		source = strings.ToLower(strings.TrimSpace(source))
		if !slices.Contains(knownLogSources, source) {
			return result, &LogQueryError{Code: "invalid_source", Message: fmt.Sprintf("unknown log source %q", source), Hint: "Sources are stdout, stderr, and kranz."}
		}
		query.Sources[index] = source
	}
	targets, err := resolveLogTargets(local.Config(), query.Selectors, query.WithActions)
	if err != nil {
		return result, err
	}
	if query.DefaultTail > 0 && query.Tail == 0 && query.Since.IsZero() && query.Run == 0 && query.Runs == 0 {
		allActions := len(targets) > 0 && !slices.ContainsFunc(targets, func(target logTarget) bool { return !target.isAction() })
		if allActions {
			query.Run = -1
		} else {
			query.Tail = query.DefaultTail
		}
	}
	if (query.Run != 0 || query.Runs != 0) && len(targets) == 0 {
		return result, &LogQueryError{Code: "no_run_streams", Message: "run and runs address executions, and nothing was selected", Hint: "Name a service, or an action as OWNER/ACTION."}
	}
	signature := logQuerySignature(query, targets)
	cursor := logCursor{SessionID: project.SessionID, Generation: project.Generation, Signature: signature, After: map[string]uint64{}}
	if query.Cursor != "" {
		decoded, decodeErr := decodeLogCursor(query.Cursor)
		if decodeErr != nil || decoded.SessionID != project.SessionID || decoded.Generation != project.Generation || decoded.Signature != signature {
			return result, &LogQueryError{Code: "invalid_cursor", Message: "log cursor does not belong to this session generation and query", Hint: "Start a new logs query without cursor."}
		}
		cursor = decoded
	}

	events := make([]LogEvent, 0)
	windows := make([]LogStreamWindow, 0, len(targets))
	for _, target := range targets {
		entries := local.Logs(target.service)
		if target.isAction() {
			entries = local.ActionLogs(target.action)
		}
		runTarget := serviceRunTargetForLogTarget(target)
		summaries := local.manager.RunSummaries(runTarget)
		low, high, selected := selectedRunRange(summaries, query.Run, query.Runs)
		if query.Run != 0 || query.Runs != 0 {
			entries = filterEntriesByRange(entries, low, high, selected)
			for _, summary := range summaries {
				if selected && summary.Run >= low && summary.Run <= high && summary.Output.MissingLines > 0 {
					notice := RunOutputNotice{Stream: target.address, Run: summary.Run, Output: summary.Output}
					for _, boundary := range local.manager.RunRetentionBoundaries() {
						if boundary.Target == runTarget {
							notice.OldestRun = boundary.OldestRetainedRun
							break
						}
					}
					result.Retention = append(result.Retention, notice)
					result.Truncated = true
				}
			}
		}
		window := LogStreamWindow{Stream: target.address}
		if len(entries) > 0 {
			window.OldestSequence = entries[0].Sequence
			window.LatestSequence = entries[len(entries)-1].Sequence
			if query.Cursor == "" && window.OldestSequence > 1 {
				result.Truncated = true
			}
			if after := cursor.After[target.address]; after != 0 && after+1 < window.OldestSequence {
				result.Truncated = true
			}
		}
		windows = append(windows, window)
		for _, entry := range entries {
			if entry.Sequence <= cursor.After[target.address] {
				continue
			}
			if entry.Sequence > cursor.After[target.address] {
				cursor.After[target.address] = entry.Sequence
			}
			if len(query.Sources) > 0 && !slices.Contains(query.Sources, entry.Source) {
				continue
			}
			if !query.Since.IsZero() && entry.Timestamp.Before(query.Since) {
				continue
			}
			events = append(events, normalizedEvents(target, entry)...)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Timestamp.Equal(events[j].Timestamp) {
			if events[i].Stream == events[j].Stream {
				return events[i].Sequence < events[j].Sequence
			}
			return events[i].Stream < events[j].Stream
		}
		return events[i].Timestamp.Before(events[j].Timestamp)
	})
	if query.Tail > 0 && len(events) > query.Tail {
		result.Truncated = true
		events = events[len(events)-query.Tail:]
	}
	result.Events = events
	result.Window.Streams = windows
	result.Window.Count = len(events)
	if len(events) > 0 {
		result.Window.First = events[0].Timestamp
		result.Window.Last = events[len(events)-1].Timestamp
	}
	result.NextCursor, _ = encodeLogCursor(cursor)
	return result, nil
}

func (l *Local) ClearLogStreams(selectors []string, withActions bool) ([]string, error) {
	targets, err := resolveLogTargets(l.Config(), selectors, withActions)
	if err != nil {
		return nil, err
	}
	cleared := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.isAction() {
			l.ClearActionLogs(target.action)
		} else {
			l.ClearLogs(target.service)
		}
		cleared = append(cleared, target.address)
	}
	return cleared, nil
}

func resolveLogTargets(cfg *config.Config, selectors []string, withActions bool) ([]logTarget, error) {
	seen := map[string]bool{}
	var targets []logTarget
	add := func(target logTarget) {
		if !seen[target.address] {
			seen[target.address] = true
			targets = append(targets, target)
		}
	}
	if len(selectors) == 0 {
		for _, name := range cfg.ServiceOrder {
			add(logTarget{address: name, service: name})
		}
		if withActions {
			for _, id := range cfg.ActionIDs() {
				add(actionLogTarget(id))
			}
		}
		return targets, nil
	}
	for _, selector := range selectors {
		if strings.Contains(selector, "/") {
			id, ok := findActionID(cfg, selector)
			if !ok {
				return nil, &LogQueryError{Code: "action_not_found", Selector: selector, Message: fmt.Sprintf("action %q was not found", selector), Hint: "Actions are named OWNER/ACTION."}
			}
			add(actionLogTarget(id))
			continue
		}
		if _, ok := cfg.Services[selector]; ok {
			add(logTarget{address: selector, service: selector})
			if withActions {
				addOwnedActions(cfg, config.ActionOwnerService, selector, add)
			}
			continue
		}
		matchedTag := false
		for _, name := range cfg.ServiceOrder {
			if slices.ContainsFunc(cfg.Services[name].Tags, func(tag string) bool { return strings.EqualFold(tag, selector) }) {
				matchedTag = true
				add(logTarget{address: name, service: name})
				if withActions {
					addOwnedActions(cfg, config.ActionOwnerService, name, add)
				}
			}
		}
		if matchedTag {
			continue
		}
		if _, ok := cfg.ActionGroups[selector]; ok {
			if !withActions {
				return nil, &LogQueryError{Code: "group_has_no_stream", Selector: selector, Message: fmt.Sprintf("%q is an action group and has no log stream of its own", selector), Hint: "Name OWNER/ACTION or enable with_actions."}
			}
			addOwnedActions(cfg, config.ActionOwnerGroup, selector, add)
			continue
		}
		return nil, &LogQueryError{Code: "selector_not_found", Selector: selector, Message: fmt.Sprintf("service or tag %q was not found", selector), Hint: "List services, tags, or actions and use OWNER/ACTION for an action."}
	}
	return targets, nil
}

func actionLogTarget(id config.ActionID) logTarget {
	return logTarget{address: id.Owner + "/" + id.Name, action: id}
}

func addOwnedActions(cfg *config.Config, kind config.ActionOwnerKind, owner string, add func(logTarget)) {
	for _, id := range cfg.ActionIDs() {
		if id.OwnerKind == kind && id.Owner == owner {
			add(actionLogTarget(id))
		}
	}
}

func findActionID(cfg *config.Config, address string) (config.ActionID, bool) {
	for _, id := range cfg.ActionIDs() {
		if id.Owner+"/"+id.Name == address {
			return id, true
		}
	}
	return config.ActionID{}, false
}

func serviceRunTargetForLogTarget(target logTarget) RunTarget {
	if target.isAction() {
		return ActionRunTarget(target.action)
	}
	return ServiceRunTarget(target.service)
}

func selectedRunRange(summaries []RunSummary, run, runs int) (low, high uint32, ok bool) {
	if run == 0 && runs == 0 {
		return 0, 0, false
	}
	var latest uint32
	for _, summary := range summaries {
		latest = max(latest, summary.Run)
	}
	if latest == 0 {
		return 0, 0, false
	}
	if run > 0 {
		low, high = uint32(run), uint32(run)
	} else if run < 0 {
		offset := uint32(-run) - 1
		if offset >= latest {
			return 0, 0, false
		}
		low, high = latest-offset, latest-offset
	} else {
		high = latest
		if uint32(runs) >= latest {
			low = 1
		} else {
			low = latest - uint32(runs) + 1
		}
	}
	return low, high, true
}

func filterEntriesByRange(entries []config.LogEntry, low, high uint32, ok bool) []config.LogEntry {
	if !ok {
		return nil
	}
	return slices.DeleteFunc(append([]config.LogEntry(nil), entries...), func(entry config.LogEntry) bool { return entry.Run < low || entry.Run > high })
}

func normalizedEvents(target logTarget, entry config.LogEntry) []LogEvent {
	source := entry.Source
	if source == "" {
		source = "unknown"
	}
	lines := strings.Split(strings.TrimRight(entry.Raw, "\r\n"), "\n")
	events := make([]LogEvent, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		parsed := kranzlog.ParseLine(line)
		event := LogEvent{Sequence: entry.Sequence, Timestamp: entry.Timestamp, Stream: target.address, Kind: "service", Owner: target.service, Run: entry.Run, Source: source, Level: logLevelName(parsed.Level), Text: strings.TrimRight(parsed.Text, "\r\n"), Raw: line}
		if target.isAction() {
			event.Kind, event.Owner, event.Action = "action", target.action.Owner, target.action.Name
		}
		events = append(events, event)
	}
	return events
}

func logLevelName(level config.LogLevel) string {
	switch level {
	case config.LogError:
		return "error"
	case config.LogWarn:
		return "warn"
	case config.LogDebug:
		return "debug"
	default:
		return "info"
	}
}

func logQuerySignature(query LogQuery, targets []logTarget) string {
	shape := struct {
		Targets []string  `json:"targets"`
		Run     int       `json:"run"`
		Runs    int       `json:"runs"`
		Sources []string  `json:"sources"`
		Since   time.Time `json:"since"`
	}{Run: query.Run, Runs: query.Runs, Sources: query.Sources, Since: query.Since}
	for _, target := range targets {
		shape.Targets = append(shape.Targets, target.address)
	}
	payload, _ := json.Marshal(shape)
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:])
}

func encodeLogCursor(cursor logCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeLogCursor(value string) (logCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return logCursor{}, err
	}
	var cursor logCursor
	err = json.Unmarshal(payload, &cursor)
	if cursor.After == nil {
		cursor.After = map[string]uint64{}
	}
	return cursor, err
}
