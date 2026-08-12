package service

import (
	"context"
	"fmt"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/health"
)

func (m *Manager) startStatusMonitor(name string) {
	svc, ok := m.GetService(name)
	if !ok || svc.Config.Lifecycle.Status == nil || m.shuttingDown.Load() {
		return
	}
	m.stopStatusMonitor(name)
	ctx, cancel := context.WithCancel(context.Background())
	monitor := &statusMonitor{cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	m.statusMu.Lock()
	m.statusMonitors[name] = monitor
	m.statusMu.Unlock()
	go m.monitorDetachedStatus(ctx, svc, monitor)
}

func (m *Manager) reconcileStatusMonitors(cfg *config.Config) {
	m.stopAllStatusMonitors()
	for name, serviceConfig := range cfg.Services {
		if serviceConfig.Lifecycle.Status != nil {
			m.startStatusMonitor(name)
		}
	}
}

func (m *Manager) stopStatusMonitor(name string) {
	m.statusMu.Lock()
	monitor := m.statusMonitors[name]
	delete(m.statusMonitors, name)
	m.statusMu.Unlock()
	if monitor != nil {
		monitor.cancel()
		<-monitor.done
	}
}

func (m *Manager) stopAllStatusMonitors() {
	m.statusMu.Lock()
	monitors := make([]*statusMonitor, 0, len(m.statusMonitors))
	for name, monitor := range m.statusMonitors {
		monitors = append(monitors, monitor)
		delete(m.statusMonitors, name)
	}
	m.statusMu.Unlock()
	for _, monitor := range monitors {
		monitor.cancel()
	}
	for _, monitor := range monitors {
		<-monitor.done
	}
}

func (m *Manager) wakeStatusMonitor(name string) {
	m.statusMu.Lock()
	monitor := m.statusMonitors[name]
	m.statusMu.Unlock()
	if monitor != nil {
		select {
		case monitor.wake <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) monitorDetachedStatus(ctx context.Context, svc *Service, monitor *statusMonitor) {
	defer close(monitor.done)
	failures := 0
	for {
		interval := m.reconcileDetachedStatus(ctx, svc, &failures)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-monitor.wake:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

func (m *Manager) reconcileDetachedStatus(ctx context.Context, svc *Service, failures *int) time.Duration {
	statusConfig := svc.Config.Lifecycle.Status
	if statusConfig == nil {
		return time.Minute
	}
	current := svc.Status()
	if current == config.StatusStarting || current == config.StatusStopping {
		return 200 * time.Millisecond
	}
	generation := svc.LifecycleGeneration()
	result, runErr := health.RunCommandCheck(ctx, statusConfig.Command, statusConfig.Timeout)
	if ctx.Err() != nil {
		return statusPollInterval(statusConfig, current)
	}
	if generation != svc.LifecycleGeneration() || svc.Status() == config.StatusStarting || svc.Status() == config.StatusStopping {
		return 200 * time.Millisecond
	}
	svc.markLifecycleStatusObserved()
	observed, classified := classifyStatusResult(statusConfig, result.ExitCode)
	if classified {
		*failures = 0
		m.applyObservedStatus(svc, observed)
	} else {
		*failures++
		if *failures >= statusFailureThreshold(statusConfig.FailureThreshold) {
			if svc.Status() != config.StatusUnknown {
				message := fmt.Sprintf("unexpected exit code %d", result.ExitCode)
				if runErr != nil {
					message = runErr.Error()
				}
				svc.AppendLog("[Kranz] Status unavailable: " + message)
			}
			svc.SetStatus(config.StatusUnknown)
			m.stopServiceHealth(svc.Name)
		}
	}
	return statusPollInterval(statusConfig, svc.Status())
}

// classifyStatusResult maps one probe exit code onto lifecycle state.
//
// The default contract is the one every shell command already follows: exit 0
// means the resource is running, and any other exit code means it is stopped.
// Declaring stopped_exit_codes opts into a three-way contract for probes that
// can also report "I could not tell", where an unlisted code is unclassified
// and becomes unknown once failure_threshold consecutive probes agree.
//
// A negative exit code means the probe never produced an exit status at all —
// it could not be spawned, timed out, or was killed by a signal. That is never
// evidence about the resource, so it is always unclassified.
func classifyStatusResult(cfg *config.LifecycleStatusConfig, exitCode int) (config.ServiceStatus, bool) {
	if exitCode < 0 {
		return config.StatusUnknown, false
	}
	running := cfg.RunningExitCodes
	if len(running) == 0 {
		running = []int{0}
	}
	for _, code := range running {
		if exitCode == code {
			return config.StatusRunning, true
		}
	}
	if len(cfg.StoppedExitCodes) == 0 {
		return config.StatusStopped, true
	}
	for _, code := range cfg.StoppedExitCodes {
		if exitCode == code {
			return config.StatusStopped, true
		}
	}
	return config.StatusUnknown, false
}

func (m *Manager) applyObservedStatus(svc *Service, observed config.ServiceStatus) {
	previous := svc.Status()
	if previous == observed {
		return
	}
	svc.SetStatus(observed)
	svc.SetDesiredRunning(observed == config.StatusRunning)
	svc.AppendLog("[Kranz] Status observed: " + observed.String())
	if observed == config.StatusRunning {
		m.startServiceHealth(svc)
		m.startDetachedLogs(svc)
	} else {
		m.stopServiceHealth(svc.Name)
		m.stopDetachedLogs(svc.Name)
	}
}

func (m *Manager) startServiceHealth(svc *Service) {
	m.mu.RLock()
	checker := m.healthChecker
	m.mu.RUnlock()
	if checker != nil {
		checker.StartMonitoring(svc.Name, svc.Config.HealthCheck)
	}
}

func (m *Manager) stopServiceHealth(name string) {
	m.mu.RLock()
	checker := m.healthChecker
	m.mu.RUnlock()
	if checker != nil {
		checker.StopMonitoring(name)
	}
}

func statusFailureThreshold(value int) int {
	if value <= 0 {
		return config.DefaultCheckFailureThreshold
	}
	return value
}

func statusPollInterval(cfg *config.LifecycleStatusConfig, status config.ServiceStatus) time.Duration {
	if status == config.StatusRunning || status == config.StatusUnhealthy {
		if cfg.Interval > 0 {
			return cfg.Interval
		}
		return config.DefaultCheckInterval
	}
	if cfg.StoppedInterval > 0 {
		return cfg.StoppedInterval
	}
	return config.DefaultStoppedStatusInterval
}
