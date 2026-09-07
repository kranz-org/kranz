package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

func rowFor(id, name string, startedAt time.Time, current bool, state kranzruntime.SessionState) runtimeRow {
	row := runtimeRow{
		Record: kranzruntime.SessionRecord{
			SessionMetadata: kranzruntime.SessionMetadata{ID: id, Name: name, StartedAt: startedAt},
			State:           state,
		},
		IsCurrent: current,
	}
	switch state {
	case kranzruntime.SessionRunning:
		row.Selectable = !current
	case kranzruntime.SessionIncompatible:
		row.Reason = "This runtime speaks a different protocol version. Update Kranz to attach to it."
	case kranzruntime.SessionUnreachable:
		row.Reason = "This runtime is registered but is not answering. The list keeps retrying it."
	}
	return row
}

// TestSwitcherShowsWhyTheSelectedRowCannotBeSelected covers PRD 3.2's
// requirement that an unavailable runtime stays visible *and* explains why:
// the status column only fits the state word, so the reason belongs on
// screen too rather than only in the row struct.
func TestSwitcherShowsWhyTheSelectedRowCannotBeSelected(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 24, true
	model.mode = ModeRuntimeSwitcher
	incompatible := rowFor("bad", "old-runtime", time.Now(), false, kranzruntime.SessionIncompatible)
	available := rowFor("ok", "new-runtime", time.Now(), false, kranzruntime.SessionRunning)
	model.switcherRows = []runtimeRow{incompatible, available}

	model.switcherCursor = 0
	plain := ansi.Strip(model.renderRuntimeSwitcherView())
	if !strings.Contains(plain, "Update Kranz to attach to it") {
		t.Fatalf("switcher does not explain why the selected row is disabled:\n%s", plain)
	}

	// A selectable row has nothing to explain, so the line goes away again
	// instead of leaving a stale reason under the list.
	model.switcherCursor = 1
	plain = ansi.Strip(model.renderRuntimeSwitcherView())
	if strings.Contains(plain, "Update Kranz to attach to it") {
		t.Fatalf("a disabled row's reason survived moving to a selectable row:\n%s", plain)
	}
}

func TestSortRuntimeRowsPutsCurrentFirstThenNewestThenStableTiebreak(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []runtimeRow{
		rowFor("z-id", "zeta", base, false, kranzruntime.SessionRunning),
		rowFor("a-id", "alpha", base.Add(time.Hour), true, kranzruntime.SessionRunning),
		rowFor("b-id", "beta", base.Add(2*time.Hour), false, kranzruntime.SessionRunning),
		rowFor("c-id", "charlie-2", base, false, kranzruntime.SessionRunning), // ties zeta's StartedAt
		rowFor("d-id", "charlie-1", base, false, kranzruntime.SessionRunning), // ties charlie-2 too
	}
	sortRuntimeRows(rows)

	if rows[0].Record.ID != "a-id" {
		t.Fatalf("current runtime must sort first, got %q", rows[0].Record.ID)
	}
	if rows[1].Record.ID != "b-id" {
		t.Fatalf("second row should be the newest non-current runtime, got %q", rows[1].Record.ID)
	}
	// Same StartedAt: stable tiebreak by name, then ID.
	if rows[2].Record.Name != "charlie-1" || rows[3].Record.Name != "charlie-2" || rows[4].Record.Name != "zeta" {
		names := []string{rows[2].Record.Name, rows[3].Record.Name, rows[4].Record.Name}
		t.Fatalf("tiebreak order = %v, want [charlie-1 charlie-2 zeta]", names)
	}

	// Sorting again must not reorder rows that are already in a stable
	// order (PRD 3.2: "rows do not jump between refreshes").
	again := append([]runtimeRow(nil), rows...)
	sortRuntimeRows(again)
	for i := range rows {
		if rows[i].Record.ID != again[i].Record.ID {
			t.Fatalf("re-sorting an already-sorted list reordered row %d", i)
		}
	}
}

