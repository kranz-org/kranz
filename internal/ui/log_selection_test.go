package ui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

func selectionTestModel(t *testing.T, lines ...string) *Model {
	t.Helper()
	m := newTestModel()
	t.Cleanup(func() { _ = m.Shutdown() })
	m.width, m.height, m.ready = 100, 24, true
	m.panelFocus = panelLogs
	for _, line := range lines {
		appendTestLog(m, "api", line)
	}
	return m
}
func selectAllSnapshot(m *Model) {
	m.beginLogSelection()
	if m.logSelection != nil {
		s := m.logSelection
		s.selectLine(0, false)
		s.selectLine(len(s.lines)-1, true)
	}
}
func mouseSelection(m *Model, x, y int, action tea.MouseAction) {
	_, _ = m.Update(tea.MouseMsg{X: x, Y: y, Action: action, Button: tea.MouseButtonLeft})
}

func TestLogSelectionExactTextFrozenAfterIncomingAndEviction(t *testing.T) {
	m := selectionTestModel(t, "\x1b[31mfailed\x1b[0m", "\t at connect (client.js:42)", "https://example.test/a?q=1")
	selectAllSnapshot(m)
	s := m.logSelection
	want := "failed\n\t at connect (client.js:42)\nhttps://example.test/a?q=1"
	if s.text() != want {
		t.Fatalf("text=%q", s.text())
	}
	appendTestLog(m, "api", "new output")
	// Cache retention compacts its backing slice in place. The snapshot must own
	// readable text rather than a slice alias to those mutable entry structs.
	entries := m.logEntries[s.target]
	for i := range entries {
		entries[i] = config.LogEntry{Sequence: uint64(100 + i), Raw: "replacement"}
	}
	m.logEntries[s.target] = entries[len(entries)-1:]
	if s.text() != want {
		t.Fatalf("frozen text changed=%q", s.text())
	}
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	_, _ = m.handleKeyMsg(keyMessage("w"))
	_ = m.View()
	if s.text() != want {
		t.Fatal("resize/wrap changed payload")
	}
	_, _ = m.handleKeyMsg(keyMessage("esc"))
	if m.logSelection != nil || !m.followMode {
		t.Fatal("Done did not restore follow")
	}
}
func TestLogSelectionMouseSubstringAndReverse(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		m := selectionTestModel(t, "prefix https://example.test/path suffix")
		x := m.dashboardLeftWidth() + 1
		y := 2
		a, b := x+7, x+31
		if reverse {
			a, b = b, a
		}
		mouseSelection(m, a, y, tea.MouseActionPress)
		if m.logSelection == nil || m.logSelection.active {
			t.Fatal("press must capture without selection")
		}
		mouseSelection(m, b, y, tea.MouseActionMotion)
		mouseSelection(m, 0, 0, tea.MouseActionRelease)
		if got := m.logSelection.text(); got != "https://example.test/path" {
			t.Fatalf("reverse=%v text=%q", reverse, got)
		}
		if m.logSelection.dragging {
			t.Fatal("release outside left drag active")
		}
	}
}
func TestLogSelectionClickDoubleTripleAndMetadata(t *testing.T) {
	m := selectionTestModel(t, "head https://example.test/path tail")
	x, y := m.dashboardLeftWidth()+1+10, 2
	mouseSelection(m, x, y, tea.MouseActionPress)
	mouseSelection(m, x, y, tea.MouseActionRelease)
	if m.logSelection != nil {
		t.Fatal("plain click froze logs")
	}
	mouseSelection(m, x, y, tea.MouseActionPress)
	mouseSelection(m, x, y, tea.MouseActionRelease)
	if m.logSelection == nil || m.logSelection.text() != "https://example.test/path" {
		t.Fatal("double click did not select token")
	}
	mouseSelection(m, x, y, tea.MouseActionPress)
	mouseSelection(m, x, y, tea.MouseActionRelease)
	if m.logSelection.text() != "head https://example.test/path tail" {
		t.Fatal("triple click did not select full line")
	}
	m.endLogSelection()
	m.showLogTime = true
	m.logClick = logClickState{}
	mouseSelection(m, m.dashboardLeftWidth()+2, y, tea.MouseActionPress)
	if m.logSelection != nil {
		t.Fatal("metadata started selection")
	}
}
func TestLogSelectionCaptureAtPressAndMissingRelease(t *testing.T) {
	m := selectionTestModel(t, "alpha beta")
	x := m.dashboardLeftWidth() + 1
	mouseSelection(m, x, 2, tea.MouseActionPress)
	appendTestLog(m, "api", "incoming")
	mouseSelection(m, x+4, 2, tea.MouseActionMotion)
	if m.logSelection.text() != "alpha" {
		t.Fatalf("press anchor replaced: %q", m.logSelection.text())
	}
	_, _ = m.Update(tea.BlurMsg{})
	mouseSelection(m, x+8, 2, tea.MouseActionMotion)
	if m.logSelection.text() != "alpha" || m.logSelection.dragging {
		t.Fatal("focus loss retained active drag")
	}
}
func TestLogSelectionKeyboardScopeAndTransitions(t *testing.T) {
	m := selectionTestModel(t, "first", "second", "third")
	_, _ = m.handleKeyMsg(keyMessage("y"))
	if m.logSelection.text() != "third" {
		t.Fatal("keyboard entry did not select last visible line")
	}
	_, _ = m.handleKeyMsg(keyMessage("shift+up"))
	if m.logSelection.text() != "second\nthird" {
		t.Fatalf("range=%q", m.logSelection.text())
	}
	_, _ = m.handleKeyMsg(keyMessage("up"))
	if m.logSelection.text() != "first" {
		t.Fatalf("line replace=%q", m.logSelection.text())
	}
	for _, key := range []string{"s", "r", "A", "S"} {
		_, _ = m.handleKeyMsg(keyMessage(key))
		if m.mode != ModeNormal || m.logSelection == nil {
			t.Fatalf("lifecycle %s escaped selection", key)
		}
	}
	_, _ = m.handleKeyMsg(keyMessage("/"))
	if m.logSelection != nil || m.mode != ModeSearch {
		t.Fatal("search transition retained selection")
	}
}
func TestLogSelectionFilterVersusHighlight(t *testing.T) {
	for _, mode := range []logSearchMode{searchFilter, searchHighlight} {
		m := selectionTestModel(t, "match first", "hidden middle", "match last")
		m.searchMode = mode
		m.searchInput.SetValue("match")
		if !m.applySearchQuery() {
			t.Fatal("query")
		}
		selectAllSnapshot(m)
		got := m.logSelection.text()
		want := "match first\nmatch last"
		if mode == searchHighlight {
			want = "match first\nhidden middle\nmatch last"
		}
		if got != want {
			t.Fatalf("mode %v text=%q", mode, got)
		}
	}
}
func TestLogSelectionPinnedRunAndOtherPanelClick(t *testing.T) {
	m := selectionTestModel(t)
	finishTestServiceRun(t, m, "api", 0, "old output")
	finishTestServiceRun(t, m, "api", 0, "new output")
	m.refreshServices()
	m.selectLatestRun()
	m.moveRun(-1, false)
	m.togglePinnedLog()
	m.panelFocus = panelPinnedLogs
	selectAllSnapshot(m)
	if !strings.Contains(m.logSelection.text(), "old output") || strings.Contains(m.logSelection.text(), "new output") {
		t.Fatal("pin mixed runs")
	}
	top, _ := m.logColumnLayout(m.height - 1 - m.dashboardFooterHeight())
	mouseSelection(m, m.dashboardLeftWidth()+2, 2+top, tea.MouseActionPress)
	if m.logSelection != nil || m.panelFocus != panelLogs {
		t.Fatal("other panel click carried selection")
	}
}
func TestLogSelectionActionsBothPanelsAndRunViews(t *testing.T) {
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "ops", Name: "sample"}
	m := NewModel(&config.Config{Project: "sample", ActionGroups: map[string]config.ActionGroup{"ops": {Actions: map[string]config.Action{"sample": {Command: "printf 'result\\n'; printf 'problem\\n' >&2"}}}}}, "test")
	defer m.Shutdown()
	m.width, m.height, m.ready = 100, 30, true
	m.focusedAction = &id
	m.focusedActionGroup = ""
	m.listMode = listServices
	m.panelFocus = panelLogs
	for range 2 {
		if _, err := m.app.RunAction(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	m.refreshServices()
	m.syncRunTarget()
	for _, combined := range []bool{false, true} {
		if combined {
			m.toggleRunView()
		}
		selectAllSnapshot(m)
		s := m.logSelection
		if strings.Contains(s.text(), "[stderr]") || strings.Contains(s.text(), "printf") || !strings.Contains(s.text(), "problem") {
			t.Fatalf("action text=%q", s.text())
		}
		if combined && !strings.Contains(s.context(), "combined") {
			t.Fatal("combined mislabeled")
		}
		m.endLogSelection()
	}
	m.togglePinnedLog()
	m.panelFocus = panelPinnedLogs
	selectAllSnapshot(m)
	if m.logSelection == nil || !strings.Contains(m.logSelection.text(), "result") {
		t.Fatal("pinned action empty")
	}
}
func TestLogSelectionDisconnectPreservesCopyAndSave(t *testing.T) {
	m := selectionTestModel(t, "captured")
	selectAllSnapshot(m)
	s := m.logSelection
	_, _ = m.handleCurrentRuntimeLostMsg(currentRuntimeLostMsg{sessionGen: m.sessionGeneration})
	if !s.disconnected {
		t.Fatal("disconnect not marked")
	}
	if !strings.Contains(ansi.Strip(m.View()), "captured") {
		t.Fatal("disconnect hid captured text")
	}
	var output bytes.Buffer
	m.terminalOutput = NewTerminalOutput(&output)
	_, cmd := m.handleKeyMsg(keyMessage("enter"))
	if cmd == nil {
		t.Fatal("copy unavailable")
	}
	_, _ = m.Update(cmd())
	if m.logSelection != s {
		t.Fatal("copy discarded selection")
	}
	_, _ = m.handleKeyMsg(keyMessage("esc"))
	if m.mode != ModeRuntimeLost {
		t.Fatal("Done skipped recovery")
	}
}
func TestLogSelectionViewportOnlyAndNowrapFullLine(t *testing.T) {
	text := strings.Repeat("x", 1200)
	m := selectionTestModel(t, text)
	m.wrapLogs = true
	m.height = 14
	m.beginLogSelection()
	if m.logSelection.text() != text {
		t.Fatal("keyboard omitted wrapped tail")
	}
	_, _ = m.handleKeyMsg(keyMessage("a"))
	s := m.logSelection
	want := s.text()
	if len(want) >= len(text) || len(want) == 0 {
		t.Fatal("viewport selection included hidden history")
	}
	_, _ = m.handleKeyMsg(keyMessage("w"))
	if s.text() != want {
		t.Fatal("wrap changed viewport endpoints")
	}
	s.selectLine(0, false)
	if s.text() != text {
		t.Fatal("nowrap omitted invisible tail")
	}
}
func TestLogSelectionFrameFitsThemesAndSizes(t *testing.T) {
	restoreDefaultTheme(t)
	m := selectionTestModel(t, "ERROR example", "\t雪 é 👩‍💻")
	selectAllSnapshot(m)
	for _, theme := range []string{DefaultTheme, "dracula", "nord"} {
		if _, err := applyAppearance(theme, "", backgroundTerminal, colorModeAuto, true); err != nil {
			t.Fatal(err)
		}
		for _, width := range []int{64, 80, 120} {
			m.width = width
			view := m.View()
			if lipgloss.Height(view) != m.height {
				t.Fatalf("height at %d", width)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("line wider than %d", width)
				}
			}
			for _, label := range []string{"Copy", "With context", "Save", "Done"} {
				if !strings.Contains(ansi.Strip(view), label) {
					t.Fatalf("missing %s at %d", label, width)
				}
			}
		}
	}
}
func TestLogProjectionGraphemesTabsControlsAndMapping(t *testing.T) {
	raw := "\x1b[31mA\x1b[0m\t雪é👩‍💻\x1b]8;;https://example.test\aZ\x1b]8;;\a"
	text := readableLog(raw)
	if text != "A\t雪é👩‍💻Z" {
		t.Fatalf("readable=%q", text)
	}
	for _, width := range []int{3, 8, 17, 80} {
		rows := projectLogLine(text, "[meta] ", width, true)
		var extracted strings.Builder
		last := -1
		for _, row := range rows {
			for _, c := range row.cells {
				if c.start >= 0 && c.start != last {
					extracted.WriteString(text[c.start:c.end])
					last = c.start
				}
			}
		}
		if extracted.String() != text {
			t.Fatalf("width %d extracted=%q", width, extracted.String())
		}
	}
	if got := readableLog("12345\rXY\bZ\x00\x7f"); got != "XZ345" {
		t.Fatalf("overwrite=%q", got)
	}
}
func TestLogSelectionWideCellAndTabsHitWholeGrapheme(t *testing.T) {
	m := selectionTestModel(t, "雪é👩‍💻\tend")
	s := m.captureLogSelection(panelLogs)
	x := m.dashboardLeftWidth() + 1
	for _, tc := range []struct{ x, a, b int }{{0, 0, 3}, {1, 0, 3}, {2, 3, 6}, {3, 6, 17}, {4, 6, 17}, {5, 17, 18}, {7, 17, 18}} {
		p, end, ok := m.selectionHit(s, x+tc.x, 2)
		if !ok || p.offset != tc.a || end != tc.b {
			t.Fatalf("cell %d => %v,%d,%v", tc.x, p, end, ok)
		}
	}
}
func TestLogSelectionClipboardBudgetFailureAndExactCommand(t *testing.T) {
	m := selectionTestModel(t, "text")
	selectAllSnapshot(m)
	var output bytes.Buffer
	m.terminalOutput = NewTerminalOutput(&output)
	_, cmd := m.handleKeyMsg(keyMessage("enter"))
	result := cmd().(clipboardResultMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("text")) + "\a"
	if output.String() != want {
		t.Fatalf("OSC=%q", output.String())
	}
	_, _ = m.Update(result)
	if !strings.Contains(m.toastMessage, "Sent") || m.logSelection == nil {
		t.Fatal("delivery claimed or selection lost")
	}
	output.Reset()
	if cmd := m.sendClipboard(strings.Repeat("z", clipboardMaxBytes+1), "selection"); cmd != nil || output.Len() != 0 {
		t.Fatal("oversized sent/truncated")
	}
	m.terminalOutput = NewTerminalOutput(failingSelectionWriter{})
	result = m.sendClipboard("text", "selection")().(clipboardResultMsg)
	if result.err == nil {
		t.Fatal("write failure ignored")
	}
	_, _ = m.Update(result)
	if m.logSelection == nil || !strings.Contains(m.toastMessage, "Save") {
		t.Fatal("failed copy lost retry")
	}
}

