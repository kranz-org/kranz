package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/health"
)

func TestDependencyGatedStartExposesAndClearsQueuedIntent(t *testing.T) {
	manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
		"server": {Command: "sleep 60", ReadyLogLine: "NEVER"},
		"api": {
			Command: "sleep 60", DependsOn: []string{"server"},
			DependencyConditions: map[string]config.DependencyConfig{
				"server": {Condition: config.DependencyLogReady},
			},
		},
	}})
	defer manager.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- manager.StartServicesContext(ctx, []string{"api"}) }()

	server, _ := manager.GetService("server")
	api, _ := manager.GetService("api")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if server.Status() == config.StatusRunning && api.Status() == config.StatusStopped && api.DesiredRunning() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if server.Status() != config.StatusRunning || api.Status() != config.StatusStopped || !api.DesiredRunning() {
		t.Fatalf("gated start state: server=%s api=%s desired=%v", server.Status(), api.Status(), api.DesiredRunning())
	}

	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("StartServicesContext() error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled dependency-gated start did not return")
	}
	if api.DesiredRunning() {
		t.Fatal("canceled service remained queued")
	}
}

func TestDetachedLifecycleRunsStartAndStopDefinitions(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "running")
	serviceConfig := config.Service{
		Supervision: config.SupervisionDetached,
		Lifecycle: config.LifecycleConfig{
			Start: &config.Action{Command: "touch " + marker, Dir: directory, Shell: "/bin/sh"},
			Stop:  &config.Action{Command: "rm " + marker, Dir: directory, Shell: "/bin/sh"},
		},
	}
	manager := NewManager(&config.Config{Project: "Detached", Services: map[string]config.Service{"stack": serviceConfig}})
	defer manager.Shutdown()
	service, _ := manager.GetService("stack")
	if service.Status() != config.StatusUnknown {
		t.Fatalf("initial detached status = %s", service.Status())
	}
	if err := manager.StartService("stack"); err != nil {
		t.Fatal(err)
	}
	if service.Status() != config.StatusRunning || service.PID() != 0 {
		t.Fatalf("started detached state = %#v", service.GetState())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("start marker: %v", err)
	}
	if err := manager.StopService("stack"); err != nil {
		t.Fatal(err)
	}
	if service.Status() != config.StatusStopped {
		t.Fatalf("stopped detached state = %#v", service.GetState())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("stop marker still exists: %v", err)
	}
}

func TestCancelDetachedStartLeavesUnknownAndAllowsCleanup(t *testing.T) {
	directory := t.TempDir()
	started := filepath.Join(directory, "start-began")
	finished := filepath.Join(directory, "start-finished")
	cleaned := filepath.Join(directory, "cleaned")
	manager := NewManager(&config.Config{Project: "Detached", Services: map[string]config.Service{
		"stack": {
			Supervision: config.SupervisionDetached,
			Lifecycle: config.LifecycleConfig{
				Start: &config.Action{
					Command: "touch start-began; sleep 30; touch start-finished",
					Dir:     directory, Shell: "/bin/sh",
				},
				Stop: &config.Action{Command: "touch cleaned", Dir: directory, Shell: "/bin/sh"},
			},
		},
	}})
	defer manager.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	startResult := make(chan error, 1)
	go func() { startResult <- manager.StartServicesContext(ctx, []string{"stack"}) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached start command did not begin")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	if err := manager.StopService("stack"); err != nil {
		t.Fatalf("cleanup after canceled start: %v", err)
	}
	if err := <-startResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled start error = %v, want context cancellation", err)
	}
	if _, err := os.Stat(cleaned); err != nil {
		t.Fatalf("stop command did not clean partial external state: %v", err)
	}
	if _, err := os.Stat(finished); !os.IsNotExist(err) {
		t.Fatalf("canceled start command reached completion: %v", err)
	}
	service, _ := manager.GetService("stack")
	if service.Status() != config.StatusStopped {
		t.Fatalf("status after cleanup = %s, want stopped", service.Status())
	}
}

