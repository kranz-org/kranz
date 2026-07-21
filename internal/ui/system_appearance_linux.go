//go:build linux

package ui

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

func detectSystemDarkMode() (dark, available bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(ctx, "gsettings", "get", "org.gnome.desktop.interface", "color-scheme").Output()
	if err != nil {
		return false, false
	}
	mode := strings.ToLower(strings.Trim(strings.TrimSpace(string(output)), "'\""))
	switch mode {
	case "prefer-dark":
		return true, true
	case "prefer-light":
		return false, true
	default:
		return false, false
	}
}
