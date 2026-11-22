package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

func TestDetachOnExitLeavesRuntimeUsable(t *testing.T) {
	cfg := &config.Config{Project: "Attached", Services: map[string]config.Service{"worker": {Command: "sleep 60"}}}
	local := app.NewLocal(cfg, nil, app.Options{})
	model := NewModelWithOptions(cfg, "test", ModelOptions{App: local, DetachOnExit: true})
	if err := model.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := local.StartServicesContext(context.Background(), []string{"worker"}); err != nil {
		t.Fatalf("runtime was shut down by detach: %v", err)
	}
	defer func() { _ = local.Shutdown() }()
	worker, ok := local.Service("worker")
	if !ok || worker.State.Status != config.StatusRunning {
		t.Fatalf("worker after detach = %+v", worker)
	}
}

func TestQuitConfirmationCanStopOrDetachRuntime(t *testing.T) {
	t.Run("detach", func(t *testing.T) {
		cfg := &config.Config{Project: "Detached exit", Services: map[string]config.Service{"worker": {Command: "sleep 60"}}}
		local := app.NewLocal(cfg, nil, app.Options{})
		if err := local.StartServicesContext(context.Background(), []string{"worker"}); err != nil {
			t.Fatal(err)
		}
		model := NewModelWithOptions(cfg, "test", ModelOptions{App: local, DetachOnExit: true})
		model.mode = ModeConfirmQuit
		_, command := model.handleConfirmQuitKeys(keyMessage("d"))
		if command == nil || !model.detachOnExit {
			t.Fatalf("detach command = %v, detachOnExit = %v", command, model.detachOnExit)
		}
		message, ok := command().(shutdownResultMsg)
		if !ok || message.err != nil {
			t.Fatalf("detach result = %#v", message)
		}
		if !local.HasRunningServices() {
			t.Fatal("detach stopped the running service")
		}
		if err := local.Shutdown(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("stop", func(t *testing.T) {
		cfg := &config.Config{Project: "Stopped exit", Services: map[string]config.Service{"worker": {Command: "sleep 60"}}}
		local := app.NewLocal(cfg, nil, app.Options{})
		if err := local.StartServicesContext(context.Background(), []string{"worker"}); err != nil {
			t.Fatal(err)
		}
		model := NewModelWithOptions(cfg, "test", ModelOptions{App: local, DetachOnExit: true})
		model.mode = ModeConfirmQuit
		_, command := model.handleConfirmQuitKeys(keyMessage("enter"))
		if command == nil || model.detachOnExit {
			t.Fatalf("shutdown command = %v, detachOnExit = %v", command, model.detachOnExit)
		}
		message, ok := command().(shutdownResultMsg)
		if !ok || message.err != nil {
			t.Fatalf("shutdown result = %#v", message)
		}
		if local.HasRunningServices() {
			t.Fatal("confirmed shutdown left the service running")
		}
	})

	t.Run("stay", func(t *testing.T) {
		model := newTestModel()
		defer model.Shutdown()
		model.mode = ModeConfirmQuit
		_, command := model.handleConfirmQuitKeys(keyMessage("n"))
		if command != nil || model.mode != ModeNormal {
			t.Fatalf("stay result = command %v, mode %v", command, model.mode)
		}
	})
}

func TestQuitAlwaysConfirmsWhenRuntimeHasNoRunningServices(t *testing.T) {
	model := NewModelWithOptions(&config.Config{Project: "Idle runtime"}, "test", ModelOptions{DetachOnExit: true})
	defer model.Shutdown()

	_, command := model.handleKeyMsg(keyMessage("q"))
	if command != nil || model.mode != ModeConfirmQuit {
		t.Fatalf("idle runtime quit = command %v, mode %v", command, model.mode)
	}
}

func TestQuitConfirmationMouseCanDetach(t *testing.T) {
	model := newTestModel()
	model.detachOnExit = false
	model.width, model.height, model.ready = 100, 40, true
	model.mode = ModeConfirmQuit
	command := clickRenderedText(t, model, "[d]       Detach and keep runtime running")
	if command == nil || !model.detachOnExit {
		t.Fatalf("detach click = command %v, detachOnExit %v", command, model.detachOnExit)
	}
	message, ok := command().(shutdownResultMsg)
	if !ok || message.err != nil {
		t.Fatalf("detach click result = %#v", message)
	}
	if err := model.app.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestQuitConfirmationOffersClickableCloseAndChooseWhenSupported(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.registry = testRegistry(t)
	model.width, model.height, model.ready = 100, 40, true
	model.mode = ModeConfirmQuit

	command := clickRenderedText(t, model, "[c]       Close & choose another runtime")
	if command == nil || !model.exiting || model.operation != "Closing runtime" {
		t.Fatalf("close-and-choose click = command %v, exiting %v, operation %q", command, model.exiting, model.operation)
	}
}

// Tests for service lifecycle actions driven from the dashboard.

func TestEnterDoesNotControlServiceLifecycle(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	serviceInstance := model.FocusedService()

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if command != nil {
		t.Fatal("Enter scheduled a lifecycle operation")
	}
	if serviceInstance.State.Status != config.StatusStopped {
		t.Fatalf("Enter changed status to %s", serviceInstance.State.Status)
	}
}

func TestDetachedServiceStartsFromUnknownAndConfirmsStop(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "running")
	model := NewModel(&config.Config{Project: "Detached", Services: map[string]config.Service{
		"stack": {
			Supervision: config.SupervisionDetached,
			Lifecycle: config.LifecycleConfig{
				Start: &config.Action{Command: "touch " + marker, Dir: directory, Shell: "/bin/sh"},
				Stop:  &config.Action{Command: "rm " + marker, Dir: directory, Shell: "/bin/sh"},
			},
		},
	}}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 30, true
	if model.FocusedService().State.Status != config.StatusUnknown {
		t.Fatalf("initial status = %s", model.FocusedService().State.Status)
	}
	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if command == nil {
		t.Fatal("detached start was not scheduled")
	}
	_, _ = model.Update(command())
	if model.FocusedService().State.Status != config.StatusRunning {
		t.Fatalf("started status = %s", model.FocusedService().State.Status)
	}
	_, command = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlA})
	if command != nil || model.mode != ModeConfirmServiceStop || !model.pendingStopAll {
		t.Fatalf("stop-all confirmation = command %v, mode %v, all %v", command, model.mode, model.pendingStopAll)
	}
	_, _ = model.handleConfirmServiceStopKeys(tea.KeyMsg{Type: tea.KeyEsc})
	_, command = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if command != nil || model.mode != ModeConfirmServiceStop {
		t.Fatalf("detached stop confirmation = command %v, mode %v", command, model.mode)
	}
	view := ansi.Strip(model.renderConfirmServiceStopView())
	if !strings.Contains(view, "Stop stack?") || !strings.Contains(view, "[Enter/y] Stop") {
		t.Fatalf("detached stop modal:\n%s", view)
	}
	_, command = model.handleConfirmServiceStopKeys(tea.KeyMsg{Type: tea.KeyEsc})
	if command != nil || model.FocusedService().State.Status != config.StatusRunning {
		t.Fatal("cancelled stop changed service")
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	_, command = model.handleConfirmServiceStopKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("confirmed stop was not scheduled")
	}
	_, _ = model.Update(command())
	if model.FocusedService().State.Status != config.StatusStopped {
		t.Fatalf("stopped status = %s", model.FocusedService().State.Status)
	}
}

func TestUnobservedDetachedServiceUsesNeutralExternalVisualState(t *testing.T) {
	model := &Model{}
	unobserved := &app.ServiceSnapshot{
		Name: "remote",
		Config: config.Service{
			Supervision: config.SupervisionDetached,
			Lifecycle: config.LifecycleConfig{
				Start: &config.Action{Command: "true"},
				Stop:  &config.Action{Command: "true"},
			},
		},
		State: config.ServiceState{Status: config.StatusUnknown},
	}
	if state := model.serviceVisualState(unobserved); state != visualExternal {
		t.Fatalf("unobserved detached visual state = %v, want external", state)
	}
	if label := serviceStatusLabel(config.StatusUnknown, visualExternal); label != "External" {
		t.Fatalf("unobserved detached label = %q, want External", label)
	}

	observed := &app.ServiceSnapshot{
		Name: "remote",
		Config: config.Service{
			Supervision: config.SupervisionDetached,
			Lifecycle: config.LifecycleConfig{Status: &config.LifecycleStatusConfig{
				CheckConfig: config.CheckConfig{Type: config.CheckCommand, Command: "exit 4"},
			}},
		},
		State: config.ServiceState{Status: config.StatusUnknown},
	}
	if state := model.serviceVisualState(observed); state != visualChecking {
		t.Fatalf("unobserved status probe visual state = %v, want checking", state)
	}
}

func TestLifecycleStartConfirmationIsConfigurable(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "started")
	confirm := true
	model := NewModel(&config.Config{Project: "Confirmed start", Services: map[string]config.Service{
		"migration": {
			Lifecycle: config.LifecycleConfig{Start: &config.Action{
				Command: "touch " + marker, Dir: directory, Shell: "/bin/sh", Confirm: &confirm,
			}},
		},
	}}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 30, true
	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if command != nil || model.mode != ModeConfirmServiceStart {
		t.Fatalf("start confirmation = command %v, mode %v", command, model.mode)
	}
	body := model.renderServiceStartConfirmationBody()
	plainBody := ansi.Strip(strings.Join(body, "\n"))
	for _, expected := range []string{"⚠ CONFIRM BEFORE STARTING", "SERVICE  migration", "COMMAND"} {
		if !strings.Contains(plainBody, expected) {
			t.Fatalf("start confirmation body is missing %q:\n%s", expected, plainBody)
		}
	}
	unwrappedBody := strings.ReplaceAll(plainBody, "\n  ", "")
	if !strings.Contains(unwrappedBody, "touch "+marker) {
		t.Fatalf("start confirmation body does not contain the complete command:\n%s", plainBody)
	}
	if body[0] != StartingBadgeStyle.Render("⚠ CONFIRM BEFORE STARTING") ||
		!strings.Contains(body[1], ServiceNameStyle.Render("migration")) {
		t.Fatalf("start confirmation does not emphasize its warning and service:\n%q", body)
	}
	view := ansi.Strip(model.renderConfirmServiceStartView())
	if !strings.Contains(view, "Start migration?") || !strings.Contains(view, "[Enter/y] Start") {
		t.Fatalf("start confirmation modal:\n%s", view)
	}
	_, command = model.handleConfirmServiceStartKeys(tea.KeyMsg{Type: tea.KeyEsc})
	if command != nil {
		t.Fatal("cancelled start scheduled a command")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("cancelled start created marker: %v", err)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	_, command = model.handleConfirmServiceStartKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("confirmed start was not scheduled")
	}
	_, _ = model.Update(command())
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("confirmed start did not create %s", marker)
}

func TestStartConfirmationIdentifiesConfirmedDependencyCommand(t *testing.T) {
	confirm := true
	model := NewModel(&config.Config{Project: "Confirmed dependency", Services: map[string]config.Service{
		"app": {
			Command:   "start-app",
			DependsOn: []string{"migration"},
		},
		"migration": {
			Lifecycle: config.LifecycleConfig{Start: &config.Action{
				Command: "run-migrations", Confirm: &confirm,
			}},
		},
	}}, "test")
	defer model.Shutdown()
	model.width = 100
	model.pendingStartNames = []string{"app"}

	body := ansi.Strip(strings.Join(model.renderServiceStartConfirmationBody(), "\n"))
	for _, expected := range []string{"SERVICE  migration", "COMMAND", "run-migrations"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("dependency confirmation is missing %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "start-app") {
		t.Fatalf("dependency confirmation includes an unconfirmed command:\n%s", body)
	}
}

func TestRestartOperationsConfirmTheirStopPhase(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.app.SetServiceStatusForTest(model.FocusedService().Name, config.StatusRunning)
	model.refreshServices()

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if command != nil || model.mode != ModeConfirmRestart || model.confirmRestartAll {
		t.Fatalf("single restart confirmation = command %v, mode %v, all %v", command, model.mode, model.confirmRestartAll)
	}
	_, _ = model.handleConfirmRestartKeys(tea.KeyMsg{Type: tea.KeyEsc})

	_, command = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlR})
	if command != nil || model.mode != ModeConfirmRestart || !model.confirmRestartAll {
		t.Fatalf("restart-all confirmation = command %v, mode %v, all %v", command, model.mode, model.confirmRestartAll)
	}
}

func TestSpaceSelectsServicesAndSTogglesSelection(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model.moveFocus(1)
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if len(model.selected) != 2 {
		t.Fatalf("selected service count = %d, want 2", len(model.selected))
	}

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if command == nil {
		t.Fatal("s did not schedule selection start")
	}
	rawMessage := command()
	message, ok := rawMessage.(operationResultMsg)
	if !ok {
		t.Fatalf("command returned %T", rawMessage)
	}
	if message.kind != operationStartSet || message.err != nil {
		t.Fatalf("selection result = kind %q, error %v", message.kind, message.err)
	}
}

func TestSStopsTargetsWhenAllAreActive(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	for _, svc := range model.allServices {
		model.app.SetServiceStatusForTest(svc.Name, config.StatusRunning)
		model.selected[svc.Name] = true
	}
	model.refreshServices()

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	command = acceptServiceStop(t, model, command)
	message := command().(operationResultMsg)
	if message.kind != operationStopSet || message.err != nil {
		t.Fatalf("selection result = kind %q, error %v", message.kind, message.err)
	}
	model.refreshServices()
	for _, svc := range model.allServices {
		if svc.State.Status != config.StatusStopped {
			t.Errorf("service %s status = %s", svc.Name, svc.State.Status)
		}
	}
}

func TestASelectsAllAndClearsAll(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	pressKey(model, 'a')
	if len(model.selected) != len(model.allServices) {
		t.Fatalf("a selected %d services, want %d", len(model.selected), len(model.allServices))
	}
	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if command == nil {
		t.Fatal("s did not start the all-services selection")
	}
	message := command().(operationResultMsg)
	if message.kind != operationStartSet || message.err != nil {
		t.Fatalf("selected-all start = kind %q, error %v", message.kind, message.err)
	}
	_, _ = model.Update(message)

	pressKey(model, 'a')
	if len(model.selected) != 0 {
		t.Fatalf("second a left selected services: %v", model.selected)
	}
}

func TestASelectsEveryServiceEvenInTagsMode(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.listMode = listTags

	pressKey(model, 'a')
	targets := model.selectedTargetNames()
	if len(targets) != len(model.allServices) {
		t.Fatalf("tag-mode a targets = %v, want every service", targets)
	}
	if label := model.selectedTargetLabel(targets); label != "2 selected services" {
		t.Fatalf("tag-mode all-selection label = %q", label)
	}
}

func TestTagsPanelSelectsLifecycleTargets(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.selected["worker"] = true
	servicesTitle := ansi.Strip(model.renderServicePanel(40, 8))
	if !strings.Contains(servicesTitle, "1→Tags") {
		t.Fatalf("services panel does not explain tag switching:\n%s", servicesTitle)
	}

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if model.listMode != listTags || model.panelFocus != panelServices {
		t.Fatalf("tag view state = mode %v panel %v", model.listMode, model.panelFocus)
	}
	if len(model.selected) != 1 || !model.selected["api"] || len(model.selectedTags) != 1 || model.selectedTags[0] != "backend" {
		t.Fatalf("selection = services %v tags %v", model.selected, model.selectedTags)
	}
	if targets := model.selectedTargetNames(); len(targets) != 1 || targets[0] != "api" {
		t.Fatalf("backend tag targets = %v", targets)
	}
	plain := ansi.Strip(model.renderServicePanel(40, 8))
	if !strings.Contains(plain, "[1] TAGS") || !strings.Contains(plain, "Enter expand") || !strings.Contains(plain, "backend (1)") {
		t.Fatalf("tag panel is incomplete:\n%s", plain)
	}
}

func TestStopInterruptsReadinessGatedStart(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {
			Command: "sleep 60", Dir: ".", Shell: "sh",
			HealthCheck: &config.HealthCheckConfig{Readiness: &config.CheckConfig{
				Type: config.CheckCommand, Command: "exit 1", Interval: time.Hour, Timeout: time.Second,
			}},
		},
	}}, "test")
	defer model.Shutdown()

	_, startCommand := model.toggleSelectedServices()
	startResult := make(chan operationResultMsg, 1)
	go func() { startResult <- startCommand().(operationResultMsg) }()
	waitForServiceStatus(t, model, model.FocusedService().Name, config.StatusRunning)

	_, stopCommand := model.toggleSelectedServices()
	stopCommand = acceptServiceStop(t, model, stopCommand)
	stopMessage := stopCommand().(operationResultMsg)
	_, _ = model.Update(stopMessage)
	if model.FocusedService().State.Status != config.StatusStopped {
		t.Fatalf("service status = %s after interrupted stop", model.FocusedService().State.Status)
	}
	select {
	case stale := <-startResult:
		_, _ = model.Update(stale)
	case <-time.After(time.Second):
		t.Fatal("canceled start did not return promptly")
	}
}