func TestDetachedStartAttachesToAlreadyRunningObservedResource(t *testing.T) {
	directory := t.TempDir()
	running := filepath.Join(directory, "running")
	startInvoked := filepath.Join(directory, "start-invoked")
	if err := os.WriteFile(running, []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(&config.Config{Project: "Detached", Services: map[string]config.Service{
		"stack": {
			Supervision: config.SupervisionDetached,
			Lifecycle: config.LifecycleConfig{
				Start: &config.Action{Command: "touch " + startInvoked, Dir: directory, Shell: "/bin/sh"},
				Stop:  &config.Action{Command: "true", Dir: directory, Shell: "/bin/sh"},
				Status: &config.LifecycleStatusConfig{
					CheckConfig: config.CheckConfig{
						Type: config.CheckCommand, Command: "test -f " + running,
						Interval: time.Hour, Timeout: time.Second,
					},
					StoppedInterval: time.Hour, RunningExitCodes: []int{0}, StoppedExitCodes: []int{1},
				},
			},
		},
	}})
	defer manager.Shutdown()

	if err := manager.StartService("stack"); err != nil {
		t.Fatal(err)
	}
	service, _ := manager.GetService("stack")
	if service.Status() != config.StatusRunning {
		t.Fatalf("attached status = %s, want running", service.Status())
	}
	if !service.LifecycleStatusObserved() {
		t.Fatal("status probe was not recorded")
	}
	if _, err := os.Stat(startInvoked); !os.IsNotExist(err) {
		t.Fatalf("start command ran for an already running resource: %v", err)
	}
}

func TestLifecycleResultHidesSuccessfulProgressButKeepsFailureOutput(t *testing.T) {
	manager := NewManager(&config.Config{Project: "Lifecycle", Services: map[string]config.Service{}})
	defer manager.Shutdown()
	service := NewService("stack", config.Service{}, 100)

	manager.appendLifecycleResult(service, "start", ActionResult{
		Status: ActionSucceeded, ExitCode: 0,
		Stdout: []string{"created"}, Stderr: []string{"Container api Running"},
	})
	logs := strings.Join(service.Logs.Lines(), "\n")
	if strings.Contains(logs, "created") || strings.Contains(logs, "Container api Running") {
		t.Fatalf("successful lifecycle progress leaked into service logs:\n%s", logs)
	}
	if !strings.Contains(logs, "Lifecycle start succeeded") {
		t.Fatalf("successful lifecycle boundary missing:\n%s", logs)
	}

	manager.appendLifecycleResult(service, "stop", ActionResult{
		Status: ActionFailed, ExitCode: 1, Stderr: []string{"permission denied"},
	})
	logs = strings.Join(service.Logs.Lines(), "\n")
	if !strings.Contains(logs, "[stderr] permission denied") {
		t.Fatalf("failed lifecycle diagnostics missing:\n%s", logs)
	}
}

func TestShutdownLeavesDetachedServiceRunningByDefault(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "running")
	serviceConfig := config.Service{
		Supervision: config.SupervisionDetached,
		Lifecycle: config.LifecycleConfig{
			Start: &config.Action{Command: "touch " + marker, Dir: directory, Shell: "/bin/sh"},
			Stop:  &config.Action{Command: "rm " + marker, Dir: directory, Shell: "/bin/sh"},
		},
	}
	manager := NewManager(&config.Config{Project: "Detached", Services: map[string]config.Service{"stack": serviceConfig}})
	if err := manager.StartService("stack"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("detached resource was stopped on exit: %v", err)
	}
}

func TestShutdownDoesNotStopDetachedDependencyExcludedByStopOnExit(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "running")
	manager := NewManager(&config.Config{Project: "Detached", Services: map[string]config.Service{
		"stack": {
			Supervision: config.SupervisionDetached,
			Lifecycle: config.LifecycleConfig{
				Start: &config.Action{Command: "touch " + marker, Dir: directory, Shell: "/bin/sh"},
				Stop:  &config.Action{Command: "rm " + marker, Dir: directory, Shell: "/bin/sh"},
			},
		},
		"api": {Command: "sleep 60", DependsOn: []string{"stack"}},
	}})
	if err := manager.StartAll(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("detached dependency was stopped on exit: %v", err)
	}
}

