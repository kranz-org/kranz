package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func operationTestLocal(t *testing.T) *Local {
	t.Helper()
	confirm := true
	cfg := &config.Config{Project: "Operations", ServiceOrder: []string{"db", "api"}, Services: map[string]config.Service{
		"db":  {Command: "sleep 30", Shell: "/bin/sh", Tags: []string{"backend"}},
		"api": {Command: "sleep 30", Shell: "/bin/sh", DependsOn: []string{"db"}, Actions: map[string]config.Action{"deploy": {Command: "true", Shell: "/bin/sh", Confirm: &confirm}}, ActionOrder: []string{"deploy"}},
	}}
	local := NewLocal(cfg, nil, Options{SessionID: "session-test"})
	t.Cleanup(func() { _ = local.Shutdown() })
	return local
}

func reloadOperationTestLocal(t *testing.T) (*Local, PlanRequest, string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "kranz.yaml")
	data := "project: Operations\nservices:\n  api:\n    command: \"true\"\n    actions:\n      deploy:\n        command: \"true\"\n        confirm: true\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	local := NewLocal(cfg, []string{path}, Options{SessionID: "reload-test"})
	t.Cleanup(func() { _ = local.Shutdown() })
	id := config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "api", Name: "deploy"}
	return local, PlanRequest{Operation: "action", Action: id}, path
}

func TestPlanUsesSharedSelectorsAndDependencyWaves(t *testing.T) {
	local := operationTestLocal(t)
	plan, err := local.Plan(PlanRequest{Operation: "start", Selectors: []string{"api"}, IncludeDependencies: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != 1 || plan.SessionID != "session-test" || plan.Generation != 1 {
		t.Fatalf("identity = %#v", plan)
	}
	if len(plan.Targets) != 2 || plan.Targets[0] != "db" || plan.Targets[1] != "api" || len(plan.Waves) != 2 {
		t.Fatalf("plan = %#v", plan)
	}
	selected, err := ResolveServiceSelectors(local.Config(), []string{"backend"})
	if err != nil || len(selected) != 1 || selected[0] != "db" {
		t.Fatalf("selector = %v, %v", selected, err)
	}
}

func TestConfirmationTokenIsOneShotAndPlanBound(t *testing.T) {
	local := operationTestLocal(t)
	id := config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "api", Name: "deploy"}
	request := PlanRequest{Operation: "action", Action: id}
	plan, err := local.Plan(request)
	if err != nil || plan.ConfirmationToken == "" {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
	result, err := local.ExecutePlan(context.Background(), request, plan.ConfirmationToken)
	if err != nil || result.ActionResult == nil || result.ActionResult.Run != 1 {
		t.Fatalf("execute = %#v, %v", result, err)
	}
	if local.nextConfirmationSequence != 1 {
		t.Fatalf("confirmation sequence = %d, want 1 without a throwaway execution token", local.nextConfirmationSequence)
	}
	_, err = local.ExecutePlan(context.Background(), request, plan.ConfirmationToken)
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) || confirmation.Code != "confirmation_expired" {
		t.Fatalf("reuse err = %#v", err)
	}

	changed, _ := local.Plan(request)
	action := local.cfg.Services["api"].Actions["deploy"]
	confirm := false
	action.Confirm = &confirm
	service := local.cfg.Services["api"]
	service.Actions["deploy"] = action
	local.cfg.Services["api"] = service
	_, err = local.ExecutePlan(context.Background(), request, changed.ConfirmationToken)
	if !errors.As(err, &confirmation) || confirmation.Code != "confirmation_plan_changed" {
		t.Fatalf("changed err = %#v", err)
	}
}

func TestReloadInvalidatesConfirmationToken(t *testing.T) {
	local, request, path := reloadOperationTestLocal(t)
	plan, _ := local.Plan(request)
	updated := "project: Operations\nservices:\n  api:\n    command: \"true\"\n    actions:\n      deploy:\n        command: \"true\"\n        description: Updated\n        confirm: true\n"
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Reload(true); err != nil {
		t.Fatal(err)
	}
	if len(local.confirmations) != 0 {
		t.Fatalf("confirmations after accepted reload = %d, want 0", len(local.confirmations))
	}
	_, err := local.ExecutePlan(context.Background(), request, plan.ConfirmationToken)
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) || confirmation.Code != "confirmation_expired" {
		t.Fatalf("reload err = %#v", err)
	}
}

