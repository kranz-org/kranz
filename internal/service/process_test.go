package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestProcessManagerReapsNaturalExitExactlyOnce(t *testing.T) {
	pm := NewProcessManager(32)
	if _, err := pm.Start(context.Background(), "exit 0", ".", nil, "sh"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if err := pm.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if err := pm.Wait(); err != nil {
		t.Fatalf("second Wait() error = %v", err)
	}
	if pm.IsRunning() {
		t.Fatal("process is still reported as running")
	}
	if err := pm.Stop(); err != nil {
		t.Fatalf("Stop() after natural exit error = %v", err)
	}
}

func TestProcessManagerWaitIncludesShortLivedOutput(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		pm := NewProcessManager(32)
		if _, err := pm.Start(context.Background(), "printf ready", ".", nil, "sh"); err != nil {
			t.Fatalf("Start() iteration %d error = %v", iteration, err)
		}
		if err := pm.Wait(); err != nil {
			t.Fatalf("Wait() iteration %d error = %v", iteration, err)
		}
		if output := strings.Join(pm.Stdout().Lines(), ""); output != "ready" {
			t.Fatalf("stdout iteration %d = %q, want ready", iteration, output)
		}
	}
}

func TestProcessManagerCapturesStdoutAndStderrInOneSequence(t *testing.T) {
	pm := NewProcessManager(32)
	stdout := processOutputWriter{process: pm, buffer: pm.stdout, source: "stdout"}
	stderr := processOutputWriter{process: pm, buffer: pm.stderr, source: "stderr"}
	_, _ = stdout.Write([]byte("out one\n"))
	_, _ = stderr.Write([]byte("err one\n"))
	_, _ = stdout.Write([]byte("out two\n"))

	entries := pm.DrainCapturedOutput()
	if len(entries) != 3 {
		t.Fatalf("captured entries = %#v", entries)
	}
	wantSources := []string{"stdout", "stderr", "stdout"}
	for index, entry := range entries {
		if entry.Sequence != uint64(index+1) || entry.Source != wantSources[index] || entry.CapturedAt.IsZero() {
			t.Fatalf("entry %d = %#v", index, entry)
		}
	}
	if remaining := pm.DrainCapturedOutput(); len(remaining) != 0 {
		t.Fatalf("second drain = %#v", remaining)
	}
	if got := strings.Join(pm.Stdout().Lines(), ""); got != "out one\nout two\n" {
		t.Fatalf("stdout snapshot = %q", got)
	}
}

func TestProcessManagerReassemblesLinesAcrossPipeWrites(t *testing.T) {
	pm := NewProcessManager(8)
	stdout := processOutputWriter{process: pm, buffer: pm.stdout, source: "stdout"}
	stderr := processOutputWriter{process: pm, buffer: pm.stderr, source: "stderr"}
	_, _ = stdout.Write([]byte("hel"))
	_, _ = stderr.Write([]byte("err"))
	if entries := pm.DrainCapturedOutput(); len(entries) != 0 {
		t.Fatalf("incomplete lines were published: %#v", entries)
	}
	_, _ = stdout.Write([]byte("lo\nnext"))
	_, _ = stderr.Write([]byte("or\n"))
	pm.flushCapturedOutput()
	entries := pm.DrainCapturedOutput()
	if len(entries) != 3 || entries[0].Text != "hello\n" || entries[1].Text != "error\n" || entries[2].Text != "next" {
		t.Fatalf("reassembled lines = %#v", entries)
	}
	if got := strings.Join(pm.Stdout().Lines(), ""); got != "hello\nnext" {
		t.Fatalf("stdout snapshot = %q", got)
	}
}

func TestProcessManagerBoundsOutputWithoutNewlines(t *testing.T) {
	pm := NewProcessManager(8)
	stdout := processOutputWriter{process: pm, buffer: pm.stdout, source: "stdout"}
	_, _ = stdout.Write([]byte(strings.Repeat("x", maxPendingOutputBytes+7)))
	if len(pm.stdoutPending) > maxPendingOutputBytes {
		t.Fatalf("pending bytes = %d", len(pm.stdoutPending))
	}
	entries := pm.DrainCapturedOutput()
	if len(entries) != 1 || len(entries[0].Text) != maxPendingOutputBytes {
		t.Fatalf("bounded fragment = %#v", entries)
	}
	_, _ = stdout.Write([]byte("tail\n"))
	entries = pm.DrainCapturedOutput()
	if len(entries) != 1 || entries[0].Text != "xxxxxxxtail\n" {
		t.Fatalf("remaining line = %#v", entries)
	}
}

