package service

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// interactiveHandoffNotice replaces captured output for an interactive action.
// Its output went to the real terminal, so retaining an empty buffer would look
// like the command printed nothing.
const interactiveHandoffNotice = "[Kranz] The terminal was handed to this action; its output stayed in the terminal."

// PrepareInteractive reserves an interactive action and returns the command to
// run plus the function that records its outcome.
//
// The caller owns the process because only it can hand over the terminal. The
// runner still owns the action's identity, owner serialization, and retained
// state, so an interactive action reports running, succeeded, or failed exactly
// like a captured one.
func (r *ActionRunner) PrepareInteractive(id config.ActionID) (*exec.Cmd, func(error) ActionResult, error) {
	r.mu.Lock()
	if r.shuttingDown {
		r.mu.Unlock()
		return nil, nil, ErrActionRunnerStopping
	}
	action, exists := r.cfg.ResolveAction(id)
	if !exists {
		r.mu.Unlock()
		return nil, nil, fmt.Errorf("%w: %s/%s", ErrActionNotFound, id.Owner, id.Name)
	}
	if !action.InteractiveEnabled() {
		r.mu.Unlock()
		return nil, nil, fmt.Errorf("action %s/%s is not interactive", id.Owner, id.Name)
	}
	owner := actionOwner{kind: id.OwnerKind, name: id.Owner}
	if running, busy := r.active[owner]; busy {
		r.mu.Unlock()
		return nil, nil, &ActionBusyError{Requested: id, Running: running.id}
	}
	command := interactiveCommand(action)
	// Cancelling a handoff kills the command outright: it owns the terminal, so
	// there is no gentler way to reclaim it during shutdown.
	active := &activeAction{
		id: id, done: make(chan struct{}), interactive: true,
		cancel: func() {
			if command.Process != nil {
				_ = command.Process.Kill()
			}
		},
	}
	r.active[owner] = active
	started := time.Now()
	r.states[id] = ActionResult{ID: id, Status: ActionRunning, ExitCode: -1, StartedAt: started}
	r.mu.Unlock()

	finish := func(runErr error) ActionResult {
		result := ActionResult{
			ID: id, Status: ActionSucceeded, StartedAt: started,
			FinishedAt: time.Now(), Stdout: []string{interactiveHandoffNotice},
		}
		result.Duration = result.FinishedAt.Sub(result.StartedAt)
		if command.ProcessState != nil {
			result.ExitCode = command.ProcessState.ExitCode()
			if command.Process != nil {
				result.PID = command.Process.Pid
			}
		}
		if runErr != nil || result.ExitCode != 0 {
			result.Status = ActionFailed
			if runErr == nil {
				runErr = &ActionExitError{ID: id, ExitCode: result.ExitCode}
			}
			result.Error = runErr.Error()
		}

		r.mu.Lock()
		delete(r.active, owner)
		r.states[id] = cloneActionResult(result)
		close(active.done)
		r.mu.Unlock()
		return result
	}
	return command, finish, nil
}