func TestDetachedStatusReconcilesExternalState(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "running")
	serviceConfig := config.Service{
		Supervision: config.SupervisionDetached,
		Lifecycle: config.LifecycleConfig{Status: &config.LifecycleStatusConfig{
			CheckConfig:      config.CheckConfig{Type: config.CheckCommand, Command: "test -f " + marker, Interval: 10 * time.Millisecond, Timeout: time.Second, FailureThreshold: 1},
			StoppedInterval:  10 * time.Millisecond,
			RunningExitCodes: []int{0}, StoppedExitCodes: []int{1},
		}},
	}
	manager := NewManager(&config.Config{Project: "Observed", Services: map[string]config.Service{"stack": serviceConfig}})
	defer manager.Shutdown()
	service, _ := manager.GetService("stack")
	waitForServiceStatus(t, service, config.StatusStopped)
	if err := os.WriteFile(marker, []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForServiceStatus(t, service, config.StatusRunning)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	waitForServiceStatus(t, service, config.StatusStopped)
}

func TestDetachedProcessHealthyWaitsForReadinessNotStatus(t *testing.T) {
	directory := t.TempDir()
	running := filepath.Join(directory, "running")
	ready := filepath.Join(directory, "ready")
	apiStarted := filepath.Join(directory, "api-started")
	if err := os.WriteFile(running, []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(&config.Config{Project: "Dependencies", Services: map[string]config.Service{
		"stack": {
			Supervision: config.SupervisionDetached,
			Lifecycle: config.LifecycleConfig{Status: &config.LifecycleStatusConfig{
				CheckConfig:     config.CheckConfig{Type: config.CheckCommand, Command: "test -f " + running, Interval: 10 * time.Millisecond, Timeout: time.Second},
				StoppedInterval: 10 * time.Millisecond, RunningExitCodes: []int{0}, StoppedExitCodes: []int{1},
			}},
			HealthCheck: &config.HealthCheckConfig{Readiness: &config.CheckConfig{Type: config.CheckCommand, Command: "test -f " + ready, Interval: 10 * time.Millisecond, Timeout: time.Second}},
		},
		"api": {
			Command: "touch " + apiStarted + "; sleep 10", Dir: directory, Shell: "/bin/sh", DependsOn: []string{"stack"},
			DependencyConditions: map[string]config.DependencyConfig{"stack": {Condition: config.DependencyHealthy}},
		},
	}})
	checker := health.NewChecker()
	manager.SetHealthChecker(checker)
	defer manager.Shutdown()
	result := make(chan error, 1)
	go func() { result <- manager.StartServices([]string{"api"}) }()
	stack, _ := manager.GetService("stack")
	waitForServiceStatus(t, stack, config.StatusRunning)
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(apiStarted); !os.IsNotExist(err) {
		t.Fatalf("api started before readiness: %v", err)
	}
	if err := os.WriteFile(ready, []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("api did not start after detached readiness")
	}
}

func TestDetachedLogFollowerStreamsAndStopsSeparately(t *testing.T) {
	directory := t.TempDir()
	serviceConfig := config.Service{
		Supervision: config.SupervisionDetached,
		Lifecycle: config.LifecycleConfig{
			Start: &config.Action{Command: "exit 0", Dir: directory, Shell: "/bin/sh"},
			Stop:  &config.Action{Command: "exit 0", Dir: directory, Shell: "/bin/sh"},
			Logs:  &config.Action{Command: "printf 'remote log\\n'; sleep 10", Dir: directory, Shell: "/bin/sh"},
		},
	}
	manager := NewManager(&config.Config{Project: "Logs", Services: map[string]config.Service{"stack": serviceConfig}})
	defer manager.Shutdown()
	if err := manager.StartService("stack"); err != nil {
		t.Fatal(err)
	}
	service, _ := manager.GetService("stack")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(strings.Join(service.Logs.Lines(), ""), "remote log") {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(strings.Join(service.Logs.Lines(), ""), "remote log") {
		t.Fatalf("detached logs were not streamed: %v", service.Logs.Lines())
	}
	if err := manager.StopService("stack"); err != nil {
		t.Fatal(err)
	}
	if service.Status() != config.StatusStopped {
		t.Fatalf("status after stop = %s", service.Status())
	}
}

func waitForServiceStatus(t *testing.T, service *Service, expected config.ServiceStatus) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if service.Status() == expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("service status = %s, want %s", service.Status(), expected)
}

func TestForceStartServicesSkipsDependencyClosure(t *testing.T) {
	manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
		"database": {Command: "sleep 60"},
		"api":      {Command: "sleep 60", DependsOn: []string{"database"}},
	}})
	defer manager.Shutdown()

	if err := manager.ForceStartServices([]string{"api", "api"}); err != nil {
		t.Fatalf("ForceStartServices() error = %v", err)
	}
	api, _ := manager.GetService("api")
	database, _ := manager.GetService("database")
	if api.Status() != config.StatusRunning {
		t.Fatalf("api status = %s", api.Status())
	}
	if database.Status() != config.StatusStopped || database.DesiredRunning() {
		t.Fatalf("dependency was started: status=%s desired=%v", database.Status(), database.DesiredRunning())
	}
}

