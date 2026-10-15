package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

const selectionMaxBytes = 1 << 20
const selectionMaxLines = 20000

type logPoint struct{ line, offset int }
type selectionLine struct {
	text, prefix string
	entry        config.LogEntry
	selectable   bool
}
type selectionRow struct {
	line   int
	visual logVisualRow
}
type logSelection struct {
	cachedRows                  []selectionRow
	capturedLast                uint64
	restoreTop                  logPoint
	priorFollow, priorPaused    bool
	priorRun                    runSelectionState
	cachedWidth                 int
	cachedWrap                  bool
	panel                       panelFocus
	target                      app.RunTarget
	label, scope, search        string
	lines                       []selectionLine
	anchor, cursor              logPoint
	start                       int
	top                         logPoint
	active, dragging, fullLines bool
	pressPoint                  logPoint
	pressEnd                    int
	pressX, pressY              int
	scrollDirection             int
	scrollQueued                bool
	disconnected                bool
	saving, overwrite, writing  bool
	savePath                    string
}

func (m *Model) selectionActive(panel panelFocus) bool {
	return m.logSelection != nil && m.logSelection.active && m.logSelection.panel == panel
}

func (s *logSelection) rows(width int, wrap bool) []selectionRow {
	if s.cachedRows != nil && s.cachedWidth == width && s.cachedWrap == wrap {
		return s.cachedRows
	}
	var rows []selectionRow
	for i, line := range s.lines {
		for _, r := range projectLogLine(line.text, line.prefix, width, wrap) {
			rows = append(rows, selectionRow{i, r})
		}
	}
	s.cachedRows, s.cachedWidth, s.cachedWrap = rows, width, wrap
	return rows
}
func pointLess(a, b logPoint) bool { return a.line < b.line || a.line == b.line && a.offset < b.offset }
func (s *logSelection) bounds() (logPoint, logPoint) {
	a, b := s.anchor, s.cursor
	if pointLess(b, a) {
		a, b = b, a
	}
	return a, b
}
func (s *logSelection) text() string {
	a, b := s.bounds()
	var lines []string
	for i := a.line; i <= b.line && i < len(s.lines); i++ {
		if i < 0 || !s.lines[i].selectable {
			continue
		}
		line := s.lines[i].text
		lo, hi := 0, len(line)
		if i == a.line {
			lo = a.offset
		}
		if i == b.line {
			hi = b.offset
		}
		lo = min(len(line), max(0, lo))
		hi = min(len(line), max(lo, hi))
		lines = append(lines, line[lo:hi])
	}
	return strings.Join(lines, "\n")
}
func (s *logSelection) selectLine(i int, extend bool) {
	i = max(0, min(i, len(s.lines)-1))
	direction := 1
	if i < s.cursor.line {
		direction = -1
	}
	if i == 0 {
		direction = 1
	}
	if i == len(s.lines)-1 {
		direction = -1
	}
	for !s.lines[i].selectable {
		next := i + direction
		if next < 0 || next >= len(s.lines) {
			return
		}
		i = next
	}
	if !extend {
		s.anchor = logPoint{i, 0}
		s.cursor = logPoint{i, len(s.lines[i].text)}
	} else {
		anchor := s.anchor.line
		if i < anchor {
			s.anchor.offset = len(s.lines[anchor].text)
			s.cursor = logPoint{i, 0}
		} else {
			s.anchor.offset = 0
			s.cursor = logPoint{i, len(s.lines[i].text)}
		}
	}
	s.fullLines = true
}

