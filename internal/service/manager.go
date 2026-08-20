package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/health"
	"github.com/kranz-org/kranz/internal/port"
)

// Manager coordinates service lifecycles, dependencies, health, and recovery.
type Manager struct {
	services             map[string]*Service
	cfg                  *config.Config
	actions              *ActionRunner
	mu                   sync.RWMutex
	healthChecker        *health.Checker
	portChecker          port.Checker
	listenerScanner      port.ListenerScanner
	listenerScanInterval time.Duration
	discoveryMu          sync.Mutex
	discoveryCancel      context.CancelFunc
	discoveryDone        chan struct{}
	shuttingDown         atomic.Bool
	exitRequested        atomic.Bool
	exitCode             atomic.Int64
	reloadMu             sync.Mutex
	statusMu             sync.Mutex
	statusMonitors       map[string]*statusMonitor
	logsMu               sync.Mutex
	detachedLogs         map[string]*detachedLogFollower
	prereqMu             sync.Mutex
	prereqSatisfied      map[config.ActionID]bool
	prereqRuns           map[config.ActionID]*prereqRun
}

type statusMonitor struct {
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}
}

type detachedLogFollower struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// ReloadResult summarizes the services changed by a live configuration reload.
type ReloadResult struct {
	Added     []string
	Removed   []string
	Restarted []string
	Updated   []string
}

// ApplyConfig atomically reconciles a validated configuration with the live
// manager. Unchanged processes keep running; changed running processes are
// stopped, updated, and restarted; removed processes are always stopped first.
func (m *Manager) ApplyConfig(next *config.Config) (ReloadResult, error) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if next == nil {
		return ReloadResult{}, errors.New("new configuration is nil")
	}
	m.stopAllStatusMonitors()
	m.stopAllDetachedLogs()
	reconcileBackground := true
	defer func() {
		if reconcileBackground {
			m.reconcileStatusMonitors(m.configSnapshot())
			m.reconcileDetachedLogs()
		}
	}()
	result := ReloadResult{}
	runningChanged := make([]string, 0)

	m.mu.RLock()
	currentNames := make([]string, 0, len(m.services))
	for name := range m.services {
		currentNames = append(currentNames, name)
	}
	m.mu.RUnlock()
	sort.Strings(currentNames)

	for _, name := range currentNames {
		svc, _ := m.GetService(name)
		incoming, exists := next.Services[name]
		if !exists {
			// An observe-only detached resource cannot be stopped. Removing it
			// only detaches Kranz from the external lifecycle.
			if !svc.Config.IsDetached() || svc.Config.Lifecycle.Stop != nil {
				if err := m.StopService(name); err != nil {
					return result, fmt.Errorf("stop removed service %s: %w", name, err)
				}
			}
			result.Removed = append(result.Removed, name)
			continue
		}
		if sameManagedServiceConfig(svc.Config, incoming) {
			continue
		}
		// Detached resources are external to Kranz. Reload their definition
		// without cycling the external resource, while retaining observed state.
		if svc.Config.IsDetached() && incoming.IsDetached() {
			replacement := NewService(name, incoming, 1000)
			replacement.CopyLogHistoryFrom(svc)
			replacement.HealthHistory = svc.HealthHistory
			replacement.RestoreState(svc.GetState(), svc.DesiredRunning())
			m.mu.Lock()
			m.services[name] = replacement
			m.mu.Unlock()
			result.Updated = append(result.Updated, name)
			continue
		}
		wasRunning := svc.Status() != config.StatusStopped || svc.DesiredRunning()
		if wasRunning {
			if err := m.StopService(name); err != nil {
				return result, fmt.Errorf("stop changed service %s: %w", name, err)
			}
			runningChanged = append(runningChanged, name)
		}
		replacement := NewService(name, incoming, 1000)
		// Keep the visible history across a hot reload without mutating the
		// configuration object observed by process-monitor goroutines.
		replacement.CopyLogHistoryFrom(svc)
		replacement.HealthHistory = svc.HealthHistory
		m.mu.Lock()
		m.services[name] = replacement
		m.mu.Unlock()
		result.Updated = append(result.Updated, name)
	}

	m.mu.Lock()
	for _, name := range result.Removed {
		delete(m.services, name)
	}
	for name, svcConfig := range next.Services {
		if _, exists := m.services[name]; !exists {
			m.services[name] = NewService(name, svcConfig, 1000)
			result.Added = append(result.Added, name)
		}
	}
	previous := m.cfg
	m.cfg = next
	m.mu.Unlock()
	m.forgetChangedPrerequisites(previous, next)
	m.actions.ApplyConfig(next)
	m.reconcileStatusMonitors(next)
	m.reconcileDetachedLogs()
	reconcileBackground = false
	sort.Strings(result.Added)

	if len(runningChanged) > 0 {
		if err := m.StartServices(runningChanged); err != nil {
			return result, fmt.Errorf("restart changed services: %w", err)
		}
		result.Restarted = append(result.Restarted, runningChanged...)
	}
	return result, nil
}