func TestStopServicesIncludesDependentsAndForceStopDoesNot(t *testing.T) {
	newManager := func(t *testing.T) *Manager {
		t.Helper()
		manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
			"backend":  {Command: "sleep 60"},
			"frontend": {Command: "sleep 60", DependsOn: []string{"backend"}},
			"browser":  {Command: "sleep 60", DependsOn: []string{"frontend"}},
			"worker":   {Command: "sleep 60"},
		}})
		if err := manager.ForceStartServices([]string{"backend", "frontend", "browser", "worker"}); err != nil {
			manager.Shutdown()
			t.Fatal(err)
		}
		return manager
	}

	t.Run("normal stop includes transitive dependents", func(t *testing.T) {
		manager := newManager(t)
		defer manager.Shutdown()
		if err := manager.StopServices([]string{"backend"}); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"backend", "frontend", "browser"} {
			svc, _ := manager.GetService(name)
			if svc.Status() != config.StatusStopped {
				t.Errorf("%s status = %s, want stopped", name, svc.Status())
			}
		}
		worker, _ := manager.GetService("worker")
		if worker.Status() != config.StatusRunning {
			t.Fatalf("unrelated worker status = %s, want running", worker.Status())
		}
	})

	t.Run("force stop leaves dependents running", func(t *testing.T) {
		manager := newManager(t)
		defer manager.Shutdown()
		if err := manager.ForceStopServices([]string{"backend"}); err != nil {
			t.Fatal(err)
		}
		backend, _ := manager.GetService("backend")
		if backend.Status() != config.StatusStopped {
			t.Fatalf("backend status = %s, want stopped", backend.Status())
		}
		for _, name := range []string{"frontend", "browser", "worker"} {
			svc, _ := manager.GetService(name)
			if svc.Status() != config.StatusRunning {
				t.Errorf("%s status = %s, want running", name, svc.Status())
			}
		}
	})
}

func TestExpandWithDependenciesLimitsBatchStartToRequestedClosure(t *testing.T) {
	manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
		"database": {Command: "run database"},
		"api":      {Command: "run api", DependsOn: []string{"database"}},
		"worker":   {Command: "run worker", DependsOn: []string{"api"}},
		"web":      {Command: "run web"},
	}})

	selected, err := manager.expandWithDependencies([]string{"worker"})
	if err != nil {
		t.Fatalf("expandWithDependencies failed: %v", err)
	}
	for _, expected := range []string{"database", "api", "worker"} {
		if !selected[expected] {
			t.Errorf("dependency closure does not contain %q", expected)
		}
	}
	if selected["web"] {
		t.Error("unrelated service was included in dependency closure")
	}
}

