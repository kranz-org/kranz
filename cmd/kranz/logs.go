package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
	kranzlog "github.com/kranz-org/kranz/internal/log"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

// defaultLogTail bounds a bare `kranz logs`. Every stream keeps a thousand
// lines, so a project with four of them answers an unqualified request with
// thousands of lines the user has to scroll past to reach the recent ones they
// were asking about. --all still returns everything.
const defaultLogTail = 50

type logOptions struct {
	selectors   []string
	tail        int
	tailSet     bool
	all         bool
	follow      bool
	since       time.Duration
	sinceSet    bool
	withActions bool
	run         int
	runSet      bool
	runs        int
	runsSet     bool
	noTimes     bool
	noLabels    bool
	sources     []string
}

// logTarget is one addressable log stream. A service owns the output of its own
// command; an action owns the output of its executions. The address is what the
// user typed and what the output labels lines with, so the two never drift.
type logTarget struct {
	address  string
	service  string
	action   config.ActionID
	isAction bool
}

func serviceLogTarget(name string) logTarget {
	return logTarget{address: name, service: name}
}

func actionLogTarget(id config.ActionID) logTarget {
	return logTarget{address: actionIDString(id), action: id, isAction: true}
}

type cliLogEvent struct {
	Stream    string    `json:"stream"`
	Kind      string    `json:"kind"`
	Owner     string    `json:"owner"`
	Action    string    `json:"action,omitempty"`
	Run       uint32    `json:"run,omitempty"`
	Source    string    `json:"source"`
	Sequence  uint64    `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Text      string    `json:"text"`
	Raw       string    `json:"raw"`
}

func runLogs(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	parsed, err := parseLogOptions(args)
	if err != nil {
		return err
	}
	client, closeClient, err := dialProjectRuntime(options)
	if err != nil {
		return err
	}
	defer closeClient()
	targets, err := resolveLogTargets(client.Config(), parsed.selectors, parsed.withActions)
	if err != nil {
		return err
	}
	parsed = applyLogDefaults(parsed, targets)
	if parsed.runSet || parsed.runsSet {
		// A run addresses one execution, and only actions execute. Keeping a
		// service's continuous stream in the result would answer a question
		// about runs with lines that belong to no run at all.
		targets, err = actionTargetsOnly(targets)
		if err != nil {
			return err
		}
	}
	allEvents := collectLogEvents(client, targets, parsed)
	cursors := make(map[string]uint64, len(targets))
	for _, event := range allEvents {
		cursors[event.Stream] = max(cursors[event.Stream], event.Sequence)
	}
	// --since narrows the window and --tail caps what that window returns, so
	// the two compose: "the last 50 lines, from the past five minutes".
	events := filterLogEventsBySource(allEvents, parsed.sources)
	if parsed.sinceSet {
		events = filterLogEventsSince(events, time.Now().Add(-parsed.since))
	}
	events = tailLogEvents(events, parsed)
	if err := writeLogEvents(stdout, options.Output, events, parsed); err != nil {
		return err
	}
	if !parsed.follow {
		return nil
	}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupts)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-interrupts:
			return nil
		case <-client.Done():
			return nil
		case <-ticker.C:
			fresh := collectLogEventsAfter(client, targets, parsed, cursors)
			// The cursor has to advance over every fresh line, including the
			// ones --source hides: a hidden line is still a line this client
			// has already seen, and filtering compacts the slice in place.
			for _, event := range fresh {
				cursors[event.Stream] = max(cursors[event.Stream], event.Sequence)
			}
			if err := writeLogEvents(stdout, options.Output, filterLogEventsBySource(fresh, parsed.sources), parsed); err != nil {
				return err
			}
		}
	}
}

// runLogsClear discards buffered history. Clearing everything is the one shape
// that cannot be narrowed after the fact, so it asks for --force the way `down`
// does rather than prompting: the CLI is expected to work in scripts.
func runLogsClear(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	selectors := make([]string, 0, len(args))
	force, withActions := false, false
	for _, arg := range args {
		switch {
		case arg == "--force":
			force = true
		case arg == "--with-actions":
			withActions = true
		case strings.HasPrefix(arg, "-"):
			return &kranzcli.Error{Code: "unknown_option", Message: fmt.Sprintf("unknown logs clear option %q", arg), Hint: "logs clear accepts --with-actions and --force.", ExitCode: kranzcli.ExitUsage}
		default:
			selectors = append(selectors, arg)
		}
	}
	if len(selectors) == 0 && !force {
		return &kranzcli.Error{
			Code:     "confirmation_required",
			Message:  "clearing every log buffer needs --force",
			Hint:     "Name what to clear, as in `kranz logs clear api`, or repeat with --force.",
			ExitCode: kranzcli.ExitUsage,
		}
	}
	client, closeClient, err := dialProjectRuntime(options)
	if err != nil {
		return err
	}
	defer closeClient()
	// An unqualified clear means the whole project, actions included: the
	// buffers it leaves behind would be exactly the ones nobody can name.
	targets, err := resolveLogTargets(client.Config(), selectors, withActions || len(selectors) == 0)
	if err != nil {
		return err
	}
	cleared := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.isAction {
			client.ClearActionLogs(target.action)
		} else {
			client.ClearLogs(target.service)
		}
		cleared = append(cleared, target.address)
	}
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, struct {
			Cleared []string `json:"cleared"`
		}{cleared})
	}
	_, _ = fmt.Fprintf(stdout, "Cleared %s.\n", pluralizeStreams(cleared))
	return nil
}

func pluralizeStreams(cleared []string) string {
	if len(cleared) == 1 {
		return "1 log stream: " + cleared[0]
	}
	return fmt.Sprintf("%d log streams: %s", len(cleared), strings.Join(cleared, ", "))
}

func dialProjectRuntime(options kranzcli.GlobalOptions) (*kranzruntime.Client, func(), error) {
	record, err := resolveSession(options)
	if err != nil {
		return nil, nil, err
	}
	client, err := kranzruntime.DialContext(context.Background(), record.Socket, version)
	if err != nil {
		return nil, nil, classifyRuntimeError(err)
	}
	return client, func() { _ = client.Close() }, nil
}

func parseLogOptions(args []string) (logOptions, error) {
	var options logOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		// -f is not a follow shorthand here: the global parser claims it as
		// --config wherever it appears, so accepting it below would document a
		// spelling that never reaches this switch.
		case arg == "--follow":
			options.follow = true
		case arg == "--all":
			options.all = true
		case arg == "--with-actions":
			options.withActions = true
		case arg == "--source" || strings.HasPrefix(arg, "--source="):
			value, consumed, err := logOptionValue(args, index, "--source")
			if err != nil {
				return logOptions{}, err
			}
			index += consumed
			selected, err := parseLogSources(value)
			if err != nil {
				return logOptions{}, err
			}
			options.sources = append(options.sources, selected...)
		case arg == "--no-timestamps":
			options.noTimes = true
		case arg == "--no-labels":
			options.noLabels = true
		case arg == "--plain":
			// The command's own output, as the command printed it. Every
			// column Kranz adds is there to tell interleaved streams apart,
			// which is exactly what a single stream does not need.
			options.noTimes, options.noLabels = true, true
		case arg == "--tail" || strings.HasPrefix(arg, "--tail="):
			value, consumed, err := logOptionValue(args, index, "--tail")
			if err != nil {
				return logOptions{}, err
			}
			index += consumed
			count, convErr := strconv.Atoi(value)
			if convErr != nil || count < 0 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_tail", Message: "--tail requires a non-negative integer", ExitCode: kranzcli.ExitUsage}
			}
			options.tail, options.tailSet = count, true
		case arg == "--since" || strings.HasPrefix(arg, "--since="):
			value, consumed, err := logOptionValue(args, index, "--since")
			if err != nil {
				return logOptions{}, err
			}
			index += consumed
			duration, convErr := time.ParseDuration(value)
			if convErr != nil || duration < 0 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_since", Message: "--since requires a non-negative duration such as 5m", ExitCode: kranzcli.ExitUsage}
			}
			options.since, options.sinceSet = duration, true
		case arg == "--run" || strings.HasPrefix(arg, "--run="):
			value, consumed, err := logOptionValue(args, index, "--run")
			if err != nil {
				return logOptions{}, err
			}
			index += consumed
			number, convErr := strconv.Atoi(value)
			if convErr != nil || number == 0 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_run", Message: "--run requires a run number, or a negative offset such as -1 for the latest", ExitCode: kranzcli.ExitUsage}
			}
			options.run, options.runSet = number, true
		case arg == "--runs" || strings.HasPrefix(arg, "--runs="):
			value, consumed, err := logOptionValue(args, index, "--runs")
			if err != nil {
				return logOptions{}, err
			}
			index += consumed
			count, convErr := strconv.Atoi(value)
			if convErr != nil || count < 1 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_runs", Message: "--runs requires a positive count", ExitCode: kranzcli.ExitUsage}
			}
			options.runs, options.runsSet = count, true
		case strings.HasPrefix(arg, "-"):
			return logOptions{}, &kranzcli.Error{Code: "unknown_option", Message: fmt.Sprintf("unknown logs option %q", arg), Hint: "logs accepts --tail N, --since D, --run N, --runs N, --source S, --all, --with-actions, --plain, --no-timestamps, --no-labels, and --follow.", ExitCode: kranzcli.ExitUsage}
		default:
			options.selectors = append(options.selectors, arg)
		}
	}
	if options.all && options.tailSet {
		return logOptions{}, &kranzcli.Error{Code: "invalid_arguments", Message: "--all and --tail contradict each other", ExitCode: kranzcli.ExitUsage}
	}
	if options.runSet && options.runsSet {
		return logOptions{}, &kranzcli.Error{Code: "invalid_arguments", Message: "--run and --runs contradict each other", ExitCode: kranzcli.ExitUsage}
	}
	return options, nil
}

// knownLogSources are the origins Kranz records for a line: the two process
// streams, and Kranz itself for the lifecycle notes it writes into the buffer.
var knownLogSources = []string{"stdout", "stderr", "kranz"}

// parseLogSources accepts one source or a comma-separated list, so both
// `--source stdout --source stderr` and `--source stdout,stderr` work.
func parseLogSources(value string) ([]string, error) {
	selected := make([]string, 0, 2)
	for _, part := range strings.Split(value, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if !slices.Contains(knownLogSources, part) {
			return nil, &kranzcli.Error{
				Code:     "invalid_source",
				Message:  fmt.Sprintf("unknown log source %q", part),
				Hint:     "Sources are " + strings.Join(knownLogSources, ", ") + ".",
				ExitCode: kranzcli.ExitUsage,
			}
		}
		selected = append(selected, part)
	}
	if len(selected) == 0 {
		return nil, &kranzcli.Error{Code: "invalid_source", Message: "--source requires a source name", Hint: "Sources are " + strings.Join(knownLogSources, ", ") + ".", ExitCode: kranzcli.ExitUsage}
	}
	return selected, nil
}

// filterLogEventsBySource keeps only the origins asked for. It runs before the
// tail so that `--source stderr --tail 20` means twenty error lines, not
// whatever errors survive in the last twenty lines of everything.
func filterLogEventsBySource(events []cliLogEvent, sources []string) []cliLogEvent {
	if len(sources) == 0 {
		return events
	}
	filtered := events[:0]
	for _, event := range events {
		if slices.Contains(sources, event.Source) {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

// applyLogDefaults decides what an unqualified request means, which depends on
// what it selected. A service streams without end, so "recent" is the useful
// answer. An action produces a finite, self-contained run, and capping that at
// the last 50 lines cuts off its head — the part explaining what the run did.
func applyLogDefaults(options logOptions, targets []logTarget) logOptions {
	if options.tailSet || options.all || options.sinceSet || options.runSet || options.runsSet {
		return options
	}
	if allActionTargets(targets) {
		options.run, options.runSet = -1, true
		return options
	}
	options.tail, options.tailSet = defaultLogTail, true
	return options
}

func allActionTargets(targets []logTarget) bool {
	for _, target := range targets {
		if !target.isAction {
			return false
		}
	}
	return len(targets) != 0
}

// logOptionValue reads a value written either as `--flag value` or `--flag=value`
// and reports how many extra arguments it consumed.
func logOptionValue(args []string, index int, name string) (string, int, error) {
	arg := args[index]
	if strings.HasPrefix(arg, name+"=") {
		return strings.TrimPrefix(arg, name+"="), 0, nil
	}
	if index+1 >= len(args) {
		return "", 0, &kranzcli.Error{Code: "missing_option_value", Message: fmt.Sprintf("%s requires a value", name), ExitCode: kranzcli.ExitUsage}
	}
	return args[index+1], 1, nil
}

// resolveLogTargets turns selectors into streams. A name without a slash
// addresses a service or a tag; a name with one addresses an action, which
// resolveActionID already disambiguates between service and group owners.
func resolveLogTargets(cfg *config.Config, selectors []string, withActions bool) ([]logTarget, error) {
	if len(selectors) == 0 {
		targets := make([]logTarget, 0, len(cfg.ServiceOrder))
		for _, name := range cfg.ServiceOrder {
			targets = append(targets, serviceLogTarget(name))
		}
		if withActions {
			for _, id := range cfg.ActionIDs() {
				targets = append(targets, actionLogTarget(id))
			}
		}
		return targets, nil
	}
	seen := make(map[string]bool)
	targets := make([]logTarget, 0, len(selectors))
	add := func(target logTarget) {
		if seen[target.address] {
			return
		}
		seen[target.address] = true
		targets = append(targets, target)
	}
	for _, selector := range selectors {
		resolved, err := resolveOneLogSelector(cfg, selector, withActions)
		if err != nil {
			return nil, err
		}
		for _, target := range resolved {
			add(target)
		}
	}
	return targets, nil
}

func resolveOneLogSelector(cfg *config.Config, selector string, withActions bool) ([]logTarget, error) {
	if strings.Contains(selector, "/") {
		id, _, err := resolveActionID(cfg, selector)
		if err != nil {
			return nil, err
		}
		return []logTarget{actionLogTarget(id)}, nil
	}
	if _, ok := cfg.Services[selector]; ok {
		targets := []logTarget{serviceLogTarget(selector)}
		if withActions {
			targets = append(targets, ownedActionTargets(cfg, config.ActionOwnerService, selector)...)
		}
		return targets, nil
	}
	var tagged []logTarget
	for _, name := range cfg.ServiceOrder {
		for _, tag := range cfg.Services[name].Tags {
			if strings.EqualFold(tag, selector) {
				tagged = append(tagged, serviceLogTarget(name))
				if withActions {
					tagged = append(tagged, ownedActionTargets(cfg, config.ActionOwnerService, name)...)
				}
				break
			}
		}
	}
	if len(tagged) != 0 {
		return tagged, nil
	}
	if _, ok := cfg.ActionGroups[selector]; ok {
		// A group has no command of its own, so its name addresses no stream.
		// Saying which two ways forward exist beats a bare "not found" for a
		// name the project really does define.
		if !withActions {
			return nil, &kranzcli.Error{
				Code:     "group_has_no_stream",
				Message:  fmt.Sprintf("%q is an action group and has no log stream of its own", selector),
				Hint:     groupSelectorHint(cfg, selector),
				ExitCode: kranzcli.ExitNotFound,
			}
		}
		return ownedActionTargets(cfg, config.ActionOwnerGroup, selector), nil
	}
	return nil, &kranzcli.Error{
		Code:     "selector_not_found",
		Message:  fmt.Sprintf("service or tag %q was not found", selector),
		Hint:     "Run `kranz list services` for services, or `kranz action list` for actions, which are named OWNER/ACTION.",
		ExitCode: kranzcli.ExitNotFound,
	}
}

func groupSelectorHint(cfg *config.Config, group string) string {
	for _, id := range cfg.ActionIDs() {
		if id.OwnerKind == config.ActionOwnerGroup && id.Owner == group {
			return fmt.Sprintf("Name an action, as in `kranz logs %s`, or pass --with-actions to read every action in the group.", actionIDString(id))
		}
	}
	return "Pass --with-actions to read every action in the group."
}

func ownedActionTargets(cfg *config.Config, kind config.ActionOwnerKind, owner string) []logTarget {
	var targets []logTarget
	for _, id := range cfg.ActionIDs() {
		if id.OwnerKind == kind && id.Owner == owner {
			targets = append(targets, actionLogTarget(id))
		}
	}
	return targets
}

func actionTargetsOnly(targets []logTarget) ([]logTarget, error) {
	filtered := make([]logTarget, 0, len(targets))
	for _, target := range targets {
		if target.isAction {
			filtered = append(filtered, target)
		}
	}
	if len(filtered) == 0 {
		return nil, &kranzcli.Error{
			Code:     "no_action_streams",
			Message:  "--run and --runs address action executions, and no action was selected",
			Hint:     "Name an action, as in `kranz logs api/migrate --run -1`.",
			ExitCode: kranzcli.ExitUsage,
		}
	}
	return filtered, nil
}

func collectLogEvents(client *kranzruntime.Client, targets []logTarget, options logOptions) []cliLogEvent {
	events := make([]cliLogEvent, 0)
	for _, target := range targets {
		entries := target.entries(client)
		if options.runSet || options.runsSet {
			entries = filterLogEntriesByRun(entries, options)
		}
		for _, entry := range entries {
			events = append(events, target.events(entry)...)
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
	return events
}

func (t logTarget) entries(client *kranzruntime.Client) []config.LogEntry {
	if t.isAction {
		return client.ActionLogs(t.action)
	}
	return client.Logs(t.service)
}

// events turns one captured entry into one event per line of text. A pipe hands
// Kranz whatever chunk it read, so a single entry can hold fourteen lines; if
// that entry stayed one event, --tail 50 would print an unpredictable number of
// lines and cut in the middle of one.
func (t logTarget) events(entry config.LogEntry) []cliLogEvent {
	source := entry.Source
	if source == "" {
		source = "unknown"
	}
	lines := strings.Split(trimLineEnding(entry.Raw), "\n")
	events := make([]cliLogEvent, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		parsed := kranzlog.ParseLine(line)
		event := cliLogEvent{
			Stream:    t.address,
			Kind:      "service",
			Owner:     t.service,
			Source:    source,
			Sequence:  entry.Sequence,
			Timestamp: entry.Timestamp,
			Level:     logLevelName(parsed.Level),
			Run:       entry.Run,
			// The line terminator is how the line arrived, not part of what it
			// says, so it does not belong in a JSON field a consumer will
			// compare or print.
			Text: trimLineEnding(parsed.Text),
			Raw:  line,
		}
		if t.isAction {
			event.Kind = "action"
			event.Owner = t.action.Owner
			event.Action = t.action.Name
		}
		events = append(events, event)
	}
	return events
}

// filterLogEntriesByRun narrows one stream to the executions asked for. Run
// numbers are resolved against the newest run still buffered, so --run -1 keeps
// meaning "the latest" no matter how many runs have aged out.
func filterLogEntriesByRun(entries []config.LogEntry, options logOptions) []config.LogEntry {
	var latest uint32
	for _, entry := range entries {
		latest = max(latest, entry.Run)
	}
	if latest == 0 {
		return nil
	}
	var lowest, highest uint32
	switch {
	case options.runSet && options.run > 0:
		lowest, highest = uint32(options.run), uint32(options.run)
	case options.runSet:
		offset := uint32(-options.run) - 1
		if offset >= latest {
			return nil
		}
		lowest, highest = latest-offset, latest-offset
	default:
		highest = latest
		if uint32(options.runs) >= latest {
			lowest = 1
		} else {
			lowest = latest - uint32(options.runs) + 1
		}
	}
	filtered := make([]config.LogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Run >= lowest && entry.Run <= highest {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

const logTimestampLayout = "2006-01-02T15:04:05.000Z07:00"

func trimLineEnding(line string) string { return strings.TrimRight(line, "\r\n") }

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

func collectLogEventsAfter(client *kranzruntime.Client, targets []logTarget, options logOptions, cursors map[string]uint64) []cliLogEvent {
	all := collectLogEvents(client, targets, options)
	fresh := all[:0]
	for _, event := range all {
		if event.Sequence > cursors[event.Stream] {
			fresh = append(fresh, event)
		}
	}
	return fresh
}

// tailLogEvents keeps the last --tail events. Each event is exactly one printed
// line, so the flag means the number of lines the user will actually see.
func tailLogEvents(events []cliLogEvent, options logOptions) []cliLogEvent {
	if !options.tailSet || options.tail >= len(events) {
		return events
	}
	return events[len(events)-options.tail:]
}

func filterLogEventsSince(events []cliLogEvent, cutoff time.Time) []cliLogEvent {
	filtered := events[:0]
	for _, event := range events {
		if !event.Timestamp.Before(cutoff) {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

// logEventLabel addresses the line the way the user would ask for it. The
// address already contains a slash for an action, so the source is separated by
// a space rather than a second slash that would read as another name segment.
func logEventLabel(event cliLogEvent) string {
	if event.Run > 0 {
		return fmt.Sprintf("%s#%d %s", event.Stream, event.Run, event.Source)
	}
	return event.Stream + " " + event.Source
}

// logEventPrefix builds the columns Kranz adds in front of a line. Both are
// there to tell interleaved streams apart, and both can be switched off for the
// common case of reading one stream back as the command printed it.
func logEventPrefix(event cliLogEvent, options logOptions) string {
	var prefix strings.Builder
	if !options.noTimes {
		// A fixed-width timestamp keeps the address column aligned; RFC3339Nano
		// drops trailing zeros and leaves the output ragged.
		prefix.WriteString(event.Timestamp.Local().Format(logTimestampLayout))
		prefix.WriteString(" ")
	}
	if !options.noLabels {
		prefix.WriteString("[" + logEventLabel(event) + "] ")
	}
	return prefix.String()
}

func writeLogEvents(stdout io.Writer, format kranzcli.OutputFormat, events []cliLogEvent, options logOptions) error {
	if format == kranzcli.OutputJSON {
		if options.follow {
			for _, event := range events {
				if err := kranzcli.WriteJSON(stdout, event); err != nil {
					return err
				}
			}
			return nil
		}
		return kranzcli.WriteJSON(stdout, events)
	}
	for _, event := range events {
		if _, err := fmt.Fprintln(stdout, logEventPrefix(event, options)+event.Raw); err != nil {
			return err
		}
	}
	return nil
}