func TestShiftSForceStartsOnlySelectedService(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"database": {Command: "sleep 60", Dir: ".", Shell: "sh"},
		"api":      {Command: "sleep 60", Dir: ".", Shell: "sh", DependsOn: []string{"database"}},
	}}, "test")
	defer model.Shutdown()
	model.selected["api"] = true

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if command == nil || model.operationKind != operationForceStart {
		t.Fatal("Shift+S did not schedule force start")
	}
	message := command().(operationResultMsg)
	_, _ = model.Update(message)
	api, _ := model.app.Service("api")
	database, _ := model.app.Service("database")
	if api.State.Status != config.StatusRunning || database.State.Status != config.StatusStopped {
		t.Fatalf("force start statuses: api=%s database=%s", api.State.Status, database.State.Status)
	}
	if !strings.Contains(model.toastMessage, "without dependencies") {
		t.Fatalf("force start notification = %q", model.toastMessage)
	}
}

func TestSStopsDependentsAndShiftSStopsOnlySelectedService(t *testing.T) {
	newModel := func(t *testing.T) *Model {
		t.Helper()
		model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
			"backend":  {Command: "sleep 60", Dir: ".", Shell: "sh"},
			"frontend": {Command: "sleep 60", Dir: ".", Shell: "sh", DependsOn: []string{"backend"}},
		}}, "test")
		if err := model.app.ForceStartServices([]string{"backend", "frontend"}); err != nil {
			model.Shutdown()
			t.Fatal(err)
		}
		model.selected["backend"] = true
		return model
	}

	t.Run("s stops dependent services", func(t *testing.T) {
		model := newModel(t)
		defer model.Shutdown()
		_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
		command = acceptServiceStop(t, model, command)
		if command == nil || model.operationKind != operationStopSet {
			t.Fatal("s did not schedule dependency-aware stop")
		}
		_, _ = model.Update(command().(operationResultMsg))
		for _, name := range []string{"backend", "frontend"} {
			svc, _ := model.app.Service(name)
			if svc.State.Status != config.StatusStopped {
				t.Errorf("%s status = %s, want stopped", name, svc.State.Status)
			}
		}
		if !strings.Contains(model.toastMessage, "dependent services stopped") {
			t.Fatalf("normal stop notification = %q", model.toastMessage)
		}
	})

	t.Run("Shift+S leaves dependents running", func(t *testing.T) {
		model := newModel(t)
		defer model.Shutdown()
		_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
		command = acceptServiceStop(t, model, command)
		if command == nil || model.operationKind != operationForceStop {
			t.Fatal("Shift+S did not schedule force stop")
		}
		_, _ = model.Update(command().(operationResultMsg))
		backend, _ := model.app.Service("backend")
		frontend, _ := model.app.Service("frontend")
		if backend.State.Status != config.StatusStopped || frontend.State.Status != config.StatusRunning {
			t.Fatalf("force stop statuses: backend=%s frontend=%s", backend.State.Status, frontend.State.Status)
		}
		if !strings.Contains(model.toastMessage, "without stopping dependents") {
			t.Fatalf("force stop notification = %q", model.toastMessage)
		}
	})
}