func TestCompletionAndLogReadyDependencyConditions(t *testing.T) {
	t.Run("completed successfully", func(t *testing.T) {
		manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
			"migration": {Command: "sleep 0.05; exit 7", SuccessExitCodes: []int{7}},
			"api": {
				Command: "sleep 60", DependsOn: []string{"migration"},
				DependencyConditions: map[string]config.DependencyConfig{"migration": {Condition: config.DependencyCompletedSuccessfully}},
			},
		}})
		defer manager.Shutdown()
		if err := manager.StartServices([]string{"api"}); err != nil {
			t.Fatalf("StartServices() error = %v", err)
		}
		migration, _ := manager.GetService("migration")
		api, _ := manager.GetService("api")
		if state := migration.GetState(); !state.Completed || state.ExitCode != 7 {
			t.Fatalf("migration state = %#v", state)
		}
		if migration.DesiredRunning() {
			t.Fatal("completed one-shot dependency remained queued for restart")
		}
		if api.Status() != config.StatusRunning {
			t.Fatalf("api status = %s", api.Status())
		}
	})

	t.Run("failed completion skips dependent", func(t *testing.T) {
		manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
			"migration": {Command: "exit 2"},
			"api": {
				Command: "sleep 60", DependsOn: []string{"migration"},
				DependencyConditions: map[string]config.DependencyConfig{"migration": {Condition: config.DependencyCompletedSuccessfully}},
			},
		}})
		defer manager.Shutdown()
		err := manager.StartServices([]string{"api"})
		if err == nil || !strings.Contains(err.Error(), "exit code 2") {
			t.Fatalf("completion error = %v", err)
		}
		api, _ := manager.GetService("api")
		if api.Status() != config.StatusStopped {
			t.Fatalf("skipped api status = %s", api.Status())
		}
	})

	t.Run("log ready", func(t *testing.T) {
		manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
			"server": {Command: "printf 'booting\\n'; sleep 0.05; printf 'READY on 9000\\n'; sleep 60", ReadyLogLine: `READY on \d+`},
			"api": {
				Command: "sleep 60", DependsOn: []string{"server"},
				DependencyConditions: map[string]config.DependencyConfig{"server": {Condition: config.DependencyLogReady}},
			},
		}})
		defer manager.Shutdown()
		if err := manager.StartServices([]string{"api"}); err != nil {
			t.Fatalf("StartServices() error = %v", err)
		}
		api, _ := manager.GetService("api")
		if api.Status() != config.StatusRunning {
			t.Fatalf("api status = %s", api.Status())
		}
	})
}

func TestOnFailureRecoveryHonorsRestartLimit(t *testing.T) {
	directory := t.TempDir()
	countPath := filepath.Join(directory, "count")
	manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
		"worker": {
			Command: "printf x >> \"$COUNT_FILE\"; exit 1", Env: map[string]string{"COUNT_FILE": countPath},
			Availability: config.AvailabilityConfig{Restart: "on_failure", Backoff: 10 * time.Millisecond, MaxRestarts: 2},
		},
	}})
	defer manager.Shutdown()
	if err := manager.StartService("worker"); err != nil {
		t.Fatal(err)
	}
	worker, _ := manager.GetService("worker")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state := worker.GetState()
		data, _ := os.ReadFile(countPath)
		if len(data) == 3 && state.Completed && state.RestartCount == 2 && worker.Status() == config.StatusStopped {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	state := worker.GetState()
	if !state.Completed || state.RestartCount != 2 || worker.Status() != config.StatusStopped {
		t.Fatalf("final worker state = %#v, status %s", state, worker.Status())
	}
	data, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "xxx" {
		t.Fatalf("process ran %d times, want 3", len(data))
	}
}

