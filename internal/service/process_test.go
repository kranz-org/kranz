package service

import (
	"context"
	"errors"
	"os"
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
