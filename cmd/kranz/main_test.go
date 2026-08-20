package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

func TestVersionTextAndJSON(t *testing.T) {
	previousVersion, previousCommit, previousBuildTime := version, commit, buildTime
	version, commit, buildTime = "v1.2.3", "abc123", "2026-07-21T00:00:00Z"
	defer func() { version, commit, buildTime = previousVersion, previousCommit, previousBuildTime }()

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "kranz 1.2.3 (commit abc123, built 2026-07-21T00:00:00Z)\n" {
		t.Fatalf("version = %q", stdout.String())
	}

	stdout.Reset()
	if code := execute([]string{"version", "--output=json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	data := envelope["data"].(map[string]any)
	if data["version"] != "1.2.3" || data["commit"] != "abc123" {
		t.Fatalf("version envelope = %#v", envelope)
	}
}

func TestForegroundHelperProcess(t *testing.T) {
	if os.Getenv("KRANZ_TEST_FOREGROUND_HELPER") != "1" {
		return
	}
	directory := os.Getenv("KRANZ_TEST_PROJECT_DIR")
	os.Exit(execute([]string{"-C", directory, "up", "--no-start"}, os.Stdout, os.Stderr))
}

func TestBackgroundHelperProcess(t *testing.T) {
	if os.Getenv("KRANZ_TEST_BACKGROUND_HELPER") != "1" {
		return
	}
	data, err := base64.StdEncoding.DecodeString(os.Getenv("KRANZ_TEST_BACKGROUND_ARGS"))
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	os.Exit(execute(args, os.Stdout, os.Stderr))
}

func TestBackgroundRuntimeReadinessConflictAndDown(t *testing.T) {
	directory := t.TempDir()
	name := fmt.Sprintf("test-background-%d", os.Getpid())
	configText := fmt.Sprintf("project: Test Background\nruntime:\n  name: %s\nservices:\n  sleeper:\n    command: sleep 60\n    tags: [workers]\n", name)
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	previousFactory := newBackgroundCommand
	newBackgroundCommand = func(_ string, args ...string) *exec.Cmd {
		data, _ := json.Marshal(args)
		command := exec.Command(os.Args[0], "-test.run=^TestBackgroundHelperProcess$")
		command.Env = append(os.Environ(), "KRANZ_TEST_BACKGROUND_HELPER=1", "KRANZ_TEST_BACKGROUND_ARGS="+base64.StdEncoding.EncodeToString(data))
		return command
	}
	defer func() { newBackgroundCommand = previousFactory }()
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", directory, "up", "-d", "--no-start"}, &stdout, &stderr); code != 0 {
		t.Fatalf("up -d exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Started "+name) {
		t.Fatalf("readiness output = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-C", directory, "up", "-d", "--no-start"}, &stdout, &stderr); code != kranzcli.ExitConflict {
		t.Fatalf("duplicate exit=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-p", name, "status", "--output=json"}, &stdout, &stderr); code != 0 || !json.Valid(stdout.Bytes()) {
		t.Fatalf("status exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-p", name, "start", "workers"}, &stdout, &stderr); code != 0 {
		t.Fatalf("start tag exit=%d stderr=%s", code, stderr.String())
	}
	if code := execute([]string{"-p", name, "restart", "sleeper"}, &stdout, &stderr); code != 0 {
		t.Fatalf("restart exit=%d stderr=%s", code, stderr.String())
	}
	if code := execute([]string{"-p", name, "stop", "sleeper"}, &stdout, &stderr); code != 0 {
		t.Fatalf("stop exit=%d stderr=%s", code, stderr.String())
	}
	if code := execute([]string{"-p", name, "reload"}, &stdout, &stderr); code != 0 {
		t.Fatalf("reload exit=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-p", name, "down"}, &stdout, &stderr); code != 0 {
		t.Fatalf("down exit=%d stderr=%s", code, stderr.String())
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err = registry.Resolve(ctx, name, "test")
		cancel()
		var missing *kranzruntime.SessionNotFoundError
		if errors.As(err, &missing) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background runtime remained after down: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestForceDownRecoversUnreachableBackgroundRuntime(t *testing.T) {
	directory := t.TempDir()
	name := fmt.Sprintf("test-force-down-%d", os.Getpid())
	configText := fmt.Sprintf("project: Test Force Down\nruntime:\n  name: %s\nservices:\n  sleeper:\n    command: sleep 60\n", name)
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	previousFactory := newBackgroundCommand
	newBackgroundCommand = func(_ string, args ...string) *exec.Cmd {
		data, _ := json.Marshal(args)
		command := exec.Command(os.Args[0], "-test.run=^TestBackgroundHelperProcess$")
		command.Env = append(os.Environ(), "KRANZ_TEST_BACKGROUND_HELPER=1", "KRANZ_TEST_BACKGROUND_ARGS="+base64.StdEncoding.EncodeToString(data))
		return command
	}
	defer func() { newBackgroundCommand = previousFactory }()
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", directory, "up", "-d"}, &stdout, &stderr); code != 0 {
		t.Fatalf("up -d exit=%d stderr=%s", code, stderr.String())
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	record, err := registry.Resolve(ctx, name, "test")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(record.Socket); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-p", name, "down", "--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("down --force exit=%d stderr=%s", code, stderr.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err = registry.Resolve(ctx, name, "test")
		cancel()
		var missing *kranzruntime.SessionNotFoundError
		if errors.As(err, &missing) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("forced runtime remained registered: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLogsSnapshotFollowAndClientInterrupt(t *testing.T) {
	directory := t.TempDir()
	name := fmt.Sprintf("test-logs-%d", os.Getpid())
	configText := fmt.Sprintf("project: Test Logs\nruntime:\n  name: %s\nservices:\n  emitter:\n    command: echo out-line; echo err-line >&2\n", name)
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	previousFactory := newBackgroundCommand
	newBackgroundCommand = func(_ string, args ...string) *exec.Cmd {
		data, _ := json.Marshal(args)
		command := exec.Command(os.Args[0], "-test.run=^TestBackgroundHelperProcess$")
		command.Env = append(os.Environ(), "KRANZ_TEST_BACKGROUND_HELPER=1", "KRANZ_TEST_BACKGROUND_ARGS="+base64.StdEncoding.EncodeToString(data))
		return command
	}
	defer func() { newBackgroundCommand = previousFactory }()
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", directory, "up", "-d", "--no-start"}, &stdout, &stderr); code != 0 {
		t.Fatalf("up -d exit=%d stderr=%s", code, stderr.String())
	}
	t.Cleanup(func() { _ = execute([]string{"-p", name, "down"}, io.Discard, io.Discard) })
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-p", name, "start", "emitter"}, &stdout, &stderr); code != 0 {
		t.Fatalf("start exit=%d stderr=%s", code, stderr.String())
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	record, err := registry.Resolve(ctx, name, "test")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	client, err := kranzruntime.Dial(record.Socket, "test")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries := client.Logs("emitter")
		hasStdout, hasStderr := false, false
		for _, entry := range entries {
			hasStdout = hasStdout || entry.Source == "stdout"
			hasStderr = hasStderr || entry.Source == "stderr"
		}
		if hasStdout && hasStderr {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("process streams were not captured: %+v", entries)
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = client.Close()
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-p", name, "logs", "emitter"}, &stdout, &stderr); code != 0 {
		t.Fatalf("logs exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "[emitter/stdout]") || !strings.Contains(stdout.String(), "[emitter/stderr]") {
		t.Fatalf("text logs lost identity:\n%s", stdout.String())
	}
	stdout.Reset()
	if code := execute([]string{"-p", name, "logs", "emitter", "--since=1h", "--output=json"}, &stdout, &stderr); code != 0 || !json.Valid(stdout.Bytes()) {
		t.Fatalf("JSON logs exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	data, _ := json.Marshal([]string{"-p", name, "logs", "emitter", "--follow", "--tail=0"})
	follow := exec.Command(os.Args[0], "-test.run=^TestBackgroundHelperProcess$")
	follow.Env = append(os.Environ(), "KRANZ_TEST_BACKGROUND_HELPER=1", "KRANZ_TEST_BACKGROUND_ARGS="+base64.StdEncoding.EncodeToString(data))
	var followOut, followErr bytes.Buffer
	follow.Stdout, follow.Stderr = &followOut, &followErr
	if err := follow.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if code := execute([]string{"-p", name, "restart", "emitter"}, io.Discard, &stderr); code != 0 {
		t.Fatalf("restart during follow exit=%d stderr=%s", code, stderr.String())
	}
	time.Sleep(300 * time.Millisecond)
	if err := follow.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := follow.Wait(); err != nil {
		t.Fatalf("follow interrupted with %v; stderr=%s", err, followErr.String())
	}
	if !strings.Contains(followOut.String(), "[emitter/") {
		t.Fatalf("follow did not stream new events: %s", followOut.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-p", name, "status"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runtime stopped with logs client: exit=%d stderr=%s", code, stderr.String())
	}
	if code := execute([]string{"-p", name, "down"}, io.Discard, &stderr); code != 0 {
		t.Fatalf("down exit=%d stderr=%s", code, stderr.String())
	}
}

func TestForegroundRuntimeStatusAndRemoteDown(t *testing.T) {
	directory := t.TempDir()
	name := fmt.Sprintf("test-foreground-%d", os.Getpid())
	configText := fmt.Sprintf("project: Test Foreground\nruntime:\n  name: %s\nservices:\n  sleeper:\n    command: sleep 60\n    env:\n      TOKEN: top-secret-value\n", name)
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestForegroundHelperProcess$")
	command.Env = append(os.Environ(), "KRANZ_TEST_FOREGROUND_HELPER=1", "KRANZ_TEST_PROJECT_DIR="+directory)
	command.Stdout = io.Discard
	var childStderr bytes.Buffer
	command.Stderr = &childStderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
		}
	})

	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err = registry.Resolve(ctx, name, "test")
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime did not appear: %v; stderr=%s", err, childStderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-p", name, "status", "--output=json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status exit=%d stderr=%s", code, stderr.String())
	}
	if !json.Valid(stdout.Bytes()) || strings.Contains(stdout.String(), "top-secret-value") || strings.Contains(stdout.String(), "TOKEN") {
		t.Fatalf("unsafe status JSON: %s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-p", name, "down"}, &stdout, &stderr); code != 0 {
		t.Fatalf("down exit=%d stderr=%s", code, stderr.String())
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("foreground owner exit: %v; stderr=%s", err, childStderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("foreground owner did not exit after remote down")
	}
}

func TestForegroundSignalsPreserveSignalDeath(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			directory := t.TempDir()
			name := fmt.Sprintf("test-signal-%d-%d", os.Getpid(), sig)
			configText := fmt.Sprintf("project: Test Signal\nruntime:\n  name: %s\nservices:\n  sleeper:\n    command: sleep 60\n", name)
			if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(configText), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], "-test.run=^TestForegroundHelperProcess$")
			command.Env = append(os.Environ(), "KRANZ_TEST_FOREGROUND_HELPER=1", "KRANZ_TEST_PROJECT_DIR="+directory)
			command.Stdout, command.Stderr = io.Discard, io.Discard
			signal.Ignore(sig)
			if err := command.Start(); err != nil {
				signal.Reset(sig)
				t.Fatal(err)
			}
			signal.Reset(sig)
			t.Cleanup(func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
				}
			})
			registry, err := kranzruntime.DefaultRegistry()
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				_, err = registry.Resolve(ctx, name, "test")
				cancel()
				if err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("runtime did not appear: %v", err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := command.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err = command.Wait()
			exitError, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("Wait = %T %v, want signal exit", err, err)
			}
			status, ok := exitError.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != sig {
				t.Fatalf("wait status = %#v, want %s", exitError.Sys(), sig)
			}
		})
	}
}