// sameManagedServiceConfig excludes actions because changing a one-shot command
// must not restart its long-running owner. The manager-level config is replaced
// after reconciliation and remains the source of truth for actions.
func sameManagedServiceConfig(current, incoming config.Service) bool {
	current.Actions = nil
	incoming.Actions = nil
	return reflect.DeepEqual(current, incoming)
}

// NewManager creates stopped runtime services from configuration.
func NewManager(cfg *config.Config) *Manager {
	m := &Manager{
		services:             make(map[string]*Service),
		cfg:                  cfg,
		actions:              NewActionRunner(cfg, defaultActionLogBuffer),
		listenerScanInterval: 2 * time.Second,
		statusMonitors:       make(map[string]*statusMonitor),
		detachedLogs:         make(map[string]*detachedLogFollower),
		prereqSatisfied:      make(map[config.ActionID]bool),
		prereqRuns:           make(map[config.ActionID]*prereqRun),
	}

	for name, svcCfg := range cfg.Services {
		m.services[name] = NewService(name, svcCfg, 1000)
	}
	for name, svcCfg := range cfg.Services {
		if svcCfg.Lifecycle.Status != nil {
			m.startStatusMonitor(name)
		}
	}

	return m
}

// SetHealthChecker configures readiness and liveness monitoring.
func (m *Manager) SetHealthChecker(hc *health.Checker) {
	m.mu.Lock()
	m.healthChecker = hc
	m.mu.Unlock()
	if hc != nil {
		hc.SetDetectedPortsProvider(func(name string) []int {
			svc, ok := m.GetService(name)
			if !ok {
				return nil
			}
			return svc.DetectedPorts()
		})
		for _, svc := range m.Services() {
			if svc.Status() == config.StatusRunning || svc.Status() == config.StatusUnhealthy {
				hc.StartMonitoring(svc.Name, svc.Config.HealthCheck)
			}
		}
	}
}

// SetPortChecker configures pre-flight listener ownership checks.
func (m *Manager) SetPortChecker(pc port.Checker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.portChecker = pc
}

// SetListenerScanner configures the runtime listener snapshot source.
func (m *Manager) SetListenerScanner(scanner port.ListenerScanner) {
	m.discoveryMu.Lock()
	defer m.discoveryMu.Unlock()
	m.listenerScanner = scanner
}

// Services returns runtime services in stable configuration order.
func (m *Manager) Services() []*Service {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := m.cfg.ServiceNames()

	result := make([]*Service, 0, len(names))
	for _, name := range names {
		if svc, ok := m.services[name]; ok {
			result = append(result, svc)
		}
	}
	return result
}

// GetService returns a runtime service by name.
func (m *Manager) GetService(name string) (*Service, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	svc, ok := m.services[name]
	return svc, ok
}

func (m *Manager) configSnapshot() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// StartService starts one service after validating ports and dependencies.

func (m *Manager) HasRunningServices() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, svc := range m.services {
		status := svc.Status()
		if status == config.StatusRunning || status == config.StatusStarting || status == config.StatusUnhealthy || status == config.StatusStopping {
			return true
		}
	}
	return false
}

// GetAllTags returns every unique configured service tag.
func (m *Manager) GetAllTags() []string {
	return m.configSnapshot().GetAllTags()
}

// PortConflictError describes the verified owner of a required listening port.

func (e *PortConflictError) Error() string {
	return fmt.Sprintf("port %d is occupied by PID %d (%s)", e.Port, e.PID, e.Process)
}

// ManagedServiceForPID returns the Kranz service that owns pid. A service may
// launch the actual listener as a child of its shell, so ownership is matched
// by process group as well as by the recorded leader PID.