func TestNoOpDebouncedAndFailedReloadsPreserveConfirmationTokens(t *testing.T) {
	t.Run("no-op", func(t *testing.T) {
		local, request, _ := reloadOperationTestLocal(t)
		plan, err := local.Plan(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := local.Reload(false); err != nil {
			t.Fatal(err)
		}
		if _, err := local.ExecutePlan(context.Background(), request, plan.ConfirmationToken); err != nil {
			t.Fatalf("execute after no-op reload: %v", err)
		}
	})

	t.Run("debounced", func(t *testing.T) {
		local, request, _ := reloadOperationTestLocal(t)
		if _, err := local.Reload(false); err != nil {
			t.Fatal(err)
		}
		plan, err := local.Plan(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := local.Reload(false); err != nil {
			t.Fatal(err)
		}
		if _, err := local.ExecutePlan(context.Background(), request, plan.ConfirmationToken); err != nil {
			t.Fatalf("execute after debounced reload: %v", err)
		}
	})

	t.Run("failed", func(t *testing.T) {
		local, request, path := reloadOperationTestLocal(t)
		plan, err := local.Plan(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("project: ["), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := local.Reload(true); err == nil {
			t.Fatal("invalid configuration reload succeeded")
		}
		if _, err := local.ExecutePlan(context.Background(), request, plan.ConfirmationToken); err != nil {
			t.Fatalf("execute after failed reload: %v", err)
		}
	})
}

func TestPreviewConfirmationPressurePreservesExecutionTokenAndEvictsFIFO(t *testing.T) {
	local := operationTestLocal(t)
	id := config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "api", Name: "deploy"}
	request := PlanRequest{Operation: "action", Action: id}

	result, err := local.ExecutePlan(context.Background(), request, "")
	var required *ConfirmationRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("initial execution = %#v, %v", result, err)
	}
	executionToken := required.Plan.ConfirmationToken

	previews := make([]string, 0, maxConfirmationTokensPerPurpose+1)
	for range maxConfirmationTokensPerPurpose + 1 {
		plan, err := local.Plan(request)
		if err != nil {
			t.Fatal(err)
		}
		previews = append(previews, plan.ConfirmationToken)
	}
	if got, want := len(local.confirmations), maxConfirmationTokensPerPurpose+1; got != want {
		t.Fatalf("pending confirmations = %d, want bounded execution plus preview pools = %d", got, want)
	}

	_, err = local.ExecutePlan(context.Background(), request, previews[0])
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) || confirmation.Code != "confirmation_expired" {
		t.Fatalf("oldest preview err = %#v, want deterministic FIFO eviction", err)
	}
	if _, err := local.ExecutePlan(context.Background(), request, executionToken); err != nil {
		t.Fatalf("execution token was evicted by previews: %v", err)
	}
	if _, err := local.ExecutePlan(context.Background(), request, previews[len(previews)-1]); err != nil {
		t.Fatalf("newest preview token was evicted before the oldest: %v", err)
	}
}

func TestWaitConditionsAndCancellation(t *testing.T) {
	local := operationTestLocal(t)
	local.SetServiceStateForTest("api", config.ServiceState{Status: config.StatusRunning})
	result, err := local.Wait(context.Background(), WaitRequest{Selectors: []string{"api"}, Condition: "ready"})
	if err != nil || len(result.Services) != 1 {
		t.Fatalf("ready = %#v, %v", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = local.Wait(ctx, WaitRequest{Selectors: []string{"api"}, Condition: "stopped"})
	var waitErr *WaitError
	if !errors.As(err, &waitErr) || waitErr.Code != "wait_timeout" {
		t.Fatalf("wait err = %#v", err)
	}
}

func TestWaitDistinguishesBlockedDependency(t *testing.T) {
	local := operationTestLocal(t)
	local.SetServiceStateForTest("db", config.ServiceState{Status: config.StatusStopped, Completed: true, ExitCode: 1})
	local.SetServiceDesiredRunningForTest("api", true)
	_, err := local.Wait(context.Background(), WaitRequest{Selectors: []string{"api"}, Condition: "ready"})
	var waitErr *WaitError
	if !errors.As(err, &waitErr) || waitErr.Code != "dependency_blocked" {
		t.Fatalf("wait err = %#v", err)
	}
}

func TestPrimaryServiceAction(t *testing.T) {
	if got := PrimaryServiceAction(&ServiceSnapshot{CanStart: true, State: config.ServiceState{Status: config.StatusStopped}}); got != "start" {
		t.Fatalf("stopped = %q", got)
	}
	if got := PrimaryServiceAction(&ServiceSnapshot{CanStop: true, DesiredRunning: true, State: config.ServiceState{Status: config.StatusStarting}}); got != "stop" {
		t.Fatalf("starting = %q", got)
	}
	if got := PrimaryServiceAction(&ServiceSnapshot{State: config.ServiceState{Status: config.StatusStopped}}); got != "" {
		t.Fatalf("disabled = %q", got)
	}
}

func TestEmptyPlanHasNoWavesAndWaitReturnsACursor(t *testing.T) {
	local := operationTestLocal(t)
	// A configuration with no services resolves to no targets, and a reader
	// counting waves must not see work that does not exist.
	bare := NewLocal(&config.Config{Project: "Empty"}, nil, Options{SessionID: "session-empty"})
	t.Cleanup(func() { _ = bare.Shutdown() })
	plan, err := bare.Plan(PlanRequest{Operation: "start", IncludeDependencies: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 0 || len(plan.Waves) != 0 {
		t.Fatalf("empty plan = %#v", plan)
	}

	local.SetServiceStatusForTest("db", config.StatusRunning)
	local.SetServiceStatusForTest("api", config.StatusRunning)
	result, err := local.Wait(context.Background(), WaitRequest{Selectors: []string{"api"}, Condition: "running"})
	if err != nil {
		t.Fatal(err)
	}
	// The cursor is what makes "what happened while I waited" answerable.
	if result.Cursor == 0 {
		t.Fatalf("wait cursor = %#v", result)
	}
	changes, err := local.Changes(ChangeQuery{Since: result.Cursor})
	if err != nil || len(changes.Changes) != 0 {
		t.Fatalf("changes after wait cursor = %#v, %v", changes.Changes, err)
	}
}
