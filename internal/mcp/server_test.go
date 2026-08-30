package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

func testServer(t *testing.T) (*Server, *app.Local) {
	t.Helper()
	interactive := true
	confirm := true
	cfg := &config.Config{Project: "demo", Services: map[string]config.Service{
		"api": {Command: "exit 0", Tags: []string{"backend"}, Env: map[string]string{
			"API_TOKEN":    "super-secret",
			"DATABASE_URL": "postgresql://app:database-secret@db.example/app",
			"PUBLIC_URL":   "https://example.com",
		}, Actions: map[string]config.Action{
			"shell":   {Command: "sh", Interactive: &interactive},
			"migrate": {Command: "printf 'migrated\\n'", Shell: "/bin/sh"},
			"deploy":  {Command: "exit 0", Shell: "/bin/sh", Confirm: &confirm},
			"fail":    {Command: "exit 7", Shell: "/bin/sh"},
			"slow":    {Command: "sleep 0.1; echo done", Shell: "/bin/sh"},
		}, ActionOrder: []string{"shell", "migrate", "deploy", "fail", "slow"}},
	}, ServiceOrder: []string{"api"}}
	local := app.NewLocal(cfg, nil, app.Options{SessionID: "session-1"})
	t.Cleanup(func() { _ = local.Shutdown() })
	server := NewServerForRuntime(local, SessionIdentity{ID: "session-1", Name: "demo", Project: "demo", ProtocolVersion: 2}, bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	return server, local
}

func TestFailedActionReturnsRunSnapshotAndCausalCode(t *testing.T) {
	server, _ := testServer(t)
	result := testTool(t, server, "action_run", `{"action":"api/fail"}`)
	if result.Error == nil || result.Error.Code != "action_failed" {
		t.Fatalf("result = %#v", result)
	}
	payload, _ := json.Marshal(result.Data)
	if !strings.Contains(string(payload), `"run":1`) || !strings.Contains(string(payload), `"exit_code":7`) {
		t.Fatalf("failed result = %s", payload)
	}
}

func TestConfigResourceUsesSharedRedaction(t *testing.T) {
	server, _ := testServer(t)
	result := testResource(t, server, "kranz://config")
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if strings.Contains(text, "super-secret") || strings.Contains(text, "database-secret") {
		t.Fatalf("config leaked a secret: %s", text)
	}
	if !strings.Contains(text, `"API_TOKEN":"[redacted]"`) || !strings.Contains(text, `"DATABASE_URL":"[redacted]"`) {
		t.Fatalf("redacted value missing: %s", text)
	}
	if !strings.Contains(text, `"PUBLIC_URL":"https://example.com"`) {
		t.Fatalf("public URL was redacted: %s", text)
	}
	services := testResource(t, server, "kranz://services")
	payload, _ = json.Marshal(services)
	if strings.Contains(string(payload), "super-secret") || strings.Contains(string(payload), "database-secret") || !strings.Contains(string(payload), "[redacted]") {
		t.Fatalf("services redaction = %s", payload)
	}
}

func TestWaitTimeoutIsCausalAndDoesNotMutateService(t *testing.T) {
	server, local := testServer(t)
	result := testTool(t, server, "wait", `{"selectors":["api"],"condition":"running","timeout":"10ms"}`)
	if result.Error == nil || result.Error.Code != "wait_timeout" {
		t.Fatalf("result = %#v", result)
	}
	service, _ := local.Service("api")
	if service.State.Status != config.StatusStopped {
		t.Fatalf("wait mutated service to %s", service.State.Status)
	}
}

func TestWaitReturnsTheJournalCursorThroughMCP(t *testing.T) {
	server, local := testServer(t)
	local.SetServiceStatusForTest("api", config.StatusRunning)
	result := testTool(t, server, "wait", `{"selectors":["api"],"condition":"running"}`)
	if result.Error != nil {
		t.Fatalf("wait = %#v", result.Error)
	}
	payload := result.Data.(map[string]any)
	if cursor, ok := payload["cursor"].(uint64); !ok || cursor == 0 {
		t.Fatalf("wait cursor = %#v", payload["cursor"])
	}
}

func TestLifecycleMutationsRequireExplicitSelectors(t *testing.T) {
	server, _ := testServer(t)
	for _, name := range []string{"start", "stop", "restart"} {
		definition := server.tools[name]
		required, ok := definition.InputSchema["required"].([]string)
		if !ok || !reflect.DeepEqual(required, []string{"selectors"}) {
			t.Fatalf("%s required = %#v", name, definition.InputSchema["required"])
		}
		selectors := definition.InputSchema["properties"].(map[string]any)["selectors"].(map[string]any)
		if selectors["minItems"] != 1 {
			t.Fatalf("%s selectors schema = %#v", name, selectors)
		}
		for _, arguments := range []string{`{}`, `{"selectors":[]}`} {
			result := testTool(t, server, name, arguments)
			if result.Error == nil || result.Error.Code != "invalid_arguments" {
				t.Fatalf("%s %s = %#v", name, arguments, result)
			}
		}
	}
}

type blockingWaitAPI struct {
	app.API
	started   chan struct{}
	cancelled chan struct{}
}

func (a *blockingWaitAPI) Wait(ctx context.Context, _ app.WaitRequest) (app.WaitResult, error) {
	close(a.started)
	<-ctx.Done()
	close(a.cancelled)
	return app.WaitResult{}, &app.WaitError{Code: "wait_cancelled", Message: ctx.Err().Error()}
}

func TestServeCancelsPendingRequestsWhenStdinCloses(t *testing.T) {
	server, _ := testServer(t)
	api := &blockingWaitAPI{API: server.testAPI(), started: make(chan struct{}), cancelled: make(chan struct{})}
	server.setTestAPI(api)
	server.initialized.Store(true)
	reader, writer := io.Pipe()
	server.stdin = reader
	var stdout bytes.Buffer
	server.stdout = &stdout
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(context.Background()) }()

	if _, err := io.WriteString(writer, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait","arguments":{"selectors":["api"],"condition":"running"}}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-api.started:
	case <-time.After(time.Second):
		t.Fatal("wait request did not start")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after stdin closed")
	}
	select {
	case <-api.cancelled:
	default:
		t.Fatal("pending wait was not cancelled")
	}
}

