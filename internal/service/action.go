package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

const (
	defaultActionLogBuffer = 1000
	actionStopGracePeriod  = time.Second
)

var (
	ErrActionNotFound       = errors.New("action not found")
	ErrActionRunnerStopping = errors.New("action runner is shutting down")
	ErrInteractiveAction    = errors.New("interactive action requires terminal handoff")
)

// ActionStatus describes the lifecycle of a finishing command.
type ActionStatus uint8

const (
	ActionReady ActionStatus = iota
	ActionRunning
	ActionSucceeded
	ActionFailed
	ActionTimedOut
	ActionCancelled
)

// String returns the stable user-facing action state.
func (s ActionStatus) String() string {
	switch s {
	case ActionReady:
		return "ready"
	case ActionRunning:
		return "running"
	case ActionSucceeded:
		return "succeeded"
	case ActionFailed:
		return "failed"
	case ActionTimedOut:
		return "timed_out"
	case ActionCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// ActionResult is the concurrency-safe snapshot retained for one action.
type ActionResult struct {
	ID         config.ActionID
	Status     ActionStatus
	PID        int
	StartedAt  time.Time
	FinishedAt time.Time
	Duration   time.Duration
	ExitCode   int
	Error      string
	Stdout     []string
	Stderr     []string
}

// ActionBusyError identifies the action currently occupying an owner.
type ActionBusyError struct {
	Requested config.ActionID
	Running   config.ActionID
}

func (e *ActionBusyError) Error() string {
	return fmt.Sprintf("action owner is busy running %s/%s", e.Running.Owner, e.Running.Name)
}

// ActionExitError reports a finishing command's unsuccessful exit code.
type ActionExitError struct {
	ID       config.ActionID
	ExitCode int
}

func (e *ActionExitError) Error() string {
	return fmt.Sprintf("action %s/%s exited with code %d", e.ID.Owner, e.ID.Name, e.ExitCode)
}

type actionOwner struct {
	kind config.ActionOwnerKind
	name string
}

type activeAction struct {
	id      config.ActionID
	cancel  context.CancelFunc
	done    chan struct{}
	process *ProcessManager
}

// ActionRunner executes normalized non-interactive actions and retains their
// latest bounded result. It serializes actions per owner while allowing
// independent services and groups to run concurrently.
type ActionRunner struct {
	mu           sync.RWMutex
	cfg          *config.Config
	states       map[config.ActionID]ActionResult
	active       map[actionOwner]*activeAction
	logBufSize   int
	shuttingDown bool
}

// NewActionRunner creates an action runner for one loaded project config.
func NewActionRunner(cfg *config.Config, logBufSize int) *ActionRunner {
	if logBufSize <= 0 {
		logBufSize = defaultActionLogBuffer
	}
	return &ActionRunner{
		cfg:        cfg,
		states:     make(map[config.ActionID]ActionResult),
		active:     make(map[actionOwner]*activeAction),
		logBufSize: logBufSize,
	}
}

// ApplyConfig swaps the source of truth for future runs. Results survive only
// while the same action definition remains configured; active runs finish from
// the immutable definition with which they started.
func (r *ActionRunner) ApplyConfig(next *config.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.states {
		if r.isActiveLocked(id) {
			continue
		}
		currentAction, currentExists := r.cfg.ResolveAction(id)
		nextAction, nextExists := next.ResolveAction(id)
		if !currentExists || !nextExists || !reflect.DeepEqual(currentAction, nextAction) {
			delete(r.states, id)
		}
	}
	r.cfg = next
}

// Run executes one non-interactive action and blocks until it finishes.
func (r *ActionRunner) Run(ctx context.Context, id config.ActionID) (ActionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.shuttingDown {
		r.mu.Unlock()
		return ActionResult{}, ErrActionRunnerStopping
	}
	action, exists := r.cfg.ResolveAction(id)
	if !exists {
		r.mu.Unlock()
		return ActionResult{}, fmt.Errorf("%w: %s/%s", ErrActionNotFound, id.Owner, id.Name)
	}
	r.mu.Unlock()
	return r.RunDefinition(ctx, id, action)
}

// RunDefinition executes a normalized internal action definition. Lifecycle
// operations use reserved IDs while sharing owner serialization, cancellation,
// timeout handling, output capture, and process reaping with user actions.
func (r *ActionRunner) RunDefinition(ctx context.Context, id config.ActionID, action config.Action) (ActionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.shuttingDown {
		r.mu.Unlock()
		return ActionResult{}, ErrActionRunnerStopping
	}
	if action.InteractiveEnabled() {
		r.mu.Unlock()
		return ActionResult{}, ErrInteractiveAction
	}
	owner := actionOwner{kind: id.OwnerKind, name: id.Owner}
	if running, busy := r.active[owner]; busy {
		r.mu.Unlock()
		return ActionResult{}, &ActionBusyError{Requested: id, Running: running.id}
	}
	runCtx, cancel := context.WithCancel(ctx)
	active := &activeAction{id: id, cancel: cancel, done: make(chan struct{})}
	r.active[owner] = active
	started := time.Now()
	r.states[id] = ActionResult{ID: id, Status: ActionRunning, ExitCode: -1, StartedAt: started}
	r.mu.Unlock()

	result, runErr := r.execute(runCtx, id, action, started)
	cancel()
	r.mu.Lock()
	delete(r.active, owner)
	r.states[id] = cloneActionResult(result)
	close(active.done)
	r.mu.Unlock()
	return result, runErr
}

func (r *ActionRunner) execute(ctx context.Context, id config.ActionID, action config.Action, started time.Time) (ActionResult, error) {
	result := ActionResult{ID: id, Status: ActionFailed, ExitCode: -1, StartedAt: started}
	if err := ctx.Err(); err != nil {
		return finishActionResult(result, ActionCancelled, nil, err)
	}

	process := NewProcessManager(r.logBufSize)
	r.setActionProcess(id, process)
	pid, err := process.Start(context.Background(), action.Command, action.Dir, action.Env, action.Shell)
	if err != nil {
		return finishActionResult(result, ActionFailed, process, err)
	}
	result.PID = pid
	r.setActionPID(id, pid)

	waitCh := make(chan error, 1)
	go func() { waitCh <- process.Wait() }()
	var timer *time.Timer
	var timeout <-chan time.Time
	if action.Timeout > 0 {
		timer = time.NewTimer(action.Timeout)
		timeout = timer.C
		defer timer.Stop()
	}

	select {
	case waitErr := <-waitCh:
		if waitErr == nil && process.ExitCode() == 0 {
			return finishActionResult(result, ActionSucceeded, process, nil)
		}
		exitErr := &ActionExitError{ID: id, ExitCode: process.ExitCode()}
		return finishActionResult(result, ActionFailed, process, errors.Join(exitErr, waitErr))
	case <-ctx.Done():
		stopErr := process.StopWithOptions(StopOptions{Signal: syscall.SIGTERM, Timeout: actionStopGracePeriod})
		<-waitCh
		return finishActionResult(result, ActionCancelled, process, errors.Join(ctx.Err(), stopErr))
	case <-timeout:
		stopErr := process.StopWithOptions(StopOptions{Signal: syscall.SIGTERM, Timeout: actionStopGracePeriod})
		<-waitCh
		timeoutErr := fmt.Errorf("action timed out after %s: %w", action.Timeout, context.DeadlineExceeded)
		return finishActionResult(result, ActionTimedOut, process, errors.Join(timeoutErr, stopErr))
	}
}

func finishActionResult(result ActionResult, status ActionStatus, process *ProcessManager, runErr error) (ActionResult, error) {
	result.Status = status
	result.FinishedAt = time.Now()
	result.Duration = result.FinishedAt.Sub(result.StartedAt)
	if process != nil {
		result.ExitCode = process.ExitCode()
		result.Stdout = append([]string(nil), process.Stdout().Lines()...)
		result.Stderr = append([]string(nil), process.Stderr().Lines()...)
	}
	if runErr != nil {
		result.Error = runErr.Error()
	}
	return result, runErr
}

func (r *ActionRunner) setActionPID(id config.ActionID, pid int) {
	r.mu.Lock()
	state := r.states[id]
	state.PID = pid
	r.states[id] = state
	r.mu.Unlock()
}

func (r *ActionRunner) setActionProcess(id config.ActionID, process *ProcessManager) {
	r.mu.Lock()
	owner := actionOwner{kind: id.OwnerKind, name: id.Owner}
	if active, exists := r.active[owner]; exists && active.id == id {
		active.process = process
	}
	r.mu.Unlock()
}

// State returns the current or most recent result of a configured action.
func (r *ActionRunner) State(id config.ActionID) (ActionResult, bool) {
	r.mu.RLock()
	state, stateExists := r.states[id]
	actionExists := r.actionExistsLocked(id)
	active := r.activeActionLocked(id)
	var process *ProcessManager
	if active != nil {
		process = active.process
	}
	r.mu.RUnlock()
	if stateExists && (active != nil || actionExists) {
		state = cloneActionResult(state)
		if process != nil {
			state.Stdout = append([]string(nil), process.Stdout().Lines()...)
			state.Stderr = append([]string(nil), process.Stderr().Lines()...)
			state.Duration = time.Since(state.StartedAt)
		}
		return state, true
	}
	if actionExists {
		return ActionResult{ID: id, Status: ActionReady, ExitCode: -1}, true
	}
	return ActionResult{}, false
}

// States returns deterministic snapshots for every currently configured action.
func (r *ActionRunner) States() []ActionResult {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := r.cfg.ActionIDs()
	states := make([]ActionResult, 0, len(ids))
	for _, id := range ids {
		if state, exists := r.states[id]; exists {
			states = append(states, cloneActionResult(state))
		} else {
			states = append(states, ActionResult{ID: id, Status: ActionReady, ExitCode: -1})
		}
	}
	return states
}

// Cancel requests termination of a running action.
func (r *ActionRunner) Cancel(id config.ActionID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, active := range r.active {
		if active.id == id {
			active.cancel()
			return true
		}
	}
	return false
}

// Shutdown rejects future runs, cancels active actions, and waits for reaping.
func (r *ActionRunner) Shutdown() {
	r.mu.Lock()
	if r.shuttingDown {
		r.mu.Unlock()
		return
	}
	r.shuttingDown = true
	active := make([]*activeAction, 0, len(r.active))
	for _, run := range r.active {
		active = append(active, run)
	}
	r.mu.Unlock()
	for _, run := range active {
		run.cancel()
	}
	for _, run := range active {
		<-run.done
	}
}

// CancelActive cancels and reaps current actions while keeping the runner
// available for ordered lifecycle-stop operations during manager shutdown.
func (r *ActionRunner) CancelActive() {
	r.mu.RLock()
	active := make([]*activeAction, 0, len(r.active))
	for _, run := range r.active {
		active = append(active, run)
	}
	r.mu.RUnlock()
	for _, run := range active {
		run.cancel()
	}
	for _, run := range active {
		<-run.done
	}
}

func (r *ActionRunner) isActiveLocked(id config.ActionID) bool {
	return r.activeActionLocked(id) != nil
}

func (r *ActionRunner) activeActionLocked(id config.ActionID) *activeAction {
	owner := actionOwner{kind: id.OwnerKind, name: id.Owner}
	active := r.active[owner]
	if active != nil && active.id == id {
		return active
	}
	return nil
}

func (r *ActionRunner) actionExistsLocked(id config.ActionID) bool {
	_, exists := r.cfg.ResolveAction(id)
	return exists
}

func cloneActionResult(result ActionResult) ActionResult {
	result.Stdout = append([]string(nil), result.Stdout...)
	result.Stderr = append([]string(nil), result.Stderr...)
	return result
}

// RunAction executes one configured non-interactive action.
func (m *Manager) RunAction(ctx context.Context, id config.ActionID) (ActionResult, error) {
	return m.actions.Run(ctx, id)
}

// ActionState returns the current or most recent state of an action.
func (m *Manager) ActionState(id config.ActionID) (ActionResult, bool) {
	return m.actions.State(id)
}

// ActionStates returns deterministic snapshots of all configured actions.
func (m *Manager) ActionStates() []ActionResult {
	return m.actions.States()
}

// CancelAction requests termination of a running action.
func (m *Manager) CancelAction(id config.ActionID) bool {
	return m.actions.Cancel(id)
}
