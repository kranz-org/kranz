package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// startTestSupervisor wires a Local for cfg behind a Supervisor listening on
// a fresh throwaway socket, and returns a dialed Client plus a cleanup that
// tears both down. It is the RPC-boundary equivalent of the manual smoke
// test поток 1 ran by hand against examples/*/kranz.yaml.
func startTestSupervisor(t *testing.T, cfg *config.Config, configPaths []string) (*Client, func()) {
	t.Helper()
	local := app.NewLocal(cfg, configPaths, app.Options{})
	supervisor := NewSupervisor(local)

	_, socketPath, cleanupDir, err := NewSocketDir()
	if err != nil {
		t.Fatalf("NewSocketDir: %v", err)
	}

	serveErr := make(chan error, 1)
	if err := supervisor.Listen(socketPath); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() { serveErr <- supervisor.Serve() }()
	// Serve binds the socket synchronously as its first step, but Dial can
	// still race it; retry briefly instead of sleeping a fixed guess.
	var client *Client
	deadline := time.Now().Add(2 * time.Second)
	for {
		client, err = Dial(socketPath, "test")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial %s: %v", socketPath, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cleanup := func() {
		_ = client.Close()
		_ = supervisor.Close()
		select {
		case err := <-serveErr:
			if err != nil {
				t.Errorf("Serve returned an error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return after Close")
		}
		cleanupDir()
		_ = local.Shutdown()
	}
	return client, cleanup
}

func TestSupervisorClientDriveARealServiceLifecycle(t *testing.T) {
	cfg, err := config.LoadFiles([]string{"../../examples/native/kranz.yaml"})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	client, cleanup := startTestSupervisor(t, cfg, []string{"../../examples/native/kranz.yaml"})
	defer cleanup()

	project := client.Project()
	if project.Name != cfg.Project {
		t.Fatalf("project name = %q, want %q", project.Name, cfg.Project)
	}
	services := client.Services()
	if len(services) != len(cfg.Services) {
		t.Fatalf("services = %d, want %d", len(services), len(cfg.Services))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.StartServicesContext(ctx, []string{"migrate"}); err != nil {
		t.Fatalf("start migrate: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		svc, ok := client.Service("migrate")
		if !ok {
			t.Fatal("migrate service disappeared")
		}
		if svc.State.Status == config.StatusRunning || svc.State.Status == config.StatusStopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("migrate did not settle, last status %s", svc.State.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// migrate is a one-shot script that may have already completed by now,
	// so it can legitimately be absent from the plan; this just proves the
	// call round-trips a real ShutdownPlan value rather than erroring.
	if plan := client.ShutdownPlan(); plan.Managed == nil && plan.DetachedStop == nil && plan.DetachedKeep == nil {
		t.Logf("shutdown plan: %#v (empty is plausible once migrate has completed)", plan)
	}

	if _, err := client.Reload(true); err != nil {
		t.Fatalf("forced reload: %v", err)
	}

	if err := client.StopAll(); err != nil {
		t.Fatalf("stop all: %v", err)
	}
}

func TestStartedServiceOutlivesCompletedRPCRequest(t *testing.T) {
	cfg := &config.Config{Project: "RPC lifetime", Services: map[string]config.Service{
		"worker": {Command: "sleep 60"},
	}}
	client, cleanup := startTestSupervisor(t, cfg, nil)
	defer cleanup()
	if err := client.StartServicesContext(context.Background(), []string{"worker"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	worker, ok := client.Service("worker")
	if !ok || worker.State.Status != config.StatusRunning || worker.State.PID <= 0 {
		t.Fatalf("service after completed start RPC = %+v, exists=%v", worker, ok)
	}
	if err := client.StopAll(); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorClientRunNonInteractiveAction(t *testing.T) {
	cfg := &config.Config{
		Project: "RPC Actions",
		ActionGroups: map[string]config.ActionGroup{
			"ops": {Actions: map[string]config.Action{"ping": {Command: "echo pong"}}},
		},
	}
	client, cleanup := startTestSupervisor(t, cfg, nil)
	defer cleanup()

	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "ops", Name: "ping"}
	result, err := client.RunAction(context.Background(), id)
	if err != nil {
		t.Fatalf("run action: %v", err)
	}
	if result.Status != app.ActionSucceeded {
		t.Fatalf("action status = %s, want succeeded", result.Status)
	}
	if len(result.Stdout) == 0 || strings.TrimSpace(result.Stdout[0]) != "pong" {
		t.Fatalf("action stdout = %#v, want [pong]", result.Stdout)
	}

	state, ok := client.ActionState(id)
	if !ok || state.Status != app.ActionSucceeded {
		t.Fatalf("action state = %#v, ok=%v", state, ok)
	}
}

func TestSupervisorClientInteractiveActionLeaseRoundTrips(t *testing.T) {
	cfg := &config.Config{
		Project: "RPC Interactive",
		Services: map[string]config.Service{
			"app": {Command: "sleep 60", Actions: map[string]config.Action{
				"console": {Command: "exit 0", Shell: "/bin/sh", Interactive: boolPointer(true)},
			}},
		},
	}
	client, cleanup := startTestSupervisor(t, cfg, nil)
	defer cleanup()

	id := config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "app", Name: "console"}
	action, lease, err := client.AcquireInteractiveAction(id)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if action.Command != "exit 0" {
		t.Fatalf("resolved action = %#v", action)
	}
	if lease == "" {
		t.Fatal("empty lease")
	}
	// A second acquire must be refused while the lease is outstanding — the
	// runtime never saw a live process for this lease, only the reservation.
	if _, _, err := client.AcquireInteractiveAction(id); err == nil {
		t.Fatal("second acquire while the lease is outstanding must fail")
	}

	command := app.BuildInteractiveCommand(action)
	if err := command.Run(); err != nil {
		t.Fatalf("run command locally: %v", err)
	}
	result, err := client.CompleteInteractiveAction(id, lease, nil, command.ProcessState.ExitCode(), 0)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if result.Status != app.ActionSucceeded {
		t.Fatalf("result = %#v", result)
	}
}

func TestDisconnectReleasesInteractiveActionLease(t *testing.T) {
	cfg := &config.Config{
		Project: "RPC Interactive Disconnect",
		Services: map[string]config.Service{
			"app": {Command: "sleep 60", Actions: map[string]config.Action{
				"console": {Command: "exit 0", Shell: "/bin/sh", Interactive: boolPointer(true)},
			}},
		},
	}
	client, cleanup := startTestSupervisor(t, cfg, nil)
	defer cleanup()

	id := config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "app", Name: "console"}
	_, lease, err := client.AcquireInteractiveAction(id)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	intruder, err := Dial(client.conn.RemoteAddr().String(), "test")
	if err != nil {
		t.Fatalf("dial second client: %v", err)
	}
	if _, err := intruder.CompleteInteractiveAction(id, lease, nil, 0, 0); err == nil {
		t.Fatal("another client completed a lease it does not own")
	}
	_ = intruder.Close()
	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}

	// Teardown is asynchronous. A fresh client must eventually be able to
	// acquire the same owner, proving the dead connection did not strand it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		next, err := Dial(client.conn.RemoteAddr().String(), "test")
		if err != nil {
			t.Fatalf("dial replacement client: %v", err)
		}
		_, lease, acquireErr := next.AcquireInteractiveAction(id)
		if acquireErr == nil {
			if _, completeErr := next.CompleteInteractiveAction(id, lease, errors.New("test cleanup"), -1, 0); completeErr != nil {
				t.Fatalf("complete replacement lease: %v", completeErr)
			}
			_ = next.Close()
			break
		}
		_ = next.Close()
		if time.Now().After(deadline) {
			t.Fatalf("lease remained busy after disconnect: %v", acquireErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func boolPointer(v bool) *bool { return &v }

func TestSupervisorClientContextCancellationInterruptsAWaitingStart(t *testing.T) {
	cfg := &config.Config{
		Project: "RPC Cancel",
		Services: map[string]config.Service{
			"dependency": {Command: "sleep 60", HealthCheck: &config.HealthCheckConfig{
				Readiness: &config.CheckConfig{Type: config.CheckCommand, Command: "exit 1", Interval: 10 * time.Millisecond},
			}},
			"dependent": {Command: "sleep 60", DependsOn: []string{"dependency"}},
		},
	}
	client, cleanup := startTestSupervisor(t, cfg, nil)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.StartServicesContext(ctx, []string{"dependent"}) }()

	// Give the call time to actually reach the server and start waiting on
	// the readiness gate before canceling it, so this exercises real
	// interruption of an in-flight wait rather than a call that never sent.
	time.Sleep(150 * time.Millisecond)
	cancel()

	// A generic remote error only carries text across the wire (see
	// decodeError), so this cannot assert errors.Is(err, context.Canceled)
	// the way an in-process caller could. What actually matters — and what
	// TestStopInterruptsReadinessGatedStart in internal/ui pins for the
	// in-process case — is that cancellation interrupts the wait promptly
	// instead of blocking for the dependency's 30-second readiness timeout.
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error from a canceled start")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not interrupt the readiness wait over the wire")
	}

	_ = client.StopAll()
}
