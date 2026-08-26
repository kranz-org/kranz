package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
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
	ErrActionRunNotFound    = errors.New("action run not found")
	ErrActionRunnerStopping = errors.New("action runner is shutting down")
	ErrInteractiveAction    = errors.New("interactive action requires terminal handoff")
)

// ActionRunEvictedError means the requested run once existed but has fallen
// out of the bounded result history. This is deliberately distinct from an
// unknown action or a run number that has never existed.
type ActionRunEvictedError struct {
	ID     config.ActionID
	Run    uint32
	Oldest uint32
}

func (e *ActionRunEvictedError) Error() string {
	return fmt.Sprintf("action %s/%s run %d was evicted; oldest retained run is %d", e.ID.Owner, e.ID.Name, e.Run, e.Oldest)
}

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

func (s ActionStatus) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

func (s *ActionStatus) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	for candidate := ActionReady; candidate <= ActionCancelled; candidate++ {
		if candidate.String() == value {
			*s = candidate
			return nil
		}
	}
	return fmt.Errorf("unknown action status %q", value)
}

// ActionResult is the concurrency-safe snapshot retained for one action.
type ActionResult struct {
	ID         config.ActionID `json:"id"`
	Run        uint32          `json:"run"`
	Status     ActionStatus    `json:"status"`
	PID        int             `json:"pid,omitempty"`
	StartedAt  time.Time       `json:"started_at,omitempty"`
	FinishedAt time.Time       `json:"finished_at,omitempty"`
	Duration   time.Duration   `json:"duration,omitempty"`
	ExitCode   int             `json:"exit_code"`
	Error      string          `json:"error,omitempty"`
	Stdout     []string        `json:"stdout,omitempty"`
	Stderr     []string        `json:"stderr,omitempty"`
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
	// interactive marks a run whose process owns the user's terminal. Kranz
	// cannot wait for it during shutdown: only the caller that handed the
	// terminal over can observe the command finishing.
	interactive bool
	// lease identifies an AcquireInteractive reservation so a later
	// CompleteInteractive call can be matched to the reservation it is
	// finishing, not to whatever now occupies the same owner slot.
	lease string
}

// ActionRunner executes normalized non-interactive actions and retains their
// latest bounded result. It serializes actions per owner while allowing
// independent services and groups to run concurrently.
type ActionRunner struct {
	mu           sync.RWMutex
	cfg          *config.Config
	states       map[config.ActionID]ActionResult
	history      map[config.ActionID][]ActionResult
	nextRun      map[config.ActionID]uint32
	historySize  int
	active       map[actionOwner]*activeAction
	logBufSize   int
	logsMu       sync.RWMutex
	logs         map[config.ActionID]*logStream
	shuttingDown bool
	leaseSeq     atomic.Uint64
	journal      *Journal
	catalog      *RunCatalog
}

// SetJournal attaches the runtime transition journal action runs record into.
func (r *ActionRunner) SetJournal(journal *Journal) { r.journal = journal }

func (r *ActionRunner) SetRunCatalog(catalog *RunCatalog) { r.catalog = catalog }

// recordActionTransition writes one action lifecycle fact. A service-owned
// action also names its owner, so "what happened to api" can be answered
// without the reader knowing which actions api owns.
func (r *ActionRunner) recordActionTransition(id config.ActionID, run uint32, from, to ActionStatus, exitCode int, summary string) {
	transition := Transition{Kind: TransitionActionState, Action: id.Owner + "/" + id.Name, From: from.String(), To: to.String(), Run: run, Summary: summary}
	if id.OwnerKind == config.ActionOwnerService {
		transition.Service = id.Owner
	}
	if to != ActionRunning {
		exit := exitCode
		transition.ExitCode = &exit
	}
	r.journal.Record(transition)
}

