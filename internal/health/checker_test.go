package health

import (
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func TestMissingReadinessIsImmediatelyReady(t *testing.T) {
	checker := NewChecker()
	defer checker.StopAll()

	checker.StartMonitoring("worker", &config.HealthCheckConfig{
		Liveness: &config.CheckConfig{Type: config.CheckCommand, Command: "true", Interval: time.Hour},
	})

	health := checker.GetHealth("worker")
	if health == nil || !health.IsReady() {
		t.Fatal("service without a readiness check should be ready immediately")
	}
}

func TestMissingLivenessIsImmediatelyAlive(t *testing.T) {
	checker := NewChecker()
	defer checker.StopAll()

	checker.StartMonitoring("api", &config.HealthCheckConfig{
		Readiness: &config.CheckConfig{Type: config.CheckCommand, Command: "true", Interval: time.Hour},
	})

	health := checker.GetHealth("api")
	if health == nil || !health.IsAlive() {
		t.Fatal("service without a liveness check should be alive immediately")
	}
}

func TestReadinessRunsImmediately(t *testing.T) {
	checker := NewChecker()
	defer checker.StopAll()

	checker.StartMonitoring("api", &config.HealthCheckConfig{
		Readiness: &config.CheckConfig{Type: config.CheckCommand, Command: "true", Interval: time.Hour},
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if health := checker.GetHealth("api"); health != nil && health.IsReady() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("readiness did not run immediately")
}

func TestLivenessRunsImmediatelyAndUsesConsecutiveFailureThreshold(t *testing.T) {
	checker := NewChecker()
	defer checker.StopAll()

	checker.StartMonitoring("api", &config.HealthCheckConfig{
		Liveness: &config.CheckConfig{Type: config.CheckCommand, Command: "false", Interval: time.Hour},
	})

	health := checker.GetHealth("api")
	deadline := time.Now().Add(time.Second)
	for health.GetLastCheck().IsZero() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if health.GetLastCheck().IsZero() {
		t.Fatal("liveness did not run immediately")
	}
	if !health.IsAlive() {
		t.Fatal("one failed liveness probe should not cross the three-failure threshold")
	}
}
