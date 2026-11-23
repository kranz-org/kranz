package ui

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

type dependencyOperationRecorder struct {
	app.API
	method string
	names  []string
	err    error
}

func (r *dependencyOperationRecorder) record(method string, names []string) error {
	r.method, r.names = method, slices.Clone(names)
	return r.err
}

func (r *dependencyOperationRecorder) StartServicesContext(_ context.Context, names []string) error {
	return r.record("start", names)
}

func (r *dependencyOperationRecorder) ForceStartServices(names []string) error {
	return r.record("force start", names)
}

func (r *dependencyOperationRecorder) StopServices(names []string) error {
	return r.record("stop", names)
}

func (r *dependencyOperationRecorder) ForceStopServices(names []string) error {
	return r.record("force stop", names)
}

func (r *dependencyOperationRecorder) RestartServices(names []string) error {
	return r.record("restart", names)
}

func (r *dependencyOperationRecorder) ForceRestartServices(names []string) error {
	return r.record("force restart", names)
}

func TestDependencyModalRunsReviewedOperation(t *testing.T) {
	for _, test := range []struct {
		name         string
		selection    string
		runControl   string
		selectedOnly bool
		running      bool
		wantMethod   string
		wantNames    []string
		wantAffected []string
	}{
		{"start", "s", "Start", false, false, "start", []string{"cache", "storage"}, []string{"cache", "storage"}},
		{"selected start", "s", "Start", true, false, "force start", []string{"storage", "cache"}, []string{"cache", "storage"}},
		{"stop", "x", "Stop", false, true, "stop", []string{"cache", "storage"}, []string{"cache", "gateway", "index", "report", "storage", "worker"}},
		{"selected stop", "x", "Stop", true, true, "force stop", []string{"cache", "storage"}, []string{"cache", "storage"}},
		{"restart", "r", "Restart", false, true, "restart", []string{"cache"}, []string{"cache", "gateway", "index", "report", "storage", "worker"}},
		{"selected restart", "r", "Restart", true, true, "force restart", []string{"cache"}, []string{"cache"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := dependencyModalFixture(t)
			r := &dependencyOperationRecorder{API: m.app}
			m.app = r
			if test.running {
				r.SetServiceStatusForTest("cache", config.StatusRunning)
				r.SetServiceStatusForTest("gateway", config.StatusRunning)
				m.refreshServices()
			}
			m.selected = nameSet([]string{"cache", "storage"})
			m.openDependencies()
			// Select something else after opening. The graph, plan and run
			// must all keep the targets captured when the modal opened.
			m.selected = nameSet([]string{"archive"})
			_, _ = m.handleKeyMsg(keyMessage("tab"))
			chooseLabel := "[" + test.selection + "] " + test.runControl
			if cmd := clickRenderedText(t, m, chooseLabel); cmd != nil || m.dependencyModal.plan || len(m.operations) != 0 {
				t.Fatal("choosing an operation should stay in the graph")
			}
			clickRenderedText(t, m, "[Tab] Steps")
			if test.selectedOnly {
				clickRenderedText(t, m, "[f] Selected only")
			}
			preview, err := m.previewDependencyOperation(m.dependencyModal.operation, m.dependencyModal.selectedOnly)
			if err != nil || !slices.Equal(preview.Requested, []string{"cache", "storage"}) {
				t.Fatalf("reviewed targets %v, error %v", preview.Requested, err)
			}
			cmd := clickRenderedText(t, m, "[Enter] Run "+test.runControl)
			if cmd == nil || m.mode != ModeDependencies || r.method != "" {
				t.Fatal("Run should schedule one command and leave the plan open")
			}
			op := m.operations[m.operationID]
			if len(op.services) != len(test.wantAffected) || slices.ContainsFunc(test.wantAffected, func(name string) bool { return !op.services[name] }) {
				t.Fatalf("claimed services = %v, want %v", op.services, test.wantAffected)
			}
			if (op.cancel != nil) != (test.wantMethod == "start") {
				t.Fatal("unexpected cancellation handle")
			}
			cmd()
			if r.method != test.wantMethod || !slices.Equal(r.names, test.wantNames) {
				t.Fatalf("called %s %v, want %s %v", r.method, r.names, test.wantMethod, test.wantNames)
			}
		})
	}
}

