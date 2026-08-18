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
)

// countingPrerequisite builds a project whose prerequisite appends one line to
// a counter file, so a test can prove how often it actually ran.
func countingPrerequisite(t *testing.T, run config.PrerequisiteRun) (*Manager, string) {
	t.Helper()
	directory := t.TempDir()
	counter := filepath.Join(directory, "runs")
	manager := NewManager(&config.Config{
		Project: "Prerequisites",
		Services: map[string]config.Service{
			"app": {
				Command: "sleep 60",
				Actions: map[string]config.Action{
					"prepare": {Command: "echo run >> " + counter, Dir: directory, Shell: "/bin/sh"},
				},
				BeforeStart: []config.Prerequisite{{Action: "prepare", Run: run}},
			},
		},
	})
	t.Cleanup(func() { manager.Shutdown() })
	return manager, counter
}

func prerequisiteRunCount(t *testing.T, counter string) int {
	t.Helper()
	content, err := os.ReadFile(counter)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(content)))
}

func TestPrerequisiteRunsBeforeServiceStarts(t *testing.T) {
	manager, counter := countingPrerequisite(t, config.PrerequisiteOnce)
	if err := manager.StartService("app"); err != nil {
		t.Fatal(err)
	}
	service, _ := manager.GetService("app")
	if service.Status() != config.StatusRunning {
		t.Fatalf("service status = %s, want running", service.Status())
	}
	if got := prerequisiteRunCount(t, counter); got != 1 {
		t.Fatalf("prerequisite ran %d times, want 1", got)
	}
}

func TestPrerequisiteOnceIsNotRepeatedOnRestart(t *testing.T) {
	manager, counter := countingPrerequisite(t, config.PrerequisiteOnce)
	if err := manager.StartService("app"); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopService("app"); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartService("app"); err != nil {
		t.Fatal(err)
	}
	if got := prerequisiteRunCount(t, counter); got != 1 {
		t.Fatalf("prerequisite ran %d times across two starts, want 1", got)
	}
}

func TestPrerequisiteAlwaysRunsBeforeEveryStart(t *testing.T) {
	manager, counter := countingPrerequisite(t, config.PrerequisiteAlways)
	for range 2 {
		if err := manager.StartService("app"); err != nil {
			t.Fatal(err)
		}
		if err := manager.StopService("app"); err != nil {
			t.Fatal(err)
		}
	}
	if got := prerequisiteRunCount(t, counter); got != 2 {
		t.Fatalf("prerequisite ran %d times across two starts, want 2", got)
	}
}

func TestFailedPrerequisiteLeavesServiceStopped(t *testing.T) {
	manager := NewManager(&config.Config{
		Project: "Prerequisites",
		Services: map[string]config.Service{
			"app": {
				Command: "sleep 60",
				Actions: map[string]config.Action{
					"prepare": {Command: "exit 7", Shell: "/bin/sh"},
				},
				BeforeStart: []config.Prerequisite{{Action: "prepare"}},
			},
		},
	})
	defer manager.Shutdown()

	err := manager.StartService("app")
	if !errors.Is(err, ErrPrerequisiteFailed) {
		t.Fatalf("StartService() error = %v, want ErrPrerequisiteFailed", err)
	}
	service, _ := manager.GetService("app")
	if service.Status() != config.StatusStopped {
		t.Fatalf("service status = %s, want stopped", service.Status())
	}
	if service.PID() != 0 {
		t.Fatalf("failed prerequisite still started a process (pid %d)", service.PID())
	}
	// A failed prerequisite is not remembered as satisfied, so the next start
	// attempt runs it again rather than silently skipping it.
	if err := manager.StartService("app"); !errors.Is(err, ErrPrerequisiteFailed) {
		t.Fatalf("second StartService() error = %v, want ErrPrerequisiteFailed", err)
	}
}

func TestGroupPrerequisiteSharedByServicesRunsOnce(t *testing.T) {
	directory := t.TempDir()
	counter := filepath.Join(directory, "runs")
	manager := NewManager(&config.Config{
		Project: "Prerequisites",
		ActionGroups: map[string]config.ActionGroup{
			"infra": {Actions: map[string]config.Action{
				// The sleep widens the window in which both services are inside the
				// same prerequisite, so a second execution would be observable.
				"up": {Command: "sleep 0.2 && echo run >> " + counter, Dir: directory, Shell: "/bin/sh"},
			}},
		},
		Services: map[string]config.Service{
			"api": {Command: "sleep 60", BeforeStart: []config.Prerequisite{{Group: "infra", Action: "up"}}},
			"web": {Command: "sleep 60", BeforeStart: []config.Prerequisite{{Group: "infra", Action: "up"}}},
		},
	})
	defer manager.Shutdown()

	if err := manager.StartServicesContext(context.Background(), []string{"api", "web"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api", "web"} {
		service, _ := manager.GetService(name)
		if service.Status() != config.StatusRunning {
			t.Fatalf("service %s status = %s, want running", name, service.Status())
		}
	}
	if got := prerequisiteRunCount(t, counter); got != 1 {
		t.Fatalf("shared prerequisite ran %d times, want 1", got)
	}
}

func TestPrerequisiteRunsAfterDependenciesAreReady(t *testing.T) {
	directory := t.TempDir()
	order := filepath.Join(directory, "order")
	manager := NewManager(&config.Config{
		Project: "Prerequisites",
		Services: map[string]config.Service{
			// The dependency announces readiness on stdout only after it has
			// appended to the order file, so "ready" and the recorded side
			// effect cannot be observed out of order.
			"database": {
				Command:      "sleep 0.5 && echo database >> " + order + " && echo ready && sleep 60",
				Dir:          directory,
				Shell:        "/bin/sh",
				ReadyLogLine: "ready",
			},
			"api": {
				Command:              "sleep 60",
				DependsOn:            []string{"database"},
				DependencyConditions: map[string]config.DependencyConfig{"database": {Condition: config.DependencyLogReady}},
				Actions: map[string]config.Action{
					"migrate": {Command: "echo migrate >> " + order, Dir: directory, Shell: "/bin/sh"},
				},
				BeforeStart: []config.Prerequisite{{Action: "migrate"}},
			},
		},
	})
	defer manager.Shutdown()

	if err := manager.StartServicesContext(context.Background(), []string{"api"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var content []byte
	for time.Now().Before(deadline) {
		content, _ = os.ReadFile(order)
		if len(strings.Fields(string(content))) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := strings.Fields(string(content))
	want := []string{"database", "migrate"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("execution order = %v, want %v", got, want)
	}
}

func TestReloadForgetsSatisfiedPrerequisiteWhenCommandChanges(t *testing.T) {
	manager, counter := countingPrerequisite(t, config.PrerequisiteOnce)
	if err := manager.StartService("app"); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopService("app"); err != nil {
		t.Fatal(err)
	}

	next := &config.Config{
		Project: "Prerequisites",
		Services: map[string]config.Service{
			"app": {
				Command: "sleep 60",
				Actions: map[string]config.Action{
					// A different command has not been satisfied by the previous run.
					"prepare": {Command: "echo changed >> " + counter, Shell: "/bin/sh"},
				},
				BeforeStart: []config.Prerequisite{{Action: "prepare"}},
			},
		},
	}
	if _, err := manager.ApplyConfig(next); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartService("app"); err != nil {
		t.Fatal(err)
	}
	if got := prerequisiteRunCount(t, counter); got != 2 {
		t.Fatalf("prerequisite ran %d times after its command changed, want 2", got)
	}
}
