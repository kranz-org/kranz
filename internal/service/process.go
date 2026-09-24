// Package service manages process lifecycles, dependency ordering, and recovery.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kranz-org/kranz/pkg/ringbuffer"
)

// ProcessManager owns one child process and its bounded stdout/stderr buffers.
type ProcessManager struct {
	mu               sync.RWMutex
	stopMu           sync.Mutex
	outputMu         sync.Mutex
	cmd              *exec.Cmd
	stdout           *ringbuffer.RingBuffer
	stderr           *ringbuffer.RingBuffer
	output           []CapturedOutput
	outputSeq        uint64
	outputStart      int
	outputBytes      uint64
	outputDropped    uint64
	outputMaxEntries int
	outputMaxBytes   uint64
	stdoutPending    string
	stderrPending    string
	waitDone         chan struct{}
	waitErr          error
}

// CapturedOutput is one completed line or bounded fragment, ordered when Kranz
// receives it. Consumers preserve Sequence across stdout and stderr.
type CapturedOutput struct {
	Sequence   uint64
	CapturedAt time.Time
	Source     string
	Text       string
}

const maxPendingOutputBytes = 64 * 1024

type processOutputWriter struct {
	process *ProcessManager
	buffer  *ringbuffer.RingBuffer
	source  string
}

func (w processOutputWriter) Write(data []byte) (int, error) {
	text := string(data)
	w.process.captureOutput(w.buffer, w.source, text)
	return len(data), nil
}

func (pm *ProcessManager) captureOutput(buffer *ringbuffer.RingBuffer, source, text string) {
	pm.outputMu.Lock()
	defer pm.outputMu.Unlock()
	if text == "" {
		return
	}
	// Pipe writes need not end at line boundaries. Retain each source's suffix
	// until its newline arrives, while sequencing completed lines together.
	pending := &pm.stdoutPending
	if source == "stderr" {
		pending = &pm.stderrPending
	}
	text = *pending + text
	for end := strings.IndexByte(text, '\n'); end >= 0; end = strings.IndexByte(text, '\n') {
		pm.recordOutputLocked(buffer, source, text[:end+1])
		text = text[end+1:]
	}
	// A process can stream forever without a newline. Publish bounded fragments
	// so the unfinished suffix cannot consume memory without limit.
	for len(text) > maxPendingOutputBytes {
		pm.recordOutputLocked(buffer, source, text[:maxPendingOutputBytes])
		text = text[maxPendingOutputBytes:]
	}
	*pending = text
}

func (pm *ProcessManager) recordOutputLocked(buffer *ringbuffer.RingBuffer, source, text string) {
	buffer.Write(text)
	pm.outputSeq++
	pm.output = append(pm.output, CapturedOutput{
		Sequence: pm.outputSeq, CapturedAt: time.Now(), Source: source, Text: text,
	})
	pm.outputBytes += uint64(len(text))
	for len(pm.output)-pm.outputStart > pm.outputMaxEntries || pm.outputBytes > pm.outputMaxBytes {
		oldest := &pm.output[pm.outputStart]
		pm.outputBytes -= uint64(len(oldest.Text))
		*oldest = CapturedOutput{}
		pm.outputStart++
		pm.outputDropped++
	}
	if pm.outputStart > len(pm.output)/2 {
		copy(pm.output, pm.output[pm.outputStart:])
		pm.output = pm.output[:len(pm.output)-pm.outputStart]
		pm.outputStart = 0
	}
}

func (pm *ProcessManager) flushCapturedOutput() {
	pm.outputMu.Lock()
	defer pm.outputMu.Unlock()
	if pm.stdoutPending != "" {
		pm.recordOutputLocked(pm.stdout, "stdout", pm.stdoutPending)
		pm.stdoutPending = ""
	}
	if pm.stderrPending != "" {
		pm.recordOutputLocked(pm.stderr, "stderr", pm.stderrPending)
		pm.stderrPending = ""
	}
}

// pendingOutput exposes unfinished lines for readiness checks without adding
// fragments to the public log before their line boundary arrives.
func (pm *ProcessManager) pendingOutput() (stdout, stderr string) {
	pm.outputMu.Lock()
	defer pm.outputMu.Unlock()
	return pm.stdoutPending, pm.stderrPending
}

// DrainCapturedOutput returns completed lines and bounded fragments in capture
// order. The per-source buffers remain intact for action result snapshots.
func (pm *ProcessManager) DrainCapturedOutput() []CapturedOutput {
	pm.outputMu.Lock()
	defer pm.outputMu.Unlock()
	entries := append([]CapturedOutput(nil), pm.output[pm.outputStart:]...)
	if pm.outputDropped > 0 {
		marker := CapturedOutput{CapturedAt: time.Now(), Source: "kranz",
			Text: fmt.Sprintf("[Kranz] %d captured lines omitted due to output backlog", pm.outputDropped)}
		if len(entries) > 0 {
			marker.Sequence = entries[0].Sequence - 1
		}
		entries = append([]CapturedOutput{marker}, entries...)
	}
	pm.output = nil
	pm.outputStart = 0
	pm.outputBytes = 0
	pm.outputDropped = 0
	return entries
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
	if logBufSize <= 0 {
		logBufSize = 1000
	}
	return &ProcessManager{
		stdout:           ringbuffer.New(logBufSize),
		stderr:           ringbuffer.New(logBufSize),
		outputMaxEntries: max(logBufSize, defaultLogBufferSize),
		outputMaxBytes:   defaultLogBufferBytes,
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
	return pm.launch(cmd, env)
}

// StartArgv launches an argument vector directly, without a shell. The first
// element is the executable and every element stays one process argument, so a
// parameter value can never become shell syntax.
func (pm *ProcessManager) StartArgv(ctx context.Context, argv []string, dir string, env map[string]string) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("argv action requires at least an executable")
	}
	pm.stopMu.Lock()
	defer pm.stopMu.Unlock()

	pm.mu.RLock()
	alreadyStarted := pm.cmd != nil
	pm.mu.RUnlock()
	if alreadyStarted {
		return 0, errors.New("process manager cannot be started more than once")
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	return pm.launch(cmd, env)
}

// launch applies the shared process-group, environment, capture, and reaping
// setup to an already-built command.
func (pm *ProcessManager) launch(cmd *exec.Cmd, env map[string]string) (int, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Explicit service variables override the inherited host environment.
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Non-file writers make os/exec own the stream-copy goroutines. Wait then
	// cannot complete until both streams have been copied into their buffers.
	cmd.Stdout = processOutputWriter{process: pm, buffer: pm.stdout, source: "stdout"}
	cmd.Stderr = processOutputWriter{process: pm, buffer: pm.stderr, source: "stderr"}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start process: %w", err)
	}

	waitDone := make(chan struct{})
	pm.mu.Lock()
	pm.cmd = cmd
	pm.waitDone = waitDone
	pm.waitErr = nil
	pm.mu.Unlock()

	// reap owns the only Wait call.
	go pm.reap(cmd, waitDone)

	return cmd.Process.Pid, nil
}

// reap owns the only exec.Cmd.Wait call and always releases the OS process handle.
func (pm *ProcessManager) reap(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	pm.flushCapturedOutput()
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
	// exec.Cmd.Wait writes ProcessState before reap closes waitDone. Reading it
	// while the process is still being reaped races inside os/exec even though
	// our pointer is protected. The close is both the completion signal and the
	// memory barrier that makes ProcessState safe to inspect.
	if pm.cmd == nil || pm.waitDone == nil || !channelClosed(pm.waitDone) || pm.cmd.ProcessState == nil {
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
