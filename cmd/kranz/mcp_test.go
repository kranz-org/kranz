package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("KRANZ_TEST_MCP_HELPER") != "1" {
		return
	}
	directory := os.Getenv("KRANZ_TEST_PROJECT_DIR")
	os.Exit(execute([]string{"mcp", "-C", directory}, os.Stdout, os.Stderr))
}

func waitForMCPRuntime(t *testing.T, name string) kranzruntime.SessionRecord {
	t.Helper()
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		record, resolveErr := registry.ResolveForAttach(context.Background(), name, version)
		if resolveErr == nil && record.State == kranzruntime.SessionRunning {
			return record
		}
		if time.Now().After(deadline) {
			t.Fatalf("MCP runtime did not publish: %v", resolveErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeMCPProject(t *testing.T) (kranzcli.GlobalOptions, string) {
	t.Helper()
	directory := t.TempDir()
	name := "mcp-test-" + strings.ToLower(strings.ReplaceAll(filepath.Base(directory), "_", "-"))
	path := filepath.Join(directory, "kranz.yaml")
	document := "project: " + name + "\nservices:\n  api:\n    command: exit 0\n"
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	return kranzcli.GlobalOptions{Directory: directory, Output: kranzcli.OutputText}, name
}

func TestMCPOwnerEOFCleansSession(t *testing.T) {
	options, name := writeMCPProject(t)
	var stdout bytes.Buffer
	if err := runMCPContext(context.Background(), options, bytes.NewReader(nil), &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("owner wrote non-MCP startup output: %q", stdout.String())
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.ResolveForAttach(context.Background(), name, version)
	var notFound *kranzruntime.SessionNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("owner session survived EOF: %v", err)
	}
}

func TestMCPOwnerContextCancellationCleansSession(t *testing.T) {
	options, name := writeMCPProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runMCPContext(ctx, options, reader, io.Discard, io.Discard) }()
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		record, resolveErr := registry.ResolveForAttach(context.Background(), name, version)
		if resolveErr == nil && record.State == kranzruntime.SessionRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("owner did not publish: %v", resolveErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, err = registry.ResolveForAttach(context.Background(), name, version)
	var notFound *kranzruntime.SessionNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("owner session survived cancellation: %v", err)
	}
}

func TestMCPOwnerRemoteDownStopsBridgeAndRemovesSessionBeforeReporting(t *testing.T) {
	options, name := writeMCPProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	done := make(chan error, 1)
	go func() { done <- runMCPContext(ctx, options, reader, io.Discard, io.Discard) }()
	record := waitForMCPRuntime(t, name)

	var stdout bytes.Buffer
	downOptions := kranzcli.GlobalOptions{Project: name, Output: kranzcli.OutputText}
	if err := runDown(downOptions, nil, &stdout); err != nil {
		t.Fatalf("down: %v", err)
	}
	if want := "Stopped " + name + " (" + shortID(record.ID) + ").\n"; stdout.String() != want {
		t.Fatalf("down output = %q, want %q", stdout.String(), want)
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.ResolveForAttach(context.Background(), name, version)
	var missing *kranzruntime.SessionNotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("down reported success before session disappeared: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("MCP owner exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MCP owner remained alive after remote down")
	}
}

func TestSecondMCPClientInitializesAgainstOwnerAndDisconnectsOnDown(t *testing.T) {
	options, name := writeMCPProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ownerReader, ownerWriter := io.Pipe()
	defer func() { _ = ownerWriter.Close() }()
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- runMCPContext(ctx, options, ownerReader, io.Discard, io.Discard) }()
	ownerRecord := waitForMCPRuntime(t, name)

	attachedReader, attachedWriter := io.Pipe()
	defer func() { _ = attachedWriter.Close() }()
	responseReader, responseWriter := io.Pipe()
	defer func() { _ = responseReader.Close() }()
	attachedDone := make(chan error, 1)
	go func() {
		if err := runMCPContext(context.Background(), options, attachedReader, responseWriter, io.Discard); err != nil {
			attachedDone <- err
			return
		}
		restartedClient, _, restartedMode, restartedOwner, err := connectMCP(options)
		if err != nil {
			attachedDone <- err
			return
		}
		if restartedMode != "owner" || restartedOwner == nil {
			_ = restartedClient.Close()
			attachedDone <- errors.New("restarted MCP did not acquire a fresh runtime after down")
			return
		}
		shutdownErr := restartedClient.Shutdown()
		closeErr := restartedOwner.Close()
		if err := errors.Join(shutdownErr, closeErr); err != nil {
			attachedDone <- err
			return
		}
		attachedDone <- nil
	}()
	if _, err := io.WriteString(attachedWriter, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	var response struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	responseDone := make(chan error, 1)
	go func() { responseDone <- json.NewDecoder(responseReader).Decode(&response) }()
	select {
	case err := <-responseDone:
		if err != nil {
			t.Fatalf("initialize response: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attached MCP did not answer initialize")
	}
	if response.JSONRPC != "2.0" || response.ID != 1 || response.Result.ProtocolVersion != "2025-11-25" || response.Result.ServerInfo.Name != "kranz" {
		t.Fatalf("initialize response = %#v", response)
	}
	if current := waitForMCPRuntime(t, name); current.ID != ownerRecord.ID {
		t.Fatalf("attached MCP created a second runtime: owner=%s current=%s", ownerRecord.ID, current.ID)
	}

	if err := runDown(kranzcli.GlobalOptions{Project: name, Output: kranzcli.OutputText}, nil, io.Discard); err != nil {
		t.Fatalf("down: %v", err)
	}
	select {
	case err := <-ownerDone:
		if err != nil {
			t.Fatalf("owner exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owner did not exit after down")
	}
	select {
	case err := <-attachedDone:
		if err != nil {
			t.Fatalf("attached exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attached MCP remained alive after owner down")
	}
}

func TestMCPSubprocessCompletesCodexStyleInitializeAndRemoteDown(t *testing.T) {
	options, name := writeMCPProject(t)
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
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	childExited := false
	defer func() {
		_ = stdin.Close()
		if !childExited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	if _, err := io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	var response struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	responseDone := make(chan error, 1)
	go func() { responseDone <- json.NewDecoder(stdout).Decode(&response) }()
	select {
	case err := <-responseDone:
		if err != nil {
			t.Fatalf("initialize response: %v; stderr=%s", err, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("MCP subprocess did not answer initialize; stderr=%s", stderr.String())
	}
	if response.JSONRPC != "2.0" || response.ID != 1 || response.Result.ProtocolVersion != "2025-06-18" || response.Result.ServerInfo.Name != "kranz" {
		t.Fatalf("initialize response = %#v", response)
	}
	waitForMCPRuntime(t, name)

	if err := runDown(kranzcli.GlobalOptions{Project: name, Output: kranzcli.OutputText}, nil, io.Discard); err != nil {
		t.Fatalf("down: %v; stderr=%s", err, stderr.String())
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	select {
	case err := <-waitDone:
		childExited = true
		if err != nil {
			t.Fatalf("MCP subprocess exit: %v; stderr=%s", err, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("MCP subprocess remained alive after down; stderr=%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected MCP diagnostics: %s", stderr.String())
	}
}

func TestMCPRefusesNonRunningRegistryEvidence(t *testing.T) {
	for _, state := range []kranzruntime.SessionState{kranzruntime.SessionStale, kranzruntime.SessionUnreachable, kranzruntime.SessionIncompatible} {
		client, _, _, owner, err := attachMCPRecord("demo", kranzruntime.SessionRecord{State: state})
		if client != nil || owner != nil || err == nil {
			t.Fatalf("state=%s client=%v owner=%v err=%v", state, client, owner, err)
		}
	}
}

func TestMCPFallsBackToOwnerThenAttachesWithoutSecondSupervisor(t *testing.T) {
	options, _ := writeMCPProject(t)
	ownerClient, ownerMetadata, mode, owner, err := connectMCP(options)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "owner" || owner == nil {
		t.Fatalf("mode=%q owner=%v", mode, owner)
	}
	defer func() { _ = ownerClient.Shutdown(); _ = owner.Close() }()

	attachedClient, attachedMetadata, attachedMode, secondOwner, err := connectMCP(options)
	if err != nil {
		t.Fatal(err)
	}
	if attachedMode != "attached" || secondOwner != nil {
		t.Fatalf("mode=%q owner=%v", attachedMode, secondOwner)
	}
	if attachedMetadata.ID != ownerMetadata.ID {
		t.Fatalf("second supervisor: owner=%s attached=%s", ownerMetadata.ID, attachedMetadata.ID)
	}
	if err := attachedClient.Close(); err != nil {
		t.Fatal(err)
	}
	if project := ownerClient.Project(); project.Name == "" {
		t.Fatal("attached disconnect stopped owner runtime")
	}
}

func TestMCPAttachesToForegroundRuntimeAndLeavesItRunning(t *testing.T) {
	options, _ := writeMCPProject(t)
	host, _, err := startRuntime(options, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.client.Shutdown(); _ = host.Close() }()

	client, metadata, mode, owner, err := connectMCP(options)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "attached" || owner != nil || metadata.ID != host.session.Metadata().ID {
		t.Fatalf("unexpected attachment: %q %#v", mode, metadata)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if got := host.client.Project().Name; got == "" {
		t.Fatal("foreground runtime stopped after MCP disconnect")
	}
}

func TestMCPAttachOnlyRefusesPhantomRuntimeAndListsRunningAlternatives(t *testing.T) {
	runningOptions, runningName := writeMCPProject(t)
	host, _, err := startRuntime(runningOptions, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.client.Shutdown(); _ = host.Close() }()

	missingOptions, missingName := writeMCPProject(t)
	client, _, _, owner, err := connectMCP(missingOptions, true)
	if client != nil || owner != nil || err == nil {
		t.Fatalf("client=%v owner=%v err=%v", client, owner, err)
	}
	if !strings.Contains(err.Error(), `runtime "`+missingName+`" is not running`) || !strings.Contains(err.Error(), "--attach-only") || !strings.Contains(err.Error(), runningName) {
		t.Fatalf("attach-only error = %v", err)
	}
	registry, registryErr := kranzruntime.DefaultRegistry()
	if registryErr != nil {
		t.Fatal(registryErr)
	}
	_, resolveErr := registry.ResolveForAttach(context.Background(), missingName, version)
	var notFound *kranzruntime.SessionNotFoundError
	if !errors.As(resolveErr, &notFound) {
		t.Fatalf("attach-only created a phantom runtime: %v", resolveErr)
	}
}

func TestMCPExplicitMissingProjectNeverCreatesOwner(t *testing.T) {
	options, _ := writeMCPProject(t)
	options.Project = "definitely-missing-mcp-runtime"
	client, _, _, owner, err := connectMCP(options)
	if client != nil || owner != nil || err == nil {
		t.Fatalf("client=%v owner=%v err=%v", client, owner, err)
	}
}

func TestMCPUsageErrorsNeverWriteStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"mcp", "unexpected"}, &stdout, &stderr); code == 0 {
		t.Fatal("invalid invocation succeeded")
	}
	if stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