func TestDependencyModalKeyboardAndRunGuards(t *testing.T) {
	m := dependencyModalFixture(t)
	m.openDependencies()
	pressKey(m, 'x')
	if m.dependencyModal.plan || m.dependencyModal.operation != "stop" {
		t.Fatal("x in the graph should select Stop and keep the graph open")
	}
	assertNoRun := func(label string) {
		t.Helper()
		if strings.Contains(ansi.Strip(m.dependencyModalContent()), label) {
			t.Fatalf("unexpected execution label %q", label)
		}
		if _, cmd := m.handleKeyMsg(keyMessage("enter")); cmd != nil || len(m.operations) != 0 {
			t.Fatal("Enter scheduled an unavailable operation")
		}
	}
	assertNoRun("[Enter] Run Stop")
	_, _ = m.handleKeyMsg(keyMessage("tab"))
	pressKey(m, 's')
	m.dependencyModal.roots = []string{"missing"}
	assertNoRun("[Enter] Run Start")
	m.dependencyModal.roots = nil
	assertNoRun("[Enter] Run Start")
	m.dependencyModal.roots = []string{"gateway"}
	if _, cmd := m.handleKeyMsg(keyMessage("enter")); cmd == nil || len(m.operations) != 1 {
		t.Fatal("Enter did not schedule the reviewed Start plan")
	}
}

func TestDependencyModalCompactRunControl(t *testing.T) {
	m := dependencyModalFixture(t)
	m.width, m.height = 41, 10
	m.app.SetServiceStatusForTest("gateway", config.StatusRunning)
	m.refreshServices()
	m.openDependencies()
	pressKey(m, 'r')
	_, _ = m.handleKeyMsg(keyMessage("tab"))
	content := ansi.Strip(m.View())
	run := strings.Index(content, "[Enter] Run Restart")
	requested := strings.Index(content, "Requested")
	if run < 0 || requested < run || strings.Count(content, "[Enter] Run Restart") != 1 ||
		!strings.Contains(content, "[Esc] Back") || !strings.Contains(content, "[r] Restart") {
		t.Fatalf("compact layout should show Run and the active operation above the body:\n%s", content)
	}
	if cmd := clickRenderedText(t, m, "[Enter] Run Restart"); cmd == nil {
		t.Fatal("compact Run control did not schedule the operation")
	}
}

func TestDependencyModalOnlyControlsCanRunOperation(t *testing.T) {
	const serviceName = "[Enter] Run Start"
	m := NewModel(&config.Config{Services: map[string]config.Service{
		serviceName: {Command: "true"},
	}}, "test")
	t.Cleanup(func() { _ = m.Shutdown() })
	m.width, m.height, m.ready = 72, 20, true
	m.openDependencies()
	pressKey(m, 's')
	_, _ = m.handleKeyMsg(keyMessage("tab"))
	if got := strings.Count(ansi.Strip(m.dependencyModalContent()), serviceName); got < 2 {
		t.Fatal("fixture should display the service name and the identically named control")
	}
	for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if !strings.Contains(line, "○ "+serviceName) {
			continue
		}
		x := lipgloss.Width(line[:strings.Index(line, serviceName)])
		_, cmd := m.handleMouseMsg(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		if cmd != nil || len(m.operations) != 0 {
			t.Fatal("clicking service text in the plan body scheduled an operation")
		}
		return
	}
	t.Fatal("service name was not visible in the plan body")
}