type failingSelectionWriter struct{}

func (failingSelectionWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestLogSelectionSaveSameTextOverwriteAndFailure(t *testing.T) {
	m := selectionTestModel(t, "  first", "\tlast")
	selectAllSnapshot(m)
	s := m.logSelection
	dir := t.TempDir()
	m.workingDirectory = dir
	_, _ = m.handleKeyMsg(keyMessage("ctrl+s"))
	m.exportInput.SetValue("fragment.log")
	_, cmd := m.handleKeyMsg(keyMessage("enter"))
	_, _ = m.Update(cmd())
	payload, err := os.ReadFile(filepath.Join(dir, "fragment.log"))
	if err != nil || string(payload) != s.text() {
		t.Fatalf("saved=%q err=%v", payload, err)
	}
	if s.saving || m.logSelection != s {
		t.Fatal("save lost selection")
	}
	_, _ = m.handleKeyMsg(keyMessage("ctrl+s"))
	m.exportInput.SetValue("fragment.log")
	_, cmd = m.handleKeyMsg(keyMessage("enter"))
	_, _ = m.Update(cmd())
	if !s.overwrite {
		t.Fatal("overwrite lacked confirmation")
	}
	_, cmd = m.handleKeyMsg(keyMessage("enter"))
	_, _ = m.Update(cmd())
	if s.saving {
		t.Fatal("overwrite failed")
	}
	_, _ = m.handleKeyMsg(keyMessage("ctrl+s"))
	m.exportInput.SetValue("missing/fragment.log")
	_, cmd = m.handleKeyMsg(keyMessage("enter"))
	_, _ = m.Update(cmd())
	if !s.saving || m.logSelection != s || s.text() != string(payload) {
		t.Fatal("write failure lost snapshot")
	}
	_, _ = m.handleKeyMsg(keyMessage("esc"))
	if s.saving || m.logSelection != s {
		t.Fatal("cancel lost selection")
	}
	if err := writeSelectionFile(filepath.Join(dir, "absent"), "x", true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overwrite created unconfirmed path: %v", err)
	}
}
func TestLogSelectionContextFenceAndMetadata(t *testing.T) {
	m := selectionTestModel(t, "``` dangerous fence", "safe")
	selectAllSnapshot(m)
	s := m.logSelection
	s.search = "filtered; hidden records omitted"
	context := s.context()
	if !strings.Contains(context, "````text\n``` dangerous fence\nsafe\n````") || !strings.Contains(context, "combined") || !strings.Contains(context, "hidden records omitted") {
		t.Fatalf("context=%q", context)
	}
	for _, forbidden := range []string{"Runtime:", "PID:", "Client:", "Command:"} {
		if strings.Contains(context, forbidden) {
			t.Fatal("unexpected metadata")
		}
	}
}
func TestLogSelectionPriorPauseAndRuntimeScope(t *testing.T) {
	m := selectionTestModel(t, "first", "second")
	m.followMode = false
	m.logPaused = true
	m.logAnchor = 2
	m.logOffset = 0
	m.beginLogSelection()
	_, _ = m.handleKeyMsg(keyMessage("esc"))
	if m.followMode || !m.logPaused || m.logOffset != 0 || m.logAnchor != 2 {
		t.Fatal("prior viewport was not preserved")
	}
	selectAllSnapshot(m)
	state := m.captureUIState()
	m.applyUIState(state)
	if m.logSelection != nil {
		t.Fatal("runtime restore carried snapshot")
	}
}
func TestLogSelectionBudgetAndSyntheticRows(t *testing.T) {
	m := selectionTestModel(t, "real")
	target := app.ServiceRunTarget("api")
	m.logEntries[target] = []config.LogEntry{{Raw: "truncation notice"}, {Sequence: 1, Raw: "real"}}
	selectAllSnapshot(m)
	if m.logSelection.text() != "real" {
		t.Fatal("synthetic marker copied")
	}
	m.endLogSelection()
	m.logEntries[target] = []config.LogEntry{{Sequence: 2, Raw: strings.Repeat("x", selectionMaxBytes+1)}}
	if s := m.captureLogSelection(panelLogs); s != nil {
		t.Fatal("oversized snapshot silently trimmed")
	}
}
func TestLogSelectionCaptureTimestampIsContextOnly(t *testing.T) {
	m := selectionTestModel(t)
	m.showLogTime = true
	appendTestLogAt(m, "api", time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC), "[12:34:56.789] app timestamp")
	selectAllSnapshot(m)
	if m.logSelection.text() != "[12:34:56.789] app timestamp" {
		t.Fatal("app timestamp lost or generated copied")
	}
	if !strings.Contains(m.logSelection.context(), "2026-10-14T09:00:00Z") {
		t.Fatal("captured time missing")
	}
}

