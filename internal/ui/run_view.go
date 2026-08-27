package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

func (m *Model) focusedRunTarget() (app.RunTarget, bool) {
	if m.focusedAction != nil {
		return app.ActionRunTarget(*m.focusedAction), true
	}
	if svc := m.FocusedService(); svc != nil {
		return app.ServiceRunTarget(svc.Name), true
	}
	return app.RunTarget{}, false
}

func (m *Model) syncRunTarget() bool {
	target, ok := m.focusedRunTarget()
	if !ok {
		return false
	}
	if target != m.runTarget {
		initial := m.runTarget.Kind == ""
		carry := m.currentRunViewportState()
		if !initial {
			m.saveRunViewport()
		}
		m.runTarget = target
		m.selectedRun = 0
		m.runMode = runViewCombined
		if target.Kind == app.RunKindAction {
			m.runMode = runViewSingle
		}
		if !initial {
			key := m.runViewportKey()
			if _, exists := m.runViewports[key]; !exists {
				m.runViewports[key] = carry
			}
			m.restoreRunViewport()
		}
	}
	if m.runMode == runViewSingle && m.selectedRun == 0 {
		m.selectedRun = latestRun(m.runsForTarget(target))
	}
	return true
}

func (m *Model) runsForTarget(target app.RunTarget) []app.RunSummary {
	all := m.app.Runs()
	runs := make([]app.RunSummary, 0)
	for _, run := range all {
		if run.Target == target {
			runs = append(runs, run)
		}
	}
	return runs
}

func latestRun(runs []app.RunSummary) uint32 {
	if len(runs) == 0 {
		return 0
	}
	return runs[len(runs)-1].Run
}

func (m *Model) toggleRunView() {
	if !m.syncRunTarget() {
		return
	}
	m.saveRunViewport()
	if m.runMode == runViewCombined {
		m.runMode = runViewSingle
		m.selectedRun = latestRun(m.runsForTarget(m.runTarget))
	} else {
		m.runMode = runViewCombined
	}
	m.restoreRunViewport()
}

func (m *Model) selectLatestRun() {
	if !m.syncRunTarget() {
		return
	}
	m.saveRunViewport()
	m.runMode = runViewSingle
	m.selectedRun = latestRun(m.runsForTarget(m.runTarget))
	m.restoreRunViewport()
}

func (m *Model) moveRun(direction int, failedOnly bool) {
	if !m.syncRunTarget() {
		return
	}
	runs := m.runsForTarget(m.runTarget)
	if len(runs) == 0 {
		return
	}
	current := m.selectedRun
	if m.runMode != runViewSingle || current == 0 {
		current = latestRun(runs)
	}
	index := len(runs) - 1
	for i := range runs {
		if runs[i].Run == current {
			index = i
			break
		}
	}
	for next := index + direction; next >= 0 && next < len(runs); next += direction {
		if !failedOnly || runFailed(runs[next]) {
			m.saveRunViewport()
			m.runMode, m.selectedRun = runViewSingle, runs[next].Run
			m.restoreRunViewport()
			return
		}
	}
}

func runFailed(run app.RunSummary) bool {
	return run.ExitCode != nil && *run.ExitCode != 0 || strings.Contains(strings.ToLower(run.Status), "fail")
}

func (m *Model) resetRunViewport() {
	m.logOffset, m.logAnchor, m.followMode, m.logPaused = 0, 0, true, false
	m.currentMatch, m.searchMode = -1, searchFilter
	if m.logSearcher != nil {
		_ = m.logSearcher.SetPattern("")
	}
}

func (m *Model) runViewportKey() runViewportKey {
	run := uint32(0)
	if m.runMode == runViewSingle {
		run = m.selectedRun
	}
	return runViewportKey{Target: m.runTarget, Mode: m.runMode, Run: run}
}

func (m *Model) saveRunViewport() {
	if m.runTarget.Kind == "" {
		return
	}
	m.runViewports[m.runViewportKey()] = m.currentRunViewportState()
}

func (m *Model) currentRunViewportState() runViewportState {
	pattern := ""
	if m.logSearcher != nil {
		pattern = m.logSearcher.Pattern()
	}
	return runViewportState{Offset: m.logOffset, Anchor: m.logAnchor, Follow: m.followMode,
		Paused: m.logPaused, Pattern: pattern, SearchMode: m.searchMode, CurrentMatch: m.currentMatch}
}

