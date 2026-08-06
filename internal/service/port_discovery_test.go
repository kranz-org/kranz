package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/port"
)

type listenerSnapshotResult struct {
	listeners []port.Listener
	err       error
}

type scriptedListenerScanner struct {
	mu      sync.Mutex
	results []listenerSnapshotResult
	calls   int
}

type blockingListenerScanner struct {
	started   chan struct{}
	release   chan struct{}
	listeners []port.Listener
}

type toggleListenerScanner struct {
	manager *Manager
	active  atomic.Bool
	calls   atomic.Int32
}

func (s *toggleListenerScanner) Snapshot(context.Context) ([]port.Listener, error) {
	s.calls.Add(1)
	if !s.active.Load() {
		return nil, nil
	}
	svc, _ := s.manager.GetService("api")
	return []port.Listener{{Protocol: "tcp", Port: 7777, PID: svc.PID()}}, nil
}

type countingPortChecker struct {
	calls int
}

func (c *countingPortChecker) CheckPort(int) (*config.PortInfo, error) {
	return nil, errors.New("unexpected configured-port preflight")
}

func (c *countingPortChecker) CheckPorts([]int) (map[int]*config.PortInfo, error) {
	c.calls++
	return nil, errors.New("unexpected configured-port preflight")
}

func (s *blockingListenerScanner) Snapshot(ctx context.Context) ([]port.Listener, error) {
	close(s.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
		return append([]port.Listener(nil), s.listeners...), nil
	}
}

func (s *scriptedListenerScanner) Snapshot(context.Context) ([]port.Listener, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if len(s.results) == 0 {
		return nil, nil
	}
	result := s.results[0]
	if len(s.results) > 1 {
		s.results = s.results[1:]
	}
	return append([]port.Listener(nil), result.listeners...), result.err
}

func (s *scriptedListenerScanner) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *scriptedListenerScanner) SetResults(results []listenerSnapshotResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append([]listenerSnapshotResult(nil), results...)
}

func TestDiscoveryMapsShellNPMNodeListenerToServiceProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm is required for the shell -> npm -> node process-group test")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for the shell -> npm -> node process-group test")
	}

	dir := t.TempDir()
	pidPath := filepath.Join(dir, "listener.pid")
	packageJSON := `{"scripts":{"start":"node server.js"}}`
	serverJS := `const fs=require('fs'); const net=require('net'); const server=net.createServer(); server.listen(0,'127.0.0.1',()=>fs.writeFileSync('listener.pid',String(process.pid)));`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(packageJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte(serverJS), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Project: "port-discovery", Services: map[string]config.Service{
		"api": {
			Command: "npm start --silent",
			Dir:     dir,
			Shell:   "sh",
			Shutdown: config.ShutdownConfig{
				Timeout: 3 * time.Second,
				Signal:  int(syscall.SIGTERM),
			},
		},
	}}
	manager := NewManager(cfg)
	if err := manager.StartService("api"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })

	listenerPID := waitForPIDFile(t, pidPath)
	scanner := &scriptedListenerScanner{results: []listenerSnapshotResult{{listeners: []port.Listener{
		{Protocol: "tcp", Address: "127.0.0.1", Port: 43123, PID: listenerPID},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 49999, PID: os.Getpid()},
		{Protocol: "udp", Address: "127.0.0.1", Port: 5353, PID: listenerPID},
	}}}}
	manager.SetListenerScanner(scanner)
	manager.refreshDetectedPorts(context.Background())

	svc, _ := manager.GetService("api")
	if got, want := svc.DetectedPorts(), []int{43123}; !reflect.DeepEqual(got, want) {
		t.Fatalf("detected ports = %v, want %v", got, want)
	}
	if owner := manager.ManagedServiceForPID(listenerPID); owner != "api" {
		t.Fatalf("ManagedServiceForPID(%d) = %q, want api", listenerPID, owner)
	}
}

