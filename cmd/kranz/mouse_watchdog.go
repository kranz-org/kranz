package main

import "time"

const (
	// mouseRecoveryInterval is the cadence for terminals that never tell us
	// when they regain focus, which is when mouse mode gets dropped.
	mouseRecoveryInterval = 250 * time.Millisecond
	// mouseRecoveryBackstop takes over once the terminal has proven it reports
	// focus. The focus handler restores mouse mode at the moment that matters,
	// so polling four times a second buys nothing and never stops costing.
	mouseRecoveryBackstop = 5 * time.Second
)

type mouseModeEnabler interface {
	EnableMouseCellMotion()
}

// mouseWatchdogConfig carries the two cadences and the signals that switch
// between them.
type mouseWatchdogConfig struct {
	interval time.Duration
	backstop time.Duration
	ready    <-chan struct{}
	relaxed  <-chan struct{}
	done     <-chan struct{}
}

// runMouseWatchdog repairs terminals that silently drop mouse mode while
// changing windows or macOS workspaces. Calling tea.Program directly keeps the
// recovery guarantee without injecting a message and rendering the full TUI.
func runMouseWatchdog(program mouseModeEnabler, config mouseWatchdogConfig) {
	select {
	case <-config.ready:
	case <-config.done:
		return
	}
	relaxed := config.relaxed
	ticker := time.NewTicker(config.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			program.EnableMouseCellMotion()
		case <-relaxed:
			// A focus event proves the terminal will announce the moment mouse
			// mode needs restoring, so the frequent poll can stand down.
			relaxed = nil
			ticker.Reset(config.backstop)
		case <-config.done:
			return
		}
	}
}
