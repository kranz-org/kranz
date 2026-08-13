package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func TestActionRunnerCapturesSuccessfulResultAndEnvironment(t *testing.T) {
	directory := t.TempDir()
	id := serviceActionID("app", "inspect")
	runner := newTestActionRunner(directory, map[string]config.Action{
		"inspect": {
			Command: `printf 'value=%s\n' "$ACTION_VALUE"; printf 'problem\n' >&2`,
			Dir:     directory,
			Shell:   "/bin/sh",
			Env:     map[string]string{"ACTION_VALUE": "inherited"},
		},
	})

	ready, exists := runner.State(id)
	if !exists || ready.Status != ActionReady || ready.ExitCode != -1 {
		t.Fatalf("initial action state = %#v, %v", ready, exists)
	}
	result, err := runner.Run(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ActionSucceeded || result.ExitCode != 0 || result.PID <= 0 || result.Duration <= 0 {
		t.Fatalf("successful result = %#v", result)
	}
	if strings.TrimSpace(strings.Join(result.Stdout, "\n")) != "value=inherited" || strings.TrimSpace(strings.Join(result.Stderr, "\n")) != "problem" {
		t.Fatalf("captured output = stdout %q stderr %q", result.Stdout, result.Stderr)
	}
	state, exists := runner.State(id)
	if !exists || state.Status != ActionSucceeded || state.FinishedAt.IsZero() {
		t.Fatalf("retained action state = %#v, %v", state, exists)
	}
	state.Stdout[0] = "mutated"
	again, _ := runner.State(id)
	if strings.TrimSpace(again.Stdout[0]) != "value=inherited" {
		t.Fatalf("state returned aliased output: %v", again.Stdout)
	}
}

func TestActionRunnerExposesOutputWhileRunning(t *testing.T) {
	id := serviceActionID("app", "stream")
	runner := newTestActionRunner(t.TempDir(), map[string]config.Action{
		"stream": {Command: "printf 'first\\n'; sleep 10", Shell: "/bin/sh"},
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = runner.Run(context.Background(), id)
	}()
	defer func() {
		_ = runner.Cancel(id)
		<-done
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := runner.State(id)
		if state.Status == ActionRunning && strings.Contains(strings.Join(state.Stdout, ""), "first") {
			if state.Duration <= 0 || !state.FinishedAt.IsZero() {
				t.Fatalf("running state timing = %#v", state)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	state, _ := runner.State(id)
	t.Fatalf("running output was not exposed: %#v", state)
}

func TestActionRunnerReportsFailureAndExitCode(t *testing.T) {
	id := serviceActionID("app", "fail")
	runner := newTestActionRunner(t.TempDir(), map[string]config.Action{
		"fail": {Command: "printf 'broken\\n' >&2; exit 7", Shell: "/bin/sh"},
	})
	result, err := runner.Run(context.Background(), id)
	var exitErr *ActionExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode != 7 {
		t.Fatalf("run error = %v", err)
	}
	if result.Status != ActionFailed || result.ExitCode != 7 || strings.TrimSpace(strings.Join(result.Stderr, "\n")) != "broken" || result.Error == "" {
		t.Fatalf("failed result = %#v", result)
	}
}

func TestActionRunnerTimesOutAndCancelsProcessGroup(t *testing.T) {
	id := serviceActionID("app", "slow")
	runner := newTestActionRunner(t.TempDir(), map[string]config.Action{
		"slow": {Command: "sleep 10", Shell: "/bin/sh", Timeout: 50 * time.Millisecond},
	})
	started := time.Now()
	result, err := runner.Run(context.Background(), id)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	if result.Status != ActionTimedOut || result.Duration < 40*time.Millisecond || time.Since(started) > 2*time.Second {
		t.Fatalf("timed-out result = %#v after %s", result, time.Since(started))
	}
}

func TestActionRunnerSerializesOwnerAndAllowsIndependentOwners(t *testing.T) {
	directory := t.TempDir()
	serviceID := serviceActionID("app", "slow")
	secondID := serviceActionID("app", "second")
	groupID := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "tools", Name: "quick"}
	cfg := &config.Config{
		Project: "Actions",
		Services: map[string]config.Service{"app": {Command: "run", Actions: map[string]config.Action{
			"slow":   {Command: "sleep 10", Dir: directory, Shell: "/bin/sh"},
			"second": {Command: "exit 0", Dir: directory, Shell: "/bin/sh"},
		}}},
		ActionGroups: map[string]config.ActionGroup{"tools": {Actions: map[string]config.Action{
			"quick": {Command: "printf 'quick\\n'", Dir: directory, Shell: "/bin/sh"},
		}}},
	}
	runner := NewActionRunner(cfg, 20)
	type runOutcome struct {
		result ActionResult
		err    error
	}
	slowDone := make(chan runOutcome, 1)
	go func() {
		result, err := runner.Run(context.Background(), serviceID)
		slowDone <- runOutcome{result: result, err: err}
	}()
	waitForActionStatus(t, runner, serviceID, ActionRunning)

	_, err := runner.Run(context.Background(), secondID)
	var busyErr *ActionBusyError
	if !errors.As(err, &busyErr) || busyErr.Running != serviceID || busyErr.Requested != secondID {
		t.Fatalf("same-owner run error = %#v", err)
	}
	groupResult, err := runner.Run(context.Background(), groupID)
	if err != nil || groupResult.Status != ActionSucceeded {
		t.Fatalf("independent group result = %#v, %v", groupResult, err)
	}
	if !runner.Cancel(serviceID) {
		t.Fatal("running action was not cancelled")
	}
	slow := <-slowDone
	if !errors.Is(slow.err, context.Canceled) || slow.result.Status != ActionCancelled {
		t.Fatalf("cancelled action = %#v, %v", slow.result, slow.err)
	}
}

func TestActionRunnerRejectsInteractiveAndUnknownActions(t *testing.T) {
	interactive := true
	runner := newTestActionRunner(t.TempDir(), map[string]config.Action{
		"console": {Command: "sh", Shell: "/bin/sh", Interactive: &interactive},
	})
	if _, err := runner.Run(context.Background(), serviceActionID("app", "console")); !errors.Is(err, ErrInteractiveAction) {
		t.Fatalf("interactive action error = %v", err)
	}
	if _, err := runner.Run(context.Background(), serviceActionID("app", "missing")); !errors.Is(err, ErrActionNotFound) {
		t.Fatalf("missing action error = %v", err)
	}
}

func TestActionRunnerApplyConfigPreservesOrResetsResults(t *testing.T) {
	directory := t.TempDir()
	id := serviceActionID("app", "version")
	action := config.Action{Command: "printf 'v1\\n'", Dir: directory, Shell: "/bin/sh"}
	runner := newTestActionRunner(directory, map[string]config.Action{"version": action})
	if _, err := runner.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	same := newActionConfig(map[string]config.Action{"version": action})
	runner.ApplyConfig(same)
	state, _ := runner.State(id)
	if state.Status != ActionSucceeded {
		t.Fatalf("unchanged action state = %#v", state)
	}
	changed := action
	changed.Command = "printf 'v2\\n'"
	runner.ApplyConfig(newActionConfig(map[string]config.Action{"version": changed}))
	state, _ = runner.State(id)
	if state.Status != ActionReady {
		t.Fatalf("changed action state = %#v", state)
	}
}

func TestActionRunnerShutdownCancelsRunsAndRejectsNewOnes(t *testing.T) {
	id := serviceActionID("app", "slow")
	runner := newTestActionRunner(t.TempDir(), map[string]config.Action{
		"slow": {Command: "sleep 10", Shell: "/bin/sh"},
	})
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), id)
		done <- err
	}()
	waitForActionStatus(t, runner, id, ActionRunning)
	runner.Shutdown()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown run error = %v", err)
	}
	if _, err := runner.Run(context.Background(), id); !errors.Is(err, ErrActionRunnerStopping) {
		t.Fatalf("post-shutdown run error = %v", err)
	}
}