func acceptServiceStop(t *testing.T, model *Model, command tea.Cmd) tea.Cmd {
	t.Helper()
	if command != nil || model.mode != ModeConfirmServiceStop {
		t.Fatalf("stop did not request confirmation: command %v, mode %v", command, model.mode)
	}
	_, command = model.handleConfirmServiceStopKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("confirmed stop was not scheduled")
	}
	return command
}

func TestShiftSOverridesQueuedDependencyStart(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"server": {Command: "sleep 60", Dir: ".", Shell: "sh", ReadyLogLine: "NEVER"},
		"api": {
			Command: "sleep 60", Dir: ".", Shell: "sh", DependsOn: []string{"server"},
			DependencyConditions: map[string]config.DependencyConfig{
				"server": {Condition: config.DependencyLogReady},
			},
		},
	}}, "test")
	defer model.Shutdown()
	model.selected["api"] = true

	_, queuedCommand := model.toggleSelectedServices()
	queuedResult := make(chan operationResultMsg, 1)
	go func() { queuedResult <- queuedCommand().(operationResultMsg) }()
	api, _ := model.app.Service("api")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && (api.State.Status != config.StatusStopped || !api.DesiredRunning) {
		time.Sleep(10 * time.Millisecond)
		api, _ = model.app.Service("api")
	}
	if api.State.Status != config.StatusStopped || !api.DesiredRunning {
		t.Fatalf("api did not enter queued state: status=%s desired=%v", api.State.Status, api.DesiredRunning)
	}

	_, forceCommand := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if forceCommand == nil || model.operationKind != operationForceStart {
		t.Fatal("Shift+S did not replace the queued start")
	}
	_, _ = model.Update(forceCommand().(operationResultMsg))
	api, _ = model.app.Service("api")
	if api.State.Status != config.StatusRunning {
		t.Fatalf("force-started api status = %s", api.State.Status)
	}
	select {
	case stale := <-queuedResult:
		_, _ = model.Update(stale)
	case <-time.After(time.Second):
		t.Fatal("overridden dependency start did not cancel promptly")
	}
}