// AcquireInteractive reserves an interactive action for a delivery surface
// that does not run in this process — over IPC, the runtime never sees the
// command's exec.Cmd, so it cannot read ProcessState the way PrepareInteractive
// does. It records the reservation and hands back the resolved action plus an
// opaque lease token; CompleteInteractive finishes the accounting once the
// caller has run the command itself and observed its outcome.
func (r *ActionRunner) AcquireInteractive(id config.ActionID) (config.Action, string, error) {
	r.mu.Lock()
	if r.shuttingDown {
		r.mu.Unlock()
		return config.Action{}, "", ErrActionRunnerStopping
	}
	action, exists := r.cfg.ResolveAction(id)
	if !exists {
		r.mu.Unlock()
		return config.Action{}, "", fmt.Errorf("%w: %s/%s", ErrActionNotFound, id.Owner, id.Name)
	}
	if !action.InteractiveEnabled() {
		r.mu.Unlock()
		return config.Action{}, "", fmt.Errorf("action %s/%s is not interactive", id.Owner, id.Name)
	}
	owner := actionOwner{kind: id.OwnerKind, name: id.Owner}
	if running, busy := r.active[owner]; busy {
		r.mu.Unlock()
		return config.Action{}, "", &ActionBusyError{Requested: id, Running: running.id}
	}
	lease := fmt.Sprintf("%s/%s:%d", owner.kind, owner.name, r.leaseSeq.Add(1))
	active := &activeAction{
		id: id, done: make(chan struct{}), interactive: true, lease: lease,
		// The process lives in the caller's address space, not this one, so
		// there is nothing local to kill. Shutdown still releases the owner
		// slot below rather than waiting on it forever.
		cancel: func() {},
	}
	r.active[owner] = active
	started := time.Now()
	r.states[id] = ActionResult{ID: id, Status: ActionRunning, ExitCode: -1, StartedAt: started}
	r.mu.Unlock()
	return action, lease, nil
}

// CompleteInteractive records the outcome of a lease returned by
// AcquireInteractive. The caller supplies the exit code and PID it observed
// running the command itself, since the runner never executed it.
func (r *ActionRunner) CompleteInteractive(id config.ActionID, lease string, exitCode, pid int, runErr error) (ActionResult, error) {
	owner := actionOwner{kind: id.OwnerKind, name: id.Owner}
	r.mu.Lock()
	active, ok := r.active[owner]
	if !ok || active.id != id || active.lease != lease {
		r.mu.Unlock()
		return ActionResult{}, fmt.Errorf("interactive lease %q is not active for %s/%s", lease, id.Owner, id.Name)
	}
	started := r.states[id].StartedAt
	r.mu.Unlock()

	result := ActionResult{
		ID: id, Status: ActionSucceeded, StartedAt: started,
		FinishedAt: time.Now(), Stdout: []string{interactiveHandoffNotice},
		ExitCode: exitCode, PID: pid,
	}
	result.Duration = result.FinishedAt.Sub(result.StartedAt)
	if runErr != nil || result.ExitCode != 0 {
		result.Status = ActionFailed
		if runErr == nil {
			runErr = &ActionExitError{ID: id, ExitCode: result.ExitCode}
		}
		result.Error = runErr.Error()
	}

	r.mu.Lock()
	delete(r.active, owner)
	r.states[id] = cloneActionResult(result)
	close(active.done)
	r.mu.Unlock()
	return result, nil
}

func interactiveCommand(action config.Action) *exec.Cmd {
	shell := action.Shell
	if shell == "" {
		shell = "sh"
	}
	// The action keeps the terminal it was given, so no process group of its
	// own: Ctrl+C must reach the command the user is looking at.
	command := exec.Command(shell, "-c", action.Command)
	command.Dir = action.Dir
	command.Env = os.Environ()
	for name, value := range action.Env {
		command.Env = append(command.Env, fmt.Sprintf("%s=%s", name, value))
	}
	return command
}

// PrepareInteractiveAction reserves an interactive action for terminal handoff.
func (m *Manager) PrepareInteractiveAction(id config.ActionID) (*exec.Cmd, func(error) ActionResult, error) {
	return m.actions.PrepareInteractive(id)
}

// AcquireInteractiveAction reserves an interactive action for a delivery
// surface that runs the command itself, out of process from the runtime.
func (m *Manager) AcquireInteractiveAction(id config.ActionID) (config.Action, string, error) {
	return m.actions.AcquireInteractive(id)
}

// CompleteInteractiveAction finishes an AcquireInteractiveAction lease with
// the outcome the caller observed running the command.
func (m *Manager) CompleteInteractiveAction(id config.ActionID, lease string, exitCode, pid int, runErr error) (ActionResult, error) {
	return m.actions.CompleteInteractive(id, lease, exitCode, pid, runErr)
}
