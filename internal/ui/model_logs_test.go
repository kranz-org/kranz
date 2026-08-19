package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
)

// Tests for log panels, pinning, and log lifecycle behaviour.

func TestFocusingServiceClearsUnreadLogs(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	firstName := model.services[0].Name
	secondName := model.services[1].Name
	model.app.AppendLogForTest(firstName, "first unread")
	model.app.AppendLogForTest(secondName, "second unread")

	model.moveFocus(1)
	first, _ := model.app.Service(firstName)
	second, _ := model.app.Service(secondName)
	if first.State.NewLogCount != 0 {
		t.Fatalf("previously focused service has %d unread logs", first.State.NewLogCount)
	}
	if second.State.NewLogCount != 0 {
		t.Fatalf("newly focused service has %d unread logs", second.State.NewLogCount)
	}

	model.app.AppendLogForTest(secondName, "visible while focused")
	model.refreshServices()
	second, _ = model.app.Service(secondName)
	if second.State.NewLogCount != 0 {
		t.Fatalf("focused service accumulated %d unread logs", second.State.NewLogCount)
	}
}

func TestClearLogsRequiresConfirmationForFocusedAndPinnedPanels(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	api := model.FocusedService()
	model.app.AppendLogForTest(api.Name, "api output")
	model.panelFocus = panelLogs

	pressKey(model, 'c')
	if model.mode != ModeConfirmClearLogs || model.clearTarget != api.Name || model.clearPinned {
		t.Fatalf("focused clear state = mode %v target %q pinned %v", model.mode, model.clearTarget, model.clearPinned)
	}
	if len(model.app.Logs(api.Name)) != 1 {
		t.Fatal("focused logs were cleared before confirmation")
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEsc})
	if model.mode != ModeNormal || len(model.app.Logs(api.Name)) != 1 {
		t.Fatal("Escape did not preserve focused logs")
	}

	model.moveFocus(1)
	worker := model.FocusedService()
	model.app.AppendLogForTest(worker.Name, "worker output")
	model.pinnedLog = api.Name
	model.panelFocus = panelPinnedLogs
	pressKey(model, 'c')
	if model.mode != ModeConfirmClearLogs || model.clearTarget != api.Name || !model.clearPinned {
		t.Fatalf("pinned clear state = mode %v target %q pinned %v", model.mode, model.clearTarget, model.clearPinned)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if len(model.app.Logs(api.Name)) != 0 {
		t.Fatal("Enter did not clear pinned logs")
	}
	if len(model.app.Logs(worker.Name)) != 1 {
		t.Fatal("clearing pinned logs cleared focused service logs")
	}
}

func TestLogTitleShowsColorCodedServiceStatus(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 24, true

	title := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 10))
	if !strings.Contains(title, "[3] LOGS │ ● api · stopped") {
		t.Fatalf("stopped log title does not expose service state: %q", title)
	}

	model.app.SetServiceStatusForTest(model.FocusedService().Name, config.StatusRunning)
	model.refreshServices()
	title = ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 10))
	if !strings.Contains(title, "[3] LOGS │ ● api · running") {
		t.Fatalf("running log title does not expose service state: %q", title)
	}
}

func TestShiftThreeIgnoresTagRowsButPinsExpandedServices(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	pressKey(model, 't')

	pressKey(model, '#')
	if model.pinnedLog != "" {
		t.Fatalf("tag row pinned stale service %q", model.pinnedLog)
	}

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyDown})
	if svc := model.focusedTagService(); svc == nil || svc.Name != "api" {
		t.Fatalf("expanded tag child = %v, want api", svc)
	}
	pressKey(model, '#')
	if model.pinnedLog != "api" {
		t.Fatalf("expanded service pinned %q, want api", model.pinnedLog)
	}
}