func TestMouseCanForceStartFocusedService(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {Command: "sleep 60", Dir: ".", Shell: "sh"},
	}}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true

	command := clickRenderedText(t, model, "Force: S")
	if command == nil || model.operationKind != operationForceStart {
		t.Fatal("force-start button did not schedule the operation")
	}
	_, _ = model.Update(command().(operationResultMsg))
	if model.FocusedService().State.Status != config.StatusRunning {
		t.Fatalf("focused service status = %s", model.FocusedService().State.Status)
	}
}

func TestMouseClickChangesServiceStartedByFooterAction(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api":    {Command: "sleep 60", Dir: ".", Shell: "sh"},
		"worker": {Command: "sleep 60", Dir: ".", Shell: "sh"},
	}}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	model.selected["api"] = true

	clickRenderedText(t, model, "worker")
	command := clickRenderedText(t, model, "Start: s")
	if command == nil {
		t.Fatal("start button did not schedule the operation")
	}
	_, _ = model.Update(command().(operationResultMsg))

	api, _ := model.app.Service("api")
	worker, _ := model.app.Service("worker")
	if api.State.Status != config.StatusStopped || worker.State.Status != config.StatusRunning {
		t.Fatalf("after clicking worker, statuses api=%s worker=%s", api.State.Status, worker.State.Status)
	}
}