func TestHelpUsesCommandTree(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"help", "action"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	for _, expected := range []string{"action", "list", "info", "run", "--project VALUE"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("help missing %q:\n%s", expected, stdout.String())
		}
	}
}

func TestPositionalYAMLPrintsMigrationHint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"prod.yaml"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d", code)
	}
	want := "Kranz: unknown command \"prod.yaml\".\nDid you mean `kranz -f prod.yaml`?\n"
	if stderr.String() != want || stdout.Len() != 0 {
		t.Fatalf("stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
}

func TestUsageErrorWithJSONKeepsStderrClean(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"--output=json", "unknown"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d", code)
	}
	if stderr.Len() != 0 || !json.Valid(stdout.Bytes()) {
		t.Fatalf("stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
}

// Lifecycle commands used to demand -p while status resolved the runtime from
// the working directory, so `kranz status` listed services that `kranz stop`
// then refused to touch. Every command now resolves the same way, and -p stays
// available to aim a command at a different project from this one's directory.
func TestLifecycleResolvesRuntimeFromDirectory(t *testing.T) {
	directory := t.TempDir()
	name := fmt.Sprintf("test-directory-%d", os.Getpid())
	configText := fmt.Sprintf("project: Test Directory\nruntime:\n  name: %s\nservices:\n  sleeper:\n    command: sleep 60\n    tags: [workers]\n", name)
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	// A second project directory that declares a runtime of its own, used to
	// prove -p still wins over whatever the working directory names.
	other := t.TempDir()
	otherText := fmt.Sprintf("project: Test Other\nruntime:\n  name: %s-other\nservices:\n  sleeper:\n    command: sleep 60\n", name)
	if err := os.WriteFile(filepath.Join(other, "kranz.yaml"), []byte(otherText), 0o600); err != nil {
		t.Fatal(err)
	}

	previousFactory := newBackgroundCommand
	newBackgroundCommand = func(_ string, args ...string) *exec.Cmd {
		data, _ := json.Marshal(args)
		command := exec.Command(os.Args[0], "-test.run=^TestBackgroundHelperProcess$")
		command.Env = append(os.Environ(), "KRANZ_TEST_BACKGROUND_HELPER=1", "KRANZ_TEST_BACKGROUND_ARGS="+base64.StdEncoding.EncodeToString(data))
		return command
	}
	defer func() { newBackgroundCommand = previousFactory }()

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", directory, "up", "-d", "--no-start"}, &stdout, &stderr); code != 0 {
		t.Fatalf("up -d exit=%d stderr=%s", code, stderr.String())
	}
	defer func() {
		var out, errOut bytes.Buffer
		_ = execute([]string{"-p", name, "down"}, &out, &errOut)
	}()

	for _, command := range [][]string{
		{"-C", directory, "status"},
		{"-C", directory, "start", "workers"},
		{"-C", directory, "restart", "sleeper"},
		{"-C", directory, "stop", "sleeper"},
		{"-C", directory, "reload"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := execute(command, &stdout, &stderr); code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", command, code, stderr.String())
		}
	}

	// From a directory owning a different project, -p still selects this one.
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-C", other, "-p", name, "status"}, &stdout, &stderr); code != 0 {
		t.Fatalf("-p override exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "sleeper") {
		t.Fatalf("-p override status = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-C", directory, "down"}, &stdout, &stderr); code != 0 {
		t.Fatalf("down exit=%d stderr=%s", code, stderr.String())
	}
}

// `kranz down SERVICE` answered "unknown down option SERVICE", calling a
// positional selector an option and saying nothing about the command that does
// what the user asked for.
func TestDownRejectsServiceSelectorWithStopHint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"down", "im-core"}, &stdout, &stderr); code != kranzcli.ExitUsage {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	output := stderr.String()
	if !strings.Contains(output, "does not take service selectors") {
		t.Errorf("down rejection does not explain itself: %q", output)
	}
	if !strings.Contains(output, "kranz stop im-core") {
		t.Errorf("down rejection does not point at stop: %q", output)
	}
}

// Without -p the runtime comes from the working directory, so a directory that
// is not a project has to say that rather than report a missing runtime.
func TestLifecycleOutsideAProjectExplainsHowToAim(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := execute([]string{"-C", t.TempDir(), "stop", "api"}, &stdout, &stderr)
	if code != kranzcli.ExitUsage {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no Kranz configuration was found") {
		t.Errorf("error does not name the real problem: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "-p NAME_OR_ID") {
		t.Errorf("error does not offer -p: %q", stderr.String())
	}
}