func TestShiftThreePinsLogsAboveFocusedLogs(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 28, true
	api := model.FocusedService()
	model.app.AppendLogForTest(api.Name, "api log remains visible")

	pressKey(model, '#')
	if model.pinnedLog != "api" || model.PinnedService() != api {
		t.Fatalf("pinned service = %q / %v", model.pinnedLog, model.PinnedService())
	}
	model.moveFocus(1)
	worker := model.FocusedService()
	model.app.AppendLogForTest(worker.Name, "hidden worker line")
	model.app.AppendLogForTest(worker.Name, "WORKER matched line")
	if err := model.logSearcher.SetPattern("WORKER"); err != nil {
		t.Fatal(err)
	}
	model.searchMode = searchFilter

	rendered := model.renderLogColumn(64, model.height-2)
	plain := ansi.Strip(rendered)
	for _, expected := range []string{"PINNED LOGS", "api log remains visible", "[3] LOGS", "worker", "WORKER matched line"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("split logs do not contain %q:\n%s", expected, plain)
		}
	}
	if strings.Contains(plain, "hidden worker line") {
		t.Fatalf("focused log filter did not hide a non-match:\n%s", plain)
	}
	if height := lipgloss.Height(rendered); height != model.height-2 {
		t.Fatalf("split log height = %d, want %d", height, model.height-2)
	}
	if model.currentLogPanelHeight() != (model.height-2)-(model.height-2)/2 {
		t.Fatalf("current log panel height = %d", model.currentLogPanelHeight())
	}
}

func TestThreeSwitchesAndScrollsPinnedLogsIndependently(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 28, true
	for index := range 40 {
		model.app.AppendLogForTest(model.FocusedService().Name, fmt.Sprintf("pinned line %d", index))
	}
	pressKey(model, '#')
	model.moveFocus(1)
	for index := range 40 {
		model.app.AppendLogForTest(model.FocusedService().Name, fmt.Sprintf("current line %d", index))
	}

	pressKey(model, '3')
	if model.panelFocus != panelLogs {
		t.Fatalf("first 3 focused panel %v, want current logs", model.panelFocus)
	}
	pressKey(model, '3')
	if model.panelFocus != panelPinnedLogs {
		t.Fatalf("second 3 focused panel %v, want pinned logs", model.panelFocus)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyUp})
	if model.pinnedOffset == 0 || model.pinnedFollow || model.logOffset != 0 || !model.followMode {
		t.Fatalf("pinned/current viewports = %d/%v and %d/%v", model.pinnedOffset, model.pinnedFollow, model.logOffset, model.followMode)
	}
	plain := ansi.Strip(model.renderLogColumn(64, model.height-2))
	if !strings.Contains(plain, "PINNED LOGS") || !strings.Contains(plain, "BROWSING") {
		t.Fatalf("focused pinned viewport does not expose its browsing state:\n%s", plain)
	}
	pressKey(model, '3')
	if model.panelFocus != panelLogs {
		t.Fatalf("third 3 focused panel %v, want current logs", model.panelFocus)
	}

	model.pinnedOffset, model.pinnedAnchor, model.pinnedFollow = 0, 0, true
	rightX := model.dashboardLeftWidth() + 1
	_, _ = model.handleMouseMsg(tea.MouseMsg{X: rightX, Y: 2, Button: tea.MouseButtonWheelUp})
	if model.panelFocus != panelPinnedLogs || model.pinnedOffset == 0 {
		t.Fatalf("wheel over pinned logs focused %v at offset %d", model.panelFocus, model.pinnedOffset)
	}
	_, _ = model.handleMouseMsg(tea.MouseMsg{X: rightX, Y: model.height - 3, Button: tea.MouseButtonWheelUp})
	if model.panelFocus != panelLogs || model.logOffset == 0 {
		t.Fatalf("wheel over current logs focused %v at offset %d", model.panelFocus, model.logOffset)
	}
}

func TestShiftThreeReplacesAndUnpinsPinnedService(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	pressKey(model, '#')
	model.moveFocus(1)
	pressKey(model, '#')
	if model.pinnedLog != "worker" {
		t.Fatalf("replacement pinned service = %q", model.pinnedLog)
	}
	pressKey(model, '#')
	if model.pinnedLog != "" || model.PinnedService() != nil {
		t.Fatalf("pinned service was not cleared: %q", model.pinnedLog)
	}
}

func TestMouseHoverAndWheelFocusLogPanel(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 24, true
	for index := range 40 {
		model.app.AppendLogForTest(model.FocusedService().Name, fmt.Sprintf("line %d", index))
	}
	model.panelFocus = panelServices
	serviceIndex := model.focused
	rightX := model.dashboardLeftWidth() + 1

	_, _ = model.handleMouseMsg(tea.MouseMsg{
		X: rightX, Y: dashboardHeaderRows + 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone,
	})
	if model.panelFocus != panelLogs {
		t.Fatalf("hover over logs focused panel %v, want logs", model.panelFocus)
	}

	_, _ = model.handleMouseMsg(tea.MouseMsg{
		X: rightX, Y: dashboardHeaderRows + 1, Button: tea.MouseButtonWheelUp,
	})
	if model.panelFocus != panelLogs || model.logOffset == 0 || model.followMode {
		t.Fatalf("wheel over logs focused panel %v at offset %d follow %v", model.panelFocus, model.logOffset, model.followMode)
	}
	if model.focused != serviceIndex {
		t.Fatalf("wheel over logs moved service cursor from %d to %d", serviceIndex, model.focused)
	}
}

