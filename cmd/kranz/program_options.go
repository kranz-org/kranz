package main

import tea "github.com/charmbracelet/bubbletea"

// dashboardFPS caps how often Bubble Tea's renderer wakes to consider a repaint.
//
// The renderer ticks at this rate whether or not anything changed, and on a
// static screen each tick copies the whole frame to compare it against what is
// already displayed — so the default of 60 burns CPU in proportion to the
// terminal's area for as long as the dashboard sits idle. Measured on a
// 143x43 dashboard with nothing running: 2.29% CPU at 60, 1.72% at 30, 1.46%
// at 15. Thirty is the point where the saving is still most of what is
// available and a frame is delivered within 33ms, which no one perceives.
const dashboardFPS = 30

// dashboardProgramOptions keeps the foreground and attached dashboards on the
// same terminal contract.
func dashboardProgramOptions() []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithReportFocus(),
		tea.WithFPS(dashboardFPS),
	}
}
