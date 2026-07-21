// Package service manages process lifecycles, dependency ordering, and recovery.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/kranz-org/kranz/pkg/ringbuffer"
)

// ProcessManager owns one child process and its bounded stdout/stderr buffers.
type ProcessManager struct {
	mu       sync.RWMutex
	stopMu   sync.Mutex
	cmd      *exec.Cmd
	stdout   *ringbuffer.RingBuffer
	stderr   *ringbuffer.RingBuffer
	waitDone chan struct{}
	waitErr  error
	pipeWG   sync.WaitGroup
}

// StopOptions customizes graceful shutdown for one process.
type StopOptions struct {
	Command    string
	Timeout    time.Duration
	Signal     syscall.Signal
	ParentOnly bool
	Dir        string
	Env        map[string]string
	Shell      string
}

// NewProcessManager creates a stopped process manager with bounded log buffers.
func NewProcessManager(logBufSize int) *ProcessManager {
	return &ProcessManager{
		stdout: ringbuffer.New(logBufSize),
		stderr: ringbuffer.New(logBufSize),
	}
}

// Start launches a command in its own process group so shutdown can include all
// descendants. An empty shell selects sh.
func (pm *ProcessManager) Start(ctx context.Context, command, dir string, env map[string]string, shell string) (int, error) {
	pm.stopMu.Lock()
	defer pm.stopMu.Unlock()

	pm.mu.RLock()
	alreadyStarted := pm.cmd != nil
	pm.mu.RUnlock()
	if alreadyStarted {
		return 0, errors.New("process manager cannot be started more than once")
	}

	if shell == "" {
		shell = "sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Explicit service variables override the inherited host environment.
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Keep streams independent so a blocked reader cannot stall the other pipe.
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return 0, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start process: %w", err)
	}

	waitDone := make(chan struct{})
	pm.mu.Lock()
	pm.cmd = cmd
	pm.waitDone = waitDone
	pm.waitErr = nil
	pm.mu.Unlock()

	// stdout and stderr are consumed independently; reap owns the only Wait call.
	pm.pipeWG.Add(2)
	go func() {
		defer pm.pipeWG.Done()
		pm.readPipe(stdoutPipe, pm.stdout)
	}()
	go func() {
		defer pm.pipeWG.Done()
		pm.readPipe(stderrPipe, pm.stderr)
	}()
	go pm.reap(cmd, waitDone)

	return cmd.Process.Pid, nil
}

// readPipe copies complete lines from one child stream into a bounded buffer.
func (pm *ProcessManager) readPipe(r io.Reader, buf *ringbuffer.RingBuffer) {
	data := make([]byte, 8192)
	for {
		n, err := r.Read(data)
		if n > 0 {
			buf.Write(string(data[:n]))
		}
		if err != nil {
			return
		}
	}
}

// reap owns the only exec.Cmd.Wait call and always releases the OS process handle.
func (pm *ProcessManager) reap(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	pm.pipeWG.Wait()
	pm.mu.Lock()
	if pm.cmd == cmd {
		pm.waitErr = err
	}
	close(done)
	pm.mu.Unlock()
}

// Stop applies the configured graceful shutdown policy to the whole process group.
func (pm *ProcessManager) Stop() error {
	return pm.StopWithOptions(StopOptions{})
}

// StopWithOptions applies a custom shutdown command or signal and escalates to
// SIGKILL when the configured grace period expires.
func (pm *ProcessManager) StopWithOptions(options StopOptions) error {
	pm.stopMu.Lock()
	defer pm.stopMu.Unlock()

	pm.mu.RLock()
	cmd := pm.cmd
	done := pm.waitDone
	pm.mu.RUnlock()
	if cmd == nil || cmd.Process == nil || done == nil {
		return nil
	}

	if channelClosed(done) {
		return nil
	}

	pid := cmd.Process.Pid
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if options.Command != "" {
		commandErr := runShutdownCommand(options, timeout)
		if commandErr != nil {
			if channelClosed(done) {
				return commandErr
			}
			return errors.Join(commandErr, killProcess(pid, done))
		}
		if waitForDone(done, timeout) {
			return nil
		}
		return killProcess(pid, done)
	}

	signal := options.Signal
	if signal == 0 {
		signal = syscall.SIGTERM
	}
	targetPID := -pid
	if options.ParentOnly {
		targetPID = pid
	}
	if err := syscall.Kill(targetPID, signal); err != nil {
		if !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("send signal %d to PID %d: %w", signal, targetPID, err)
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return killProcess(pid, done)
	}
}

func runShutdownCommand(options StopOptions, timeout time.Duration) error {
	shell := options.Shell
	if shell == "" {
		shell = "sh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-c", options.Command)
	cmd.Dir = options.Dir
	cmd.Env = os.Environ()
	for key, value := range options.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("shutdown command: %w", err)
	}
	return nil
}

func waitForDone(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func killProcess(pid int, done <-chan struct{}) error {
	targetPID := -pid
	if err := syscall.Kill(targetPID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("send SIGKILL to PID %d: %w", targetPID, err)
	}
	<-done
	return nil
}

// Stdout returns the bounded standard-output buffer.
func (pm *ProcessManager) Stdout() *ringbuffer.RingBuffer {
	return pm.stdout
}

// Stderr returns the bounded standard-error buffer.
func (pm *ProcessManager) Stderr() *ringbuffer.RingBuffer {
	return pm.stderr
}

// IsRunning reports whether a child process is currently owned.
func (pm *ProcessManager) IsRunning() bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if pm.cmd == nil || pm.cmd.Process == nil || pm.waitDone == nil {
		return false
	}
	return !channelClosed(pm.waitDone)
}

// PID returns the child PID, or zero while stopped.
func (pm *ProcessManager) PID() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if pm.cmd == nil || pm.cmd.Process == nil || pm.waitDone == nil || channelClosed(pm.waitDone) {
		return 0
	}
	return pm.cmd.Process.Pid
}

// Done closes after the child has exited and been reaped.
func (pm *ProcessManager) Done() <-chan struct{} {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.waitDone
}

// Wait blocks until the child exits and returns its normalized result.
func (pm *ProcessManager) Wait() error {
	pm.mu.RLock()
	done := pm.waitDone
	pm.mu.RUnlock()
	if done == nil {
		return nil
	}
	<-done
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.waitErr
}

// ExitCode returns the conventional exit status, including 128+signal for
// processes terminated by a Unix signal. It returns -1 before process exit.
func (pm *ProcessManager) ExitCode() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if pm.cmd == nil || pm.cmd.ProcessState == nil {
		return -1
	}
	if status, ok := pm.cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return pm.cmd.ProcessState.ExitCode()
}

func channelClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
