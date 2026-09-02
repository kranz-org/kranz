package ui

import (
	"fmt"
	"slices"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

const (
	activeRuntimePollInterval = 250 * time.Millisecond
	idleRuntimePollInterval   = time.Second
)

type cachedActionLogLine struct {
	sequence uint64
	run      uint32
	text     string
}

// runtimePollInterval keeps running output responsive while letting a quiet
// dashboard settle. Mouse recovery has its own direct 250 ms watchdog and is
// deliberately not coupled to this model clock.
func (m *Model) runtimePollInterval() time.Duration {
	for _, svc := range m.allServices {
		if app.ServiceStartPlanned(svc) {
			return activeRuntimePollInterval
		}
	}
	for _, state := range m.actionStates {
		if state.Status == app.ActionRunning {
			return activeRuntimePollInterval
		}
	}
	return idleRuntimePollInterval
}

// refreshActionStates reads every action's state in one runtime round trip.
// Asking per action costs a socket round trip each, and a config with a couple
// of dozen actions pays that on every poll for a dashboard where nothing is
// running. A runtime too old to answer the batch method falls back to the
// per-action path rather than showing every action as never run.
func (m *Model) refreshActionStates() {
	ids := m.cfg.ActionIDs()
	states := make(map[config.ActionID]app.ActionResult, len(ids))
	if batch := m.app.ActionStates(); len(batch) > 0 {
		for _, state := range batch {
			states[state.ID] = state
		}
		m.actionStates = states
		return
	}
	for _, id := range ids {
		state, _ := m.app.ActionState(id)
		states[id] = state
	}
	m.actionStates = states
}

func (m *Model) refreshRunSummaries() {
	m.runs = m.app.Runs()
}

func (m *Model) cachedActionState(id config.ActionID) app.ActionResult {
	if state, ok := m.actionStates[id]; ok {
		return state
	}
	return app.ActionResult{ID: id, Status: app.ActionReady, ExitCode: -1}
}

func (m *Model) cachedService(name string) (*app.ServiceSnapshot, bool) {
	for _, svc := range m.allServices {
		if svc.Name == name {
			return svc, true
		}
	}
	return nil, false
}

func (m *Model) visibleLogTargets() []app.RunTarget {
	targets := make([]app.RunTarget, 0, 2)
	if m.focusedAction != nil {
		targets = append(targets, app.ActionRunTarget(*m.focusedAction))
	} else if svc := m.FocusedService(); svc != nil {
		targets = append(targets, app.ServiceRunTarget(svc.Name))
	}
	if target, ok := m.pinnedRunTarget(); ok && !slices.Contains(targets, target) {
		targets = append(targets, target)
	}
	return targets
}

func (m *Model) refreshVisibleLogCaches() {
	for _, target := range m.visibleLogTargets() {
		m.refreshLogCache(target)
	}
}

func (m *Model) refreshLogCache(target app.RunTarget) {
	selector := target.Name
	if target.Kind == app.RunKindAction {
		selector = target.Action.Owner + "/" + target.Action.Name
	}
	if selector == "" {
		return
	}
	cursor := m.logCursors[target]
	result, err := m.app.QueryLogs(app.LogQuery{Selectors: []string{selector}, Cursor: cursor})
	if err != nil && cursor != "" {
		m.invalidateLogCache(target)
		cursor = ""
		result, err = m.app.QueryLogs(app.LogQuery{Selectors: []string{selector}})
	}
	if err != nil {
		return
	}
	if cursor != "" && result.Truncated {
		m.invalidateLogCache(target)
		result, err = m.app.QueryLogs(app.LogQuery{Selectors: []string{selector}})
		if err != nil {
			return
		}
	}

	entries := m.logEntries[target]
	if len(result.Window.Streams) > 0 && result.Window.Streams[0].OldestSequence > 0 {
		oldest := result.Window.Streams[0].OldestSequence
		entries = slices.DeleteFunc(entries, func(entry config.LogEntry) bool { return entry.Sequence < oldest })
		m.actionLogLines[target] = slices.DeleteFunc(m.actionLogLines[target], func(line cachedActionLogLine) bool { return line.sequence < oldest })
		for run, lines := range m.actionRunLogLines[target] {
			lines = slices.DeleteFunc(lines, func(line cachedActionLogLine) bool { return line.sequence < oldest })
			if len(lines) == 0 {
				delete(m.actionRunLogLines[target], run)
				continue
			}
			m.actionRunLogLines[target][run] = lines
		}
	}
	for _, event := range result.Events {
		entries = append(entries, config.LogEntry{
			Sequence:  event.Sequence,
			Timestamp: event.Timestamp,
			Source:    event.Source,
			Run:       event.Run,
			Level:     cachedLogLevel(event.Level),
			Text:      event.Text,
			Raw:       event.Raw,
		})
		if target.Kind == app.RunKindAction {
			prefix := ""
			if event.Source == "stderr" {
				prefix = "[stderr] "
			}
			for _, line := range appendSafeActionOutput(nil, event.Raw, prefix) {
				cachedLine := cachedActionLogLine{sequence: event.Sequence, run: event.Run, text: line}
				m.actionLogLines[target] = append(m.actionLogLines[target], cachedLine)
				if m.actionRunLogLines[target] == nil {
					m.actionRunLogLines[target] = make(map[uint32][]cachedActionLogLine)
				}
				m.actionRunLogLines[target][event.Run] = append(m.actionRunLogLines[target][event.Run], cachedLine)
			}
		}
	}
	m.logEntries[target] = entries
	m.logCursors[target] = result.NextCursor
}

func cachedLogLevel(level string) config.LogLevel {
	switch level {
	case "error":
		return config.LogError
	case "warn":
		return config.LogWarn
	case "debug":
		return config.LogDebug
	default:
		return config.LogInfo
	}
}

func (m *Model) cachedLogEntries(target app.RunTarget) []config.LogEntry {
	return m.logEntries[target]
}

func (m *Model) invalidateLogCache(target app.RunTarget) {
	delete(m.logEntries, target)
	delete(m.actionLogLines, target)
	delete(m.actionRunLogLines, target)
	delete(m.logCursors, target)
}

func (m *Model) resetLogCaches() {
	m.logEntries = make(map[app.RunTarget][]config.LogEntry)
	m.actionLogLines = make(map[app.RunTarget][]cachedActionLogLine)
	m.actionRunLogLines = make(map[app.RunTarget]map[uint32][]cachedActionLogLine)
	m.logCursors = make(map[app.RunTarget]string)
}

func (m *Model) cachedActionLogRecords(target app.RunTarget, run uint32) []cachedActionLogLine {
	if run == 0 {
		return m.actionLogLines[target]
	}
	return m.actionRunLogLines[target][run]
}

func (m *Model) cachedActionOutputLines(target app.RunTarget, run uint32) []string {
	cached := m.cachedActionLogRecords(target, run)
	lines := make([]string, 0, len(cached))
	for _, line := range cached {
		lines = append(lines, line.text)
	}
	if run > 0 {
		if summary, ok := m.runSummary(target, run); ok && summary.Output.MissingLines > 0 {
			marker := formatMissingOutputMarker(summary)
			lines = append([]string{marker}, lines...)
		}
	}
	return lines
}

func formatMissingOutputMarker(summary app.RunSummary) string {
	return fmt.Sprintf("[Kranz] Output truncated · missing %d lines / %d bytes", summary.Output.MissingLines, summary.Output.MissingBytes)
}