func TestStartAndStopAppendLifecycleBoundaries(t *testing.T) {
	manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {Command: "sleep 60"},
	}})
	defer manager.Shutdown()
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopService("api"); err != nil {
		t.Fatal(err)
	}

	api, _ := manager.GetService("api")
	logs := strings.Join(api.Logs.Lines(), "\n")
	for _, expected := range []string{
		"[Kranz] Starting",
		"[Kranz] Started · PID ",
		"[Kranz] Stopping",
		"[Kranz] Stopped",
	} {
		if !strings.Contains(logs, expected) {
			t.Errorf("lifecycle logs do not contain %q:\n%s", expected, logs)
		}
	}
}

func TestApplyConfigPreservesUnchangedProcessesAndReconcilesChanges(t *testing.T) {
	manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
		"stable":  {Command: "sleep 60"},
		"changed": {Command: "sleep 60"},
		"removed": {Command: "sleep 60"},
	}})
	defer manager.Shutdown()
	if err := manager.StartAll(); err != nil {
		t.Fatal(err)
	}
	stableBefore, _ := manager.GetService("stable")
	changedBefore, _ := manager.GetService("changed")
	stablePID, changedPID := stableBefore.PID(), changedBefore.PID()

	result, err := manager.ApplyConfig(&config.Config{Project: "Test", Services: map[string]config.Service{
		"stable":  {Command: "sleep 60"},
		"changed": {Command: "sleep 61"},
		"added":   {Command: "sleep 60"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	stableAfter, _ := manager.GetService("stable")
	changedAfter, _ := manager.GetService("changed")
	added, _ := manager.GetService("added")
	if stableAfter != stableBefore || stableAfter.PID() != stablePID {
		t.Fatalf("unchanged service restarted: before PID %d after PID %d", stablePID, stableAfter.PID())
	}
	if changedAfter == changedBefore || changedAfter.PID() == 0 || changedAfter.PID() == changedPID {
		t.Fatalf("changed service was not replaced/restarted: before PID %d after PID %d", changedPID, changedAfter.PID())
	}
	if _, exists := manager.GetService("removed"); exists || added.Status() != config.StatusStopped {
		t.Fatalf("removed/added reconciliation failed; result %#v", result)
	}
	if strings.Join(result.Restarted, ",") != "changed" {
		t.Fatalf("reload result = %#v", result)
	}
}

func TestApplyConfigDoesNotRestartServiceForActionOnlyChanges(t *testing.T) {
	manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {Command: "sleep 60", Actions: map[string]config.Action{
			"migrate": {Command: "migrate-v1"},
		}},
	}})
	defer manager.Shutdown()
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	before, _ := manager.GetService("api")
	pid := before.PID()

	result, err := manager.ApplyConfig(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {Command: "sleep 60", Actions: map[string]config.Action{
			"migrate": {Command: "migrate-v2"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := manager.GetService("api")
	if after != before || after.PID() != pid {
		t.Fatalf("action-only reload restarted service: before PID %d after PID %d", pid, after.PID())
	}
	if len(result.Updated) != 0 || len(result.Restarted) != 0 {
		t.Fatalf("action-only reload result = %#v", result)
	}
	if got := manager.cfg.Services["api"].Actions["migrate"].Command; got != "migrate-v2" {
		t.Fatalf("manager action config = %q", got)
	}
}

func TestApplyConfigUpdatesDetachedDefinitionWithoutCyclingResource(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "running")
	start := &config.Action{Command: "touch " + marker, Dir: directory, Shell: "/bin/sh"}
	manager := NewManager(&config.Config{Project: "Detached", Services: map[string]config.Service{
		"stack": {Supervision: config.SupervisionDetached, Lifecycle: config.LifecycleConfig{Start: start, Stop: &config.Action{Command: "rm " + marker}}},
	}})
	defer manager.Shutdown()
	if err := manager.StartService("stack"); err != nil {
		t.Fatal(err)
	}
	before, _ := manager.GetService("stack")
	result, err := manager.ApplyConfig(&config.Config{Project: "Detached", Services: map[string]config.Service{
		"stack": {Supervision: config.SupervisionDetached, Lifecycle: config.LifecycleConfig{Start: start, Stop: &config.Action{Command: "printf stopped"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := manager.GetService("stack")
	if after == before || after.Status() != config.StatusRunning || len(result.Restarted) != 0 {
		t.Fatalf("detached reload state = %s, result = %#v", after.Status(), result)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("detached resource was cycled during reload: %v", err)
	}
}

func TestApplyConfigCanRemoveObserveOnlyDetachedService(t *testing.T) {
	manager := NewManager(&config.Config{Project: "Observed", Services: map[string]config.Service{
		"stack": {Supervision: config.SupervisionDetached, Lifecycle: config.LifecycleConfig{Status: &config.LifecycleStatusConfig{
			CheckConfig: config.CheckConfig{Type: config.CheckCommand, Command: "exit 0", Interval: time.Hour, Timeout: time.Second},
		}}},
	}})
	defer manager.Shutdown()
	result, err := manager.ApplyConfig(&config.Config{Project: "Observed", Services: map[string]config.Service{
		"placeholder": {Command: "exit 0"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := manager.GetService("stack"); exists || len(result.Removed) != 1 || result.Removed[0] != "stack" {
		t.Fatalf("observe-only removal result = %#v", result)
	}
}

func TestExitOnEndRequestsProjectTerminationAndStopsPeers(t *testing.T) {
	manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
		"peer": {Command: "sleep 60"},
		"task": {Command: "sleep 0.05; exit 3", Availability: config.AvailabilityConfig{ExitOnEnd: true}},
	}})
	defer manager.Shutdown()
	if err := manager.StartAll(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		requested, code := manager.ProjectExitRequested()
		peer, _ := manager.GetService("peer")
		if requested && code == 3 && peer.Status() == config.StatusStopped {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	requested, code := manager.ProjectExitRequested()
	peer, _ := manager.GetService("peer")
	t.Fatalf("project exit = %v/%d, peer status = %s", requested, code, peer.Status())
}

func TestPortConflictIdentifiesExternalOwner(t *testing.T) {
	manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {Command: "exit 0", Ports: []int{8080}},
	}})
	manager.SetPortChecker(staticPortChecker{details: map[int]*config.PortInfo{
		8080: {Port: 8080, PID: 999999, Process: "outside", Command: "outside --serve"},
	}})

	err := manager.StartService("api")
	var conflict *PortConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("StartService error = %v, want PortConflictError", err)
	}
	if !conflict.External || conflict.OwnerService != "" || conflict.Service != "api" || conflict.Command != "outside --serve" {
		t.Fatalf("external conflict = %#v", conflict)
	}
}

func TestPortConflictIdentifiesKranzOwner(t *testing.T) {
	manager := NewManager(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {Command: "exit 0"},
		"web": {Command: "exit 0", Ports: []int{8080}},
	}})
	api, _ := manager.GetService("api")
	api.SetPID(os.Getpid())
	api.SetStatus(config.StatusRunning)
	manager.SetPortChecker(staticPortChecker{details: map[int]*config.PortInfo{
		8080: {Port: 8080, PID: os.Getpid(), Process: "kranz-child"},
	}})

	err := manager.StartService("web")
	var conflict *PortConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("StartService error = %v, want PortConflictError", err)
	}
	if conflict.External || conflict.OwnerService != "api" {
		t.Fatalf("managed conflict = %#v", conflict)
	}
}

type staticPortChecker struct {
	details map[int]*config.PortInfo
}

func (checker staticPortChecker) CheckPort(portNumber int) (*config.PortInfo, error) {
	return checker.details[portNumber], nil
}

func (checker staticPortChecker) CheckPorts([]int) (map[int]*config.PortInfo, error) {
	return checker.details, nil
}