func TestDependencyModalReopensWithAdoptedConfig(t *testing.T) {
	m := dependencyModalFixture(t)
	stale := *m.cfg
	stale.Services = maps.Clone(m.cfg.Services)
	delete(stale.Services, "archive")
	stale.UI.Theme = "local-choice"
	m.cfg = &stale

	m.app = &dependencyOperationRecorder{API: m.app}
	m.app.SetServiceStatusForTest("gateway", config.StatusRunning)
	m.refreshServices()
	m.openDependencies()
	pressKey(m, 'r')
	_, _ = m.handleKeyMsg(keyMessage("tab"))
	_, cmd := m.handleKeyMsg(keyMessage("enter"))
	if cmd == nil {
		t.Fatal("restart plan did not schedule a command")
	}
	m.handleOperationResult(cmd().(operationResultMsg))
	if m.cfg.UI.Theme != "local-choice" {
		t.Fatal("refreshing service definitions replaced local appearance")
	}
	m.selected["archive"] = true
	m.openDependencies()
	pressKey(m, 's')
	_, _ = m.handleKeyMsg(keyMessage("tab"))
	preview, err := m.previewDependencyOperation("start", false)
	if !preview.runnable(err) || !slices.Equal(preview.Requested, []string{"archive"}) {
		t.Fatalf("plan should use the adopted config, got %+v, %v", preview, err)
	}
}

func TestDependencyModalStartShortcutRequestsConfirmation(t *testing.T) {
	m := dependencyModalFixture(t)
	recorder := &dependencyOperationRecorder{API: m.app}
	m.app = recorder
	m.selected = nameSet([]string{"cache", "storage"})
	m.openDependencies()
	if !m.dependencyModal.plan {
		t.Fatal("multiple selected services should open the Start plan")
	}

	for range 2 {
		if _, cmd := m.handleKeyMsg(keyMessage("s")); cmd != nil || len(m.operations) != 0 {
			t.Fatal("Start key repeat should leave the confirmation open")
		}
		if !strings.Contains(ansi.Strip(m.dependencyModalContent()), "[Enter] Confirm Start") {
			t.Fatal("Start shortcut did not show a confirmation control")
		}
	}
	pressEsc(m)
	if m.mode != ModeDependencies || m.dependencyModal.confirming {
		t.Fatal("Escape did not return to the plan")
	}
	pressKey(m, 's')
	cmd := clickRenderedText(t, m, "[Enter] Confirm Start")
	if cmd == nil {
		t.Fatal("clicking Confirm Start did not schedule the operation")
	}
	cmd()
	if recorder.method != "start" || !slices.Equal(recorder.names, []string{"cache", "storage"}) {
		t.Fatalf("unexpected operation: %s %v", recorder.method, recorder.names)
	}
}

func TestDependencyModalShowsRunProgressAndResult(t *testing.T) {
	for _, runErr := range []error{nil, errors.New("the service returned an error")} {
		m := dependencyModalFixture(t)
		m.app = &dependencyOperationRecorder{API: m.app, err: runErr}
		m.app.SetServiceStatusForTest("gateway", config.StatusRunning)
		m.refreshServices()
		m.openDependencies()
		pressKey(m, 'r')
		_, _ = m.handleKeyMsg(keyMessage("tab"))
		_, cmd := m.handleKeyMsg(keyMessage("enter"))
		if cmd == nil || m.mode != ModeDependencies {
			t.Fatal("Enter should run the plan without dismissing it")
		}
		if !strings.Contains(ansi.Strip(m.dependencyModalContent()), "Running Restart") {
			t.Fatal("running plan should show progress")
		}
		if _, duplicate := m.handleKeyMsg(keyMessage("enter")); duplicate != nil {
			t.Fatal("a running plan accepted a duplicate operation")
		}
		m.handleOperationResult(cmd().(operationResultMsg))
		want := "Restart complete"
		if runErr != nil {
			want = "Restart failed"
		}
		if m.mode != ModeDependencies || !strings.Contains(ansi.Strip(m.dependencyModalContent()), want) {
			t.Fatalf("the open plan should show %q", want)
		}
		if _, duplicate := m.handleKeyMsg(keyMessage("enter")); duplicate != nil {
			t.Fatal("a completed plan accepted another Enter without selecting it again")
		}
		pressKey(m, 'r')
		if !strings.Contains(ansi.Strip(m.dependencyModalContent()), "[Enter] Confirm Restart") {
			t.Fatal("choosing Restart after completion should let the operator run it again")
		}
	}
}

