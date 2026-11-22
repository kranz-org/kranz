package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

func TestDependencyStatusUsesObservedProbeResults(t *testing.T) {
	datastore := &app.ServiceSnapshot{
		Name: "datastore",
		Config: config.Service{HealthCheck: &config.HealthCheckConfig{
			Readiness: &config.CheckConfig{},
			Liveness:  &config.CheckConfig{},
		}},
		State:  config.ServiceState{Status: config.StatusRunning},
		Health: app.HealthSnapshot{Observed: true, Alive: true},
	}
	model := &Model{allServices: []*app.ServiceSnapshot{
		datastore,
		{Name: "schema", State: config.ServiceState{Status: config.StatusStopped, Completed: true, ExitCode: 17}},
		{Name: "idle", State: config.ServiceState{Status: config.StatusStopped}},
		{Name: "client", State: config.ServiceState{Status: config.StatusStopped}, DesiredRunning: true},
	}}

	expect := func(name string, parts ...string) {
		t.Helper()
		status := ansi.Strip(model.dependencyStatuses()[name].render(name))
		for _, part := range parts {
			if !strings.Contains(status, part) {
				t.Errorf("%s status = %q, missing %q", name, status, part)
			}
		}
	}

	expect("datastore", "starting", "readiness pending", "liveness pending")
	expect("schema", "○ schema", "exit 17")
	if got := ansi.Strip(model.dependencyStatuses()["idle"].render("idle")); got != "○ idle" {
		t.Errorf("idle status = %q, want only the hollow marker and name", got)
	}
	expect("client", "queued")

	now := time.Now()
	datastore.Health.Readiness.LastAttempt = now
	datastore.Health.Liveness.LastAttempt = now
	datastore.Health.LastCheck = now
	expect("datastore", "not ready", "live")

	datastore.Health.Ready = true
	expect("datastore", "● datastore", "running", " · ready", " · live")

	datastore.Health.Liveness.ConsecutiveFailures = 1
	expect("datastore", "liveness degraded")
	datastore.Health.Alive = false
	expect("datastore", "unhealthy", "liveness failed")
}
