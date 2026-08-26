package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

// startTestSupervisor wires a Local for cfg behind a Supervisor listening on
// a fresh throwaway socket, and returns a dialed Client plus a cleanup that
// tears both down. It is the RPC-boundary equivalent of the manual smoke
// test поток 1 ran by hand against examples/*/kranz.yaml.
func startTestSupervisor(t *testing.T, cfg *config.Config, configPaths []string) (*Client, func()) {
	return startTestSupervisorIdentity(t, cfg, configPaths, ClientIdentity{Surface: "cli", Label: "kranz CLI"})
}

func startTestSupervisorIdentity(t *testing.T, cfg *config.Config, configPaths []string, identity ClientIdentity) (*Client, func()) {
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
		client, err = DialWithIdentity(socketPath, "test", identity)
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

func TestSupervisorCloseDisconnectsAllAttachedClients(t *testing.T) {
	cfg := &config.Config{Project: "Close attached clients"}
	local := app.NewLocal(cfg, nil, app.Options{})
	defer func() { _ = local.Shutdown() }()
	supervisor := NewSupervisor(local)
	_, socketPath, cleanupDir, err := NewSocketDir()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupDir()
	if err := supervisor.Listen(socketPath); err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- supervisor.Serve() }()

	first, err := Dial(socketPath, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := Dial(socketPath, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	closeDone := make(chan error, 1)
	go func() { closeDone <- supervisor.Close() }()
	for index, client := range []*Client{first, second} {
		select {
		case <-client.Done():
		case <-time.After(2 * time.Second):
			_ = first.Close()
			_ = second.Close()
			t.Fatalf("client %d remained connected after supervisor close", index+1)
		}
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Supervisor.Close blocked on attached clients")
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve remained blocked after Close")
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

func TestRunCatalogPreservesConnectionProvenanceAcrossRPC(t *testing.T) {
	cfg := &config.Config{Project: "RPC provenance", ActionGroups: map[string]config.ActionGroup{
		"ops": {Actions: map[string]config.Action{"ping": {Command: "echo pong"}}},
	}}
	client, cleanup := startTestSupervisorIdentity(t, cfg, nil, ClientIdentity{Surface: "mcp", Label: "agent session"})
	defer cleanup()

	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "ops", Name: "ping"}
	if _, err := client.RunAction(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	runs := client.Runs()
	if len(runs) != 1 {
		t.Fatalf("runs = %#v, want one", runs)
	}
	if runs[0].Surface != "mcp" || runs[0].ClientLabel != "agent session" || runs[0].StartReason != "invoked" {
		t.Fatalf("run provenance = %+v", runs[0])
	}
}

func TestExecutePlanPreservesFailedActionRunAcrossIPC(t *testing.T) {
	cfg := &config.Config{Project: "RPC Failed Action", ActionGroups: map[string]config.ActionGroup{
		"ops": {Actions: map[string]config.Action{"fail": {Command: "exit 7", Shell: "/bin/sh"}}},
	}}
	client, cleanup := startTestSupervisor(t, cfg, nil)
	defer cleanup()
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "ops", Name: "fail"}
	result, err := client.ExecutePlan(context.Background(), app.PlanRequest{Operation: "action", Action: id}, "")
	var exit *app.ActionExitError
	if !errors.As(err, &exit) || exit.ExitCode != 7 {
		t.Fatalf("error = %T %v", err, err)
	}
	if result.ActionResult == nil || result.ActionResult.Run != 1 || result.ActionResult.Status != app.ActionFailed || result.ActionResult.ExitCode != 7 {
		t.Fatalf("result = %#v", result)
	}
}

func TestExecutePlanRequestCancellationDoesNotCancelAction(t *testing.T) {
	cfg := &config.Config{Project: "RPC Durable Action", ActionGroups: map[string]config.ActionGroup{
		"ops": {Actions: map[string]config.Action{"slow": {Command: "sleep 0.1; echo done", Shell: "/bin/sh"}}},
	}}
	client, cleanup := startTestSupervisor(t, cfg, nil)
	defer cleanup()
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "ops", Name: "slow"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		result app.OperationResult
		err    error
	}, 1)
	go func() {
		result, err := client.ExecutePlan(ctx, app.PlanRequest{Operation: "action", Action: id}, "")
		done <- struct {
			result app.OperationResult
			err    error
		}{result, err}
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	completed := <-done
	if completed.err != nil || completed.result.ActionResult == nil || completed.result.ActionResult.Status != app.ActionSucceeded {
		t.Fatalf("durable execution = %#v, %v", completed.result, completed.err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		result, err := client.ActionResult(id, -1)
		if err == nil && result.Status == app.ActionSucceeded {
			break
		}
		if err == nil && result.Status == app.ActionCancelled {
			t.Fatal("request cancellation cancelled action")
		}
		if time.Now().After(deadline) {
			t.Fatalf("durable result = %#v, %v", result, err)
		}
		time.Sleep(5 * time.Millisecond)
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
	dependency, ok := client.Service("dependency")
	if !ok || dependency.State.Status == config.StatusStopped {
		t.Fatalf("request cancellation stopped an already started service: %#v", dependency)
	}

	_ = client.StopAll()
}

func TestChangesGraphAndPreflightCrossTheIPCBoundary(t *testing.T) {
	cfg, err := config.LoadFiles([]string{"../../examples/native/kranz.yaml"})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	client, cleanup := startTestSupervisor(t, cfg, []string{"../../examples/native/kranz.yaml"})
	defer cleanup()

	before, err := client.Changes(app.ChangeQuery{})
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	name := cfg.ServiceNames()[0]
	if err := client.ForceStartServices([]string{name}); err != nil {
		t.Fatalf("start: %v", err)
	}
	// An attached MCP session reads the same journal the owner records into;
	// a transition that only existed in-process would make the two disagree.
	after, err := client.Changes(app.ChangeQuery{Since: before.Cursor, Selectors: []string{name}})
	if err != nil {
		t.Fatalf("changes since: %v", err)
	}
	if len(after.Changes) == 0 || after.Changes[0].Service != name {
		t.Fatalf("changes = %#v", after.Changes)
	}
	if after.Changes[0].Run == 0 {
		t.Fatalf("run identity lost across IPC: %#v", after.Changes[0])
	}

	if graph := client.Graph(); len(graph.Nodes) == 0 {
		t.Fatal("graph did not cross the boundary")
	}
	if preflight := client.Preflight(); preflight.ServicesChecked != len(cfg.Services) {
		t.Fatalf("preflight = %#v", preflight)
	}
	if err := client.StopServices([]string{name}); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestPrerequisiteFailureAndWaitTimeoutKeepTheirIdentityAcrossIPC(t *testing.T) {
	cfg := &config.Config{Project: "Prerequisites", ServiceOrder: []string{"api"}, Services: map[string]config.Service{
		"api": {
			Command: "sleep 30", Shell: "/bin/sh",
			BeforeStart: []config.Prerequisite{{Action: "preflight", Run: config.PrerequisiteAlways}},
			Actions:     map[string]config.Action{"preflight": {Command: "exit 3", Shell: "/bin/sh"}},
			ActionOrder: []string{"preflight"},
		},
	}}
	client, cleanup := startTestSupervisor(t, cfg, nil)
	defer cleanup()

	_, err := client.ExecutePlan(context.Background(), app.PlanRequest{Operation: "start", Selectors: []string{"api"}, IncludeDependencies: true}, "")
	// Flattening this to text is what made an attached client report a
	// specific, answerable failure as a generic one.
	var prerequisite *service.PrerequisiteError
	if !errors.As(err, &prerequisite) {
		t.Fatalf("start err = %#v", err)
	}
	if prerequisite.Service != "api" || prerequisite.Action.Name != "preflight" || prerequisite.Run != 1 {
		t.Fatalf("prerequisite = %#v", prerequisite)
	}
	if !errors.Is(err, service.ErrPrerequisiteFailed) {
		t.Fatal("errors.Is lost the prerequisite kind")
	}
	if strings.Count(prerequisite.Error(), "not started") != 1 {
		t.Fatalf("message repeats itself: %q", prerequisite.Error())
	}

	// The runtime owns the wait timeout. When the transport deadline owned it,
	// the client gave up first and a timeout was reported as a cancellation.
	_, err = client.Wait(context.Background(), app.WaitRequest{Selectors: []string{"api"}, Condition: "ready", Timeout: 200 * time.Millisecond})
	var waitErr *app.WaitError
	if !errors.As(err, &waitErr) || waitErr.Code != "wait_timeout" {
		t.Fatalf("wait err = %#v", err)
	}
}
