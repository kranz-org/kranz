package health

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// CommandResult retains the exit semantics needed by lifecycle status probes.
type CommandResult struct {
	ExitCode int
	Output   string
}

// RunCommandCheck executes the common command-probe transport while preserving
// its exit code. Health checks consume only the error; lifecycle status also
// classifies configured running and stopped codes.
func RunCommandCheck(parent context.Context, command string, timeout time.Duration) (CommandResult, error) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	output, err := cmd.CombinedOutput()
	result := CommandResult{ExitCode: 0, Output: string(output)}
	if err == nil {
		return result, nil
	}
	result.ExitCode = -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	}
	if ctx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("command timed out after %s", timeout)
	}
	return result, fmt.Errorf("command %q failed: %w\n%s", command, err, string(output))
}

// checkCommand succeeds only when the configured shell command exits with zero.
func checkCommand(command string, timeout time.Duration) error {
	_, err := RunCommandCheck(context.Background(), command, timeout)
	return err
}