func TestMCPRequestCancellationReturnsWaitCancelled(t *testing.T) {
	server, _ := testServer(t)
	server.initialized.Store(true)
	params := json.RawMessage(`{"name":"wait","arguments":{"selectors":["api"],"condition":"running"}}`)
	done := make(chan struct{})
	go func() {
		server.handleRequest(context.Background(), rpcMessage{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: params})
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		server.pendingMu.Lock()
		pending := server.pending[requestKey(json.RawMessage("1"))]
		server.pendingMu.Unlock()
		if pending != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("wait request was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	// The client may spell the same id differently in the cancellation than it
	// did in the request; the id is a number, not the bytes that carried it.
	server.handleNotification(rpcMessage{Method: "notifications/cancelled", Params: json.RawMessage(`{"requestId":1.0,"reason":"test"}`)})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled wait did not return")
	}
	output := server.stdout.(*bytes.Buffer).String()
	if !strings.Contains(output, `"code":"wait_cancelled"`) {
		t.Fatalf("response = %s", output)
	}
}

func TestActionRequestCancellationLeavesActionRunning(t *testing.T) {
	server, local := testServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan ResultEnvelope, 1)
	go func() { done <- testToolContext(t, ctx, server, "action_run", `{"action":"api/slow"}`) }()
	id, _, _ := server.testScope().resolveAction("api/slow")
	deadline := time.Now().Add(time.Second)
	for {
		state, _ := local.ActionState(id)
		if state.Status == app.ActionRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("action did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	result := <-done
	if result.Error == nil || result.Error.Code != "cancelled" {
		t.Fatalf("cancel response = %#v", result)
	}
	deadline = time.Now().Add(time.Second)
	for {
		state, _ := local.ActionState(id)
		if state.Status == app.ActionSucceeded {
			break
		}
		if state.Status == app.ActionCancelled {
			t.Fatal("request cancellation cancelled the action")
		}
		if time.Now().After(deadline) {
			t.Fatalf("action did not finish: %#v", state)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestActionRunConfirmationAndRunIdentityRoundTrip(t *testing.T) {
	server, _ := testServer(t)
	first := testTool(t, server, "action_run", `{"action":"api/deploy"}`)
	if first.Error == nil || first.Error.Code != "confirmation_required" {
		t.Fatalf("first = %#v", first)
	}
	token, _ := first.Error.Details["confirmation_token"].(string)
	if token == "" {
		t.Fatalf("confirmation details = %#v", first.Error.Details)
	}
	second := testTool(t, server, "action_run", `{"action":"api/deploy","confirmation_token":"`+token+`"}`)
	if second.Error != nil {
		t.Fatalf("second = %#v", second)
	}

	run := testTool(t, server, "action_run", `{"action":"api/migrate"}`)
	if run.Error != nil {
		t.Fatalf("run = %#v", run)
	}
	result := testTool(t, server, "action_result", `{"action":"api/migrate","run":-1}`)
	if result.Error != nil {
		t.Fatalf("result = %#v", result)
	}
	logs := testTool(t, server, "logs", `{"selectors":["api/migrate"],"run":-1}`)
	if logs.Error != nil {
		t.Fatalf("logs = %#v", logs)
	}
	payload, _ := json.Marshal(logs.Data)
	if !strings.Contains(string(payload), `"run":1`) || !strings.Contains(string(payload), "migrated") {
		t.Fatalf("action log identity = %s", payload)
	}
}

func TestMCPUsesSameSelectorsPlansAndLogsAsApplication(t *testing.T) {
	server, local := testServer(t)
	local.AppendLogForTest("api", "[stderr] first")
	local.AppendLogForTest("api", "[stderr] second")

	directPlan, err := local.Plan(app.PlanRequest{Operation: "start", Selectors: []string{"backend"}, IncludeDependencies: true})
	if err != nil {
		t.Fatal(err)
	}
	toolPlan := testTool(t, server, "plan", `{"operation":"start","selectors":["backend"],"include_dependencies":true}`)
	if toolPlan.Error != nil {
		t.Fatalf("tool plan = %#v", toolPlan)
	}
	if got := toolPlan.Data.(app.OperationPlan); got.Fingerprint != directPlan.Fingerprint || !reflect.DeepEqual(got.Targets, directPlan.Targets) {
		t.Fatalf("plan mismatch: %#v != %#v", got, directPlan)
	}

	directLogs, err := local.QueryLogs(app.LogQuery{Selectors: []string{"backend"}, Tail: 1, Sources: []string{"stderr"}})
	if err != nil {
		t.Fatal(err)
	}
	toolLogs := testTool(t, server, "logs", `{"selectors":["backend"],"tail":1,"sources":["stderr"]}`)
	if toolLogs.Error != nil {
		t.Fatalf("tool logs = %#v", toolLogs)
	}
	got := toolLogs.Data.(app.LogResult)
	if !reflect.DeepEqual(got.Events, directLogs.Events) || got.Truncated != directLogs.Truncated {
		t.Fatalf("logs mismatch: %#v != %#v", got, directLogs)
	}
}

func TestRunDeleteToolRequiresConfirmationAndDeletesExactRun(t *testing.T) {
	server, local := testServer(t)
	local.SetServiceStatusForTest("api", config.StatusStarting)
	local.AppendLogForTest("api", "retained")
	snapshot, _ := local.Service("api")
	completed := snapshot.State
	completed.Completed = true
	completed.ExitCode = 0
	local.SetServiceStateForTest("api", completed)
	local.SetServiceStatusForTest("api", config.StatusStopped)

	result := testTool(t, server, "run_delete", `{"target":"api","run":1}`)
	if result.Error == nil || result.Error.Code != "confirmation_required" || len(local.Runs()) != 1 {
		t.Fatalf("unconfirmed delete = %#v, runs=%#v", result, local.Runs())
	}
	result = testTool(t, server, "run_delete", `{"target":"api","run":1,"confirm":true}`)
	if result.Error != nil || len(local.Runs()) != 0 || len(local.Logs("api")) != 0 {
		t.Fatalf("confirmed delete = %#v, runs=%#v logs=%#v", result, local.Runs(), local.Logs("api"))
	}
}

func TestCapabilitySurfaceIsExactAllowList(t *testing.T) {
	server, _ := testServer(t)
	// toolOrder is built from toolNames, so comparing the two proves nothing.
	// The allow-list is only meaningful against the implemented set.
	implemented := make([]string, 0, len(server.tools))
	for name := range server.tools {
		implemented = append(implemented, name)
	}
	sort.Strings(implemented)
	want := append([]string(nil), toolNames...)
	sort.Strings(want)
	if !reflect.DeepEqual(implemented, want) {
		t.Fatalf("implemented tools = %v, allow-list = %v", implemented, want)
	}
	for _, definition := range server.listTools() {
		if definition.Name == "" || (definition.scoped == nil && definition.global == nil) {
			t.Fatalf("listed tool has no definition: %#v", definition)
		}
		if definition.OutputSchema == nil {
			t.Errorf("%s declares no output schema", definition.Name)
		}
	}
	for _, forbidden := range []string{"toggle", "StopAll", "ForceStartServices", "ForceStopServices", "Shutdown", "ReleaseExternalPort", "dispatch", "shell"} {
		if _, ok := server.tools[forbidden]; ok {
			t.Fatalf("unsafe tool %q is reachable", forbidden)
		}
	}
	if got := len(server.resourceOrder); got != 9 {
		t.Fatalf("resources = %d, want 9", got)
	}
	for _, definition := range server.listTools() {
		if definition.InputSchema["additionalProperties"] != false {
			t.Errorf("%s schema is not closed", definition.Name)
		}
	}
	if _, protocolErr := server.callTool(context.Background(), json.RawMessage(`{"name":"Shutdown","arguments":{}}`)); protocolErr == nil {
		t.Fatal("generic/unsafe tool dispatch succeeded")
	}
}

func TestRuntimesToolAndResourceListEveryRunningRuntime(t *testing.T) {
	server, _ := testServer(t)
	entries := []RuntimeEntry{
		{ID: "session-1", Name: "shop-dev", State: kranzruntime.SessionRunning},
		{ID: "other-1", Name: "billing", State: kranzruntime.SessionRunning, Services: intPointer(4)},
	}
	server.runtimeListOverride = func(context.Context) ([]RuntimeEntry, error) { return entries, nil }
	for _, result := range []ResultEnvelope{
		testTool(t, server, "runtimes", `{}`),
		testResource(t, server, "kranz://runtimes"),
	} {
		if result.Error != nil {
			t.Fatalf("runtimes = %#v", result.Error)
		}
		if result.Session != nil {
			t.Fatalf("runtimes is global and must not claim a runtime answered it: %#v", result.Session)
		}
		payload, _ := json.Marshal(result.Data)
		if !strings.Contains(string(payload), `"name":"shop-dev"`) || !strings.Contains(string(payload), `"name":"billing"`) {
			t.Fatalf("runtimes payload = %s", payload)
		}
	}
}

func TestSelectorNotFoundNamesCurrentAndMatchingRuntime(t *testing.T) {
	server, _ := testServer(t)
	server.setTestSessionName("shop-dev")
	server.selectorMatchOverride = func(_ context.Context, selector string) ([]RuntimeSelectorMatch, error) {
		if selector != "reports" {
			t.Fatalf("selector = %q", selector)
		}
		return []RuntimeSelectorMatch{{Runtime: "billing", ID: "ec52bcfb", Kind: "service", Service: "reports"}}, nil
	}
	result := testTool(t, server, "status", `{"selectors":["reports"]}`)
	if result.Error == nil || result.Error.Code != "selector_not_found" || !strings.Contains(result.Error.Message, `runtime "shop-dev"`) || !strings.Contains(result.Error.Hint, "another running runtime") {
		t.Fatalf("error = %#v", result.Error)
	}
	payload, _ := json.Marshal(result.Error.Details)
	if !strings.Contains(string(payload), `"runtime":"billing"`) || !strings.Contains(string(payload), `"service":"reports"`) {
		t.Fatalf("details = %s", payload)
	}
}

func intPointer(value int) *int { return &value }

func TestEveryResultCarriesIdentityGenerationAndSchema(t *testing.T) {
	server, _ := testServer(t)
	for _, uri := range server.resourceOrder {
		result := testResource(t, server, uri)
		if result.SchemaVersion != SchemaVersion {
			t.Fatalf("%s envelope = %#v", uri, result)
		}
		// A global resource is answered by the MCP process, so it carries no
		// runtime identity; a runtime-scoped one always names who answered.
		if server.resources[uri].global != nil {
			if result.Session != nil {
				t.Fatalf("%s is global and carries a session: %#v", uri, result.Session)
			}
			continue
		}
		if result.Session == nil || result.Session.ID != "session-1" || result.Generation == 0 {
			t.Fatalf("%s envelope = %#v", uri, result)
		}
	}
	result := testTool(t, server, "status", `{"selectors":["backend"]}`)
	if result.Error != nil || result.SchemaVersion != SchemaVersion || result.Session == nil || result.Session.ID != "session-1" {
		t.Fatalf("status = %#v", result)
	}
	payload, _ := json.Marshal(result)
	if !strings.Contains(string(payload), `"state":{"status":"stopped"`) || !strings.Contains(string(payload), `"primary_action":"start"`) || strings.Contains(string(payload), `"State"`) {
		t.Fatalf("status wire shape = %s", payload)
	}
}

func TestInteractiveActionReturnsStableAddressAndHint(t *testing.T) {
	server, _ := testServer(t)
	result := testTool(t, server, "action_run", `{"action":"api/shell"}`)
	if result.Error == nil || result.Error.Code != "interactive_action" {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(result.Error.Message, "api/shell") || !strings.Contains(result.Error.Hint, "OWNER/ACTION") {
		t.Fatalf("error = %#v", result.Error)
	}
}

func TestStdioContainsOnlyJSONRPCFrames(t *testing.T) {
	server, _ := testServer(t)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"
	var stdout, stderr bytes.Buffer
	server.stdin, server.stdout, server.stderr = strings.NewReader(input), &stdout, &stderr
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout lines = %d: %q", len(lines), stdout.String())
	}
	for _, line := range lines {
		var response rpcResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil || response.JSONRPC != "2.0" {
			t.Fatalf("foreign stdout %q: %v", line, err)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected diagnostics: %s", stderr.String())
	}
}

func TestStartAllowsPlannedExactTargetsWithoutExposingForceMethod(t *testing.T) {
	server, _ := testServer(t)
	result := testTool(t, server, "start", `{"selectors":["api"],"include_dependencies":false}`)
	if result.Error != nil {
		t.Fatalf("result = %#v", result)
	}
	if _, exposed := server.tools["ForceStartServices"]; exposed {
		t.Fatal("raw force-start method is exposed")
	}
}

func TestHandlerPanicFailsOneCallInsteadOfTheProcess(t *testing.T) {
	server, _ := testServer(t)
	server.initialized.Store(true)
	// This process may also be the supervisor: a panic inside one tool must
	// not take the managed services down with it.
	server.tools["status"] = toolDefinition{Name: "status", InputSchema: objectSchema(nil), scoped: func(*scope, context.Context, json.RawMessage) ResultEnvelope {
		panic("handler exploded")
	}}
	var stdout, stderr bytes.Buffer
	server.stdout, server.stderr = &stdout, &stderr
	server.handleRequest(context.Background(), rpcMessage{JSONRPC: "2.0", ID: json.RawMessage("7"), Method: "tools/call", Params: json.RawMessage(`{"name":"status","arguments":{}}`)})

	var response rpcResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("stdout = %q: %v", stdout.String(), err)
	}
	if response.Error == nil || response.Error.Code != -32603 {
		t.Fatalf("response = %#v", response.Error)
	}
	if !strings.Contains(stderr.String(), "handler exploded") {
		t.Fatalf("diagnostics = %q", stderr.String())
	}
}

func TestUnavailableServiceIsCausalRatherThanFatal(t *testing.T) {
	server, _ := testServer(t)
	// ResolveServiceSelectors answers from configuration; the runtime answers
	// for live state. When the two disagree, the tool must say so.
	if _, err := server.testScope().selectedServices([]string{"api"}); err != nil {
		t.Fatalf("configured service = %v", err)
	}
	server.setTestAPI(missingServiceAPI{API: server.testAPI()})
	result := testTool(t, server, "status", `{"selectors":["api"]}`)
	if result.Error == nil || result.Error.Code != "service_unavailable" {
		t.Fatalf("result = %#v", result.Error)
	}
}

// missingServiceAPI is a runtime that knows a service in configuration but no
// longer answers for it, which is what an IPC client returns after the runtime
// it was attached to goes away.
type missingServiceAPI struct{ app.API }

func (missingServiceAPI) Service(string) (*app.ServiceSnapshot, bool) { return nil, false }

func TestChangesGraphAndPreflightAnswerThroughTheApplicationLayer(t *testing.T) {
	server, local := testServer(t)
	before := testTool(t, server, "changes", `{}`)
	if before.Error != nil {
		t.Fatalf("changes = %#v", before.Error)
	}
	cursor := before.Data.(app.ChangeResult).Cursor
	local.SetServiceStatusForTest("api", config.StatusStarting)

	after := testTool(t, server, "changes", fmt.Sprintf(`{"since":%d}`, cursor))
	changes := after.Data.(app.ChangeResult).Changes
	if len(changes) != 1 || changes[0].Service != "api" || changes[0].To != "starting" {
		t.Fatalf("changes = %#v", changes)
	}

	graph := testTool(t, server, "graph", `{}`)
	if graph.Error != nil || len(graph.Data.(app.Graph).Nodes) == 0 {
		t.Fatalf("graph = %#v", graph)
	}
	doctor := testTool(t, server, "doctor", `{}`)
	if doctor.Error != nil || doctor.Data.(app.PreflightResult).ServicesChecked != 1 {
		t.Fatalf("doctor = %#v", doctor)
	}
	health := testTool(t, server, "health", `{"selectors":["api"],"history":true}`)
	if health.Error != nil {
		t.Fatalf("health = %#v", health.Error)
	}
	reload := testTool(t, server, "reload", `{"force":false}`)
	if reload.Error != nil {
		t.Fatalf("reload = %#v", reload.Error)
	}
}

func TestPortInspectSeparatesManagedFromForeignListeners(t *testing.T) {
	server, _ := testServer(t)
	result := testTool(t, server, "port_inspect", `{"ports":[65535]}`)
	if result.Error != nil {
		t.Fatalf("port_inspect = %#v", result.Error)
	}
	entries := result.Data.(map[string]any)["ports"].([]map[string]any)
	if len(entries) != 1 || entries[0]["port"] != 65535 {
		t.Fatalf("entries = %#v", entries)
	}
	if bad := testTool(t, server, "port_inspect", `{"ports":[]}`); bad.Error == nil {
		t.Fatal("an empty port list must be rejected")
	}
}

func TestResourceTemplatesListPublishesTheAddressedForm(t *testing.T) {
	server, _ := testServer(t)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/templates/list","params":{}}`,
	}, "\n") + "\n"
	var stdout bytes.Buffer
	server.stdin, server.stdout = strings.NewReader(input), &stdout
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"uriTemplate":"kranz://runtimes/{runtime}/config"`) || strings.Contains(stdout.String(), "-32601") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