func TestDependencyModalPortConflictRetryKeepsSelectedPlan(t *testing.T) {
	m := dependencyModalFixture(t)
	recorder := &dependencyOperationRecorder{API: m.app, err: &app.PortConflictError{Service: "gateway", Port: 3077}}
	m.app = recorder
	m.openDependencies()
	pressKey(m, 's')
	_, _ = m.handleKeyMsg(keyMessage("tab"))
	_, cmd := m.handleKeyMsg(keyMessage("enter"))
	if cmd == nil {
		t.Fatal("Start was not scheduled")
	}
	m.handleOperationResult(cmd().(operationResultMsg))
	if m.mode != ModePortConflict {
		t.Fatal("the plan did not open port conflict details")
	}

	m.selected = nameSet([]string{"archive"})
	recorder.err = nil
	_, retry := m.handlePortConflictKeys(keyMessage("r"))
	if retry == nil || m.mode != ModeDependencies {
		t.Fatal("retry should run the original plan and keep it open")
	}
	m.handleOperationResult(retry().(operationResultMsg))
	if recorder.method != "start" || !slices.Equal(recorder.names, []string{"gateway"}) {
		t.Fatalf("retry used unexpected operation %s %v", recorder.method, recorder.names)
	}
}

func TestDependencyModalRestartActionUsesFixedRow(t *testing.T) {
	for _, width := range []int{41, 72} {
		m := dependencyModalFixture(t)
		m.width, m.height = width, 10
		m.openDependencies()
		pressKey(m, 'r')
		_, _ = m.handleKeyMsg(keyMessage("tab"))
		row := func(label string) int {
			t.Helper()
			for i, line := range strings.Split(ansi.Strip(m.View()), "\n") {
				if strings.Contains(line, label) {
					return i
				}
			}
			t.Fatalf("visible label %q is missing at width %d", label, width)
			return -1
		}
		planRow, actionRow := row("[r] Restart"), row("No running targets")
		requestedRow := row("Requested")
		for range 2 {
			if _, cmd := m.handleKeyMsg(keyMessage("enter")); cmd != nil {
				t.Fatal("Restart was available with no active targets")
			}
			pressKey(m, 'f')
		}
		m.app.SetServiceStatusForTest("gateway", config.StatusRunning)
		m.refreshServices()
		if row("[Enter] Run Restart") != actionRow || row("[r] Restart") != planRow || row("Requested") != requestedRow {
			t.Fatal("making a restart available moved the action row or the plan")
		}
		pressKey(m, 'r')
		if row("[Enter] Confirm Restart") != actionRow || row("[r] Restart") != planRow || row("Requested") != requestedRow {
			t.Fatal("requesting confirmation moved the action row or the plan")
		}
	}
}

func TestDependencyModalStartsDirectlyFromGraph(t *testing.T) {
	for _, useMouse := range []bool{false, true} {
		m := dependencyModalFixture(t)
		recorder := &dependencyOperationRecorder{API: m.app}
		m.app = recorder
		m.openDependencies()
		pressKey(m, 's')
		if m.dependencyModal.plan || !strings.Contains(ansi.Strip(m.View()), "[Enter] Confirm Start") {
			t.Fatal("Start confirmation should be available in Graph")
		}
		var cmd tea.Cmd
		if useMouse {
			cmd = clickRenderedText(t, m, "[Enter] Confirm Start")
		} else {
			_, cmd = m.handleKeyMsg(keyMessage("enter"))
		}
		if cmd == nil || m.dependencyModal.plan || m.mode != ModeDependencies {
			t.Fatal("Start should run directly in Graph")
		}
		m.handleOperationResult(cmd().(operationResultMsg))
		if recorder.method != "start" || !slices.Equal(recorder.names, []string{"gateway"}) {
			t.Fatalf("Graph ran %s %v", recorder.method, recorder.names)
		}
		if m.dependencyModal.plan || m.mode != ModeDependencies || !strings.Contains(ansi.Strip(m.View()), "Start complete") {
			t.Fatal("Graph should stay open and display the result")
		}
	}
}