func TestManagerReloadUpdatesActionRunnerWithoutRestartingService(t *testing.T) {
	directory := t.TempDir()
	id := serviceActionID("app", "version")
	manager := NewManager(&config.Config{Project: "Actions", Services: map[string]config.Service{
		"app": {Command: "sleep 10", Actions: map[string]config.Action{
			"version": {Command: "printf 'v1\\n'", Dir: directory, Shell: "/bin/sh"},
		}},
	}})
	defer manager.Shutdown()
	if err := manager.StartService("app"); err != nil {
		t.Fatal(err)
	}
	serviceBefore, _ := manager.GetService("app")
	pidBefore := serviceBefore.PID()
	if pidBefore <= 0 {
		t.Fatalf("started service PID = %d", pidBefore)
	}
	result, err := manager.RunAction(context.Background(), id)
	if err != nil || strings.TrimSpace(strings.Join(result.Stdout, "\n")) != "v1" {
		t.Fatalf("first action = %#v, %v", result, err)
	}
	if _, err := manager.ApplyConfig(&config.Config{Project: "Actions", Services: map[string]config.Service{
		"app": {Command: "sleep 10", Actions: map[string]config.Action{
			"version": {Command: "printf 'v2\\n'", Dir: directory, Shell: "/bin/sh"},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	state, _ := manager.ActionState(id)
	if state.Status != ActionReady {
		t.Fatalf("reloaded action state = %#v", state)
	}
	serviceAfter, _ := manager.GetService("app")
	if serviceAfter != serviceBefore || serviceAfter.PID() != pidBefore {
		t.Fatalf("action-only reload replaced service: before PID %d, after PID %d", pidBefore, serviceAfter.PID())
	}
	result, err = manager.RunAction(context.Background(), id)
	if err != nil || strings.TrimSpace(strings.Join(result.Stdout, "\n")) != "v2" {
		t.Fatalf("reloaded action = %#v, %v", result, err)
	}
}

func newTestActionRunner(directory string, actions map[string]config.Action) *ActionRunner {
	for name, action := range actions {
		if action.Dir == "" {
			action.Dir = directory
		}
		if action.Shell == "" {
			action.Shell = "/bin/sh"
		}
		actions[name] = action
	}
	return NewActionRunner(newActionConfig(actions), 20)
}

func newActionConfig(actions map[string]config.Action) *config.Config {
	return &config.Config{Project: "Actions", Services: map[string]config.Service{
		"app": {Command: "run", Actions: actions},
	}}
}

func serviceActionID(owner, name string) config.ActionID {
	return config.ActionID{OwnerKind: config.ActionOwnerService, Owner: owner, Name: name}
}

func waitForActionStatus(t *testing.T, runner *ActionRunner, id config.ActionID, status ActionStatus) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, exists := runner.State(id)
		if exists && state.Status == status && (status != ActionRunning || state.PID > 0) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	state, exists := runner.State(id)
	t.Fatalf("action state = %#v, %v; want %s", state, exists, status)
}
