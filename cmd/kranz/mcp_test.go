package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	kranzmcp "github.com/kranz-org/kranz/internal/mcp"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

var mcpProjectCounter atomic.Int64

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("KRANZ_TEST_MCP_HELPER") != "1" {
		return
	}
	directory := os.Getenv("KRANZ_TEST_PROJECT_DIR")
	os.Exit(execute([]string{"mcp", "-C", directory}, os.Stdout, os.Stderr))
}

// writeMCPProject creates a project directory with a unique runtime name. It
// does not start anything: after v0.11.0 an MCP connection creates no runtime,
// so every test that wants one starts it explicitly.
func writeMCPProject(t *testing.T) (kranzcli.GlobalOptions, string) {
	t.Helper()
	return writeMCPProjectWithService(t, "api")
}

func writeMCPProjectWithService(t *testing.T, service string) (kranzcli.GlobalOptions, string) {
	t.Helper()
	directory := t.TempDir()
	// t.TempDir names are only unique inside one test, and a runtime name is
	// unique across the machine.
	name := fmt.Sprintf("mcp-test-%d-%d", os.Getpid(), mcpProjectCounter.Add(1))
	document := "project: " + name + "\nservices:\n  " + service + ":\n    command: sleep 60\n"
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	return kranzcli.GlobalOptions{Directory: directory, DirectoryExplicit: true, Output: kranzcli.OutputText}, name
}

// useHelperBackgroundRuntimes makes `up -d` spawn this test binary as the
// detached runtime process. A hosted in-process runtime cannot be used here:
// startRuntime owns the process working directory, so two of them cannot exist
// at once, and the whole point of this release is one client serving several.
func useHelperBackgroundRuntimes(t *testing.T) {
	t.Helper()
	previous := newBackgroundCommand
	newBackgroundCommand = func(_ string, args ...string) *exec.Cmd {
		data, _ := json.Marshal(args)
		command := exec.Command(os.Args[0], "-test.run=^TestBackgroundHelperProcess$")
		command.Env = append(os.Environ(), "KRANZ_TEST_BACKGROUND_HELPER=1", "KRANZ_TEST_BACKGROUND_ARGS="+base64.StdEncoding.EncodeToString(data))
		return command
	}
	t.Cleanup(func() { newBackgroundCommand = previous })
}

// startTestRuntime brings a detached runtime up the way `kranz up -d` does, so
// a test has something for MCP to address.
func startTestRuntime(t *testing.T, options kranzcli.GlobalOptions, name string) kranzruntime.SessionRecord {
	t.Helper()
	useHelperBackgroundRuntimes(t)
	if err := runUp(options, []string{"-d", "--no-start"}, io.Discard); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = runDown(kranzcli.GlobalOptions{Project: name, Output: kranzcli.OutputText}, nil, io.Discard)
	})
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	record, err := registry.Resolve(context.Background(), name, version)
	if err != nil {
		t.Fatalf("runtime %s did not publish: %v", name, err)
	}
	return record
}

// mcpSession drives one MCP server over pipes the way an agent client does.
type mcpSession struct {
	t         *testing.T
	stdin     *io.PipeWriter
	responses *bufio.Reader
	done      chan error
	cancel    context.CancelFunc
	closeOnce sync.Once
	nextID    int
}

// unpinned is how an agent client actually registers the server: no -C, so the
// working directory is a default rather than a statement that this connection
// speaks for one project.
func unpinned(options kranzcli.GlobalOptions) kranzcli.GlobalOptions {
	options.DirectoryExplicit = false
	return options
}

func startMCPSession(t *testing.T, options kranzcli.GlobalOptions) *mcpSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	session := &mcpSession{t: t, stdin: stdinWriter, responses: bufio.NewReader(stdoutReader), done: make(chan error, 1), cancel: cancel}
	go func() {
		err := runMCPContext(ctx, options, stdinReader, stdoutWriter, io.Discard)
		_ = stdoutWriter.Close()
		session.done <- err
	}()
	t.Cleanup(session.close)
	session.request("initialize", map[string]any{"protocolVersion": "2025-11-25"})
	session.notify("notifications/initialized")
	return session
}