func TestManualLogPauseFreezesVisibleTail(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 10, true
	for index := range 10 {
		model.app.AppendLogForTest(model.FocusedService().Name, fmt.Sprintf("before %02d", index))
	}
	pressKey(model, 'f')
	model.app.AppendLogForTest(model.FocusedService().Name, "after pause")

	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 8))
	if !strings.Contains(plain, "PAUSED") || strings.Contains(plain, "after pause") {
		t.Fatalf("manual pause did not freeze the visible log tail:\n%s", plain)
	}
}

func TestLogWrappingKeepsPanelAndDashboardGeometry(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	model.panelFocus = panelLogs
	for range 3 {
		model.app.AppendLogForTest(model.FocusedService().Name, strings.Repeat("long-log-value ", 40))
	}

	plainRows := model.displayedLogLineCount()
	panel := model.renderLogPanel(model.FocusedService(), 50, 12)
	if lipgloss.Height(panel) != 12 {
		t.Fatalf("unwrapped panel height = %d, want 12", lipgloss.Height(panel))
	}
	pressKey(model, 'w')
	wrappedRows := model.displayedLogLineCount()
	if wrappedRows <= plainRows {
		t.Fatalf("wrapped rows = %d, unwrapped rows = %d", wrappedRows, plainRows)
	}
	panel = model.renderLogPanel(model.FocusedService(), 50, 12)
	if lipgloss.Height(panel) != 12 || !strings.Contains(ansi.Strip(panel), "WRAP") {
		t.Fatalf("wrapped panel geometry/state is invalid: height=%d\n%s", lipgloss.Height(panel), ansi.Strip(panel))
	}
	if height := lipgloss.Height(model.View()); height != model.height {
		t.Fatalf("dashboard grew to %d rows, want %d", height, model.height)
	}
	model.scrollLogs(-1)
	if model.followMode || model.logOffset == 0 {
		t.Fatalf("wrapped log scrolling did not enter browsing mode: follow=%v offset=%d", model.followMode, model.logOffset)
	}
}

func TestLogTimestampToggleUsesCaptureTimeWithoutChangingSearchText(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 90, 24, true
	captured := time.Date(2026, 7, 20, 12, 34, 56, 789000000, time.Local)
	model.app.AppendLogAtForTest(model.FocusedService().Name, captured, "request complete")

	pressKey(model, 'i')
	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 10))
	if !strings.Contains(plain, "TIME") || !strings.Contains(plain, "[12:34:56.789] request complete") {
		t.Fatalf("timestamp mode did not render capture time:\n%s", plain)
	}
	if err := model.logSearcher.SetPattern(`^request complete$`); err != nil {
		t.Fatal(err)
	}
	if matches := model.logSearcher.Search(model.serviceLogLines(model.FocusedService())); len(matches) != 1 {
		t.Fatalf("timestamp polluted regex source: matches=%v lines=%v", matches, model.serviceLogLines(model.FocusedService()))
	}

	pressKey(model, 'i')
	plain = ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 10))
	if strings.Contains(plain, "12:34:56.789") || model.showLogTime {
		t.Fatalf("timestamp toggle did not hide capture time:\n%s", plain)
	}
}

func TestServiceLogCannotClearOrRepositionTheTUI(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 90, 24, true
	model.app.AppendLogForTest(model.FocusedService().Name, "\x1b[2J\x1b[H\x1b[31mserver ready\x1b[0m")

	rendered := model.View()
	if strings.Contains(rendered, "\x1b[2J") || strings.Contains(rendered, "\x1b[H") {
		t.Fatalf("unsafe child-process control sequence reached the TUI: %q", rendered)
	}
	plain := ansi.Strip(rendered)
	for _, expected := range []string{"KRANZ", "SERVICES", "DETAILS", "LOGS", "server ready", "Start"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("sanitized render lost %q:\n%s", expected, plain)
		}
	}
}