func TestRuntimeRowStatusLabelsMatchPRDStates(t *testing.T) {
	now := time.Now()
	current := rowFor("c", "current", now, true, kranzruntime.SessionRunning)
	if label := runtimeRowStatusLabel(current); label != "current" {
		t.Fatalf("current row label = %q, want current", label)
	}
	started := rowFor("s", "started", now, false, kranzruntime.SessionRunning)
	if label := runtimeRowStatusLabel(started); label != "started" {
		t.Fatalf("no-other-clients row label = %q, want started", label)
	}
	withClients := started
	withClients.Record.ClientSurfaces = []string{"mcp", "tui"}
	if label := runtimeRowStatusLabel(withClients); label != "started" {
		t.Fatalf("client row status = %q, want started", label)
	}
	if label := runtimeRowSurfaceLabel(withClients); label != "MCP · TUI" {
		t.Fatalf("client-surface label = %q, want %q", label, "MCP · TUI")
	}
	incompatible := rowFor("i", "incompatible", now, false, kranzruntime.SessionIncompatible)
	if label := runtimeRowStatusLabel(incompatible); label != "incompatible" {
		t.Fatalf("incompatible row label = %q", label)
	}
	unreachable := rowFor("u", "unreachable", now, false, kranzruntime.SessionUnreachable)
	if label := runtimeRowStatusLabel(unreachable); label != "unreachable" {
		t.Fatalf("unreachable row label = %q", label)
	}
}

func TestRuntimeTableSeparatesStatusClientsAndServices(t *testing.T) {
	row := rowFor("s", "shop", time.Now(), false, kranzruntime.SessionRunning)
	row.Record.ClientSurfaces = []string{"mcp", "tui"}
	services, running := 5, 3
	row.Record.Services, row.Record.Running = &services, &running

	header := runtimeRowHeader(100)
	line := runtimeRowLine(row, 100)
	for _, column := range []string{"RUNTIME", "STATUS", "CLIENTS", "SERVICES", "UPTIME", "DIRECTORY"} {
		if !strings.Contains(header, column) {
			t.Fatalf("runtime table header is missing %q: %q", column, header)
		}
	}
	for _, value := range []string{"shop", "started", "MCP · TUI", "3/5"} {
		if !strings.Contains(line, value) {
			t.Fatalf("runtime row is missing %q: %q", value, line)
		}
	}
}

func TestRuntimeTableKeepsJustNowComplete(t *testing.T) {
	row := rowFor("s", "shop", time.Now(), false, kranzruntime.SessionRunning)
	line := runtimeRowLine(row, 100)
	if !strings.Contains(line, "just now") || strings.Contains(line, "just n…") {
		t.Fatalf("runtime uptime was truncated: %q", line)
	}
}

func TestRuntimeSwitcherFitsNarrowTerminalWithoutLosingCoreControls(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 62, 18, true
	model.mode = ModeRuntimeSwitcher
	row := rowFor("s", "shop", time.Now(), false, kranzruntime.SessionRunning)
	row.Record.ClientSurfaces = []string{"mcp", "tui"}
	services, running := 5, 3
	row.Record.Services, row.Record.Running = &services, &running
	model.switcherRows = []runtimeRow{row}

	rendered := model.renderRuntimeSwitcherView()
	plain := ansi.Strip(rendered)
	for _, expected := range []string{"RUNTIME", "STATUS", "CLIENTS", "SERVICES", "shop", "started", "MCP", "3/5", "[↑/↓] [j/k] Select", "[Enter] Connect", "[Esc] Cancel"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("narrow runtime modal lost %q:\n%s", expected, plain)
		}
	}
	for index, line := range strings.Split(rendered, "\n") {
		if width := lipgloss.Width(line); width > model.width {
			t.Fatalf("narrow runtime modal row %d is %d cells wide in a %d-cell terminal", index, width, model.width)
		}
	}
}

