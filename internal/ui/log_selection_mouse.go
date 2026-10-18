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
	return m.selectionPointAt(s, x, y, false)
}

// Gestures may start and end in padding. Map it to a source boundary without
// copying generated prefixes or the panel's empty screen rows.
func (m *Model) selectionPointAt(s *logSelection, x, y int, clampEnd bool) (logPoint, int, bool) {
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
	if index < 0 || len(rows) == 0 || index >= len(rows) && !clampEnd {
		return logPoint{}, 0, false
	}
	if index >= len(rows) {
		index = len(rows) - 1
		for index >= 0 && !s.lines[rows[index].line].selectable {
			index--
		}
		if index < 0 {
			return logPoint{}, 0, false
		}
		row := rows[index]
		end := max(0, row.visual.end)
		return logPoint{row.line, end}, end, true
	}
	row := rows[index]
	if !s.lines[row.line].selectable {
		return logPoint{}, 0, false
	}
	column, sourceEnd := 0, -1
	for _, c := range row.visual.cells {
		if cx >= column && cx < column+c.width {
			if c.start < 0 {
				if clampEnd {
					offset := max(0, row.visual.start)
					if sourceEnd >= 0 {
						offset = sourceEnd
					}
					return logPoint{row.line, offset}, offset, true
				}
				return logPoint{}, 0, false
			}
			return logPoint{row.line, c.start}, c.end, true
		}
		column += c.width
		if c.end >= 0 {
			sourceEnd = c.end
		}
	}
	if clampEnd {
		end := max(0, row.visual.end)
		return logPoint{row.line, end}, end, true
	}
	return logPoint{}, 0, false
}
func (m *Model) handleLogSelectionMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	s := m.logSelection
	if msg.Action == tea.MouseActionRelease {
		if s != nil {
			// A plain click collapses the range at press, then restores the live
			// or browsed view at release. A drag or repeated click keeps it.
			if !s.active {
				m.logSelection = nil
			} else if s.dragging && m.logClick.count <= 1 && s.anchor == s.cursor {
				m.endLogSelection()
			}
			s.dragging = false
		}
		return m, nil, s != nil
	}
	if s != nil && s.active {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if msg.Y >= m.height-lenSelectionFooter(m) {
				rendered := m.View()
				for _, binding := range []mouseKeyBinding{{"[Enter] Copy", "enter"}, {"[Shift+A] All", "A"}, {"[a] Viewport", "a"}, {"[Esc] Done", "esc"}} {
					if renderedTextHit(rendered, msg.X, msg.Y, binding.label) {
						model, cmd := m.handleSelectionKey(keyMessage(binding.key))
						return model, cmd, true
					}
				}
				m.endLogSelection()
				m.logClick = logClickState{}
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
				if s.disconnected {
					return m, nil, true
				}
				return m.handleLogSelectionMouse(msg)
			}
			p, end, hit := m.selectionPointAt(s, msg.X, msg.Y, true)
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
			} else {
				m.endLogSelection()
				m.logClick = logClickState{}
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
				s.scrollTo(logPoint{rows[start].line, max(0, rows[start].visual.start)})
			}
			return m, nil, true
		}
	}
	if msg.Action == tea.MouseActionMotion && s != nil && s.dragging {
		direction := m.selectionEdgeDirection(s, msg.X, msg.Y)
		s.scrollDirection = direction
		if direction != 0 && !s.scrollQueued {
			m.logClick = logClickState{}
			s.active = true
			s.scrollQueued = true
			return m, selectionScrollTick(s), true
		}

		p, end, ok := m.selectionPointAt(s, msg.X, msg.Y, true)
		if ok && (msg.X != s.pressX || msg.Y != s.pressY) {
			// A drag is not the first click of a double/triple click. Keeping
			// its press here made a quick clearing click select a token instead.
			m.logClick = logClickState{}
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
		p, end, hit := m.selectionPointAt(s, msg.X, msg.Y, true)
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
	line     int
	offset   int
	at       time.Time
	count    int
}

func (m *Model) startSelectionGesture(s *logSelection, p logPoint, end int, msg tea.MouseMsg) {
	now := time.Now()
	last := m.logClick
	count := 1
	_, _, textHit := m.selectionHit(s, msg.X, msg.Y)
	if textHit && last.count < 3 && last.panel == s.panel && last.sequence == s.lines[p.line].entry.Sequence && last.line == p.line && last.offset == p.offset && now.Sub(last.at) < doubleClickInterval {
		count = last.count + 1
	}
	m.logClick = logClickState{panel: s.panel, sequence: s.lines[p.line].entry.Sequence, line: p.line, offset: p.offset, at: now, count: count}
	if !textHit {
		m.logClick = logClickState{}
	}
	s.anchor, s.cursor = p, p
	s.pressPoint, s.pressEnd = p, end
	s.pressX, s.pressY = msg.X, msg.Y
	s.dragging = true
	s.scrollDirection = 0
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
	// Only positions beyond the body start autoscroll; body rows remain a
	// dead zone so the pointer can stop on the first or last visible line.
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
	s.scrollTo(logPoint{rows[next].line, max(0, rows[next].visual.start)})
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
