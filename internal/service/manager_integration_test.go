package service

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/health"
	"github.com/kranz-org/kranz/internal/port"
)

type managedPortScanner struct {
	manager *Manager
	port    int
}

func (s managedPortScanner) Snapshot(context.Context) ([]port.Listener, error) {
	serviceInstance, _ := s.manager.GetService("api")
	return []port.Listener{{Protocol: "tcp", Port: s.port, PID: serviceInstance.PID()}}, nil
}

func TestManagerRestartAndShutdownReleasePort(t *testing.T) {
	portNumber := reservePort(t)
	command := fmt.Sprintf("%s -test.run=^TestKranzPortHelper$ -- %d", strconv.Quote(os.Args[0]), portNumber)
	cfg := &config.Config{
		Project: "lifecycle-test",
		Services: map[string]config.Service{
			"api": {
				Command: command,
				Dir:     ".",
				Shell:   "sh",
				Ports:   []int{portNumber},
				Env:     map[string]string{"KRANZ_PORT_HELPER": "1"},
			},
		},
	}

	manager := NewManager(cfg)
	if err := manager.StartService("api"); err != nil {
		t.Fatalf("StartService() error = %v", err)
	}
	waitForPort(t, portNumber, true)
	serviceInstance, _ := manager.GetService("api")
	firstPID := serviceInstance.PID()

	if err := manager.RestartService("api"); err != nil {
		t.Fatalf("RestartService() error = %v", err)
	}
	waitForPort(t, portNumber, true)
	secondPID := serviceInstance.PID()
	if secondPID == 0 || secondPID == firstPID {
		t.Fatalf("restart did not replace process: before=%d after=%d", firstPID, secondPID)
	}

	if err := manager.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	waitForPort(t, portNumber, false)
	if err := manager.Shutdown(); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
	if err := manager.StartService("api"); err == nil {
		t.Fatal("StartService() succeeded after shutdown began")
	}
}

func TestManagerWiresDetectedPortsIntoDynamicReadiness(t *testing.T) {
	portNumber := reservePort(t)
	command := fmt.Sprintf("%s -test.run=^TestKranzPortHelper$ -- %d", strconv.Quote(os.Args[0]), portNumber)
	cfg := &config.Config{Project: "dynamic-health-test", Services: map[string]config.Service{
		"api": {
			Command: command, Dir: ".", Shell: "sh", Env: map[string]string{"KRANZ_PORT_HELPER": "1"},
			HealthCheck: &config.HealthCheckConfig{Readiness: &config.CheckConfig{
				Type: config.CheckTCP, PortFrom: config.PortFromDetected, Interval: 10 * time.Millisecond, Timeout: 50 * time.Millisecond,
			}},
		},
	}}

	manager := NewManager(cfg)
	checker := health.NewChecker()
	manager.SetHealthChecker(checker)
	manager.SetListenerScanner(managedPortScanner{manager: manager, port: portNumber})
	manager.listenerScanInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		if err := manager.Shutdown(); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})

	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool {
		state := checker.GetHealth("api")
		return state != nil && state.IsReady()
	}, "dynamic readiness to use the manager listener snapshot")
}

func TestKranzPortHelper(t *testing.T) {
	if os.Getenv("KRANZ_PORT_HELPER") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator == 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}
	portNumber, err := strconv.Atoi(os.Args[separator+1])
	if err != nil {
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", portNumber))
	if err != nil {
		os.Exit(3)
	}
	defer listener.Close() //nolint:errcheck // releasing a probe listener during cleanup.
	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		_ = connection.Close()
	}
}

func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	portNumber := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release reserved port: %v", err)
	}
	return portNumber
}

func waitForPort(t *testing.T, portNumber int, occupied bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", portNumber))
		isOccupied := err != nil
		if listener != nil {
			_ = listener.Close()
		}
		if isOccupied == occupied {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("port %d occupied=%v did not become %v", portNumber, !occupied, occupied)
}
