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
