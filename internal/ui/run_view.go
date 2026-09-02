package ui

import (
	"fmt"
	"slices"
	"sort"
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
		m.runFollowsLatest = false
		if target.Kind == app.RunKindAction {
			m.runMode = runViewSingle
			m.runFollowsLatest = true
		}
		if !initial {
			key := m.runViewportKey()
			if _, exists := m.runViewports[key]; !exists {
				m.runViewports[key] = carry
			}
			m.restoreRunViewport()
		}
	}
	if m.runMode == runViewSingle && (m.selectedRun == 0 || m.runFollowsLatest) {
		latest := latestRun(m.runsForTarget(target))
		if latest != m.selectedRun {
			if m.selectedRun > 0 {
				m.saveRunViewport()
			}
			m.selectedRun = latest
			m.restoreRunViewport()
		}
	}
	return true
}

func (m *Model) runsForTarget(target app.RunTarget) []app.RunSummary {
	runs := make([]app.RunSummary, 0)
	for _, run := range m.runs {
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
		m.runFollowsLatest = true
	} else {
		m.runMode = runViewCombined
		m.runFollowsLatest = false
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
	m.runFollowsLatest = true
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
			m.runFollowsLatest = runs[next].Run == latestRun(runs)
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
	entries := m.cachedLogEntries(target)
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

// runViewLabel names the history position. "RUN #2/3" carries the identity,
// the distance from the newest run, and the fact that a newer one exists, so
// the words LATEST, HISTORY, and "N NEWER" that used to spell those out are
// three restatements of one fraction.
func (m *Model) runViewLabel() string {
	if !m.syncRunTarget() || m.runMode == runViewCombined {
		return "ALL RUNS"
	}
	latest := latestRun(m.runsForTarget(m.runTarget))
	switch {
	case latest == 0:
		return "NO RUNS YET"
	case m.selectedRun > latest:
		// Defensive: a fraction whose numerator exceeds its denominator would
		// read as a bug rather than as a position.
		return fmt.Sprintf("RUN #%d", m.selectedRun)
	case m.selectedRun == latest:
		return fmt.Sprintf("RUN #%d/%d", m.selectedRun, latest)
	}
	return fmt.Sprintf("RUN #%d/%d · [L] LATEST", m.selectedRun, latest)
}

func runTargetLabel(target app.RunTarget) string {
	if target.Kind == app.RunKindService {
		return target.Name
	}
	return target.Action.Owner + "/" + target.Action.Name
}

func (m *Model) pinnedRunViewLabel() string {
	if m.pinnedRunMode == runViewSingle {
		return fmt.Sprintf("FROZEN · RUN #%d", m.pinnedRun)
	}
	return fmt.Sprintf("FROZEN · RUNS ≤ #%d", m.pinnedRun)
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
	title := "[3] PINNED ACTION [Shift+3 UNPIN]" + ContextBarStyle.Render(" │ ") + ServiceNameStyle.Render(runTargetLabel(target)) +
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
	if !m.syncRunTarget() || len(m.runsForTarget(m.runTarget)) == 0 {
		return
	}
	m.normalizeRunStatusFilter()
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
	if m.runStatusFilter == "" || m.runStatusFilter == runFilterAll {
		return runs
	}
	filtered := make([]app.RunSummary, 0)
	for _, run := range runs {
		if m.runStatusFilter == runFilterFailed && runFailed(run) || strings.EqualFold(run.Status, m.runStatusFilter) {
			filtered = append(filtered, run)
		}
	}
	return filtered
}

const (
	runFilterAll    = "all"
	runFilterFailed = "failed"
)

// runStatusFilters lists only the filters that actually divide the focused
// target's history. Two things made the old fixed cycle useless. Services and
// actions use disjoint status vocabularies — a service that exits cleanly is
// "stopped" and never "succeeded" — so half the cycle was dead on whichever
// kind was focused. And a filter every run matches narrows nothing: on three
// successful actions, "succeeded" showed the same three rows as "all".
// A filter earns its place only by selecting a proper, non-empty subset.
func (m *Model) runStatusFilters() []string {
	runs := m.runsForTarget(m.runTarget)
	candidates := make([]string, 0, 5)
	if slices.ContainsFunc(runs, runFailed) {
		candidates = append(candidates, runFilterFailed)
	}
	statuses := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, run := range runs {
		status := strings.ToLower(strings.TrimSpace(run.Status))
		if status == "" || status == runFilterFailed || seen[status] {
			continue
		}
		seen[status] = true
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	candidates = append(candidates, statuses...)

	filters := []string{runFilterAll}
	for _, candidate := range candidates {
		if matched := m.runsMatchingFilter(runs, candidate); matched > 0 && matched < len(runs) {
			filters = append(filters, candidate)
		}
	}
	return filters
}

func (m *Model) runsMatchingFilter(runs []app.RunSummary, filter string) int {
	matched := 0
	for _, run := range runs {
		if filter == runFilterFailed && runFailed(run) || strings.EqualFold(run.Status, filter) {
			matched++
		}
	}
	return matched
}

// normalizeRunStatusFilter drops a filter that the current history can no
// longer offer, so reopening the list after the runs changed never lands on a
// filter whose only possible result is "No runs match this filter".
func (m *Model) normalizeRunStatusFilter() {
	if !slices.Contains(m.runStatusFilters(), m.runStatusFilter) {
		m.runStatusFilter = runFilterAll
	}
}

func (m *Model) handleRunListKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	runs := m.filteredRunList()
	switch {
	case key.Matches(msg, m.keys.Up):
		m.runListCursor = max(0, m.runListCursor-1)
	case key.Matches(msg, m.keys.Down):
		m.runListCursor = min(max(0, len(runs)-1), m.runListCursor+1)
	case msg.String() == "tab":
		filters := m.runStatusFilters()
		index := max(0, slices.Index(filters, m.runStatusFilter))
		m.runStatusFilter = filters[(index+1)%len(filters)]
		m.runListCursor = 0
	case msg.String() == "enter":
		if len(runs) > 0 {
			m.saveRunViewport()
			m.selectedRun, m.runMode = runs[m.runListCursor].Run, runViewSingle
			m.runFollowsLatest = m.selectedRun == latestRun(m.runsForTarget(m.runTarget))
			m.restoreRunViewport()
		}
		m.mode = ModeNormal
	case msg.String() == "d":
		if len(runs) > 0 {
			run := runs[min(m.runListCursor, len(runs)-1)]
			if run.Live {
				m.addNotification(runTargetLabel(run.Target), fmt.Sprintf("Run #%d is still active and cannot be deleted", run.Run), config.LogError)
			} else {
				m.deleteTarget, m.deleteRun, m.mode = run.Target, run.Run, ModeConfirmDeleteRun
			}
		}
	case msg.String() == "esc", msg.String() == "q", msg.String() == "v":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) renderRunListView() string {
	runs := m.filteredRunList()
	lines := []string{ModalTitleStyle.Render(" Run history · " + runTargetLabel(m.runTarget)), ""}
	// Retention is an exception report, not a permanent header. While nothing
	// has been lost, the budgets only told the user that nothing had been lost,
	// in ninety characters. They appear when they explain a gap.
	if notice := m.runRetentionNotice(); notice != "" {
		lines = append(lines, ContextBarStyle.Render("  "+notice), "")
	}
	if len(runs) == 0 {
		lines = append(lines, "  No runs match this filter")
	} else {
		lines = append(lines, DetailLabelStyle.Render(fmt.Sprintf("  %-5s %-10s  %-8s  %8s  %-8s  %-18s  %-18s  %s",
			"RUN", "STATUS", "START", "DURATION", "EXIT", "REASON", "INITIATOR", "OUTPUT")))
	}
	// A target retains up to RunRetention().MaxRuns summaries, far more than any
	// terminal can show. Without a window the modal grew past m.height, the
	// overlay clipped the bottom, and the cursor and the shortcut footer both
	// vanished — the list looked frozen while the selection kept moving.
	start, visible, windowed := runListWindow(len(runs), m.runListCursor, m.runListCapacity(len(lines)))
	for index := start; index < start+visible; index++ {
		run := runs[index]
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
		line := fmt.Sprintf("  %-5s %-10s  %-8s  %8s  %-8s  %-18s  %-18s  %s", fmt.Sprintf("#%d", run.Run), run.Status,
			run.StartedAt.Local().Format("15:04:05"), duration.Round(time.Millisecond), exit, run.StartReason, initiator, run.Output.State)
		if index == m.runListCursor {
			line = SelectionStyle.Render(line)
		}
		lines = append(lines, line)
	}
	if windowed {
		lines = append(lines, "  "+ContextBarStyle.Render(fmt.Sprintf("%d/%d", m.runListCursor+1, len(runs))))
	}
	// The filter belongs with the key that changes it. As its own header line it
	// spent a row restating "all" on the common path where nothing is filtered.
	shortcuts := "  [↑/↓] Select  [Enter] Open run  [d] Delete"
	if filters := m.runStatusFilters(); len(filters) > 1 {
		filter := m.runStatusFilter
		if filter == "" {
			filter = runFilterAll
		}
		shortcuts += "  [Tab] " + filter
	}
	lines = append(lines, "", renderModalShortcuts(shortcuts+"  [Esc] Close", lipgloss.NewStyle().Foreground(ColorDim)))
	return m.placeOverlay(renderFlushModal(strings.Join(lines, "\n")))
}

// runRetentionNotice describes what this target has already lost, and returns
// empty while it has lost nothing. The budgets are named only alongside a real
// gap, where they are the explanation for it rather than trivia.
func (m *Model) runRetentionNotice() string {
	var boundary app.RunRetentionBoundary
	for _, candidate := range m.app.RunRetention() {
		if candidate.Target == m.runTarget {
			boundary = candidate
			break
		}
	}
	parts := make([]string, 0, 2)
	if boundary.EvictedRuns > 0 {
		parts = append(parts, fmt.Sprintf("%d older %s dropped · oldest kept #%d of %d",
			boundary.EvictedRuns, pluralRuns(boundary.EvictedRuns), boundary.OldestRetainedRun, boundary.MaxRuns))
	}
	var missingLines uint64
	incomplete := 0
	for _, run := range m.runsForTarget(m.runTarget) {
		if run.Output.MissingLines > 0 {
			incomplete++
			missingLines += run.Output.MissingLines
		}
	}
	if incomplete > 0 {
		parts = append(parts, fmt.Sprintf("%d %s missing output · %d lines lost to the %s buffer",
			incomplete, pluralRuns(uint64(incomplete)), missingLines, formatBytes(boundary.MaxBytes)))
	}
	return strings.Join(parts, "   ")
}

func pluralRuns(count uint64) string {
	if count == 1 {
		return "run"
	}
	return "runs"
}

// formatBytes keeps a budget readable: "4 MB" is a size, "4194304 bytes" is a
// number the reader has to divide before it means anything.
func formatBytes(bytes uint64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.3g GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.3g MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.3g KB", float64(bytes)/(1<<10))
	}
	return fmt.Sprintf("%d B", bytes)
}

// runListCapacity is the number of run rows the modal can show once the header
// rows already collected, the trailing blank and shortcut rows, and the modal's
// own vertical padding are subtracted from the terminal height.
func (m *Model) runListCapacity(headerLines int) int {
	const trailingRows = 2 // the blank separator and the shortcut footer
	return max(1, m.height-modalVerticalChrome-headerLines-trailingRows)
}

// runListWindow centres a capacity-sized window on the cursor and reports
// whether the list had to be windowed at all. When it did, one row is given
// back to the position indicator so the footer keeps its place.
func runListWindow(total, cursor, capacity int) (start, visible int, windowed bool) {
	if total <= capacity {
		return 0, total, false
	}
	visible = max(1, capacity-1) // the position indicator row
	start = max(0, min(cursor-visible/2, total-visible))
	return start, visible, true
}

func (m *Model) renderConfirmDeleteRunView() string {
	identity := fmt.Sprintf("%s#%d", runTargetLabel(m.deleteTarget), m.deleteRun)
	return m.placeOverlay(renderConfirmationModal(
		fmt.Sprintf("Delete %s from run history?", identity),
		[]string{"This removes the retained summary and output.", "The transition journal is kept. This cannot be undone."},
		"[Enter] Delete  [Esc] Cancel",
	))
}

func (m *Model) handleConfirmDeleteRunKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "y":
		m.confirmDeleteRun()
	case "esc", "n", "q":
		m.cancelDeleteRun()
	}
	return m, nil
}

