package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/logcommand"
)

// A processor belongs to a selected history window, never to a service's
// capture pipeline. Pinned and main panels can process different runs at once.
type logCommandKey struct {
	target  app.RunTarget
	mode    runViewMode
	run     uint32
	session uint64
	command config.LogCommandConfig
}

type logCommandView struct {
	key                               logCommandKey
	process                           *logcommand.Process
	input                             []config.LogEntry
	entries                           []config.LogEntry
	actionLines                       []cachedActionLogLine
	epoch                             uint64
	first                             uint64
	waiting, closed, finished, failed bool
}

type logCommandChangedMsg struct {
	slot   logPanelSlot
	view   *logCommandView
	output logcommand.Snapshot
}

type logViewerFinishedMsg struct{ err error }

func (m *Model) effectiveLogCommand(target app.RunTarget) config.LogCommandConfig {
	value := m.cfg.UI.Logs
	if m.userSettings.Logs != nil {
		value = m.userSettings.Logs
	}
	owner := target.Name
	if target.Kind == app.RunKindAction && target.Action.OwnerKind != config.ActionOwnerGroup {
		owner = target.Action.Owner
	}
	if service, ok := m.cfg.Services[owner]; ok && service.UI.Logs != nil {
		value = service.UI.Logs
	}
	if value == nil || strings.TrimSpace(value.Command) == "" {
		return config.LogCommandConfig{}
	}
	result := *value
	if result.Mode == "" {
		result.Mode = config.LogCommandStream
	}
	if result.Shell == "" {
		result.Shell = "/bin/sh"
	}
	return result
}

func (m *Model) logCommandScope(slot logPanelSlot) (logCommandKey, []config.LogEntry, bool) {
	var key logCommandKey
	key.session = m.sessionGeneration
	if slot == logSlotPinned {
		target, ok := m.pinnedRunTarget()
		if !ok {
			return key, nil, false
		}
		key.target, key.mode, key.run = target, m.pinnedRunMode, m.pinnedRun
		key.command = m.effectiveLogCommand(target)
		return key, m.rawPinnedEntries(target), true
	}
	if !m.syncRunTarget() {
		return key, nil, false
	}
	key.target, key.mode = m.runTarget, m.runMode
	if key.mode == runViewSingle {
		key.run = m.selectedRun
	}
	key.command = m.effectiveLogCommand(key.target)
	return key, m.rawMainLogEntries(key.target), true
}

func (m *Model) rawMainLogEntries(target app.RunTarget) []config.LogEntry {
	if m.runMode == runViewSingle && m.runTarget == target {
		return m.entriesForRun(target, m.selectedRun)
	}
	return m.cachedLogEntries(target)
}

func (m *Model) mainLogEntries(target app.RunTarget) []config.LogEntry {
	return m.presentedLogEntries(logSlotMain, target, m.rawMainLogEntries(target))
}

func (m *Model) pinnedEntries(target app.RunTarget) []config.LogEntry {
	return m.presentedLogEntries(logSlotPinned, target, m.rawPinnedEntries(target))
}

func (m *Model) presentedLogEntries(slot logPanelSlot, target app.RunTarget, original []config.LogEntry) []config.LogEntry {
	command := m.effectiveLogCommand(target)
	if strings.TrimSpace(command.Command) == "" || command.Mode != config.LogCommandStream || m.logCommandBypass[slot] {
		return original
	}
	key, _, ok := m.logCommandScope(slot)
	view := m.logCommands[slot]
	if !ok || view == nil || view.key != key {
		return nil
	}
	if view.failed {
		return original
	}
	return view.entries
}