func TestMouseCheckboxKeepsExplicitMultiSelection(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api":    {Command: "true"},
		"worker": {Command: "true"},
	}}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	model.selected["api"] = true

	clickRenderedText(t, model, "[ ]")
	if !model.selected["api"] || !model.selected["worker"] || len(model.selected) != 2 {
		t.Fatalf("checkbox selection = %#v, want api and worker", model.selected)
	}
}

func TestMouseClickInTagGroupChangesServiceStartedByFooterAction(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api":    {Command: "sleep 60", Dir: ".", Shell: "sh", Tags: []string{"backend"}},
		"worker": {Command: "sleep 60", Dir: ".", Shell: "sh", Tags: []string{"backend"}},
	}}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	model.listMode = listTags
	model.expandedTags["backend"] = true
	model.selected["api"] = true

	clickRenderedText(t, model, "worker")
	command := clickRenderedText(t, model, "Start: s")
	if command == nil {
		t.Fatal("start button did not schedule the operation")
	}
	_, _ = model.Update(command().(operationResultMsg))

	api, _ := model.app.Service("api")
	worker, _ := model.app.Service("worker")
	if api.State.Status != config.StatusStopped || worker.State.Status != config.StatusRunning {
		t.Fatalf("after clicking grouped worker, statuses api=%s worker=%s", api.State.Status, worker.State.Status)
	}
}

