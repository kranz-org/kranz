package service

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/port"
)

type procfilePortScanner struct {
	manager *Manager
	ports   []int
}

func (s procfilePortScanner) Snapshot(context.Context) ([]port.Listener, error) {
	web, ok := s.manager.GetService("web")
	if !ok || web.PID() == 0 {
		return nil, nil
	}
	listeners := make([]port.Listener, 0, len(s.ports))
	for _, portNumber := range s.ports {
		connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", portNumber), 20*time.Millisecond)
		if err != nil {
			continue
		}
		_ = connection.Close()
		listeners = append(listeners, port.Listener{Protocol: "tcp", Port: portNumber, PID: web.PID()})
	}
	return listeners, nil
}

func TestProcfileReleaseScenario(t *testing.T) {
	directory := t.TempDir()
	procfilePath := filepath.Join(directory, "Procfile")
	dotenvPath := filepath.Join(directory, ".env")
	restartMarker := filepath.Join(directory, "restarted")
	reloadMarker := filepath.Join(directory, "worker-reloaded")
	firstPort := reservePort(t)
	secondPort := reservePort(t)
	for secondPort == firstPort {
		secondPort = reservePort(t)
	}

	webCommand := fmt.Sprintf(
		"if [ -f %s ]; then exec %s -test.run=^TestKranzPortHelper$ -- %d; else : > %s; exec %s -test.run=^TestKranzPortHelper$ -- %d; fi",
		shellQuote(restartMarker), shellQuote(os.Args[0]), secondPort,
		shellQuote(restartMarker), shellQuote(os.Args[0]), firstPort,
	)
	originalProcfile := []byte("web: " + webCommand + "\nworker: while true; do sleep 1; done\n")
	originalDotenv := []byte("KRANZ_PORT_HELPER=1\n")
	if err := os.WriteFile(procfilePath, originalProcfile, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dotenvPath, originalDotenv, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(procfilePath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	manager := NewManager(cfg)
	manager.SetListenerScanner(procfilePortScanner{manager: manager, ports: []int{firstPort, secondPort}})
	manager.listenerScanInterval = 10 * time.Millisecond
	t.Cleanup(func() { _ = manager.Shutdown() })

	if err := manager.StartServices([]string{"web", "worker"}); err != nil {
		t.Fatalf("StartServices() error = %v", err)
	}
	waitForPort(t, firstPort, true)
	waitForCondition(t, func() bool {
		web, _ := manager.GetService("web")
		worker, _ := manager.GetService("worker")
		return reflect.DeepEqual(web.DetectedPorts(), []int{firstPort}) && len(worker.DetectedPorts()) == 0
	}, "Procfile web listener discovery without a worker false positive")
	assertFileBytes(t, procfilePath, originalProcfile)
	assertFileBytes(t, dotenvPath, originalDotenv)

	if err := manager.RestartService("web"); err != nil {
		t.Fatalf("RestartService() error = %v", err)
	}
	waitForPort(t, firstPort, false)
	waitForPort(t, secondPort, true)
	waitForCondition(t, func() bool {
		web, _ := manager.GetService("web")
		return reflect.DeepEqual(web.DetectedPorts(), []int{secondPort})
	}, "restart to replace the stale detected port")
	assertFileBytes(t, procfilePath, originalProcfile)
	assertFileBytes(t, dotenvPath, originalDotenv)

	updatedProcfile := []byte("web: " + webCommand + "\nworker: : > " + shellQuote(reloadMarker) + "; while true; do sleep 1; done\n")
	if err := os.WriteFile(procfilePath, updatedProcfile, 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := config.Load(procfilePath)
	if err != nil {
		t.Fatalf("Load(updated Procfile) error = %v", err)
	}
	result, err := manager.ApplyConfig(next)
	if err != nil {
		t.Fatalf("ApplyConfig() error = %v", err)
	}
	if !reflect.DeepEqual(result.Updated, []string{"worker"}) {
		t.Fatalf("ApplyConfig() updated = %v, want [worker]", result.Updated)
	}
	_ = waitForTestFile(t, reloadMarker)
	assertFileBytes(t, procfilePath, updatedProcfile)
	assertFileBytes(t, dotenvPath, originalDotenv)

	if err := manager.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	waitForPort(t, secondPort, false)
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("%s changed unexpectedly\ngot:  %q\nwant: %q", path, actual, want)
	}
}

func TestProcfileCommandRunsInProcfileDirectory(t *testing.T) {
	directory := t.TempDir()
	markerPath := filepath.Join(directory, "actual-cwd")
	procfilePath := filepath.Join(directory, "Procfile")
	command := "pwd > " + shellQuote(markerPath)
	if err := os.WriteFile(procfilePath, []byte("cwd: "+command+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(procfilePath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	manager := NewManager(cfg)
	t.Cleanup(func() { _ = manager.StopAll() })
	if err := manager.StartService("cwd"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var actual []byte
	for time.Now().Before(deadline) {
		actual, err = os.ReadFile(markerPath)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("command did not create cwd marker: %v", err)
	}
	wantDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	actualDirectory, err := filepath.EvalSymlinks(strings.TrimSpace(string(actual)))
	if err != nil {
		t.Fatal(err)
	}
	if actualDirectory != wantDirectory {
		t.Errorf("command cwd = %q, want %q", actualDirectory, wantDirectory)
	}
}

func TestProcfileServiceOrderReachesManager(t *testing.T) {
	cfg := &config.Config{
		Project:      "ordered",
		Services:     map[string]config.Service{"web": {Command: "web"}, "worker": {Command: "worker"}},
		ServiceOrder: []string{"worker", "web"},
	}

	services := NewManager(cfg).Services()
	names := make([]string, 0, len(services))
	for _, managed := range services {
		names = append(names, managed.Name)
	}
	if strings.Join(names, ",") != "worker,web" {
		t.Fatalf("manager service order = %v, want [worker web]", names)
	}
}

func TestProcfileProcessEnvironmentOverridesDotenv(t *testing.T) {
	directory := t.TempDir()
	markerPath := filepath.Join(directory, "environment")
	procfilePath := filepath.Join(directory, "Procfile")
	t.Setenv("KRANZ_PROCFILE_PRECEDENCE", "from-process")
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("KRANZ_PROCFILE_PRECEDENCE=from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := "printf '%s' \"$KRANZ_PROCFILE_PRECEDENCE\" > " + shellQuote(markerPath)
	if err := os.WriteFile(procfilePath, []byte("env: "+command+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(procfilePath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	manager := NewManager(cfg)
	t.Cleanup(func() { _ = manager.StopAll() })
	if err := manager.StartService("env"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}

	actual := waitForTestFile(t, markerPath)
	if string(actual) != "from-process" {
		t.Errorf("process environment = %q, want from-process", actual)
	}
}

func waitForTestFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			return data
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("command did not create %s", path)
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
