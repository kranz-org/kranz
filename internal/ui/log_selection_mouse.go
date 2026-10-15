package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) logBodyAt(x, y int) (panelFocus, int, int, bool) {
	if x <= m.dashboardLeftWidth() || x >= m.width-1 || y < dashboardHeaderRows || y >= m.height-m.dashboardFooterHeight() {
		return 0, 0, 0, false
	}
	panel := m.logPanelFocusAt(y)
	top := dashboardHeaderRows
	h := m.currentLogPanelHeight()
	if m.hasPinnedRunView() {
		ph, _ := m.logColumnLayout(m.height - 1 - m.dashboardFooterHeight())
		if panel == panelPinnedLogs {
			h = ph
		} else {
			top += ph
		}
	}
	if y <= top || y >= top+h-1 {
		return 0, 0, 0, false
	}
	return panel, x - m.dashboardLeftWidth() - 1, y - top - 1, true
}
func (m *Model) selectionHit(s *logSelection, x, y int) (logPoint, int, bool) {
	panel, cx, cy, ok := m.logBodyAt(x, y)
	if !ok || panel != s.panel {
		return logPoint{}, 0, false
	}
	rows := s.rows(m.currentLogContentWidth(), m.wrapLogs)
	start := s.start
	if s.active {
		start = m.selectionViewport(s, rows)
	}
	index := start + cy
	if index < 0 || index >= len(rows) {
		return logPoint{}, 0, false
	}
	row := rows[index]
	if !s.lines[row.line].selectable {
		return logPoint{}, 0, false
	}
	column := 0
	for _, c := range row.visual.cells {
		if cx >= column && cx < column+c.width {
			if c.start < 0 {
				return logPoint{}, 0, false
			}
			return logPoint{row.line, c.start}, c.end, true
		}
		column += c.width
	}
	return logPoint{}, 0, false
}
func (m *Model) handleLogSelectionMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	s := m.logSelection
	if s != nil && s.saving {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			rendered := m.View()
			for _, binding := range []mouseKeyBinding{{"[Enter] Write file", "enter"}, {"[Enter] Overwrite", "enter"}, {"[Esc] Cancel", "esc"}} {
				if renderedTextHit(rendered, msg.X, msg.Y, binding.label) {
					model, cmd := m.handleSelectionKey(keyMessage(binding.key))
					return model, cmd, true
				}
			}
		}
		return m, nil, true
	}
	if msg.Action == tea.MouseActionRelease {
		if s != nil {
			s.dragging = false
			if !s.active {
				m.logSelection = nil
			}
		}
		return m, nil, s != nil
	}
	if s != nil && s.active {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if msg.Y >= m.height-lenSelectionFooter(m) {
				rendered := m.View()
				for _, binding := range []mouseKeyBinding{{"[Enter/y] Copy", "enter"}, {"[Y] With context", "Y"}, {"[Ctrl+S] Save", "ctrl+s"}, {"[Esc] Done", "esc"}} {
					if renderedTextHit(rendered, msg.X, msg.Y, binding.label) {
						model, cmd := m.handleSelectionKey(keyMessage(binding.key))
						return model, cmd, true
					}
				}
				return m, nil, true
			}
			if msg.X >= 0 && msg.X < m.dashboardLeftWidth() && msg.Y >= dashboardHeaderRows && msg.Y < m.height-m.dashboardFooterHeight() {
				m.endLogSelection()
				if s.disconnected {
					return m, nil, true
				}
				model, cmd := m.handleLeftColumnClick(msg)
				return model, cmd, true
			}
			panel, _, _, ok := m.logBodyAt(msg.X, msg.Y)
			if ok && panel != s.panel {
				m.endLogSelection()
				m.panelFocus = panel
				return m, nil, true
			}
			p, end, hit := m.selectionHit(s, msg.X, msg.Y)
			if hit {
				if msg.Shift {
					if s.fullLines {
						s.selectLine(p.line, true)
					} else if pointLess(p, s.anchor) {
						s.cursor = p
					} else {
						s.cursor = logPoint{p.line, end}
					}
					return m, nil, true
				}
				m.startSelectionGesture(s, p, end, msg)

			}
			return m, nil, true
		}
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			panel, _, _, ok := m.logBodyAt(msg.X, msg.Y)
			if !ok || panel != s.panel {
				return m, nil, true
			}
			rows := s.rows(m.currentLogContentWidth(), m.wrapLogs)
			start := m.selectionViewport(s, rows)
			if msg.Button == tea.MouseButtonWheelUp {
				start = max(0, start-1)
			} else {
				start = min(max(0, len(rows)-m.selectionHeight(s.panel)), start+1)
			}
			if len(rows) > 0 {
				s.top = logPoint{rows[start].line, max(0, rows[start].visual.start)}
			}
			return m, nil, true
		}
	}
	if msg.Action == tea.MouseActionMotion && s != nil && s.dragging {
		direction := m.selectionEdgeDirection(s, msg.X, msg.Y)
		s.scrollDirection = direction
		if direction != 0 && !s.scrollQueued {
			s.active = true
			s.scrollQueued = true
			return m, selectionScrollTick(s), true
		}

		p, end, ok := m.selectionHit(s, msg.X, msg.Y)
		if ok && (msg.X != s.pressX || msg.Y != s.pressY) {
			s.active = true
			s.fullLines = false
			if pointLess(p, s.pressPoint) {
				s.anchor = logPoint{s.pressPoint.line, s.pressEnd}
				s.cursor = p
			} else {
				s.anchor = s.pressPoint
				s.cursor = logPoint{p.line, end}
			}
		}
		return m, nil, true
	}
	if s != nil && s.active {
		return m, nil, true
	}
	if m.mode == ModeNormal && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		panel, _, _, ok := m.logBodyAt(msg.X, msg.Y)
		if !ok {
			return m, nil, false
		}
		s = m.captureLogSelection(panel)
		if s == nil {
			return m, nil, false
		}
		p, end, hit := m.selectionHit(s, msg.X, msg.Y)
		if !hit {
			m.panelFocus = panel
			return m, nil, true
		}
		m.startSelectionGesture(s, p, end, msg)
		m.logSelection = s
		m.panelFocus = panel

		return m, nil, true
	}
	return m, nil, false
}
func lenSelectionFooter(m *Model) int { return 1 + countNewlines(m.selectionFooter()) }
func countNewlines(s string) int {
	n := 0
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}
	return n
}