// TestSwitcherKeyboardNavigationAndDisabledRows exercises the reducer
// (PRD 9's "navigation, sorting, refresh reconciliation, disabled rows")
// without any network I/O: rows are fabricated directly.
func TestSwitcherKeyboardNavigationAndDisabledRows(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	now := time.Now()
	model.mode = ModeRuntimeSwitcher
	model.switcherRows = []runtimeRow{
		rowFor("cur", "current", now, true, kranzruntime.SessionRunning),
		rowFor("bad1", "incompatible-one", now.Add(-time.Minute), false, kranzruntime.SessionIncompatible),
		rowFor("ok1", "ok-one", now.Add(-2*time.Minute), false, kranzruntime.SessionRunning),
	}
	model.switcherCursor = 0

	model.handleRuntimeSwitcherKeys(tea.KeyMsg{Type: tea.KeyDown})
	if model.switcherCursor != 1 {
		t.Fatalf("cursor after down = %d, want 1", model.switcherCursor)
	}
	model.handleRuntimeSwitcherKeys(tea.KeyMsg{Type: tea.KeyDown})
	if model.switcherCursor != 2 {
		t.Fatalf("cursor after second down = %d, want 2", model.switcherCursor)
	}
	// Cursor does not run past the end of the list.
	model.handleRuntimeSwitcherKeys(tea.KeyMsg{Type: tea.KeyDown})
	if model.switcherCursor != 2 {
		t.Fatalf("cursor overran the list: %d", model.switcherCursor)
	}

	// Enter on an incompatible (disabled) row must not connect anywhere:
	// with no Registry configured, beginSwitchTo is a no-op regardless, so
	// what this actually proves is that a disabled row is rejected before
	// beginSwitchTo is even reached (switcherConnecting stays empty).
	model.switcherCursor = 1
	model.handleRuntimeSwitcherKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if model.switcherConnecting != "" {
		t.Fatalf("connecting to a disabled row set switcherConnecting = %q", model.switcherConnecting)
	}

	// Enter on the current row is also a no-op (nothing to switch to).
	model.switcherCursor = 0
	model.handleRuntimeSwitcherKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if model.switcherConnecting != "" {
		t.Fatal("connecting to the current row should be a no-op")
	}

	// Esc closes the modal without touching any row.
	model.handleRuntimeSwitcherKeys(tea.KeyMsg{Type: tea.KeyEsc})
	if model.mode != ModeNormal {
		t.Fatalf("Esc left mode = %v, want ModeNormal", model.mode)
	}
}

func TestClosingSwitcherSupersedesPendingConnection(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.mode = ModeRuntimeSwitcher
	model.switchSeq = 8
	model.switcherConnecting = "pending-runtime"

	model.closeRuntimeSwitcher()

	if model.mode != ModeNormal || model.switcherConnecting != "" {
		t.Fatalf("closing switcher left mode=%v connecting=%q", model.mode, model.switcherConnecting)
	}
	if model.switchSeq != 9 {
		t.Fatalf("switchSeq = %d, want pending result invalidated at 9", model.switchSeq)
	}
}

// TestRuntimeListRefreshKeepsCursorOnSameRuntime covers the reconciliation
// PRD 3.2 requires: a live refresh must not move the cursor while the
// runtime it was on is still listed, even if its position in the list (or
// the total row count) changed.
func TestRuntimeListRefreshKeepsCursorOnSameRuntime(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	now := time.Now()
	model.mode = ModeRuntimeSwitcher
	model.switcherGeneration = 5
	model.switcherRows = []runtimeRow{
		rowFor("a", "alpha", now, false, kranzruntime.SessionRunning),
		rowFor("b", "beta", now, false, kranzruntime.SessionRunning),
		rowFor("c", "gamma", now, false, kranzruntime.SessionRunning),
	}
	model.switcherCursor = 2 // sitting on "gamma"

	refreshed := []runtimeRow{
		rowFor("d", "delta", now, false, kranzruntime.SessionRunning), // a new runtime appeared first
		rowFor("a", "alpha", now, false, kranzruntime.SessionRunning),
		rowFor("c", "gamma", now, false, kranzruntime.SessionRunning),
	}
	model.handleRuntimeListMsg(runtimeListMsg{generation: 5, rows: refreshed})
	if model.switcherRows[model.switcherCursor].Record.ID != "c" {
		t.Fatalf("cursor followed to index %d (%q), want to stay on gamma",
			model.switcherCursor, model.switcherRows[model.switcherCursor].Record.ID)
	}

	// Now "gamma" disappears entirely: the cursor lands on the nearest
	// remaining row instead of resetting to the top.
	shrunk := []runtimeRow{
		rowFor("d", "delta", now, false, kranzruntime.SessionRunning),
		rowFor("a", "alpha", now, false, kranzruntime.SessionRunning),
	}
	model.handleRuntimeListMsg(runtimeListMsg{generation: 5, rows: shrunk})
	if model.switcherCursor < 0 || model.switcherCursor >= len(model.switcherRows) {
		t.Fatalf("cursor %d out of range after the selected row vanished", model.switcherCursor)
	}

	// A stale generation (a refresh that started before the modal was
	// closed and reopened) must not touch the list at all.
	model.handleRuntimeListMsg(runtimeListMsg{generation: 4, rows: nil})
	if len(model.switcherRows) != len(shrunk) {
		t.Fatal("a stale-generation refresh result was applied")
	}
}
