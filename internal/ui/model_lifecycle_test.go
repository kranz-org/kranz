package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
)

// Tests for service lifecycle actions driven from the dashboard.

func TestEnterDoesNotControlServiceLifecycle(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	serviceInstance := model.FocusedService()

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if command != nil {
		t.Fatal("Enter scheduled a lifecycle operation")
	}
	if serviceInstance.Status() != config.StatusStopped {
		t.Fatalf("Enter changed status to %s", serviceInstance.Status())
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
		svc.SetStatus(config.StatusRunning)
		model.selected[svc.Name] = true
	}

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	message := command().(operationResultMsg)
	if message.kind != operationStopSet || message.err != nil {
		t.Fatalf("selection result = kind %q, error %v", message.kind, message.err)
	}
	for _, svc := range model.allServices {
		if svc.Status() != config.StatusStopped {
			t.Errorf("service %s status = %s", svc.Name, svc.Status())
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
	waitForServiceStatus(t, model.FocusedService(), config.StatusRunning)

	_, stopCommand := model.toggleSelectedServices()
	if stopCommand == nil {
		t.Fatal("s was blocked while start waited for readiness")
	}
	stopMessage := stopCommand().(operationResultMsg)
	_, _ = model.Update(stopMessage)
	if model.FocusedService().Status() != config.StatusStopped {
		t.Fatalf("service status = %s after interrupted stop", model.FocusedService().Status())
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
	api, _ := model.manager.GetService("api")
	database, _ := model.manager.GetService("database")
	if api.Status() != config.StatusRunning || database.Status() != config.StatusStopped {
		t.Fatalf("force start statuses: api=%s database=%s", api.Status(), database.Status())
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
		if err := model.manager.ForceStartServices([]string{"backend", "frontend"}); err != nil {
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
		if command == nil || model.operationKind != operationStopSet {
			t.Fatal("s did not schedule dependency-aware stop")
		}
		_, _ = model.Update(command().(operationResultMsg))
		for _, name := range []string{"backend", "frontend"} {
			svc, _ := model.manager.GetService(name)
			if svc.Status() != config.StatusStopped {
				t.Errorf("%s status = %s, want stopped", name, svc.Status())
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
		if command == nil || model.operationKind != operationForceStop {
			t.Fatal("Shift+S did not schedule force stop")
		}
		_, _ = model.Update(command().(operationResultMsg))
		backend, _ := model.manager.GetService("backend")
		frontend, _ := model.manager.GetService("frontend")
		if backend.Status() != config.StatusStopped || frontend.Status() != config.StatusRunning {
			t.Fatalf("force stop statuses: backend=%s frontend=%s", backend.Status(), frontend.Status())
		}
		if !strings.Contains(model.toastMessage, "without stopping dependents") {
			t.Fatalf("force stop notification = %q", model.toastMessage)
		}
	})
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
	api, _ := model.manager.GetService("api")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && (api.Status() != config.StatusStopped || !api.DesiredRunning()) {
		time.Sleep(10 * time.Millisecond)
	}
	if api.Status() != config.StatusStopped || !api.DesiredRunning() {
		t.Fatalf("api did not enter queued state: status=%s desired=%v", api.Status(), api.DesiredRunning())
	}

	_, forceCommand := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if forceCommand == nil || model.operationKind != operationForceStart {
		t.Fatal("Shift+S did not replace the queued start")
	}
	_, _ = model.Update(forceCommand().(operationResultMsg))
	if api.Status() != config.StatusRunning {
		t.Fatalf("force-started api status = %s", api.Status())
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
	if model.FocusedService().Status() != config.StatusRunning {
		t.Fatalf("focused service status = %s", model.FocusedService().Status())
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
	state := model.FocusedService().GetState()
	state.StartedAt = time.Now().Add(-2 * time.Minute)
	state.Completed = true
	state.ExitCode = 1
	state.RestartCount = 2
	model.FocusedService().SetState(state)
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

func waitForServiceStatus(t *testing.T, svc interface{ Status() config.ServiceStatus }, expected config.ServiceStatus) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if svc.Status() == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("service status = %s, want %s", svc.Status(), expected)
}