type logClickState struct {
	panel    panelFocus
	sequence uint64
	offset   int
	at       time.Time
	count    int
}

func (m *Model) startSelectionGesture(s *logSelection, p logPoint, end int, msg tea.MouseMsg) {
	now := time.Now()
	last := m.logClick
	count := 1
	if last.panel == s.panel && last.sequence == s.lines[p.line].entry.Sequence && last.offset == p.offset && now.Sub(last.at) < doubleClickInterval {
		count = last.count + 1
	}
	m.logClick = logClickState{s.panel, s.lines[p.line].entry.Sequence, p.offset, now, count}
	s.anchor, s.cursor = p, p
	s.pressPoint, s.pressEnd = p, end
	s.pressX, s.pressY = msg.X, msg.Y
	s.dragging = true
	s.fullLines = false
	if count == 2 {
		s.active = true
		s.selectToken(p)
	} else if count >= 3 {
		s.active = true
		s.selectLine(p.line, false)
	}
}

type selectionScrollMsg struct{ selection *logSelection }

func selectionScrollTick(s *logSelection) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return selectionScrollMsg{s} })
}
func (m *Model) selectionEdgeDirection(s *logSelection, x, y int) int {
	if x <= m.dashboardLeftWidth() || x >= m.width-1 {
		return 0
	}
	top := dashboardHeaderRows + 1
	if m.hasPinnedRunView() && s.panel == panelLogs {
		ph, _ := m.logColumnLayout(m.height - 1 - m.dashboardFooterHeight())
		top += ph
	}
	bottom := top + m.selectionHeight(s.panel) - 1
	// Only the border and the outer half of the edge row start autoscroll;
	// the interior remains a dead zone with no timer.
	if y < top {
		return -1
	}
	if y > bottom {
		return 1
	}
	return 0
}
func (m *Model) handleSelectionScroll(msg selectionScrollMsg) (tea.Model, tea.Cmd) {
	s := msg.selection
	s.scrollQueued = false
	if m.logSelection != s || !s.active || !s.dragging || s.scrollDirection == 0 {
		return m, nil
	}
	rows := s.rows(m.currentLogContentWidth(), m.wrapLogs)
	start := m.selectionViewport(s, rows)
	next := max(0, min(start+s.scrollDirection, max(0, len(rows)-m.selectionHeight(s.panel))))
	if next == start {
		return m, nil
	}
	s.top = logPoint{rows[next].line, max(0, rows[next].visual.start)}
	index := next
	if s.scrollDirection > 0 {
		index = min(len(rows)-1, next+m.selectionHeight(s.panel)-1)
	}
	row := rows[index]
	if s.lines[row.line].selectable {
		p := logPoint{row.line, max(0, row.visual.start)}
		if pointLess(p, s.pressPoint) {
			s.anchor = logPoint{s.pressPoint.line, s.pressEnd}
			s.cursor = p
		} else {
			s.anchor = s.pressPoint
			s.cursor = logPoint{row.line, row.visual.end}
		}
	}
	s.scrollQueued = true
	return m, selectionScrollTick(s)
}