func (m *Model) restoreRunViewport() {
	state, ok := m.runViewports[m.runViewportKey()]
	if !ok {
		m.resetRunViewport()
		return
	}
	m.logOffset, m.logAnchor, m.followMode, m.logPaused = state.Offset, state.Anchor, state.Follow, state.Paused
	m.searchMode, m.currentMatch = state.SearchMode, state.CurrentMatch
	if m.logSearcher != nil {
		_ = m.logSearcher.SetPattern(state.Pattern)
	}
}

func (m *Model) entriesForRun(target app.RunTarget, run uint32) []config.LogEntry {
	var entries []config.LogEntry
	if target.Kind == app.RunKindService {
		entries = m.app.Logs(target.Name)
	} else {
		entries = m.app.ActionLogs(target.Action)
	}
	if run == 0 {
		return entries
	}
	filtered := make([]config.LogEntry, 0)
	for _, entry := range entries {
		if entry.Run == run {
			filtered = append(filtered, entry)
		}
	}
	if summary, ok := m.runSummary(target, run); ok && summary.Output.MissingLines > 0 {
		marker := fmt.Sprintf("[Kranz] Output truncated · missing %d lines / %d bytes", summary.Output.MissingLines, summary.Output.MissingBytes)
		filtered = append([]config.LogEntry{{Raw: marker, Text: marker, Source: "kranz", Run: run}}, filtered...)
	}
	return filtered
}

func (m *Model) runSummary(target app.RunTarget, run uint32) (app.RunSummary, bool) {
	for _, summary := range m.runsForTarget(target) {
		if summary.Run == run {
			return summary, true
		}
	}
	return app.RunSummary{}, false
}

func (m *Model) runViewLabel() string {
	if !m.syncRunTarget() || m.runMode == runViewCombined {
		return "ALL"
	}
	latest := latestRun(m.runsForTarget(m.runTarget))
	label := fmt.Sprintf("RUN #%d", m.selectedRun)
	if m.selectedRun == latest {
		label += " · LIVE/LATEST"
	} else if latest > m.selectedRun {
		label += fmt.Sprintf(" · %d NEWER", latest-m.selectedRun)
	}
	return label
}

func runTargetLabel(target app.RunTarget) string {
	if target.Kind == app.RunKindService {
		return target.Name
	}
	return target.Action.Owner + "/" + target.Action.Name
}

func (m *Model) pinnedRunViewLabel() string {
	if m.pinnedRunMode == runViewSingle {
		return fmt.Sprintf("RUN #%d · SNAPSHOT", m.pinnedRun)
	}
	return fmt.Sprintf("ALL ≤ #%d · SNAPSHOT", m.pinnedRun)
}