func commandInputEntries(entries []config.LogEntry) []config.LogEntry {
	result := make([]config.LogEntry, 0, len(entries))
	for _, entry := range entries {
		// Missing-history notices are Kranz metadata, not processor input.
		if entry.Sequence == 0 && entry.Source == "kranz" {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func logInputExtends(before, after []config.LogEntry) bool {
	if len(after) < len(before) {
		return false
	}
	for index, entry := range before {
		if after[index].Sequence != entry.Sequence || after[index].Raw != entry.Raw || after[index].Run != entry.Run {
			return false
		}
	}
	return true
}

func (m *Model) logScopeLive(key logCommandKey) bool {
	for _, run := range m.runsForTarget(key.target) {
		if key.mode == runViewSingle && run.Run != key.run {
			continue
		}
		if key.mode == runViewCombined && key.run > 0 && run.Run > key.run {
			continue
		}
		if run.FinishedAt.IsZero() {
			return true
		}
	}
	return false
}

// Called only from Init/Update. Rendering and scrolling never execute commands.
func (m *Model) syncLogCommands() tea.Cmd {
	if m.exiting || m.mode == ModeRuntimeLost {
		m.stopLogCommands()
		return nil
	}
	var commands []tea.Cmd
	for slot := logSlotMain; slot < logSlotCount; slot++ {
		key, source, exists := m.logCommandScope(slot)
		view := m.logCommands[slot]
		if !exists || key.command.Command == "" || key.command.Mode != config.LogCommandStream || m.logCommandBypass[slot] {
			if view != nil {
				view.process.Cancel()
				m.logCommands[slot] = nil
				m.logRowCache[slot] = nil
			}
			continue
		}
		input := commandInputEntries(source)
		live := m.logScopeLive(key)
		replace := view == nil || view.key != key || !logInputExtends(view.input, input)
		if view != nil && !view.failed && (len(input) > len(view.input) || live) && view.closed {
			replace = true
		}
		if view != nil && !view.failed && view.finished && len(input) > len(view.input) {
			replace = true
		}
		if replace {
			if view != nil {
				view.process.Cancel()
			}
			m.logCommandEpoch++
			view = &logCommandView{key: key, epoch: m.logCommandEpoch,
				process: logcommand.Start(logcommand.Options{Command: key.command.Command, Shell: key.command.Shell,
					Directory: m.workingDirectory, Environment: commandEnvironment("KRANZ_LOG_TARGET="+runTargetLabel(key.target), "KRANZ_LOG_SCOPE="+logCommandScopeLabel(key), "KRANZ_LOG_RUN="+strconv.FormatUint(uint64(key.run), 10))})}
			m.logCommands[slot] = view
			m.logRowCache[slot] = nil
		}
		if !view.failed && !view.finished && !view.closed && (len(input) > len(view.input) || !live) {
			var text strings.Builder
			for _, entry := range input[len(view.input):] {
				text.WriteString(strings.TrimSuffix(entry.Raw, "\n"))
				text.WriteByte('\n')
			}
			view.input = append(view.input[:0], input...)
			view.process.Send(text.String(), !live)
			view.closed = !live
		}
		if !view.waiting && !view.finished {
			view.waiting = true
			commands = append(commands, waitLogCommand(slot, view))
		}
	}
	return tea.Batch(commands...)
}

func waitLogCommand(slot logPanelSlot, view *logCommandView) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-view.process.Changed:
		case <-view.process.Done:
		}
		return logCommandChangedMsg{slot: slot, view: view, output: view.process.Snapshot()}
	}
}

