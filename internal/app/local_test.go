package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func TestServicesSnapshotReflectsConfigAndRuntimeState(t *testing.T) {
	confirm := true
	cfg := &config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {Command: "exit 0", Dir: ".", Shell: "sh", Ports: []int{8080}},
		"migration": {Lifecycle: config.LifecycleConfig{Start: &config.Action{
			Command: "true", Confirm: &confirm,
		}}, DependsOn: []string{"api"}},
	}}
	local := NewLocal(cfg, nil, Options{})
	defer func() { _ = local.Shutdown() }()

	snapshots := local.Services()
	if len(snapshots) != 2 {
		t.Fatalf("Services() returned %d snapshots, want 2", len(snapshots))
	}
	api, ok := local.Service("api")
	if !ok {
		t.Fatal("Service(api) not found")
	}
	if api.State.Status != config.StatusStopped {
		t.Fatalf("api initial status = %v, want stopped", api.State.Status)
	}
	if !api.CanStart || api.CanStop {
		t.Fatalf("api capability = start:%v stop:%v, want startable and not stoppable", api.CanStart, api.CanStop)
	}
}

func TestStartConfirmationNamesIncludesConfirmedDependencies(t *testing.T) {
	confirm := true
	cfg := &config.Config{Project: "Test", Services: map[string]config.Service{
		"migration": {Lifecycle: config.LifecycleConfig{Start: &config.Action{
			Command: "true", Confirm: &confirm,
		}}},
		"api": {Command: "true", DependsOn: []string{"migration"}},
	}}
	local := NewLocal(cfg, nil, Options{})
	defer func() { _ = local.Shutdown() }()

	names := local.StartConfirmationNames([]string{"api"}, true)
	if len(names) != 1 || names[0] != "migration" {
		t.Fatalf("StartConfirmationNames(with deps) = %v, want [migration]", names)
	}

	names = local.StartConfirmationNames([]string{"api"}, false)
	if len(names) != 0 {
		t.Fatalf("StartConfirmationNames(without deps) = %v, want none", names)
	}
}

func TestRequiresStopConfirmationOnlyForStoppableServices(t *testing.T) {
	cfg := &config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {Command: "sleep 60"},
	}}
	local := NewLocal(cfg, nil, Options{})
	defer func() { _ = local.Shutdown() }()

	if local.RequiresStopConfirmation([]string{"api"}) {
		t.Fatal("a stopped service should not require stop confirmation")
	}
	if err := local.StartServicesContext(context.Background(), []string{"api"}); err != nil {
		t.Fatalf("start api: %v", err)
	}
	if !local.RequiresStopConfirmation([]string{"api"}) {
		t.Fatal("a running service should require stop confirmation")
	}
}

func TestShutdownPlanClassifiesManagedAndDetachedServices(t *testing.T) {
	stopOnExit := true
	cfg := &config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {Command: "sleep 60"},
		// StopOnExit defaults to false for a detached resource (Kranz does not
		// own it), so this one must opt in explicitly to land in DetachedStop.
		"external": {
			Supervision: config.SupervisionDetached,
			StopOnExit:  &stopOnExit,
			Lifecycle: config.LifecycleConfig{
				Start: &config.Action{Command: "true"},
				Stop:  &config.Action{Command: "true"},
			},
		},
		"unmanaged": {
			Supervision: config.SupervisionDetached,
			Lifecycle: config.LifecycleConfig{
				Start: &config.Action{Command: "true"},
				Stop:  &config.Action{Command: "true"},
			},
		},
	}}
	local := NewLocal(cfg, nil, Options{})
	defer func() { _ = local.Shutdown() }()

	if err := local.StartServicesContext(context.Background(), []string{"api"}); err != nil {
		t.Fatalf("start api: %v", err)
	}
	if err := local.ForceStartServices([]string{"external", "unmanaged"}); err != nil {
		t.Fatalf("start detached services: %v", err)
	}

	plan := local.ShutdownPlan()
	if len(plan.Managed) != 1 || plan.Managed[0] != "api" {
		t.Fatalf("plan.Managed = %v, want [api]", plan.Managed)
	}
	if len(plan.DetachedStop) != 1 || plan.DetachedStop[0] != "external" {
		t.Fatalf("plan.DetachedStop = %v, want [external]", plan.DetachedStop)
	}
	if len(plan.DetachedKeep) != 1 || plan.DetachedKeep[0] != "unmanaged" {
		t.Fatalf("plan.DetachedKeep = %v, want [unmanaged]", plan.DetachedKeep)
	}
}

func TestReloadRaisesGenerationAndKeepsLastKnownGoodOnInvalidFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "kranz.yaml")
	writeConfig(t, path, "project: Test\nservices:\n  api:\n    command: \"true\"\n")
	cfg, err := config.LoadFiles([]string{path})
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	local := NewLocal(cfg, []string{path}, Options{})
	defer func() { _ = local.Shutdown() }()

	if got := local.Project().Generation; got != 1 {
		t.Fatalf("initial generation = %d, want 1", got)
	}

	writeConfig(t, path, "project: Test\nservices:\n  api:\n    command: \"true\"\n  worker:\n    command: \"true\"\n")
	touchLater(t, path)
	if _, err := local.Reload(true); err != nil {
		t.Fatalf("reload valid change: %v", err)
	}
	if got := local.Project().Generation; got != 2 {
		t.Fatalf("generation after reload = %d, want 2", got)
	}
	if len(local.Config().Services) != 2 {
		t.Fatalf("services after reload = %d, want 2", len(local.Config().Services))
	}

	writeConfig(t, path, "project: Test\nservices:\n")
	touchLater(t, path)
	if _, err := local.Reload(true); err == nil {
		t.Fatal("reload with no services should fail validation")
	}
	if got := local.Project().Generation; got != 2 {
		t.Fatalf("generation after failed reload = %d, want unchanged 2", got)
	}
	if len(local.Config().Services) != 2 {
		t.Fatalf("services after failed reload = %d, want last known good 2", len(local.Config().Services))
	}
	if local.Project().LastReloadError == "" {
		t.Fatal("failed reload did not record an error")
	}
}

func TestReloadDebouncesWithinOneSecondUnlessForced(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "kranz.yaml")
	writeConfig(t, path, "project: Test\nservices:\n  api:\n    command: \"true\"\n")
	cfg, err := config.LoadFiles([]string{path})
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	local := NewLocal(cfg, []string{path}, Options{})
	defer func() { _ = local.Shutdown() }()

	writeConfig(t, path, "project: Test\nservices:\n  api:\n    command: \"true\"\n  worker:\n    command: \"true\"\n")
	touchLater(t, path)
	if _, err := local.Reload(false); err != nil {
		t.Fatalf("first reload: %v", err)
	}
	if got := local.Project().Generation; got != 2 {
		t.Fatalf("generation after first reload = %d, want 2", got)
	}

	writeConfig(t, path, "project: Test\nservices:\n  api:\n    command: \"true\"\n")
	touchLater(t, path)
	if _, err := local.Reload(false); err != nil {
		t.Fatalf("debounced reload: %v", err)
	}
	if got := local.Project().Generation; got != 2 {
		t.Fatalf("generation after debounced reload = %d, want still 2", got)
	}
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// touchLater backdates the config's mtime stamp so a fast test run still sees
// a change: the reload pipeline detects edits by mtime and size, and two
// writes within the same nanosecond-granularity tick would otherwise look
// identical to it.
func touchLater(t *testing.T, path string) {
	t.Helper()
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}