func TestTerminalOutputPreservesTerminalFileAndShortWrite(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := NewTerminalOutput(f)
	if w.Fd() != f.Fd() {
		t.Fatal("terminal descriptor hidden from Bubble Tea")
	}
	if _, err = w.Write([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	if _, err = NewTerminalOutput(shortSelectionWriter{}).Write([]byte("osc")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("short write ignored")
	}
}

type shortSelectionWriter struct{}

func (shortSelectionWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestLogSelectionEdgeScrollAndStaleTimer(t *testing.T) {
	var lines []string
	for range 50 {
		lines = append(lines, "synthetic output")
	}
	m := selectionTestModel(t, lines...)
	m.beginLogSelection()
	s := m.logSelection
	s.dragging = true
	s.pressPoint = s.anchor
	s.pressEnd = s.cursor.offset
	s.scrollDirection = -1
	top := s.top
	_, cmd := m.handleSelectionScroll(selectionScrollMsg{s})
	if cmd == nil || !pointLess(s.top, top) {
		t.Fatal("edge drag did not scroll bounded snapshot")
	}
	s.dragging = false
	_, cmd = m.handleSelectionScroll(selectionScrollMsg{s})
	if cmd != nil {
		t.Fatal("lost release retained timer")
	}
	m.endLogSelection()
	_, cmd = m.handleSelectionScroll(selectionScrollMsg{s})
	if cmd != nil {
		t.Fatal("stale timer changed new view")
	}
}

func TestLogSelectionFooterMouseAndClearTransition(t *testing.T) {
	m := selectionTestModel(t, "sample")
	m.beginLogSelection()
	clickRenderedText(t, m, "[Ctrl+S] Save")
	if !m.logSelection.saving {
		t.Fatal("Save footer did not open input")
	}
	clickRenderedText(t, m, "[Esc] Cancel")
	if m.logSelection.saving {
		t.Fatal("Cancel footer did not retain selection")
	}
	_, _ = m.handleKeyMsg(keyMessage("c"))
	if m.logSelection != nil || m.mode != ModeConfirmClearLogs {
		t.Fatal("clear skipped existing confirmation")
	}
}

func TestRunExportUsesSameTerminalTransportAndKeepsFileShortcut(t *testing.T) {
	m := selectionTestModel(t)
	finishTestServiceRun(t, m, "api", 0, "run report")
	m.refreshServices()
	var output bytes.Buffer
	m.terminalOutput = NewTerminalOutput(&output)
	_, cmd := m.handleKeyMsg(keyMessage("e"))
	if cmd == nil {
		t.Fatal("e did not export run")
	}
	result, ok := cmd().(clipboardResultMsg)
	if !ok || result.err != nil {
		t.Fatal("run export bypassed terminal transport")
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(output.String(), "\x1b]52;c;"), "\a")
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !strings.Contains(string(payload), "Kranz run: api#1") || !strings.Contains(string(payload), "run report") {
		t.Fatal("run report changed")
	}
	_, _ = m.handleKeyMsg(keyMessage("E"))
	if m.mode != ModeRunExport {
		t.Fatal("Shift+E no longer opens run file export")
	}
}

func TestLogSelectionFilteredActionPerLineAndPinnedCombinedBoundary(t *testing.T) {
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "ops", Name: "sample"}
	m := NewModel(&config.Config{Project: "sample", ActionGroups: map[string]config.ActionGroup{"ops": {Actions: map[string]config.Action{"sample": {Command: "exit 0"}}}}}, "test")
	defer m.Shutdown()
	m.width, m.height, m.ready = 100, 30, true
	m.focusedAction = &id
	m.focusedActionGroup = ""
	m.listMode = listServices
	m.panelFocus = panelLogs
	m.syncRunTarget()
	m.runMode = runViewCombined
	target := app.ActionRunTarget(id)
	m.logEntries[target] = []config.LogEntry{{Sequence: 1, Run: 1, Source: "stdout", Raw: "hidden\nkeep one"}, {Sequence: 2, Run: 2, Source: "stdout", Raw: "keep two"}, {Sequence: 3, Run: 3, Source: "stdout", Raw: "keep three"}}
	m.searchMode = searchFilter
	if err := m.logSearcher.SetPattern("keep"); err != nil {
		t.Fatal(err)
	}
	selectAllSnapshot(m)
	if m.logSelection.text() != "keep one\nkeep two\nkeep three" {
		t.Fatalf("hidden action line returned: %q", m.logSelection.text())
	}
	m.endLogSelection()
	m.pinnedTarget = target
	m.pinnedRunMode = runViewCombined
	m.pinnedRun = 2
	m.panelFocus = panelPinnedLogs
	selectAllSnapshot(m)
	if strings.Contains(m.logSelection.text(), "three") {
		t.Fatal("combined pin included later run")
	}
}

func TestLogSelectionRetainedBrowseAnchorAfterEvictionAndResize(t *testing.T) {
	var lines []string
	for i := range 60 {
		lines = append(lines, strings.Repeat("long ", 20)+string(rune('A'+i%26)))
	}
	m := selectionTestModel(t, lines...)
	m.followMode = false
	m.logAnchor = 60
	m.logOffset = 8
	m.beginLogSelection()
	s := m.logSelection
	seq := s.lines[s.restoreTop.line].entry.Sequence
	entries := m.logEntries[s.target]
	m.logEntries[s.target] = append([]config.LogEntry(nil), entries[5:]...)
	m.width = 80
	m.wrapLogs = true
	m.endLogSelection()
	if m.followMode {
		t.Fatal("browse became live")
	}
	fresh := m.captureLogSelection(panelLogs)
	rows := fresh.rows(m.currentLogContentWidth(), m.wrapLogs)
	if fresh.lines[rows[fresh.start].line].entry.Sequence != seq {
		t.Fatal("semantic browsing anchor moved after resize/eviction")
	}
}

func TestLogSelectionShiftClickResizeEndsDragAndSourceRemoval(t *testing.T) {
	m := selectionTestModel(t, "first", "second")
	m.beginLogSelection()
	s := m.logSelection
	x := m.dashboardLeftWidth() + 1
	_, _ = m.Update(tea.MouseMsg{X: x, Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Shift: true})
	if s.text() != "first\nsecond" {
		t.Fatalf("shift click=%q", s.text())
	}
	s.dragging = true
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if s.dragging {
		t.Fatal("resize left drag captured")
	}
	m.allServices = nil
	m.services = nil
	if !strings.Contains(ansi.Strip(m.View()), "first") {
		t.Fatal("source removal hid captured text")
	}
	if s.text() != "first\nsecond" {
		t.Fatal("removed source rebound selection")
	}
}

func TestLogSelectionGeneratedSourcePrefixesAreMetadata(t *testing.T) {
	m := selectionTestModel(t)
	target := app.ServiceRunTarget("api")
	m.logEntries[target] = []config.LogEntry{{Sequence: 1, Source: "kranz", Raw: "[Kranz] Starting"}, {Sequence: 2, Source: "stdout", Raw: "[Kranz] application text"}}
	selectAllSnapshot(m)
	if m.logSelection.text() != "Starting\n[Kranz] application text" {
		t.Fatalf("generated source badge copied or application content lost: %q", m.logSelection.text())
	}
}