func (m *Model) cancelDeleteRun() {
	m.deleteTarget, m.deleteRun, m.mode = app.RunTarget{}, 0, ModeRunList
}

func (m *Model) confirmDeleteRun() {
	target, run := m.deleteTarget, m.deleteRun
	if _, err := m.app.DeleteRun(target, run); err != nil {
		m.addNotification(runTargetLabel(target), err.Error(), config.LogError)
		m.cancelDeleteRun()
		return
	}
	m.refreshRunSummaries()
	m.logEntries[target] = slices.DeleteFunc(m.logEntries[target], func(entry config.LogEntry) bool { return entry.Run == run })
	m.actionLogLines[target] = slices.DeleteFunc(m.actionLogLines[target], func(line cachedActionLogLine) bool { return line.run == run })
	delete(m.actionRunLogLines[target], run)
	for key := range m.runViewports {
		if key.Target == target && key.Run == run {
			delete(m.runViewports, key)
		}
	}
	if m.pinnedTarget == target && m.pinnedRun == run {
		m.pinnedLog, m.pinnedTarget, m.pinnedRun = "", app.RunTarget{}, 0
		m.pinnedRunMode, m.pinnedOffset, m.pinnedAnchor, m.pinnedMatch = runViewCombined, 0, 0, -1
	}
	if m.runTarget == target && m.selectedRun == run {
		m.selectedRun = latestRun(m.runsForTarget(target))
		m.runFollowsLatest = true
	}
	runs := m.filteredRunList()
	m.runListCursor = min(m.runListCursor, max(0, len(runs)-1))
	m.addNotification(runTargetLabel(target), fmt.Sprintf("Run #%d deleted", run), config.LogInfo)
	m.deleteTarget, m.deleteRun, m.mode = app.RunTarget{}, 0, ModeRunList
}