func TestDiscoveryUsesOneSnapshotForAllRunningServices(t *testing.T) {
	shutdown := config.ShutdownConfig{Timeout: time.Second, Signal: int(syscall.SIGTERM)}
	manager := NewManager(&config.Config{Project: "port-discovery", Services: map[string]config.Service{
		"api":    {Command: "sleep 30", Dir: ".", Shell: "sh", Shutdown: shutdown},
		"worker": {Command: "sleep 30", Dir: ".", Shell: "sh", Shutdown: shutdown},
	}})
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartService("worker"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	api, _ := manager.GetService("api")
	worker, _ := manager.GetService("worker")
	scanner := &scriptedListenerScanner{results: []listenerSnapshotResult{{listeners: []port.Listener{
		{Protocol: "tcp", Address: "127.0.0.1", Port: 3000, PID: api.PID()},
		{Protocol: "tcp", Address: "::1", Port: 3000, PID: api.PID()},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 4000, PID: worker.PID()},
	}}}}
	manager.SetListenerScanner(scanner)

	manager.refreshDetectedPorts(context.Background())

	if calls := scanner.Calls(); calls != 1 {
		t.Fatalf("scanner calls = %d, want one snapshot for all services", calls)
	}
	if got := api.DetectedPorts(); !reflect.DeepEqual(got, []int{3000}) {
		t.Fatalf("api detected ports = %v", got)
	}
	if got := worker.DetectedPorts(); !reflect.DeepEqual(got, []int{4000}) {
		t.Fatalf("worker detected ports = %v", got)
	}
}

func TestDiscoveryEffectiveEnablementMatrix(t *testing.T) {
	enabled := true
	disabled := false
	shutdown := config.ShutdownConfig{Timeout: time.Second, Signal: int(syscall.SIGTERM)}
	manager := NewManager(&config.Config{Project: "port-discovery", Services: map[string]config.Service{
		"auto":           {Command: "sleep 30", Shell: "sh", Shutdown: shutdown},
		"configured":     {Command: "sleep 30", Shell: "sh", Ports: []int{8101}, Shutdown: shutdown},
		"configured-opt": {Command: "sleep 30", Shell: "sh", Ports: []int{8102}, DetectPorts: &enabled, Shutdown: shutdown},
		"disabled":       {Command: "sleep 30", Shell: "sh", DetectPorts: &disabled, Shutdown: shutdown},
	}})
	for _, name := range []string{"auto", "configured", "configured-opt", "disabled"} {
		if err := manager.StartService(name); err != nil {
			t.Fatalf("StartService(%q) error = %v", name, err)
		}
	}
	t.Cleanup(func() { _ = manager.Shutdown() })

	listeners := make([]port.Listener, 0, 4)
	for index, name := range []string{"auto", "configured", "configured-opt", "disabled"} {
		svc, _ := manager.GetService(name)
		listeners = append(listeners, port.Listener{Protocol: "tcp", Port: 8200 + index, PID: svc.PID()})
	}
	scanner := &scriptedListenerScanner{results: []listenerSnapshotResult{{listeners: listeners}}}
	manager.SetListenerScanner(scanner)
	manager.refreshDetectedPorts(context.Background())

	wants := map[string][]int{
		"auto":           {8200},
		"configured":     nil,
		"configured-opt": {8202},
		"disabled":       nil,
	}
	for name, want := range wants {
		svc, _ := manager.GetService(name)
		if got := svc.DetectedPorts(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s detected ports = %v, want %v", name, got, want)
		}
	}
}

func TestConfiguredPortsDoNotStartDiscoveryLoopWithoutOptIn(t *testing.T) {
	manager := NewManager(&config.Config{Project: "port-discovery", Services: map[string]config.Service{
		"api": {
			Command: "sleep 30", Shell: "sh", Ports: []int{8101},
			Shutdown: config.ShutdownConfig{Timeout: time.Second, Signal: int(syscall.SIGTERM)},
		},
	}})
	scanner := &scriptedListenerScanner{}
	manager.SetListenerScanner(scanner)
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	manager.discoveryMu.Lock()
	loopStarted := manager.discoveryCancel != nil
	manager.discoveryMu.Unlock()
	if loopStarted || scanner.Calls() != 0 {
		t.Fatalf("configured-only service started discovery: loop=%v calls=%d", loopStarted, scanner.Calls())
	}
}