func TestDetailsShowLifecycleConfiguration(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"db": {Command: "sleep 60", ReadyLogLine: "READY"},
		"api": {
			Command: "sleep 60", DependsOn: []string{"db"},
			DependencyConditions: map[string]config.DependencyConfig{"db": {Condition: config.DependencyLogReady}},
			Availability:         config.AvailabilityConfig{Restart: "on_failure", Backoff: 2 * time.Second, MaxRestarts: 3, ExitOnSkipped: true},
			Shutdown:             config.ShutdownConfig{Signal: 2, Timeout: 5 * time.Second, ParentOnly: true},
			EnvFiles:             []string{"api.env"}, SuccessExitCodes: []int{7}, Disabled: true,
		},
	}}, "test")
	defer model.Shutdown()
	model.focused = 0
	for index, svc := range model.services {
		if svc.Name == "api" {
			model.focused = index
		}
	}
	state := model.FocusedService().State
	state.StartedAt = time.Now().Add(-2 * time.Minute)
	state.Completed = true
	state.ExitCode = 1
	state.RestartCount = 2
	model.app.SetServiceStateForTest(model.FocusedService().Name, state)
	model.FocusedService().State = state
	plain := ansi.Strip(strings.Join(model.serviceDetailLines(model.FocusedService(), 80), "\n"))
	for _, expected := range []string{
		"LAST START", "LAST EXIT code 1",
		"db · process_log_ready", "RECOVERY\n  ↳ restart on_failure\n  ↳ backoff 2s\n  ↳ restarts 2/3",
		"SHUTDOWN\n  ↳ signal 2\n  ↳ timeout 5s\n  ↳ target parent only",
		"ENV FILES api.env", "SUCCESS 0, 7", "DISABLED manual start only",
	} {
		if !strings.Contains(plain, expected) {
			t.Errorf("details do not contain %q:\n%s", expected, plain)
		}
	}
	if disabled, pid := strings.Index(plain, "DISABLED manual start only"), strings.Index(plain, "PID "); disabled < 0 || pid < 0 || disabled > pid {
		t.Errorf("DISABLED state is not shown before process details:\n%s", plain)
	}
}

// gatedModel builds a project whose "api" start is permanently blocked on
// "server"'s log line, so its operation stays in flight for a test to race.
func gatedModel(t *testing.T) *Model {
	t.Helper()
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"server": {Command: "sleep 60", Dir: ".", Shell: "sh", ReadyLogLine: "NEVER"},
		"api": {
			Command: "sleep 60", Dir: ".", Shell: "sh", DependsOn: []string{"server"},
			DependencyConditions: map[string]config.DependencyConfig{
				"server": {Condition: config.DependencyLogReady},
			},
		},
		"api2": {
			Command: "sleep 60", Dir: ".", Shell: "sh", DependsOn: []string{"server"},
			DependencyConditions: map[string]config.DependencyConfig{
				"server": {Condition: config.DependencyLogReady},
			},
		},
		"docs": {Command: "sleep 60", Dir: ".", Shell: "sh"},
	}}, "test")
	return model
}

func TestDisjointStartRunsWhileDependencyGateBlocks(t *testing.T) {
	model := gatedModel(t)
	defer model.Shutdown()
	model.selected["api"] = true

	_, first := model.toggleSelectedServices()
	if first == nil {
		t.Fatal("dependency-gated start was not scheduled")
	}
	firstResult := make(chan operationResultMsg, 1)
	go func() { firstResult <- first().(operationResultMsg) }()
	waitForServiceStatus(t, model, "server", config.StatusRunning)

	// docs shares no dependency with the gated api closure, so starting it
	// must not be serialized behind the api operation.
	model.selected = map[string]bool{"docs": true}
	_, second := model.toggleSelectedServices()
	if second == nil {
		t.Fatal("start disjoint from the in-flight dependency gate was refused")
	}
	_, _ = model.Update(second().(operationResultMsg))
	docs, _ := model.app.Service("docs")
	if docs.State.Status != config.StatusRunning {
		t.Fatalf("docs status = %s, want running beside the gated api start", docs.State.Status)
	}
	if len(model.operations) != 1 {
		t.Fatalf("tracked operations = %d, want the gated api start still in flight", len(model.operations))
	}

	model.cancelStartOperation()
	select {
	case stale := <-firstResult:
		_, _ = model.Update(stale)
	case <-time.After(time.Second):
		t.Fatal("cancelled dependency-gated start did not return promptly")
	}
	if len(model.operations) != 0 {
		t.Fatalf("operations after cancel = %d, want 0", len(model.operations))
	}
}