func (m *Model) captureLogSelection(panel panelFocus) *logSelection {
	m.syncRunTarget()
	target := m.runTarget
	scope := m.runViewLabel()
	run := uint32(0)
	follow, anchor, offset := m.followMode, m.logAnchor, m.logOffset
	searcher, mode := m.logSearcher, m.searchMode
	if m.runMode == runViewSingle {
		run = m.selectedRun
	}
	if panel == panelPinnedLogs {
		var ok bool
		target, ok = m.pinnedRunTarget()
		if !ok {
			return nil
		}
		scope = m.pinnedRunViewLabel()
		if m.pinnedRunMode == runViewSingle {
			run = m.pinnedRun
		}
		follow, anchor, offset = m.pinnedFollow, m.pinnedAnchor, m.pinnedOffset
		searcher, mode = m.pinnedSearcher, m.pinnedSearchMode
	}
	if target.Kind == "" {
		return nil
	}
	if panel == panelPinnedLogs && m.pinnedRunMode == runViewCombined || panel != panelPinnedLogs && m.runMode == runViewCombined {
		scope = "combined"
	}
	s := &logSelection{priorFollow: follow, priorPaused: m.logPaused, priorRun: runSelectionState{m.runMode, m.selectedRun, m.runFollowsLatest}, panel: panel, target: target, label: runTargetLabel(target), scope: scope, search: "unfiltered"}
	if searcher != nil && searcher.HasPattern() {
		s.search = "highlight"
		if mode == searchFilter {
			s.search = "filtered; hidden records omitted"
		}
	}
	entries := m.cachedLogEntries(target)
	if len(entries) > 0 {
		s.capturedLast = entries[len(entries)-1].Sequence
	}
	if panel == panelPinnedLogs {
		entries = m.pinnedEntries(target)
	} else if run > 0 {
		entries = m.entriesForRun(target, run)
	}
	// Include generated action rows for viewport geometry, never for extraction.
	if panel == panelLogs && target.Kind == app.RunKindAction {
		if id, action, state, ok := m.focusedActionDefinition(); ok {
			view := m.actionLogContentView(id, action, state)
			for _, line := range view.prefix {
				if searcher != nil && searcher.HasPattern() && mode == searchFilter && len(searcher.Search([]string{line})) == 0 {
					continue
				}
				s.lines = append(s.lines, selectionLine{text: line})
			}
			if view.marker != "" && run == 0 {
				s.lines = append(s.lines, selectionLine{text: view.marker})
			}
		}
	}
	bytes := 0
	for _, entry := range entries {
		if len(entry.Raw) > selectionMaxBytes {
			m.addNotification("selection", "View exceeds snapshot budget (1 MiB / 20000 lines); choose a single run or narrow the filter", config.LogWarn)
			return nil
		}
		text := readableLog(entry.Raw)
		if target.Kind != app.RunKindAction && searcher != nil && searcher.HasPattern() && mode == searchFilter && len(searcher.Search([]string{text})) == 0 {
			continue
		}
		prefix := ""
		if m.showLogTime && !entry.Timestamp.IsZero() && (panel == panelPinnedLogs || target.Kind != app.RunKindAction) {
			prefix = "[" + entry.Timestamp.Local().Format("15:04:05.000") + "] "
		}
		if target.Kind == app.RunKindAction && entry.Source == "stderr" {
			prefix = "[stderr] " + prefix
		}
		if entry.Source == "kranz" && strings.HasPrefix(text, "[Kranz] ") {
			prefix += "[Kranz] "
			text = strings.TrimPrefix(text, "[Kranz] ")
		}
		for _, line := range strings.Split(text, "\n") {
			if target.Kind == app.RunKindAction && searcher != nil && searcher.HasPattern() && mode == searchFilter && len(searcher.Search([]string{prefix + line})) == 0 {
				continue
			}
			bytes += len(line) + len(prefix)
			if bytes > selectionMaxBytes || len(s.lines) >= selectionMaxLines {
				m.addNotification("selection", "View exceeds snapshot budget (1 MiB / 20000 lines); choose a single run or narrow the filter", config.LogWarn)
				return nil
			}
			s.lines = append(s.lines, selectionLine{line, prefix, entry, entry.Sequence != 0})
		}
	}
	selectable := false
	for _, line := range s.lines {
		selectable = selectable || line.selectable
	}
	if !selectable {
		m.addNotification("selection", "No log text to select", config.LogWarn)
		return nil
	}
	rows := s.rows(m.currentLogContentWidth(), m.wrapLogs)
	height := m.selectionHeight(panel)
	s.start, _ = actionLogViewport(len(rows), height, follow, anchor, offset)
	if len(rows) > 0 {
		s.top = logPoint{rows[s.start].line, max(0, rows[s.start].visual.start)}
	}
	s.restoreTop = s.top
	return s
}
func (m *Model) selectionHeight(panel panelFocus) int {
	if panel == panelPinnedLogs {
		return max(1, m.pinnedLogPanelHeight()-2)
	}
	return max(1, m.currentLogPanelHeight()-2)
}
func (m *Model) beginLogSelection() {
	if m.panelFocus != panelLogs && m.panelFocus != panelPinnedLogs {
		return
	}
	s := m.captureLogSelection(m.panelFocus)
	if s == nil {
		return
	}
	rows := s.rows(m.currentLogContentWidth(), m.wrapLogs)
	i := min(len(rows)-1, s.start+m.selectionHeight(s.panel)-1)
	for i >= 0 && !s.lines[rows[i].line].selectable {
		i--
	}
	if i >= 0 {
		s.selectLine(rows[i].line, false)
	}
	m.logSelection = s
	s.active = true
	if i >= 0 {
		start := max(s.start, i-m.selectionHeight(s.panel)+1)
		s.top = logPoint{rows[start].line, max(0, rows[start].visual.start)}
	}
}
func (m *Model) endLogSelection() {
	s := m.logSelection
	m.logSelection = nil
	if s != nil && s.disconnected {
		m.mode = ModeRuntimeLost
		return
	}
	if s == nil || !s.active {
		return
	}
	if s.panel == panelLogs {
		m.followMode = s.priorFollow
		m.logPaused = s.priorPaused
	} else {
		m.pinnedFollow = s.priorFollow
	}
	if s.priorFollow {
		return
	}
	if s.panel == panelLogs && m.runTarget == s.target {
		m.runMode = s.priorRun.Mode
		m.selectedRun = s.priorRun.Run
		m.runFollowsLatest = false
	}
	fresh := m.captureLogSelection(s.panel)
	if fresh == nil {
		return
	}
	original := s.lines[s.restoreTop.line].entry.Sequence
	part := 0
	for i := s.restoreTop.line - 1; i >= 0 && s.lines[i].entry.Sequence == original; i-- {
		part++
	}
	found := -1
	for i, line := range fresh.lines {
		if line.entry.Sequence == original {
			if part == 0 {
				found = i
				break
			}
			part--
		}
	}
	lost := found < 0
	if lost {
		found = 0
	}
	rows := fresh.rows(m.currentLogContentWidth(), m.wrapLogs)
	desired := 0
	for i, row := range rows {
		if row.line > found {
			break
		}
		if row.line == found {
			desired = i
			if row.visual.end > s.restoreTop.offset {
				break
			}
		}
	}
	total := len(rows)
	offset := max(0, total-m.selectionHeight(s.panel)-desired)
	if s.panel == panelLogs {
		m.logAnchor = total
		m.logOffset = offset
		m.followMode = false
		m.logPaused = s.priorPaused
	} else {
		m.pinnedAnchor = total
		m.pinnedOffset = offset
		m.pinnedFollow = false
	}
	if lost {
		m.addNotification("selection", "Earlier records are no longer available; showing the nearest retained position", config.LogWarn)
	}

}
func (m *Model) selectionViewport(s *logSelection, rows []selectionRow) int {
	// Preserve the source offset at the top when width or wrap changes.
	start := sort.Search(len(rows), func(i int) bool {
		return rows[i].line > s.top.line || rows[i].line == s.top.line && (rows[i].visual.end > s.top.offset || rows[i].visual.start == rows[i].visual.end)
	})
	if start == len(rows) {
		start = max(0, len(rows)-1)
	}
	return min(start, max(0, len(rows)-m.selectionHeight(s.panel)))
}
func (m *Model) renderLogSelection(width, height int) string {
	s := m.logSelection
	rows := s.rows(max(1, width-2), m.wrapLogs)
	s.start = m.selectionViewport(s, rows)
	a, b := s.bounds()
	var visible []string
	for _, row := range rows[s.start:min(len(rows), s.start+max(1, height-2))] {
		var text strings.Builder
		for _, cell := range row.visual.cells {
			selected := s.lines[row.line].selectable && cell.start >= 0 && !pointLess(logPoint{row.line, cell.start}, a) && pointLess(logPoint{row.line, cell.start}, b)
			if selected {
				text.WriteString(LogTextSelectionStyle.Render(cell.text))
			} else {
				text.WriteString(styleLogLine(cell.text))
			}
		}
		visible = append(visible, text.String())
	}
	payload := s.text()
	kind := "fragment"
	if s.fullLines {
		kind = "lines"
	}
	title := fmt.Sprintf("SELECT · frozen · %d lines / %d B · %s · %s · %s", strings.Count(payload, "\n")+1, len(payload), kind, s.label, s.scope)
	if s.disconnected {
		title = strings.Replace(title, "SELECT · frozen", "SELECT · disconnected · frozen", 1)
	} else {
		var available bool
		if s.target.Kind == app.RunKindService {
			_, available = m.cachedService(s.target.Name)
		} else {
			_, available = m.cfg.ResolveAction(s.target.Action)
		}
		if !available {
			title = strings.Replace(title, "SELECT · frozen", "SELECT · source removed · frozen", 1)
		}
		entries := m.cachedLogEntries(s.target)
		if len(entries) > 0 {

			if entries[len(entries)-1].Sequence > s.capturedLast {
				title = strings.Replace(title, "SELECT · frozen", "SELECT · frozen · New logs", 1)
			}
		}
	}
	return renderTitledPanel(m.panelStyle(s.panel), m.panelTitleStyle(s.panel), max(1, width-2), max(1, height-2), title, visible)
}
func (m *Model) selectionFooter() string {
	groups := []string{"[Enter/y] Copy", "[Y] With context", "[Ctrl+S] Save", "[Esc] Done"}
	rows := renderModalShortcutRows(groups, m.width, ContextBarStyle)
	m.notifMu.RLock()
	toast := m.toastMessage
	m.notifMu.RUnlock()
	if toast != "" {
		rows = append(rows, ContextBarStyle.Render(ansi.Truncate(toast, m.width, "…")))
	}
	return strings.Join(rows, "\n")
}
func (m *Model) handleSelectionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.logSelection
	s.dragging = false
	if s.saving {
		return m.handleSelectionSaveKey(msg)
	}
	k := msg.String()
	extend := strings.HasPrefix(k, "shift+")
	base := strings.TrimPrefix(k, "shift+")
	if k == "alt+pgup" || k == "alt+pgdown" {
		extend = true
		base = strings.TrimPrefix(k, "alt+")
	}
	switch k {
	case "esc":
		m.endLogSelection()
	case "enter", "y":
		return m, m.sendClipboard(s.text(), "selection")
	case "Y":
		return m, m.sendClipboard(s.context(), "selection")
	case "ctrl+s":
		s.saving = true
		m.exportInput.SetValue("kranz-selection.log")
		return m, m.exportInput.Focus()
	case "w":
		m.wrapLogs = !m.wrapLogs
	case "a":
		rows := s.rows(m.currentLogContentWidth(), m.wrapLogs)
		start := m.selectionViewport(s, rows)
		end := min(len(rows), start+m.selectionHeight(s.panel))
		for start < end && !s.lines[rows[start].line].selectable {
			start++
		}
		for end > start && !s.lines[rows[end-1].line].selectable {
			end--
		}
		if start < end {
			s.anchor = logPoint{rows[start].line, max(0, rows[start].visual.start)}
			s.cursor = logPoint{rows[end-1].line, rows[end-1].visual.end}
			s.fullLines = false
		}
	case "tab", "shift+tab", "1", "2", "3", "#", "/", "x", "[", "]", "v", "l", "F", "c", "p":
		m.endLogSelection()
		if s.disconnected {
			return m, nil
		}
		return m.handleNormalKeys(msg)
	case "q":
		m.endLogSelection()
		if s.disconnected {
			return m, nil
		}
		return m.handleNormalKeys(msg)
	default:
		i := s.cursor.line
		switch base {
		case "up", "k":
			i--
		case "down", "j":
			i++
		case "pgup":
			i -= m.selectionHeight(s.panel)
		case "pgdown":
			i += m.selectionHeight(s.panel)
		case "home":
			i = 0
		case "end":
			i = len(s.lines) - 1
		default:
			return m, nil
		}
		s.selectLine(i, extend)
		rows := s.rows(m.currentLogContentWidth(), m.wrapLogs)
		index := sort.Search(len(rows), func(i int) bool { return rows[i].line >= s.cursor.line })
		start := m.selectionViewport(s, rows)
		if index < start {
			start = index
		} else if index >= start+m.selectionHeight(s.panel) {
			start = index - m.selectionHeight(s.panel) + 1
		}
		if start < len(rows) {
			s.top = logPoint{rows[start].line, max(0, rows[start].visual.start)}
		}
	}
	return m, nil
}
func (s *logSelection) context() string {
	text := s.text()
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	a, b := s.bounds()
	sources := map[string]bool{}
	var first, last time.Time
	for i := a.line; i <= b.line; i++ {
		line := s.lines[i]
		if !line.selectable {
			continue
		}
		sources[line.entry.Source] = true
		if !line.entry.Timestamp.IsZero() {
			if first.IsZero() {
				first = line.entry.Timestamp
			}
			last = line.entry.Timestamp
		}
	}
	var sourceNames []string
	for name := range sources {
		if name != "" {
			sourceNames = append(sourceNames, name)
		}
	}
	sort.Strings(sourceNames)
	header := fmt.Sprintf("Kranz logs — %s, %s\n", s.label, s.scope)
	if !first.IsZero() {
		header += fmt.Sprintf("Captured: %s–%s\n", first.Format(time.RFC3339Nano), last.Format(time.RFC3339Nano))
	}
	return header + "View: " + s.search + "\nSources: " + strings.Join(sourceNames, ", ") + "\n\n" + fence + "text\n" + text + "\n" + fence
}

// A token is a maximal non-whitespace span; punctuation remains part of it.
func (s *logSelection) selectToken(p logPoint) {
	text := s.lines[p.line].text
	start, end := 0, len(text)
	for i, r := range text {
		if unicode.IsSpace(r) {
			if i < p.offset {
				start = i + len(string(r))
			} else {
				end = i
				break
			}
		}
	}
	s.anchor = logPoint{p.line, start}
	s.cursor = logPoint{p.line, end}
	s.fullLines = false
}

func (m *Model) dashboardFooterHeight() int {
	if m.logSelection != nil && m.logSelection.active {
		return lenSelectionFooter(m)
	}
	return dashboardFooterRows
}