// NewActionRunner creates an action runner for one loaded project config.
func NewActionRunner(cfg *config.Config, logBufSize int) *ActionRunner {
	if logBufSize <= 0 {
		logBufSize = defaultActionLogBuffer
	}
	return &ActionRunner{
		cfg:         cfg,
		states:      make(map[config.ActionID]ActionResult),
		history:     make(map[config.ActionID][]ActionResult),
		nextRun:     make(map[config.ActionID]uint32),
		active:      make(map[actionOwner]*activeAction),
		logs:        make(map[config.ActionID]*logStream),
		logBufSize:  logBufSize,
		historySize: logBufSize,
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
			delete(r.history, id)
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
	run := r.nextRun[id] + 1
	r.nextRun[id] = run
	r.states[id] = ActionResult{ID: id, Run: run, Status: ActionRunning, ExitCode: -1, StartedAt: started}
	r.mu.Unlock()
	r.catalog.Begin(RunSummary{Target: ActionRunTarget(id), Run: run, Status: ActionRunning.String(), StartedAt: started,
		StartReason: "invoked"})

	stream := r.logStreamFor(id)
	if stream != nil {
		stream.BeginRunNumber(run)
		stream.Append(started, "kranz", fmt.Sprintf("[Kranz] %s/%s #%d started", id.Owner, id.Name, run))
	}
	r.recordActionTransition(id, run, ActionReady, ActionRunning, 0, fmt.Sprintf("%s/%s #%d started", id.Owner, id.Name, run))
	result, runErr := r.execute(runCtx, id, action, run, started, stream)
	if stream != nil {
		stream.Append(time.Now(), "kranz", fmt.Sprintf("[Kranz] %s/%s #%d %s · exit %d · %s",
			id.Owner, id.Name, run, result.Status, result.ExitCode, result.Duration.Round(time.Millisecond)))
	}
	cancel()
	r.mu.Lock()
	delete(r.active, owner)
	r.states[id] = cloneActionResult(result)
	r.retainResultLocked(result)
	close(active.done)
	r.mu.Unlock()
	r.recordActionTransition(id, run, ActionRunning, result.Status, result.ExitCode,
		fmt.Sprintf("%s/%s #%d %s · exit %d", id.Owner, id.Name, run, result.Status, result.ExitCode))
	r.catalog.Finish(ActionRunTarget(id), run, result.Status.String(), result.FinishedAt, result.ExitCode, nil)
	return result, runErr
}

func (r *ActionRunner) execute(ctx context.Context, id config.ActionID, action config.Action, run uint32, started time.Time, stream *logStream) (ActionResult, error) {
	result := ActionResult{ID: id, Run: run, Status: ActionFailed, ExitCode: -1, StartedAt: started}
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
	// The pump copies output into the addressable stream while the action runs,
	// so `kranz logs owner/action --follow` sees lines as they land instead of
	// only at exit. Its final sweep runs on every return path.
	defer startOutputPump(stream, process)()

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
	r.catalog.Update(ActionRunTarget(id), state.Run, "", pid, nil)
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

// Result returns one current or retained run. Positive numbers are absolute;
// negative numbers are offsets from the newest run (-1 is newest). The run
// counter survives history eviction and configuration replacement.
func (r *ActionRunner) Result(id config.ActionID, requested int) (ActionResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.actionExistsLocked(id) {
		return ActionResult{}, fmt.Errorf("%w: %s/%s", ErrActionNotFound, id.Owner, id.Name)
	}
	latest := r.nextRun[id]
	if latest == 0 || requested == 0 {
		return ActionResult{}, fmt.Errorf("%w: %s/%s run %d", ErrActionRunNotFound, id.Owner, id.Name, requested)
	}
	var wanted uint32
	if requested < 0 {
		offset := uint32(-requested) - 1
		if offset >= latest {
			return ActionResult{}, fmt.Errorf("%w: %s/%s run offset %d", ErrActionRunNotFound, id.Owner, id.Name, requested)
		}
		wanted = latest - offset
	} else {
		wanted = uint32(requested)
		if wanted > latest {
			return ActionResult{}, fmt.Errorf("%w: %s/%s run %d", ErrActionRunNotFound, id.Owner, id.Name, requested)
		}
	}
	if state, ok := r.states[id]; ok && state.Run == wanted && state.Status == ActionRunning {
		state = cloneActionResult(state)
		if active := r.activeActionLocked(id); active != nil && active.process != nil {
			state.Stdout = append([]string(nil), active.process.Stdout().Lines()...)
			state.Stderr = append([]string(nil), active.process.Stderr().Lines()...)
			state.Duration = time.Since(state.StartedAt)
		}
		return state, nil
	}
	history := r.history[id]
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Run == wanted {
			return cloneActionResult(history[index]), nil
		}
	}
	oldest := latest + 1
	if len(history) > 0 {
		oldest = history[0].Run
	}
	if wanted < oldest {
		return ActionResult{}, &ActionRunEvictedError{ID: id, Run: wanted, Oldest: oldest}
	}
	return ActionResult{}, fmt.Errorf("%w: %s/%s run %d", ErrActionRunNotFound, id.Owner, id.Name, wanted)
}

func (r *ActionRunner) retainResultLocked(result ActionResult) {
	history := append(r.history[result.ID], cloneActionResult(result))
	if len(history) > r.historySize {
		history = append([]ActionResult(nil), history[len(history)-r.historySize:]...)
	}
	r.history[result.ID] = history
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
		if run.interactive {
			continue
		}
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
		if run.interactive {
			continue
		}
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

// ActionResult returns one current or retained execution by absolute run or
// negative offset from the newest retained identity.
func (m *Manager) ActionResult(id config.ActionID, run int) (ActionResult, error) {
	return m.actions.Result(id, run)
}

// ActionStates returns deterministic snapshots of all configured actions.
func (m *Manager) ActionStates() []ActionResult {
	return m.actions.States()
}

// CancelAction requests termination of a running action.
func (m *Manager) CancelAction(id config.ActionID) bool {
	return m.actions.Cancel(id)
}

// actionLogPumpInterval paces copying a running action's output into its
// addressable stream. It matches the detached log follower's cadence: fast
// enough that --follow feels live, slow enough to stay off the hot path.
const actionLogPumpInterval = 100 * time.Millisecond

// logStreamFor returns the addressable log stream for an action, creating it on
// first use. Lifecycle actions get none: their output already belongs to the
// service they act on, and `kranz logs api` is where it is read.
func (r *ActionRunner) logStreamFor(id config.ActionID) *logStream {
	if id.OwnerKind == config.ActionOwnerLifecycle {
		return nil
	}
	r.logsMu.Lock()
	defer r.logsMu.Unlock()
	stream, exists := r.logs[id]
	if !exists {
		stream = newLogStream(r.logBufSize)
		stream.SetCatalog(r.catalog, ActionRunTarget(id))
		r.logs[id] = stream
	}
	return stream
}

// ActionLogEntries returns the buffered history of one action.
func (r *ActionRunner) ActionLogEntries(id config.ActionID) []config.LogEntry {
	r.logsMu.RLock()
	stream := r.logs[id]
	r.logsMu.RUnlock()
	if stream == nil {
		return nil
	}
	return stream.Entries()
}

// ClearActionLogs discards one action's buffered history.
func (r *ActionRunner) ClearActionLogs(id config.ActionID) {
	r.logsMu.RLock()
	stream := r.logs[id]
	r.logsMu.RUnlock()
	if stream != nil {
		stream.Clear()
	}
	r.mu.Lock()
	delete(r.history, id)
	r.mu.Unlock()
}

// startOutputPump copies a process's captured output into stream until the
// returned stop function is called, which sweeps whatever arrived last. It
// reads by offset rather than draining, so the ActionResult snapshot the caller
// builds from the same buffers stays complete.
func startOutputPump(stream *logStream, process *ProcessManager) func() {
	if stream == nil || process == nil {
		return func() {}
	}
	stdoutOffset, stderrOffset := 0, 0
	sweep := func() {
		lines := process.Stdout().Lines()
		for ; stdoutOffset < len(lines); stdoutOffset++ {
			stream.Append(time.Now(), "stdout", lines[stdoutOffset])
		}
		lines = process.Stderr().Lines()
		for ; stderrOffset < len(lines); stderrOffset++ {
			stream.Append(time.Now(), "stderr", lines[stderrOffset])
		}
	}
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(actionLogPumpInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()
	// The offsets are owned by the pump goroutine; the final sweep runs only
	// after it has exited, so the two never advance them concurrently.
	return func() {
		close(done)
		<-finished
		sweep()
	}
}

// ActionLogs returns the buffered history of one action.
func (m *Manager) ActionLogs(id config.ActionID) []config.LogEntry {
	return m.actions.ActionLogEntries(id)
}

// ClearActionLogs discards one action's buffered history.
func (m *Manager) ClearActionLogs(id config.ActionID) { m.actions.ClearActionLogs(id) }
