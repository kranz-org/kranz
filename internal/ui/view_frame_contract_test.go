package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
)

// The scroll position and the "n–m/total" counter are computed from cached row
// counts for lines nobody has styled yet. If counting ever disagreed with
// rendering, the viewport would silently drift away from the output.
func TestLogRowCountMatchesRenderedRows(t *testing.T) {
	model := newTestModel()
	entries := []config.LogEntry{
		{Sequence: 1, Raw: "short line"},
		{Sequence: 2, Raw: strings.Repeat("a very long word sequence ", 12)},
		{Sequence: 3, Raw: "first segment\nsecond segment\nthird segment"},
		{Sequence: 4, Raw: "ключи и юникод — ширина считается по графемам 🚀🚀🚀"},
		{Sequence: 5, Raw: "ERROR something failed\n\ttrailing indent"},
		{Sequence: 6, Timestamp: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC), Raw: "with a timestamp"},
		{Sequence: 7, Raw: ""},
	}
	for _, wrap := range []bool{false, true} {
		for _, showTime := range []bool{false, true} {
			for _, width := range []int{1, 5, 20, 78, 200} {
				model.wrapLogs, model.showLogTime = wrap, showTime
				for _, entry := range entries {
					counted := model.countLogEntryRows(entry, width)
					rendered := len(model.logEntryVisualRows(entry, width))
					if counted != rendered {
						t.Fatalf("wrap=%v time=%v width=%d seq=%d: counted %d rows, rendered %d",
							wrap, showTime, width, entry.Sequence, counted, rendered)
					}
				}
			}
		}
	}
}

// The cache is keyed by log sequence, so it must be dropped whenever anything
// that changes the answer changes. A stale count would misplace the viewport.
func TestLogRowMetricsInvalidateOnLayoutChange(t *testing.T) {
	model := newTestModel()
	target := model.visibleLogTargets()
	if len(target) == 0 {
		t.Skip("no visible log target")
	}
	first := model.logRowMetricsFor(logSlotMain, target[0], 80)
	if same := model.logRowMetricsFor(logSlotMain, target[0], 80); same != first {
		t.Fatal("an unchanged layout must reuse the cache")
	}
	if resized := model.logRowMetricsFor(logSlotMain, target[0], 120); resized == first {
		t.Fatal("a width change must invalidate the cache")
	}
	model.wrapLogs = !model.wrapLogs
	if wrapped := model.logRowMetricsFor(logSlotMain, target[0], 120); wrapped.wrap != model.wrapLogs {
		t.Fatal("a wrap change must invalidate the cache")
	}
}

// View skips its fitting pass whenever the dashboard reports that it already
// fills the terminal. The saving only materialises if that stays true, so the
// dashboard has to keep assembling an exactly sized frame.
func TestDashboardFillsTerminalExactly(t *testing.T) {
	for _, size := range [][2]int{{64, 14}, {80, 24}, {120, 40}, {200, 60}, {321, 47}} {
		model := newTestModel()
		_, _ = model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for i := range 50 {
			appendTestLog(model, "api", fmt.Sprintf("line %d", i))
		}
		block := model.renderDashboardBlock(model.renderStatusBar())
		if block.width != size[0] || len(block.lines) != size[1] {
			t.Fatalf("%dx%d: dashboard assembled %dx%d", size[0], size[1], block.width, len(block.lines))
		}
		for index, line := range block.lines {
			if rendered := ansi.StringWidth(line); rendered != size[0] {
				t.Fatalf("%dx%d: line %d is %d cells wide", size[0], size[1], index, rendered)
			}
		}
	}
}
