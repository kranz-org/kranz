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
