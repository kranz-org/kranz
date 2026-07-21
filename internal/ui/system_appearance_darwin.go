//go:build darwin

package ui

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

func detectSystemDarkMode() (dark, available bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(ctx, "defaults", "read", "-g", "AppleInterfaceStyle").Output()
	if err == nil {
		return strings.EqualFold(strings.TrimSpace(string(output)), "Dark"), true
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		// macOS removes AppleInterfaceStyle rather than writing "Light".
		return false, true
	}
	return false, false
}