func TestExplicitDiscoveryOptInStartsLoopWithConfiguredPorts(t *testing.T) {
	enabled := true
	manager := NewManager(&config.Config{Project: "port-discovery", Services: map[string]config.Service{
		"api": {
			Command: "sleep 30", Shell: "sh", Ports: []int{8101}, DetectPorts: &enabled,
			Shutdown: config.ShutdownConfig{Timeout: time.Second, Signal: int(syscall.SIGTERM)},
		},
	}})
	manager.listenerScanInterval = time.Hour
	scanner := &scriptedListenerScanner{}
	manager.SetListenerScanner(scanner)
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	waitForScannerCalls(t, scanner, 1)
}

func TestExplicitDiscoveryOptOutDoesNotStartLoopWithoutConfiguredPorts(t *testing.T) {
	disabled := false
	manager := NewManager(&config.Config{Project: "port-discovery", Services: map[string]config.Service{
		"api": {
			Command: "sleep 30", Shell: "sh", DetectPorts: &disabled,
			Shutdown: config.ShutdownConfig{Timeout: time.Second, Signal: int(syscall.SIGTERM)},
		},
	}})
	scanner := &scriptedListenerScanner{}
	manager.SetListenerScanner(scanner)
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	manager.discoveryMu.Lock()
	loopStarted := manager.discoveryCancel != nil
	manager.discoveryMu.Unlock()
	if loopStarted || scanner.Calls() != 0 {
		t.Fatalf("explicit opt-out started discovery: loop=%v calls=%d", loopStarted, scanner.Calls())
	}
}

func TestDiscoveryRejectsLateSnapshotAfterStop(t *testing.T) {
	manager := newSleepingDiscoveryManager(t)
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	svc, _ := manager.GetService("api")
	scanner := &blockingListenerScanner{
		started: make(chan struct{}),
		release: make(chan struct{}),
		listeners: []port.Listener{{
			Protocol: "tcp", Address: "127.0.0.1", Port: 3000, PID: svc.PID(),
		}},
	}
	manager.SetListenerScanner(scanner)
	refreshDone := make(chan struct{})
	go func() {
		manager.refreshDetectedPorts(context.Background())
		close(refreshDone)
	}()
	<-scanner.started
	if err := manager.StopService("api"); err != nil {
		t.Fatal(err)
	}
	close(scanner.release)
	<-refreshDone
	if got := svc.DetectedPorts(); len(got) != 0 {
		t.Fatalf("late snapshot restored stale ports: %v", got)
	}
}

func TestDiscoveryRestartReplacesPriorGenerationPorts(t *testing.T) {
	manager := newSleepingDiscoveryManager(t)
	preflight := &countingPortChecker{}
	manager.SetPortChecker(preflight)
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	svc, _ := manager.GetService("api")
	scanner := &scriptedListenerScanner{results: []listenerSnapshotResult{
		{listeners: []port.Listener{
			{Protocol: "tcp", Port: 3000, PID: svc.PID()},
		}},
	}}
	manager.SetListenerScanner(scanner)
	manager.refreshDetectedPorts(context.Background())
	if got := svc.DetectedPorts(); !reflect.DeepEqual(got, []int{3000}) {
		t.Fatalf("first generation ports = %v", got)
	}

	if err := manager.RestartService("api"); err != nil {
		t.Fatal(err)
	}
	if got := svc.DetectedPorts(); len(got) != 0 {
		t.Fatalf("ports were not cleared on restart: %v", got)
	}
	scanner.SetResults([]listenerSnapshotResult{
		{listeners: []port.Listener{
			{Protocol: "tcp", Port: 4000, PID: svc.PID()},
		}},
	})
	manager.refreshDetectedPorts(context.Background())
	if got := svc.DetectedPorts(); !reflect.DeepEqual(got, []int{4000}) {
		t.Fatalf("second generation ports = %v", got)
	}
	if preflight.calls != 0 {
		t.Fatalf("detected-only service triggered configured-port preflight %d times", preflight.calls)
	}
}