func TestOverlappingStartIsRefusedWhileDependencyGateBlocks(t *testing.T) {
	model := gatedModel(t)
	defer model.Shutdown()
	model.selected["api"] = true

	_, first := model.toggleSelectedServices()
	if first == nil {
		t.Fatal("first start was not scheduled")
	}
	firstResult := make(chan operationResultMsg, 1)
	go func() { firstResult <- first().(operationResultMsg) }()
	waitForServiceStatus(t, model, "server", config.StatusRunning)

	// api2 depends on the same server, so its closure overlaps the in-flight
	// operation and must be refused rather than racing the shared dependency.
	model.selected = map[string]bool{"api2": true}
	_, second := model.toggleSelectedServices()
	if second != nil {
		t.Fatal("start sharing a dependency with the in-flight operation was allowed")
	}
	if !strings.Contains(model.toastMessage, "Wait for the current operation") {
		t.Fatalf("refusal notification = %q", model.toastMessage)
	}
	if len(model.operations) != 1 {
		t.Fatalf("tracked operations = %d, want only the original gate", len(model.operations))
	}

	model.cancelStartOperation()
	select {
	case stale := <-firstResult:
		_, _ = model.Update(stale)
	case <-time.After(time.Second):
		t.Fatal("cancelled dependency-gated start did not return promptly")
	}
}

func TestStoppingDisjointServiceKeepsDependencyGatedStart(t *testing.T) {
	model := gatedModel(t)
	defer model.Shutdown()
	model.selected["api"] = true

	_, first := model.toggleSelectedServices()
	firstResult := make(chan operationResultMsg, 1)
	go func() { firstResult <- first().(operationResultMsg) }()
	waitForServiceStatus(t, model, "server", config.StatusRunning)

	model.selected = map[string]bool{"docs": true}
	_, startDocs := model.toggleSelectedServices()
	_, _ = model.Update(startDocs().(operationResultMsg))
	_, confirmation := model.toggleSelectedServices()
	if confirmation != nil || model.mode != ModeConfirmServiceStop {
		t.Fatal("stopping docs did not open the expected confirmation")
	}
	if len(model.operations) != 1 {
		t.Fatalf("confirmation cancelled disjoint start; tracked operations = %d", len(model.operations))
	}
	_, stopDocs := model.confirmServiceStop()
	if stopDocs == nil {
		t.Fatal("stop disjoint from the gated start was refused")
	}
	_, _ = model.Update(stopDocs().(operationResultMsg))
	if len(model.operations) != 1 {
		t.Fatalf("tracked operations = %d, want gated start to remain in flight", len(model.operations))
	}

	model.cancelStartOperation()
	select {
	case stale := <-firstResult:
		_, _ = model.Update(stale)
	case <-time.After(time.Second):
		t.Fatal("cancelled dependency-gated start did not return promptly")
	}
}

func TestForceStartDisjointServiceKeepsDependencyGatedStart(t *testing.T) {
	model := gatedModel(t)
	defer model.Shutdown()
	model.selected["api"] = true

	_, first := model.toggleSelectedServices()
	firstResult := make(chan operationResultMsg, 1)
	go func() { firstResult <- first().(operationResultMsg) }()
	waitForServiceStatus(t, model, "server", config.StatusRunning)

	model.selected = map[string]bool{"docs": true}
	_, forceDocs := model.forceToggleSelectedServices()
	if forceDocs == nil {
		t.Fatal("force start disjoint from the gated start was refused")
	}
	_, _ = model.Update(forceDocs().(operationResultMsg))
	if len(model.operations) != 1 {
		t.Fatalf("tracked operations = %d, want gated start to remain in flight", len(model.operations))
	}

	model.cancelStartOperation()
	select {
	case stale := <-firstResult:
		_, _ = model.Update(stale)
	case <-time.After(time.Second):
		t.Fatal("cancelled dependency-gated start did not return promptly")
	}
}

func TestForceOperationDoesNotRaceUncancelableOverlap(t *testing.T) {
	model := gatedModel(t)
	defer model.Shutdown()
	model.operations[1] = &activeOperation{
		id:       1,
		kind:     operationRestart,
		label:    "Restarting server",
		services: nameSet([]string{"server"}),
	}
	model.operationID = 1
	model.refreshOperationMirror()

	_, command := model.beginOperation(operationForceStop, "server", "Force stopping server", []string{"server"}, func() error {
		return nil
	})
	if command != nil {
		t.Fatal("force stop raced an overlapping operation without a cancellation handle")
	}
	if len(model.operations) != 1 {
		t.Fatalf("tracked operations = %d, want original operation retained", len(model.operations))
	}
	if !strings.Contains(model.toastMessage, "Restarting server") {
		t.Fatalf("refusal notification = %q, want the actual overlapping operation", model.toastMessage)
	}
}

