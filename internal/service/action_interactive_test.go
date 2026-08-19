package service

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func interactiveTestManager(t *testing.T, action config.Action) (*Manager, config.ActionID) {
	t.Helper()
	manager := NewManager(&config.Config{
		Project:  "Interactive",
		Services: map[string]config.Service{"app": {Command: "sleep 60", Actions: map[string]config.Action{"console": action}}},
	})
	t.Cleanup(func() { manager.Shutdown() })
	return manager, config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "app", Name: "console"}
}

func TestPrepareInteractiveTracksSuccessfulRun(t *testing.T) {
	manager, id := interactiveTestManager(t, config.Action{
		Command: "exit 0", Shell: "/bin/sh", Interactive: boolPointer(true),
	})

	command, finish, err := manager.PrepareInteractiveAction(id)
	if err != nil {
		t.Fatal(err)
	}
	// While the terminal is handed over, the action reports as running.
	if state, _ := manager.ActionState(id); state.Status != ActionRunning {
		t.Fatalf("state during handoff = %s, want running", state.Status)
	}

	result := finish(command.Run())
	if result.Status != ActionSucceeded || result.ExitCode != 0 {
		t.Fatalf("finished result = %#v", result)
	}
	if result.Duration <= 0 {
		t.Fatalf("duration was not measured: %#v", result)
	}
	// Output went to the terminal, so the retained buffer says so rather than
	// looking like a command that printed nothing.
	if len(result.Stdout) != 1 || !strings.Contains(result.Stdout[0], "handed") {
		t.Fatalf("retained output = %#v", result.Stdout)
	}
	state, _ := manager.ActionState(id)
	if state.Status != ActionSucceeded {
		t.Fatalf("state after handoff = %s, want succeeded", state.Status)
	}
}

func TestPrepareInteractiveRecordsFailureExitCode(t *testing.T) {
	manager, id := interactiveTestManager(t, config.Action{
		Command: "exit 3", Shell: "/bin/sh", Interactive: boolPointer(true),
	})
	command, finish, err := manager.PrepareInteractiveAction(id)
	if err != nil {
		t.Fatal(err)
	}
	result := finish(command.Run())
	if result.Status != ActionFailed || result.ExitCode != 3 {
		t.Fatalf("failed result = %#v", result)
	}
	if result.Error == "" {
		t.Fatal("a failed interactive action must retain a reason")
	}
}

func TestPrepareInteractiveSerializesPerOwnerAndReleases(t *testing.T) {
	manager, id := interactiveTestManager(t, config.Action{
		Command: "exit 0", Shell: "/bin/sh", Interactive: boolPointer(true),
	})
	command, finish, err := manager.PrepareInteractiveAction(id)
	if err != nil {
		t.Fatal(err)
	}
	// A second handoff cannot start while the first owns the terminal.
	var busy *ActionBusyError
	if _, _, err := manager.PrepareInteractiveAction(id); !errors.As(err, &busy) {
		t.Fatalf("second handoff error = %v, want ActionBusyError", err)
	}
	finish(command.Run())
	next, finishNext, err := manager.PrepareInteractiveAction(id)
	if err != nil {
		t.Fatalf("handoff after completion was refused: %v", err)
	}
	finishNext(next.Run())
}

func TestShutdownDoesNotWaitForAnUnfinishedHandoff(t *testing.T) {
	// Only the caller holding the terminal can observe an interactive command
	// finishing, so shutdown must cancel it rather than wait for it.
	manager, id := interactiveTestManager(t, config.Action{
		Command: "sleep 60", Shell: "/bin/sh", Interactive: boolPointer(true),
	})
	command, _, err := manager.PrepareInteractiveAction(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		manager.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown blocked on an in-flight terminal handoff")
	}
	_ = command.Wait()
}

func TestPrepareInteractiveRejectsCapturedActions(t *testing.T) {
	manager, id := interactiveTestManager(t, config.Action{Command: "true", Shell: "/bin/sh"})
	if _, _, err := manager.PrepareInteractiveAction(id); err == nil {
		t.Fatal("a captured action must not be prepared for terminal handoff")
	}
	missing := config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "app", Name: "absent"}
	if _, _, err := manager.PrepareInteractiveAction(missing); !errors.Is(err, ErrActionNotFound) {
		t.Fatalf("missing action error = %v, want ErrActionNotFound", err)
	}
}

