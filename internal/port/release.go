package port

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// TerminateExternalPID stops the exact process selected by the user. It first
// sends SIGTERM, waits for a graceful exit, and uses SIGKILL only after the
// grace period. Kranz ownership is checked by the caller before this function.
func TerminateExternalPID(pid int, grace time.Duration) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to stop protected PID %d", pid)
	}
	if pid == os.Getpid() {
		return errors.New("refusing to stop the Kranz process")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find PID %d: %w", pid, err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return fmt.Errorf("send SIGTERM to PID %d: %w", pid, err)
	}

	deadline := time.Now().Add(max(0, grace))
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !processExists(pid) {
		return nil
	}
	if err := process.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("send SIGKILL to PID %d: %w", pid, err)
	}
	killDeadline := time.Now().Add(time.Second)
	for time.Now().Before(killDeadline) {
		if !processExists(pid) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	if processExists(pid) {
		return fmt.Errorf("PID %d did not exit after SIGKILL", pid)
	}
	return nil
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	return err == nil && process.Signal(syscall.Signal(0)) == nil
}