func TestShutdownCancelsEveryTrackedStart(t *testing.T) {
	model := gatedModel(t)
	model.detachOnExit = true
	firstCtx, firstCancel := context.WithCancel(context.Background())
	secondCtx, secondCancel := context.WithCancel(context.Background())
	model.operations[1] = &activeOperation{id: 1, kind: operationStart, cancel: firstCancel}
	model.operations[2] = &activeOperation{id: 2, kind: operationStartSet, cancel: secondCancel}

	if err := model.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	for name, ctx := range map[string]context.Context{"first": firstCtx, "second": secondCtx} {
		select {
		case <-ctx.Done():
		default:
			t.Fatalf("%s tracked start was not cancelled", name)
		}
	}
	if len(model.operations) != 0 {
		t.Fatalf("tracked operations after shutdown = %d, want 0", len(model.operations))
	}
}

func TestActionButtonsUseDependencyClosureForOperationOverlap(t *testing.T) {
	model := gatedModel(t)
	defer model.Shutdown()
	model.width = 120
	model.selected = map[string]bool{"api2": true}
	model.operations[1] = &activeOperation{
		id:       1,
		kind:     operationStartSet,
		label:    "Starting api",
		services: model.dependencyClosure([]string{"api"}),
	}

	buttons := model.actionButtons()
	if len(buttons) == 0 {
		t.Fatal("actionButtons() returned no buttons")
	}
	want := DisabledButtonStyle.Render("▶ Start: s")
	if buttons[0].rendered != want {
		t.Fatalf("overlapping dependency start button = %q, want disabled %q", buttons[0].rendered, want)
	}
}

func waitForServiceStatus(t *testing.T, model *Model, name string, expected config.ServiceStatus) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if svc, ok := model.app.Service(name); ok && svc.State.Status == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	svc, _ := model.app.Service(name)
	t.Fatalf("service status = %s, want %s", svc.State.Status, expected)
}

func TestShiftATogglesFocusedServiceWithDependencies(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"database": {},
		"api":      {DependsOn: []string{"database"}},
		"frontend": {DependsOn: []string{"api"}},
		"other":    {},
	}}, "test")
	defer model.Shutdown()
	shiftA := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}}
	for index, svc := range model.services {
		if svc.Name == "frontend" {
			model.moveFocus(index)
			break
		}
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	_, _ = model.handleKeyMsg(shiftA)
	if len(model.selected) != 3 || !model.selected["frontend"] || !model.selected["api"] || !model.selected["database"] {
		t.Fatalf("Shift+A selected %v, want frontend, api, and database", model.selected)
	}
	if panel := model.renderServicePanel(46, 8); !strings.Contains(panel, "SELECTED 3") {
		t.Fatal("narrow service panel hides the selected count")
	}
	_, _ = model.handleKeyMsg(shiftA)
	if len(model.selected) != 0 {
		t.Fatalf("repeating Shift+A left %v selected", model.selected)
	}
	_, _ = model.handleKeyMsg(shiftA)
	for index, svc := range model.services {
		if svc.Name == "database" {
			model.moveFocus(index)
			break
		}
	}
	_, _ = model.handleKeyMsg(shiftA)
	if len(model.selected) != 1 || !model.selected["database"] {
		t.Fatalf("Shift+A on a service without dependencies selected %v, want database only", model.selected)
	}
}

func TestShiftRRestartsOnlySelectedTargets(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"database": {Command: "sleep 60", Dir: ".", Shell: "sh"},
		"api":      {Command: "sleep 60", Dir: ".", Shell: "sh", DependsOn: []string{"database"}},
		"frontend": {Command: "sleep 60", Dir: ".", Shell: "sh", DependsOn: []string{"api"}},
		"worker":   {Command: "sleep 60", Dir: ".", Shell: "sh"},
	}}, "test")
	defer model.Shutdown()
	if err := model.app.ForceStartServices([]string{"api", "frontend"}); err != nil {
		t.Fatal(err)
	}
	model.refreshServices()
	before, _ := model.app.Service("frontend")
	model.selected["api"] = true
	model.selected["worker"] = true

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	if command != nil || model.mode != ModeConfirmRestart || model.confirmRestartAll {
		t.Fatal("Shift+R did not request a targeted restart confirmation")
	}
	_, command = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil || model.operationKind != operationForceRestart {
		t.Fatal("confirmed Shift+R did not schedule a targeted restart")
	}
	result := command().(operationResultMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	for name, wantRun := range map[string]uint32{"api": 2, "database": 0, "frontend": before.State.Run, "worker": 1} {
		svc, _ := model.app.Service(name)
		if svc.State.Run != wantRun {
			t.Errorf("%s run = %d, want %d", name, svc.State.Run, wantRun)
		}
	}
}
