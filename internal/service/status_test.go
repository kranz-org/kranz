package service

import (
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func TestClassifyStatusResultDefaultContract(t *testing.T) {
	// With no exit codes configured, a status probe behaves like every other
	// shell command: zero means running, anything else means stopped.
	cfg := &config.LifecycleStatusConfig{}
	cases := []struct {
		exitCode   int
		want       config.ServiceStatus
		classified bool
	}{
		{exitCode: 0, want: config.StatusRunning, classified: true},
		{exitCode: 1, want: config.StatusStopped, classified: true},
		{exitCode: 3, want: config.StatusStopped, classified: true},
		{exitCode: 127, want: config.StatusStopped, classified: true},
		{exitCode: -1, want: config.StatusUnknown, classified: false},
	}
	for _, tc := range cases {
		got, classified := classifyStatusResult(cfg, tc.exitCode)
		if got != tc.want || classified != tc.classified {
			t.Errorf("exit %d: got (%v, %v), want (%v, %v)", tc.exitCode, got, classified, tc.want, tc.classified)
		}
	}
}

func TestClassifyStatusResultThreeWayContract(t *testing.T) {
	// Declaring stopped_exit_codes opts into the three-way contract, where an
	// unlisted code is unclassified rather than silently reported as stopped.
	cfg := &config.LifecycleStatusConfig{
		RunningExitCodes: []int{0},
		StoppedExitCodes: []int{3},
	}
	cases := []struct {
		exitCode   int
		want       config.ServiceStatus
		classified bool
	}{
		{exitCode: 0, want: config.StatusRunning, classified: true},
		{exitCode: 3, want: config.StatusStopped, classified: true},
		{exitCode: 4, want: config.StatusUnknown, classified: false},
		{exitCode: 1, want: config.StatusUnknown, classified: false},
	}
	for _, tc := range cases {
		got, classified := classifyStatusResult(cfg, tc.exitCode)
		if got != tc.want || classified != tc.classified {
			t.Errorf("exit %d: got (%v, %v), want (%v, %v)", tc.exitCode, got, classified, tc.want, tc.classified)
		}
	}
}

func TestClassifyStatusResultCustomRunningCodes(t *testing.T) {
	// Naming only running codes keeps the two-way contract: everything else is
	// stopped. Unknown requires an explicit gap between the two sets.
	cfg := &config.LifecycleStatusConfig{RunningExitCodes: []int{0, 1}}
	if got, classified := classifyStatusResult(cfg, 1); got != config.StatusRunning || !classified {
		t.Errorf("exit 1: got (%v, %v), want (running, true)", got, classified)
	}
	if got, classified := classifyStatusResult(cfg, 2); got != config.StatusStopped || !classified {
		t.Errorf("exit 2: got (%v, %v), want (stopped, true)", got, classified)
	}
}

func TestStatusPollIntervalDefaults(t *testing.T) {
	cfg := &config.LifecycleStatusConfig{}
	if got := statusPollInterval(cfg, config.StatusRunning); got != config.DefaultCheckInterval {
		t.Errorf("running interval: got %s, want %s", got, config.DefaultCheckInterval)
	}
	// The stopped interval is flat and predictable rather than derived from the
	// running interval, so a configured interval never changes it.
	cfg.Interval = time.Minute
	if got := statusPollInterval(cfg, config.StatusStopped); got != config.DefaultStoppedStatusInterval {
		t.Errorf("stopped interval: got %s, want %s", got, config.DefaultStoppedStatusInterval)
	}
	if got := statusPollInterval(cfg, config.StatusUnknown); got != config.DefaultStoppedStatusInterval {
		t.Errorf("unknown interval: got %s, want %s", got, config.DefaultStoppedStatusInterval)
	}
}

func TestServiceStatusStringDistinguishesUnknownFromInvalid(t *testing.T) {
	if got := config.StatusUnknown.String(); got != "unknown" {
		t.Errorf("StatusUnknown: got %q, want %q", got, "unknown")
	}
	if got := config.ServiceStatus(99).String(); got == "unknown" {
		t.Errorf("an out-of-range status must not be reported as a deliberate unknown, got %q", got)
	}
}
