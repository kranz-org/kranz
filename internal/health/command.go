package health

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// checkCommand succeeds only when the configured shell command exits with zero.
func checkCommand(command string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("command timed out after %s", timeout)
		}
		return fmt.Errorf("command %q failed: %w\n%s", command, err, string(output))
	}

	return nil
}