// close is idempotent: a test that ends a session explicitly still has the
// cleanup registered at startup.
func (s *mcpSession) close() {
	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.stdin.Close()
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
			s.t.Error("MCP server did not exit")
		}
	})
}

func (s *mcpSession) notify(method string) {
	s.t.Helper()
	if _, err := fmt.Fprintf(s.stdin, `{"jsonrpc":"2.0","method":%q}`+"\n", method); err != nil {
		s.t.Fatal(err)
	}
}

func (s *mcpSession) request(method string, params any) map[string]any {
	s.t.Helper()
	s.nextID++
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": s.nextID, "method": method, "params": params})
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.stdin.Write(append(body, '\n')); err != nil {
		s.t.Fatal(err)
	}
	type response struct {
		ID     int             `json:"id"`
		Result map[string]any  `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	line := make(chan []byte, 1)
	go func() {
		data, readErr := s.responses.ReadBytes('\n')
		if readErr != nil && len(data) == 0 {
			s.t.Errorf("read %s response: %v", method, readErr)
		}
		line <- data
	}()
	var raw []byte
	select {
	case raw = <-line:
	case err := <-s.done:
		s.done <- err
		s.t.Fatalf("MCP server exited before answering %s: %v", method, err)
	case <-time.After(5 * time.Second):
		s.t.Fatalf("no response to %s", method)
	}
	var decoded response
	if err := json.Unmarshal(raw, &decoded); err != nil {
		s.t.Fatalf("decode %s response %q: %v", method, raw, err)
	}
	if len(decoded.Error) > 0 {
		s.t.Fatalf("%s protocol error: %s", method, decoded.Error)
	}
	return decoded.Result
}

// callTool returns the result envelope, which is where every causal answer is.
func (s *mcpSession) callTool(name string, arguments map[string]any) kranzmcp.ResultEnvelope {
	s.t.Helper()
	if arguments == nil {
		arguments = map[string]any{}
	}
	result := s.request("tools/call", map[string]any{"name": name, "arguments": arguments})
	structured, err := json.Marshal(result["structuredContent"])
	if err != nil {
		s.t.Fatal(err)
	}
	var envelope kranzmcp.ResultEnvelope
	if err := json.Unmarshal(structured, &envelope); err != nil {
		s.t.Fatalf("decode envelope %s: %v", structured, err)
	}
	return envelope
}

func envelopeDetails(t *testing.T, envelope kranzmcp.ResultEnvelope) string {
	t.Helper()
	payload, err := json.Marshal(envelope.Error)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestMCPStartsOutsideAnyProjectAndCreatesNoRuntime(t *testing.T) {
	empty := kranzcli.GlobalOptions{Directory: t.TempDir(), DirectoryExplicit: true, Output: kranzcli.OutputText}
	session := startMCPSession(t, empty)

	tools := session.request("tools/list", map[string]any{})
	if len(tools["tools"].([]any)) == 0 {
		t.Fatal("tools/list is empty outside a project")
	}
	resources := session.request("resources/list", map[string]any{})
	if len(resources["resources"].([]any)) == 0 {
		t.Fatal("resources/list is empty outside a project")
	}
	if envelope := session.callTool("runtimes", nil); envelope.Error != nil {
		t.Fatalf("runtimes outside a project = %s", envelopeDetails(t, envelope))
	}

	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	records, err := registry.List(context.Background(), version)
	if err != nil {
		t.Fatal(err)
	}
	// Scoped to this test's directory on purpose: the developer's own machine
	// may still hold a phantom runtime published by a pre-0.11 build.
	for _, record := range records {
		if record.Directory == empty.Directory {
			t.Fatalf("MCP registered itself as a supervisor: %#v", record)
		}
	}
}

func TestMCPResolvesTheProjectItWasStartedIn(t *testing.T) {
	options, name := writeMCPProject(t)
	startTestRuntime(t, options, name)
	session := startMCPSession(t, options)

	envelope := session.callTool("status", nil)
	if envelope.Error != nil {
		t.Fatalf("status = %s", envelopeDetails(t, envelope))
	}
	if envelope.Session == nil || envelope.Session.Name != name {
		t.Fatalf("status was served by %#v, want %s", envelope.Session, name)
	}
}

func TestMCPAnswersOneCallPerRuntimeInOneSession(t *testing.T) {
	first, firstName := writeMCPProject(t)
	second, secondName := writeMCPProject(t)
	startTestRuntime(t, first, firstName)
	startTestRuntime(t, second, secondName)

	// Unbound: no -C, so nothing about the launch prefers either project.
	session := startMCPSession(t, kranzcli.GlobalOptions{Directory: t.TempDir(), DirectoryExplicit: true, Output: kranzcli.OutputText})
	for _, name := range []string{firstName, secondName, firstName} {
		envelope := session.callTool("status", map[string]any{"runtime": name})
		if envelope.Error != nil {
			t.Fatalf("status %s = %s", name, envelopeDetails(t, envelope))
		}
		if envelope.Session == nil || envelope.Session.Name != name {
			t.Fatalf("call addressed to %s was answered by %#v", name, envelope.Session)
		}
	}
}

func TestMCPWithoutAnAddressAnswersWithCandidates(t *testing.T) {
	options, name := writeMCPProject(t)
	startTestRuntime(t, options, name)
	session := startMCPSession(t, kranzcli.GlobalOptions{Directory: t.TempDir(), DirectoryExplicit: true, Output: kranzcli.OutputText})

	envelope := session.callTool("status", nil)
	if envelope.Error == nil || envelope.Error.Code != "runtime_required" {
		t.Fatalf("status without an address = %#v", envelope)
	}
	details := envelopeDetails(t, envelope)
	if !strings.Contains(details, name) {
		t.Fatalf("runtime_required did not name the running candidate: %s", details)
	}
	// The candidate must be usable as-is: that is the whole point of carrying it.
	retry := session.callTool("status", map[string]any{"runtime": name})
	if retry.Error != nil {
		t.Fatalf("retry with the candidate = %s", envelopeDetails(t, retry))
	}
}

func TestMCPReadToolsNeverStartARuntime(t *testing.T) {
	options, name := writeMCPProject(t)
	session := startMCPSession(t, options)

	envelope := session.callTool("logs", nil)
	if envelope.Error == nil || envelope.Error.Code != "runtime_not_found" {
		t.Fatalf("logs against a stopped project = %#v", envelope)
	}
	if !strings.Contains(envelope.Error.Hint, "up") {
		t.Fatalf("hint does not name the tool that would fix it: %q", envelope.Error.Hint)
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(context.Background(), name, version); err == nil {
		t.Fatal("a read tool created a runtime")
	}
}

func TestMCPPinRefusesAnyOtherRuntime(t *testing.T) {
	pinned, pinnedName := writeMCPProject(t)
	other, otherName := writeMCPProject(t)
	startTestRuntime(t, pinned, pinnedName)
	startTestRuntime(t, other, otherName)

	for _, options := range []kranzcli.GlobalOptions{
		pinned,
		{Directory: t.TempDir(), DirectoryExplicit: true, Project: pinnedName, Output: kranzcli.OutputText},
	} {
		session := startMCPSession(t, options)
		if envelope := session.callTool("status", nil); envelope.Error != nil || envelope.Session == nil || envelope.Session.Name != pinnedName {
			t.Fatalf("pinned status = %#v %s", envelope.Session, envelopeDetails(t, envelope))
		}
		envelope := session.callTool("status", map[string]any{"runtime": otherName})
		if envelope.Error == nil || envelope.Error.Code != "runtime_pinned" {
			t.Fatalf("pinned server addressed another runtime: %#v", envelope)
		}
		session.close()
	}
}

func TestMCPPinCannotBeEscapedWhileItsRuntimeIsStopped(t *testing.T) {
	pinned, pinnedName := writeMCPProject(t)
	other, otherName := writeMCPProject(t)
	startTestRuntime(t, other, otherName)

	session := startMCPSession(t, pinned)
	envelope := session.callTool("status", map[string]any{"runtime": otherName})
	if envelope.Error == nil || envelope.Error.Code != "runtime_pinned" {
		t.Fatalf("stopped pin allowed another runtime: %#v", envelope)
	}
	if pinnedResult := session.callTool("status", map[string]any{"runtime": pinnedName}); pinnedResult.Error == nil || pinnedResult.Error.Code != "runtime_not_found" {
		t.Fatalf("the pinned runtime did not report its own stopped state: %#v", pinnedResult)
	}
}

func TestMCPUpStartsARuntimeWithoutServicesAndOutlivesTheSession(t *testing.T) {
	options, name := writeMCPProject(t)
	useHelperBackgroundRuntimes(t)

	session := startMCPSession(t, options)
	if envelope := session.callTool("up", nil); envelope.Error == nil || envelope.Error.Code != "confirmation_required" {
		t.Fatalf("up without confirmation = %#v", envelope)
	}
	envelope := session.callTool("up", map[string]any{"confirm": true})
	if envelope.Error != nil {
		t.Fatalf("up = %s", envelopeDetails(t, envelope))
	}
	defer func() {
		_ = runDown(kranzcli.GlobalOptions{Project: name, Output: kranzcli.OutputText}, nil, io.Discard)
	}()

	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	record, err := registry.Resolve(context.Background(), name, version)
	if err != nil {
		t.Fatalf("up published no runtime: %v", err)
	}
	if record.Mode != "background" {
		t.Fatalf("runtime mode = %q, want background", record.Mode)
	}
	if record.Running == nil || *record.Running != 0 {
		t.Fatalf("up started services: running = %v", record.Running)
	}

	// The runtime belongs to nobody in particular, so the MCP process leaving
	// must not take it with it.
	session.close()
	if _, err := registry.Resolve(context.Background(), name, version); err != nil {
		t.Fatalf("runtime did not outlive the MCP session: %v", err)
	}
}

func TestMCPDownRefusesARuntimeItDidNotStart(t *testing.T) {
	options, name := writeMCPProject(t)
	startTestRuntime(t, options, name)
	session := startMCPSession(t, options)

	envelope := session.callTool("down", map[string]any{"runtime": name, "confirm": true})
	if envelope.Error == nil || envelope.Error.Code != "not_owned" {
		t.Fatalf("down on a runtime someone else started = %#v", envelope)
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(context.Background(), name, version); err != nil {
		t.Fatalf("refused down stopped the runtime anyway: %v", err)
	}
}

func TestMCPUpDoesNotClaimAnAlreadyRunningRuntime(t *testing.T) {
	options, name := writeMCPProject(t)
	startTestRuntime(t, options, name)
	session := startMCPSession(t, options)

	up := session.callTool("up", map[string]any{"confirm": true})
	if up.Error != nil {
		t.Fatalf("up against an existing runtime = %s", envelopeDetails(t, up))
	}
	if up.Session != nil || up.Generation != 0 {
		t.Fatalf("global up claimed a runtime served it: %#v", up)
	}
	data := up.Data.(map[string]any)
	if created, _ := data["created"].(bool); created {
		t.Fatalf("up claimed it created an existing runtime: %#v", up.Data)
	}
	down := session.callTool("down", map[string]any{"runtime": name, "confirm": true})
	if down.Error == nil || down.Error.Code != "not_owned" {
		t.Fatalf("up granted ownership of an existing runtime: %#v", down)
	}

	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(context.Background(), name, version); err != nil {
		t.Fatalf("refused down stopped the existing runtime: %v", err)
	}
}

func TestMCPClientIsVisibleToTheRuntimeItTalksTo(t *testing.T) {
	options, name := writeMCPProject(t)
	record := startTestRuntime(t, options, name)
	session := startMCPSession(t, options)
	if envelope := session.callTool("status", nil); envelope.Error != nil {
		t.Fatalf("status = %s", envelopeDetails(t, envelope))
	}

	client, err := kranzruntime.Dial(record.Socket, version)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	clients, err := client.Clients()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, connected := range clients {
		if connected.Surface == "mcp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("connected clients = %#v, want an mcp surface", clients)
	}
}

func TestMCPSubprocessCompletesCodexStyleInitialize(t *testing.T) {
	options, name := writeMCPProject(t)
	startTestRuntime(t, options, name)
	command := exec.Command(os.Args[0], "-test.run=^TestMCPHelperProcess$")
	command.Env = append(os.Environ(), "KRANZ_TEST_MCP_HELPER=1", "KRANZ_TEST_PROJECT_DIR="+options.Directory)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	if _, err := io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadBytes('\n')
	if err != nil {
		t.Fatalf("initialize response: %v", err)
	}
	if !bytes.Contains(line, []byte(`"protocolVersion":"2025-11-25"`)) || !bytes.Contains(line, []byte(`"name":"kranz"`)) {
		t.Fatalf("initialize response = %s", line)
	}
	_ = stdin.Close()
	_ = command.Wait()
}

func TestMCPUsageErrorsNeverWriteStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"mcp", "--nonsense"}, &stdout, &stderr); code == 0 {
		t.Fatal("unknown mcp option was accepted")
	}
	if stdout.Len() != 0 {
		t.Fatalf("usage error wrote to the JSON-RPC stream: %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"--output=json", "mcp"}, &stdout, &stderr); code == 0 {
		t.Fatal("--output json was accepted for MCP stdio")
	}
	if stdout.Len() != 0 {
		t.Fatalf("usage error wrote to the JSON-RPC stream: %q", stdout.String())
	}
}

// The field failure this release exists for: an agent asked for a service that
// lives one runtime over, and concluded it was unreachable. The error must
// carry the argument that fixes it, and that argument must work.
func TestSelectorNotFoundCarriesARetryThatSucceeds(t *testing.T) {
	here, hereName := writeMCPProjectWithService(t, "api")
	there, thereName := writeMCPProjectWithService(t, "reports")
	startTestRuntime(t, here, hereName)
	startTestRuntime(t, there, thereName)

	session := startMCPSession(t, unpinned(here))
	envelope := session.callTool("status", map[string]any{"selectors": []string{"reports"}})
	if envelope.Error == nil || envelope.Error.Code != "selector_not_found" {
		t.Fatalf("missing selector = %#v", envelope)
	}
	details := envelopeDetails(t, envelope)
	if !strings.Contains(details, thereName) {
		t.Fatalf("available_in did not name the runtime that has it: %s", details)
	}
	retry := session.callTool("status", map[string]any{"runtime": thereName, "selectors": []string{"reports"}})
	if retry.Error != nil {
		t.Fatalf("retry named by available_in failed: %s", envelopeDetails(t, retry))
	}
	if retry.Session == nil || retry.Session.Name != thereName {
		t.Fatalf("retry was answered by %#v", retry.Session)
	}
}

func TestTwoAgentsShareOneRuntime(t *testing.T) {
	options, name := writeMCPProject(t)
	record := startTestRuntime(t, options, name)
	first := startMCPSession(t, options)
	second := startMCPSession(t, options)

	for index, session := range []*mcpSession{first, second, first, second} {
		envelope := session.callTool("status", nil)
		if envelope.Error != nil || envelope.Session == nil || envelope.Session.ID != record.ID {
			t.Fatalf("call %d = %#v %s", index, envelope.Session, envelopeDetails(t, envelope))
		}
	}

	client, err := kranzruntime.Dial(record.Socket, version)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	clients, err := client.Clients()
	if err != nil {
		t.Fatal(err)
	}
	agents := 0
	for _, connected := range clients {
		if connected.Surface == "mcp" {
			agents++
		}
	}
	if agents != 2 {
		t.Fatalf("connected MCP clients = %d, want 2: %#v", agents, clients)
	}
}