func TestDiscoveryFailureIsNonFatalAndRecoveryReplacesSnapshots(t *testing.T) {
	manager := newSleepingDiscoveryManager(t)
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	svc, _ := manager.GetService("api")
	pid := svc.PID()
	scanner := &scriptedListenerScanner{results: []listenerSnapshotResult{
		{err: errors.New("ss permission denied")},
		{listeners: []port.Listener{
			{Protocol: "tcp", Address: "127.0.0.1", Port: 8080, PID: pid},
			{Protocol: "tcp", Address: "::1", Port: 8080, PID: pid},
		}},
		{listeners: []port.Listener{{Protocol: "tcp", Port: 9090, PID: pid}}},
		{},
	}}
	manager.SetListenerScanner(scanner)
	logsBefore := len(svc.Logs.Lines())

	manager.refreshDetectedPorts(context.Background())
	if svc.Status() != config.StatusRunning || len(svc.DetectedPorts()) != 0 {
		t.Fatalf("scanner failure affected service: status=%s ports=%v", svc.Status(), svc.DetectedPorts())
	}
	if logsAfter := len(svc.Logs.Lines()); logsAfter != logsBefore {
		t.Fatalf("scanner failure added service log spam: before=%d after=%d", logsBefore, logsAfter)
	}

	manager.refreshDetectedPorts(context.Background())
	if got := svc.DetectedPorts(); !reflect.DeepEqual(got, []int{8080}) {
		t.Fatalf("recovered snapshot = %v", got)
	}
	manager.refreshDetectedPorts(context.Background())
	if got := svc.DetectedPorts(); !reflect.DeepEqual(got, []int{9090}) {
		t.Fatalf("replacement snapshot = %v", got)
	}
	manager.refreshDetectedPorts(context.Background())
	if got := svc.DetectedPorts(); len(got) != 0 {
		t.Fatalf("closed listener remained after empty snapshot: %v", got)
	}
}

func TestDiscoveryCadenceRateLimitsFailuresAndStopsOnShutdown(t *testing.T) {
	manager := newSleepingDiscoveryManager(t)
	manager.listenerScanInterval = 25 * time.Millisecond
	scanner := &scriptedListenerScanner{results: []listenerSnapshotResult{{err: errors.New("lsof unavailable")}}}
	manager.SetListenerScanner(scanner)
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	waitForScannerCalls(t, scanner, 2)
	time.Sleep(55 * time.Millisecond)
	if calls := scanner.Calls(); calls > 5 {
		t.Fatalf("scanner failure retried without cadence limit: %d calls", calls)
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	callsAfterShutdown := scanner.Calls()
	time.Sleep(40 * time.Millisecond)
	if calls := scanner.Calls(); calls != callsAfterShutdown {
		t.Fatalf("scanner continued after shutdown: before=%d after=%d", callsAfterShutdown, calls)
	}
}

func TestDiscoveryLoopFindsDelayedListenerAndRemovesClosedListener(t *testing.T) {
	manager := newSleepingDiscoveryManager(t)
	manager.listenerScanInterval = 15 * time.Millisecond
	scanner := &toggleListenerScanner{manager: manager}
	manager.SetListenerScanner(scanner)
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	svc, _ := manager.GetService("api")
	waitForCondition(t, func() bool { return scanner.calls.Load() > 0 }, "initial empty snapshot")
	scanner.active.Store(true)
	waitForCondition(t, func() bool {
		return reflect.DeepEqual(svc.DetectedPorts(), []int{7777})
	}, "delayed listener discovery")
	scanner.active.Store(false)
	waitForCondition(t, func() bool { return len(svc.DetectedPorts()) == 0 }, "closed listener removal")
}

func TestDiscoverySnapshotDoesNotDelayRunningTransition(t *testing.T) {
	manager := newSleepingDiscoveryManager(t)
	scanner := &blockingListenerScanner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	manager.SetListenerScanner(scanner)
	startDone := make(chan error, 1)
	go func() { startDone <- manager.StartService("api") }()
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("StartService waited for listener snapshot")
	}
	<-scanner.started
	svc, _ := manager.GetService("api")
	if svc.Status() != config.StatusRunning {
		t.Fatalf("status = %s, want running", svc.Status())
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func newSleepingDiscoveryManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(&config.Config{Project: "port-discovery", Services: map[string]config.Service{
		"api": {
			Command: "sleep 30",
			Dir:     ".",
			Shell:   "sh",
			Shutdown: config.ShutdownConfig{
				Timeout: time.Second,
				Signal:  int(syscall.SIGTERM),
			},
		},
	}})
}

func waitForScannerCalls(t *testing.T, scanner *scriptedListenerScanner, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if scanner.Calls() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("scanner calls = %d, want at least %d", scanner.Calls(), want)
}

func waitForCondition(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(string(contents))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for PID file %s", path)
	return 0
}