func (m *Model) pinnedEntries(target app.RunTarget) []config.LogEntry {
	entries := m.entriesForRun(target, 0)
	if m.pinnedRun == 0 {
		return entries
	}
	if m.pinnedRunMode == runViewSingle {
		return m.entriesForRun(target, m.pinnedRun)
	}
	filtered := make([]config.LogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Run > 0 && entry.Run <= m.pinnedRun {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func (m *Model) renderPinnedActionRunPanel(target app.RunTarget, width, height int) string {
	contentWidth, contentHeight := max(1, width-2), max(1, height-2)
	title := "[3] PINNED ACTION" + ContextBarStyle.Render(" │ ") + ServiceNameStyle.Render(runTargetLabel(target)) +
		" " + RunningBadgeStyle.Render(m.pinnedRunViewLabel())
	entries := m.pinnedEntries(target)
	if len(entries) == 0 {
		return renderTitledPanel(m.panelStyle(panelPinnedLogs), m.panelTitleStyle(panelPinnedLogs), contentWidth, contentHeight, title,
			[]string{"", ContextBarStyle.Render("No retained output for this snapshot")})
	}
	indices := make([]int, len(entries))
	for index := range indices {
		indices[index] = index
	}
	lines := logEntryLines(entries)
	matches := []int(nil)
	if m.pinnedSearcher != nil && m.pinnedSearcher.HasPattern() {
		matches = m.pinnedSearcher.Search(lines)
		if m.pinnedSearchMode == searchFilter {
			indices = append([]int(nil), matches...)
		}
		title += SearchInputStyle.Render(fmt.Sprintf("  /%s/ · %d", m.pinnedSearcher.Pattern(), len(matches)))
	}
	matchSet := make(map[int]bool, len(matches))
	for _, index := range matches {
		matchSet[index] = true
	}
	rows := make([]string, 0, len(indices))
	for _, index := range indices {
		entry := entries[index]
		line := m.displayLogEntry(entry)
		if entry.Source == "stderr" {
			line = "[stderr] " + line
		}
		styled := styleLogLine(line)
		visual := []string{ansi.Truncate(styled, contentWidth, "…")}
		if m.wrapLogs {
			visual = strings.Split(ansi.Hardwrap(styled, contentWidth, true), "\n")
		}
		if m.pinnedSearchMode == searchHighlight && matchSet[index] {
			for visualIndex := range visual {
				visual[visualIndex] = SearchHighlightStyle.Render(preserveStyleAfterReset(visual[visualIndex], SearchHighlightStyle))
			}
		}
		rows = append(rows, visual...)
	}
	maxStart := max(0, len(rows)-contentHeight)
	start := maxStart
	if !m.pinnedFollow {
		anchor := min(len(rows), max(0, m.pinnedAnchor))
		start = max(0, max(0, anchor-contentHeight)-m.pinnedOffset)
	}
	end := min(len(rows), start+contentHeight)
	return renderTitledPanel(m.panelStyle(panelPinnedLogs), m.panelTitleStyle(panelPinnedLogs), contentWidth, contentHeight, title, rows[start:end])
}

func (m *Model) openRunList() {
	if !m.syncRunTarget() || len(m.filteredRunList()) == 0 {
		return
	}
	m.runListCursor = 0
	runs := m.filteredRunList()
	for index := range runs {
		if runs[index].Run == m.selectedRun {
			m.runListCursor = index
		}
	}
	m.mode = ModeRunList
}

func (m *Model) filteredRunList() []app.RunSummary {
	runs := m.runsForTarget(m.runTarget)
	if m.runStatusFilter == "" || m.runStatusFilter == "all" {
		return runs
	}
	filtered := make([]app.RunSummary, 0)
	for _, run := range runs {
		if m.runStatusFilter == "failed" && runFailed(run) || strings.EqualFold(run.Status, m.runStatusFilter) {
			filtered = append(filtered, run)
		}
	}
	return filtered
}

func (m *Model) handleRunListKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	runs := m.filteredRunList()
	switch {
	case key.Matches(msg, m.keys.Up):
		m.runListCursor = max(0, m.runListCursor-1)
	case key.Matches(msg, m.keys.Down):
		m.runListCursor = min(max(0, len(runs)-1), m.runListCursor+1)
	case msg.String() == "tab":
		filters := []string{"all", "running", "succeeded", "failed", "stopped"}
		index := 0
		for i := range filters {
			if filters[i] == m.runStatusFilter {
				index = i
			}
		}
		m.runStatusFilter = filters[(index+1)%len(filters)]
		m.runListCursor = 0
	case msg.String() == "enter":
		if len(runs) > 0 {
			m.saveRunViewport()
			m.selectedRun, m.runMode = runs[m.runListCursor].Run, runViewSingle
			m.restoreRunViewport()
		}
		m.mode = ModeNormal
	case msg.String() == "esc", msg.String() == "q", msg.String() == "v":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) renderRunListView() string {
	runs := m.filteredRunList()
	filter := m.runStatusFilter
	if filter == "" {
		filter = "all"
	}
	lines := []string{ModalTitleStyle.Render(" Run history "), "", ContextBarStyle.Render("  Filter: " + filter + " · Tab changes filter"), ""}
	if len(runs) == 0 {
		lines = append(lines, "  No runs match this filter")
	}
	for index, run := range runs {
		exit := "-"
		if run.ExitCode != nil {
			exit = fmt.Sprint(*run.ExitCode)
		}
		duration := time.Since(run.StartedAt)
		if !run.FinishedAt.IsZero() {
			duration = run.FinishedAt.Sub(run.StartedAt)
		}
		initiator := run.Surface
		if run.ClientLabel != "" {
			initiator += ":" + run.ClientLabel
		}
		line := fmt.Sprintf("  #%d  %-10s  %s  %8s  exit %-3s  %-18s  %-18s  %s", run.Run, run.Status,
			run.StartedAt.Local().Format("15:04:05"), duration.Round(time.Millisecond), exit, run.StartReason, initiator, run.Output.State)
		if index == m.runListCursor {
			line = SelectionStyle.Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", renderModalShortcuts("  [↑/↓] Select  [Enter] Open run  [Tab] Filter  [Esc] Close", lipgloss.NewStyle().Foreground(ColorDim)))
	return m.placeOverlay(renderFlushModal(strings.Join(lines, "\n")))
}