func (m *Model) handleLogCommandChanged(msg logCommandChangedMsg) {
	view := m.logCommands[msg.slot]
	if view == nil || view != msg.view {
		return
	}
	view.waiting = false
	key, source, exists := m.logCommandScope(msg.slot)
	if !exists || key != view.key {
		return
	}
	view.finished = msg.output.Finished
	if msg.output.Err != nil {
		if !view.failed {
			m.addNotification("logs", readableLog(msg.output.Err.Error())+"; showing original output", config.LogWarn)
		}
		view.failed = true
		m.logRowCache[msg.slot] = nil
		return
	}
	entries := make([]config.LogEntry, 0, len(msg.output.Lines)+1)
	for _, entry := range source {
		if entry.Sequence == 0 && entry.Source == "kranz" {
			entries = append(entries, entry)
		}
	}
	// Preserve capture metadata only while the command demonstrably preserves
	// the input text and line order. General transformations have stream scope,
	// never an invented per-line association with an original event.
	inputLines := make([]config.LogEntry, 0, len(view.input))
	for _, entry := range view.input {
		for _, line := range strings.Split(readableLog(entry.Raw), "\n") {
			copy := entry
			copy.Text = line
			inputLines = append(inputLines, copy)
		}
	}
	identity := key.command.PreserveLines
	if identity {
		valid := msg.output.First+uint64(len(msg.output.Lines)) <= uint64(len(inputLines))
		if msg.output.Finished && msg.output.First+uint64(len(msg.output.Lines)) != uint64(len(inputLines)) {
			valid = false
		}
		if valid {
			for index, raw := range msg.output.Lines {
				if readableLog(raw) != inputLines[int(msg.output.First)+index].Text {
					valid = false
					break
				}
			}
		}
		if !valid {
			view.process.Cancel()
			view.failed = true
			m.logRowCache[msg.slot] = nil
			m.addNotification("logs", "Log command changed text, order, or line count with preserve_lines enabled; showing original output", config.LogWarn)
			return
		}
	}
	for index, raw := range msg.output.Lines {
		entry := config.LogEntry{Run: key.run}
		if identity {
			entry = inputLines[int(msg.output.First)+index]
		}
		entry.Raw = styledReadableLog(raw)
		entry.Text = readableLog(entry.Raw)
		entry.Sequence = uint64(1)<<63 | view.epoch<<32 | (msg.output.First + uint64(index) + 1)
		entries = append(entries, entry)
	}
	if msg.output.First > view.first {
		// Output eviction has the same anchor correction as source retention.
		// Width-dependent row counts are measured from the old presentation.
		dropped := min(int(msg.output.First-view.first), len(view.entries))
		rows := 0
		for _, entry := range view.entries[:dropped] {
			rows += m.countLogEntryRows(entry, m.currentLogContentWidth())
		}
		if msg.slot == logSlotPinned {
			m.pinnedAnchor = max(0, m.pinnedAnchor-rows)
		} else {
			m.logAnchor = max(0, m.logAnchor-rows)
		}
	}
	if msg.output.First > 0 && view.first == 0 {
		m.addNotification("logs", "Processed output exceeded its display budget; retaining the latest 4 MiB / 20000 lines. Original logs remain available", config.LogWarn)
	}
	if !logInputExtends(view.entries, entries) {
		m.logRowCache[msg.slot] = nil
	}
	view.first, view.entries = msg.output.First, entries
	view.actionLines = nil
	if key.target.Kind == app.RunKindAction {
		for _, entry := range entries {
			if entry.Sequence == 0 {
				continue
			}
			prefix := ""
			if entry.Source == "stderr" {
				prefix = "[stderr] "
			}
			for _, line := range strings.Split(entry.Raw, "\n") {
				view.actionLines = append(view.actionLines, cachedActionLogLine{sequence: entry.Sequence, run: entry.Run, text: prefix + line})
			}
		}
	}
}

func (m *Model) formattedEntry(entry config.LogEntry) bool {
	if entry.Sequence&(uint64(1)<<63) == 0 {
		return false
	}
	epoch := (entry.Sequence &^ (uint64(1) << 63)) >> 32
	for _, view := range m.logCommands {
		if view != nil && view.epoch == epoch {
			return true
		}
	}
	return false
}

func (m *Model) logEntryContent(entry config.LogEntry) string {
	if m.formattedEntry(entry) {
		return entry.Raw
	}
	return readableLog(entry.Raw)
}

func (m *Model) stopLogCommands() {
	for slot, view := range m.logCommands {
		if view != nil {
			view.process.Cancel()
			m.logCommands[slot] = nil
		}
	}
}

func (m *Model) toggleLogCommand() tea.Cmd {
	slot := logSlotMain
	if m.panelFocus == panelPinnedLogs {
		slot = logSlotPinned
	}
	key, _, ok := m.logCommandScope(slot)
	if !ok || key.command.Command == "" {
		m.addNotification("logs", "Configure ui.logs.command or the user logs preference to connect a log tool", config.LogInfo)
		return nil
	}
	if key.command.Mode == config.LogCommandTerminal {
		return m.openLogViewer()
	}
	m.logCommandBypass[slot] = !m.logCommandBypass[slot]
	m.logRowCache[slot] = nil
	// The coordinate spaces may differ; toggling starts at the current tail.
	if slot == logSlotPinned {
		m.pinnedOffset, m.pinnedAnchor, m.pinnedFollow = 0, 0, true
	} else {
		m.logOffset, m.logAnchor, m.followMode, m.logPaused = 0, 0, true, false
	}
	return nil
}

