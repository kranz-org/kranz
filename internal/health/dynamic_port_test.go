package health

import (
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func TestResolveCheckUsesSortedDetectedPortSelector(t *testing.T) {
	checker := NewChecker()
	checker.SetDetectedPortsProvider(func(string) []int { return []int{70000, 9200, 0, 8100, 9200} })
	index := 1

	resolved, err := checker.resolveCheck("api", &config.CheckConfig{
		Type: config.CheckTCP, PortFrom: config.PortFromDetected, DetectedPortIndex: &index,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Port != 9200 {
		t.Fatalf("resolved port = %d, want 9200", resolved.Port)
	}
}

func TestResolveCheckTreatsOmittedTCPPortAsDetected(t *testing.T) {
	checker := NewChecker()
	checker.SetDetectedPortsProvider(func(string) []int { return []int{43801} })

	resolved, err := checker.resolveCheck("im-widgets", &config.CheckConfig{Type: config.CheckTCP})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Port != 43801 {
		t.Fatalf("resolved port = %d, want 43801", resolved.Port)
	}
}

func TestResolveCheckRejectsMissingAmbiguousAndUnavailableDetectedPorts(t *testing.T) {
	index := 2
	tests := []struct {
		name    string
		ports   []int
		index   *int
		wantErr string
	}{
		{name: "missing", wantErr: "waiting for a detected port"},
		{name: "missing with selector", index: &index, wantErr: "waiting for a detected port"},
		{name: "ambiguous", ports: []int{8081, 8080}, wantErr: "ambiguous: [8080 8081]"},
		{name: "selector unavailable", ports: []int{8080, 8081}, index: &index, wantErr: "index 2 is unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checker := NewChecker()
			checker.SetDetectedPortsProvider(func(string) []int { return test.ports })
			_, err := checker.resolveCheck("api", &config.CheckConfig{
				Type: config.CheckTCP, PortFrom: config.PortFromDetected, DetectedPortIndex: test.index,
			})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestResolveCheckInjectsDetectedPortIntoHTTPURL(t *testing.T) {
	checker := NewChecker()
	checker.SetDetectedPortsProvider(func(string) []int { return []int{18443} })

	resolved, err := checker.resolveCheck("api", &config.CheckConfig{
		Type: config.CheckHTTP, URL: "http://[::1]/health?full=1", PortFrom: config.PortFromDetected,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.URL != "http://[::1]:18443/health?full=1" {
		t.Fatalf("resolved URL = %q", resolved.URL)
	}
}

func TestResolveCheckTreatsOmittedHTTPURLPortAsDetected(t *testing.T) {
	checker := NewChecker()
	checker.SetDetectedPortsProvider(func(string) []int { return []int{43304} })

	resolved, err := checker.resolveCheck("im-core", &config.CheckConfig{
		Type: config.CheckHTTP, URL: "http://localhost/api/health",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.URL != "http://localhost:43304/api/health" {
		t.Fatalf("resolved URL = %q", resolved.URL)
	}
}

func TestDynamicLivenessFollowsChangingDetectedPortSnapshot(t *testing.T) {
	first, firstPort := listenTCP(t)
	var ports atomic.Value
	ports.Store([]int{firstPort})

	checker := NewChecker()
	defer checker.StopAll()
	checker.SetDetectedPortsProvider(func(string) []int { return ports.Load().([]int) })
	checker.StartMonitoring("api", &config.HealthCheckConfig{Liveness: &config.CheckConfig{
		Type: config.CheckTCP, PortFrom: config.PortFromDetected, Interval: 10 * time.Millisecond, Timeout: 20 * time.Millisecond, FailureThreshold: 1,
	}})

	health := checker.GetHealth("api")
	waitForHealth(t, time.Second, func() bool { return !health.GetLastCheck().IsZero() && health.IsAlive() }, "initial detected port to pass")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	ports.Store([]int(nil))
	waitForHealth(t, time.Second, func() bool { return !health.IsAlive() }, "empty snapshot to fail")

	second, secondPort := listenTCP(t)
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close replacement listener: %v", err)
		}
	})
	ports.Store([]int{secondPort})
	waitForHealth(t, time.Second, func() bool { return health.IsAlive() }, "replacement detected port to pass")
}

func listenTCP(t *testing.T) (net.Listener, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener, listener.Addr().(*net.TCPAddr).Port
}

func waitForHealth(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