func TestProcessManagerBoundsCapturedOutputBacklog(t *testing.T) {
	pm := NewProcessManager(3)
	pm.outputMaxEntries = 3
	pm.outputMaxBytes = 32
	stdout := processOutputWriter{process: pm, buffer: pm.stdout, source: "stdout"}
	for index := range 10 {
		_, _ = fmt.Fprintf(stdout, "line-%d\n", index)
	}
	entries := pm.DrainCapturedOutput()
	if len(entries) != 4 || entries[0].Source != "kranz" || !strings.Contains(entries[0].Text, "7 captured lines omitted") {
		t.Fatalf("bounded backlog = %#v", entries)
	}
	if entries[1].Text != "line-7\n" || entries[3].Text != "line-9\n" {
		t.Fatalf("recent output was lost: %#v", entries)
	}
	pm = NewProcessManager(10)
	pm.outputMaxBytes = 8
	stdout = processOutputWriter{process: pm, buffer: pm.stdout, source: "stdout"}
	_, _ = stdout.Write([]byte("hello\nworld\n"))
	entries = pm.DrainCapturedOutput()
	if len(entries) != 2 || entries[1].Text != "world\n" {
		t.Fatalf("byte-bounded backlog = %#v", entries)
	}
}

func TestProcessManagerUsesUnixSignalExitConvention(t *testing.T) {
	pm := NewProcessManager(32)
	if _, err := pm.Start(context.Background(), "kill -TERM $$", ".", nil, "sh"); err != nil {
		t.Fatal(err)
	}
	_ = pm.Wait()
	if code := pm.ExitCode(); code != 143 {
		t.Fatalf("signal exit code = %d, want 143", code)
	}
}

func TestProcessManagerExitCodeStaysUnknownUntilReaped(t *testing.T) {
	pm := NewProcessManager(10)
	if _, err := pm.Start(t.Context(), "sleep 0.1", "", nil, "/bin/sh"); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if code := pm.ExitCode(); code != -1 {
			t.Fatalf("ExitCode() while running = %d, want -1", code)
		}
	}
	if err := pm.Wait(); err != nil {
		t.Fatal(err)
	}
	if code := pm.ExitCode(); code != 0 {
		t.Fatalf("ExitCode() after reap = %d, want 0", code)
	}
}

func TestShutdownCommandFailureStillKillsManagedProcess(t *testing.T) {
	pm := NewProcessManager(32)
	if _, err := pm.Start(context.Background(), "while :; do sleep 1; done", ".", nil, "sh"); err != nil {
		t.Fatal(err)
	}
	err := pm.StopWithOptions(StopOptions{Command: "exit 9", Timeout: 100 * time.Millisecond})
	if err == nil {
		t.Fatal("failed shutdown command did not return an error")
	}
	select {
	case <-pm.Done():
	case <-time.After(time.Second):
		t.Fatal("managed process survived a failed shutdown command")
	}
	if pm.IsRunning() || pm.PID() != 0 {
		t.Fatalf("stale process after shutdown error: PID=%d running=%v", pm.PID(), pm.IsRunning())
	}
}

func TestConfiguredShutdownSignalIsDelivered(t *testing.T) {
	pm := NewProcessManager(32)
	directory := t.TempDir()
	marker := directory + "/stopped"
	command := `trap 'printf stopped > "$MARKER"; exit 0' USR1; while :; do sleep 0.05; done`
	if _, err := pm.Start(context.Background(), command, ".", map[string]string{"MARKER": marker}, "sh"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := pm.StopWithOptions(StopOptions{Signal: syscall.SIGUSR1, Timeout: 2 * time.Second, ParentOnly: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if string(data) != "stopped" {
		t.Fatalf("signal trap marker = %q", data)
	}
}

func TestProcessManagerConcurrentStopIsIdempotent(t *testing.T) {
	pm := NewProcessManager(32)
	if _, err := pm.Start(context.Background(), "while :; do sleep 1; done", ".", nil, "sh"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	const callers = 6
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- pm.Stop()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	}

	select {
	case <-pm.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process was not reaped after Stop")
	}
	if pm.PID() != 0 || pm.IsRunning() {
		t.Fatalf("stale process state: PID=%d running=%v", pm.PID(), pm.IsRunning())
	}
}

func TestStartArgvKeepsEveryElementOneArgument(t *testing.T) {
	pm := NewProcessManager(32)
	// Each element carries syntax a shell would act on. Reaching echo intact
	// is what proves no shell sat between the vector and the process.
	argv := []string{"/bin/echo", "a b", "*", "$HOME", "; id", "|", "&&", `"quoted"`}
	if _, err := pm.StartArgv(context.Background(), argv, ".", nil); err != nil {
		t.Fatalf("StartArgv() error = %v", err)
	}
	if err := pm.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	want := strings.Join(argv[1:], " ") + "\n"
	if got := strings.Join(pm.Stdout().Lines(), ""); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestStartArgvRequiresAnExecutable(t *testing.T) {
	pm := NewProcessManager(32)
	if _, err := pm.StartArgv(context.Background(), nil, ".", nil); err == nil {
		t.Fatal("an empty vector started a process")
	}
}