// A terminal viewer gets a frozen snapshot of the active run scope and regex
// filter. It owns the real terminal until it exits, then Kranz resumes with the
// same viewport. The input file leaves stdin available for interactive keys.
func (m *Model) logViewerCommand() (*exec.Cmd, func(), error) {
	slot := logSlotMain
	searcher, searchMode := m.logSearcher, m.searchMode
	if m.panelFocus == panelPinnedLogs {
		slot = logSlotPinned
		searcher, searchMode = m.pinnedSearcher, m.pinnedSearchMode
	}
	key, entries, ok := m.logCommandScope(slot)
	if !ok {
		return nil, func() {}, fmt.Errorf("select a service or action first")
	}
	var text strings.Builder
	for _, entry := range entries {
		if entry.Sequence == 0 {
			continue
		}
		plain := readableLog(entry.Raw)
		filter := searchMode == searchFilter && searcher != nil && searcher.HasPattern()
		if key.target.Kind != app.RunKindAction && filter && len(searcher.Search([]string{plain})) == 0 {
			continue
		}
		for _, line := range strings.Split(plain, "\n") {
			if key.target.Kind == app.RunKindAction && filter {
				searchLine := line
				if entry.Source == "stderr" {
					searchLine = "[stderr] " + searchLine
				}
				if len(searcher.Search([]string{searchLine})) == 0 {
					continue
				}
			}
			text.WriteString(line)
			text.WriteByte('\n')
		}
	}
	file, err := os.CreateTemp("", "kranz-log-view-*.log")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	if _, err = file.WriteString(text.String()); err == nil {
		err = file.Close()
	} else {
		_ = file.Close()
	}
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	command := key.command
	process := exec.Command(command.Shell, "-c", command.Command)
	process.Dir = m.workingDirectory
	process.Env = commandEnvironment("KRANZ_LOG_FILE="+file.Name(), "KRANZ_LOG_TARGET="+runTargetLabel(key.target),
		"KRANZ_LOG_SCOPE="+logCommandScopeLabel(key), "KRANZ_LOG_RUN="+strconv.FormatUint(uint64(key.run), 10), "KRANZ_LOG_LINES="+strconv.Itoa(strings.Count(text.String(), "\n")))
	return process, cleanup, nil
}

func (m *Model) openLogViewer() tea.Cmd {
	command, cleanup, err := m.logViewerCommand()
	if err != nil {
		m.addNotification("logs", readableLog(err.Error()), config.LogWarn)
		return nil
	}
	return tea.ExecProcess(command, func(err error) tea.Msg { cleanup(); return logViewerFinishedMsg{err} })
}

func logCommandScopeLabel(key logCommandKey) string {
	if key.mode == runViewSingle {
		return fmt.Sprintf("run #%d", key.run)
	}
	if key.run > 0 {
		return fmt.Sprintf("runs through #%d", key.run)
	}
	return "all runs"
}

func (m *Model) logCommandHint(slot logPanelSlot, target app.RunTarget) string {
	command := m.effectiveLogCommand(target)
	if command.Command == "" {
		return ""
	}
	if command.Mode == config.LogCommandTerminal {
		return " · [Ctrl+G] VIEWER"
	}
	view := m.logCommands[slot]
	if m.logCommandBypass[slot] || view != nil && view.failed {
		return " · ORIGINAL [Ctrl+G]"
	}
	return " · PROCESSED [Ctrl+G]"
}

func (m *Model) emptyLogCommandMessage(slot logPanelSlot, target app.RunTarget) string {
	command := m.effectiveLogCommand(target)
	if command.Command == "" || command.Mode != config.LogCommandStream || m.logCommandBypass[slot] {
		return ""
	}
	view := m.logCommands[slot]
	if view != nil && view.failed {
		return ""
	}
	if view != nil && view.finished {
		return "Log command produced no output; Ctrl+G shows original logs"
	}
	return "Waiting for log command output; Ctrl+G shows original logs"
}

func (m *Model) serviceEmptyLogMessage(slot logPanelSlot, name string) string {
	if message := m.emptyLogCommandMessage(slot, app.ServiceRunTarget(name)); message != "" {
		return message
	}
	return "Output will appear after the service starts"
}

func (m *Model) actionEmptyLogMessage(id config.ActionID) string {
	if message := m.emptyLogCommandMessage(logSlotMain, app.ActionRunTarget(id)); message != "" {
		return message
	}
	return "Press s to run this action"
}

func (m *Model) pinnedEmptyLogMessage(target app.RunTarget) string {
	if message := m.emptyLogCommandMessage(logSlotPinned, target); message != "" {
		return message
	}
	return "No retained output for this snapshot"
}

func (m *Model) processedActionLogRecords(target app.RunTarget, run uint32) []cachedActionLogLine {
	key, _, ok := m.logCommandScope(logSlotMain)
	view := m.logCommands[logSlotMain]
	if !ok || view == nil || view.key != key {
		return nil
	}
	if view.failed {
		return m.cachedActionLogRecords(target, run)
	}
	return view.actionLines
}