func TestAcquireInteractiveTracksSuccessfulRunWithoutOwningTheCommand(t *testing.T) {
	manager, id := interactiveTestManager(t, config.Action{
		Command: "exit 0", Shell: "/bin/sh", Interactive: boolPointer(true),
	})

	action, lease, err := manager.AcquireInteractiveAction(id)
	if err != nil {
		t.Fatal(err)
	}
	if action.Command != "exit 0" {
		t.Fatalf("resolved action = %#v", action)
	}
	if state, _ := manager.ActionState(id); state.Status != ActionRunning {
		t.Fatalf("state during handoff = %s, want running", state.Status)
	}

	// The caller, not the runner, runs the command: it observes exit code and
	// PID itself and reports them back.
	command := interactiveCommand(action)
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}

	result, err := manager.CompleteInteractiveAction(id, lease, command.ProcessState.ExitCode(), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ActionSucceeded || result.ExitCode != 0 {
		t.Fatalf("finished result = %#v", result)
	}
	if result.Duration <= 0 {
		t.Fatalf("duration was not measured: %#v", result)
	}
	state, _ := manager.ActionState(id)
	if state.Status != ActionSucceeded {
		t.Fatalf("state after handoff = %s, want succeeded", state.Status)
	}
}

func TestCompleteInteractiveRecordsFailureExitCode(t *testing.T) {
	manager, id := interactiveTestManager(t, config.Action{
		Command: "exit 3", Shell: "/bin/sh", Interactive: boolPointer(true),
	})
	_, lease, err := manager.AcquireInteractiveAction(id)
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.CompleteInteractiveAction(id, lease, 3, 4242, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ActionFailed || result.ExitCode != 3 || result.PID != 4242 {
		t.Fatalf("failed result = %#v", result)
	}
	if result.Error == "" {
		t.Fatal("a failed interactive action must retain a reason")
	}
}

func TestCompleteInteractiveRejectsAMismatchedOrUnknownLease(t *testing.T) {
	manager, id := interactiveTestManager(t, config.Action{
		Command: "exit 0", Shell: "/bin/sh", Interactive: boolPointer(true),
	})
	if _, err := manager.CompleteInteractiveAction(id, "not-a-real-lease", 0, 0, nil); err == nil {
		t.Fatal("completing an unknown lease must fail")
	}
	_, lease, err := manager.AcquireInteractiveAction(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CompleteInteractiveAction(id, "wrong-lease", 0, 0, nil); err == nil {
		t.Fatal("completing with the wrong lease token must fail")
	}
	// The genuine lease must still be completable after a mismatched attempt.
	if _, err := manager.CompleteInteractiveAction(id, lease, 0, 0, nil); err != nil {
		t.Fatalf("completing the genuine lease failed: %v", err)
	}
}

func TestAcquireInteractiveSerializesPerOwner(t *testing.T) {
	manager, id := interactiveTestManager(t, config.Action{
		Command: "exit 0", Shell: "/bin/sh", Interactive: boolPointer(true),
	})
	_, lease, err := manager.AcquireInteractiveAction(id)
	if err != nil {
		t.Fatal(err)
	}
	var busy *ActionBusyError
	if _, _, err := manager.AcquireInteractiveAction(id); !errors.As(err, &busy) {
		t.Fatalf("second handoff error = %v, want ActionBusyError", err)
	}
	if _, err := manager.CompleteInteractiveAction(id, lease, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AcquireInteractiveAction(id); err != nil {
		t.Fatalf("handoff after completion was refused: %v", err)
	}
}

func TestInteractiveCommandCarriesExecutionContext(t *testing.T) {
	directory := t.TempDir()
	command := interactiveCommand(config.Action{
		Command: "true", Shell: "/bin/sh", Dir: directory, Env: map[string]string{"KRANZ_EXAMPLE": "yes"},
	})
	if command.Dir != directory {
		t.Fatalf("command dir = %q, want %q", command.Dir, directory)
	}
	if !containsEnv(command, "KRANZ_EXAMPLE=yes") {
		t.Fatalf("command env did not carry the action variables: %v", command.Env)
	}
	// The handoff keeps the terminal's own process group so Ctrl+C reaches the
	// command the user is looking at.
	if command.SysProcAttr != nil {
		t.Fatalf("interactive command must not create its own process group: %#v", command.SysProcAttr)
	}
}

func containsEnv(command *exec.Cmd, entry string) bool {
	for _, value := range command.Env {
		if value == entry {
			return true
		}
	}
	return false
}

func boolPointer(value bool) *bool { return &value }
