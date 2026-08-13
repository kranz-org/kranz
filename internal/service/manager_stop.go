package service

import (
	"context"
	"errors"
	"fmt"
	"syscall"

	"github.com/kranz-org/kranz/internal/config"
)

// Stopping services and shutting the manager down, including the ordered stop
// of detached resources that declare their own stop command.

func (m *Manager) StopService(name string) error {
	svc, ok := m.GetService(name)
	if !ok {
		return fmt.Errorf("service %q not found", name)
	}
	svc.lifecycleMu.Lock()
	defer svc.lifecycleMu.Unlock()
	if svc.Config.IsDetached() {
		return m.stopDetachedService(svc)
	}
	svc.SetDesiredRunning(false)

	pm, monitorStop := svc.runtime()
	if svc.Status() == config.StatusStopped && pm == nil {
		return nil
	}

	svc.SetStatus(config.StatusStopping)
	svc.AppendLog("[Kranz] Stopping")

	if monitorStop != nil {
		close(monitorStop)
	}

	m.mu.RLock()
	hc := m.healthChecker
	m.mu.RUnlock()
	if hc != nil {
		hc.StopMonitoring(name)
	}

	var stopErr error
	if pm != nil {
		shutdown := svc.Config.Shutdown
		stopErr = pm.StopWithOptions(StopOptions{
			Command: shutdown.Command, Timeout: shutdown.Timeout, Signal: syscall.Signal(shutdown.Signal),
			ParentOnly: shutdown.ParentOnly, Dir: svc.Config.Dir, Env: svc.Config.Env, Shell: svc.Config.Shell,
		})
		m.drainProcessLogs(svc, pm)
		svc.clearRuntime(pm)
	}
	if stopErr != nil {
		svc.AppendLog(fmt.Sprintf("[Kranz] Failed to stop %s: %v", name, stopErr))
	}

	svc.SetPID(0)
	svc.SetStatus(config.StatusStopped)
	svc.AppendLog("[Kranz] Stopped")
	return stopErr
}

func (m *Manager) stopDetachedService(svc *Service) error {
	if svc.Status() == config.StatusStopped {
		return nil
	}
	stop := svc.Config.Lifecycle.Stop
	if stop == nil {
		return fmt.Errorf("service %q has no stop capability", svc.Name)
	}
	svc.SetDesiredRunning(false)
	svc.SetStatus(config.StatusStopping)
	svc.AppendLog("[Kranz] Stopping detached service")
	m.stopDetachedLogs(svc.Name)
	m.mu.RLock()
	hc := m.healthChecker
	m.mu.RUnlock()
	if hc != nil {
		hc.StopMonitoring(svc.Name)
	}
	id := lifecycleActionID(svc.Name, "stop")
	result, err := m.actions.RunDefinition(context.Background(), id, *stop)
	m.appendLifecycleResult(svc, "stop", result)
	if err != nil {
		svc.SetStatus(config.StatusUnknown)
		m.wakeStatusMonitor(svc.Name)
		return fmt.Errorf("stop detached service %q: %w", svc.Name, err)
	}
	svc.SetPID(0)
	svc.SetStatus(config.StatusStopped)
	m.wakeStatusMonitor(svc.Name)
	return nil
}

func lifecycleActionID(serviceName, operation string) config.ActionID {
	return config.ActionID{OwnerKind: config.ActionOwnerLifecycle, Owner: serviceName, Name: operation}
}

func (m *Manager) appendLifecycleResult(svc *Service, operation string, result ActionResult) {
	// Compose and similar tools render progress to stderr even on success. A
	// successful lifecycle operation only needs its concise boundary; keep
	// command output for failures where it is diagnostic.
	if result.Status != ActionSucceeded {
		lines := append([]string(nil), result.Stdout...)
		for _, line := range result.Stderr {
			lines = append(lines, "[stderr] "+line)
		}
		const maxLifecycleFailureLines = 40
		if omitted := len(lines) - maxLifecycleFailureLines; omitted > 0 {
			svc.AppendLog(fmt.Sprintf("[Kranz] %d earlier lifecycle output lines omitted", omitted))
			lines = lines[omitted:]
		}
		for _, line := range lines {
			svc.AppendLog(line)
		}
	}
	var resultErr error
	if result.Error != "" {
		resultErr = errors.New(result.Error)
	}
	svc.RecordExit(result.ExitCode, resultErr)
	svc.AppendLog(fmt.Sprintf("[Kranz] Lifecycle %s %s · exit %d", operation, result.Status.String(), result.ExitCode))
}

// StartAll starts every enabled service in dependency order.

func (m *Manager) StopAll() error {
	return m.stopServices(m.configSnapshot().ServiceNames(), false)
}

// StopServices stops the requested services and every transitive dependent, in
// reverse dependency order.

func (m *Manager) StopServices(names []string) error {
	return m.stopServices(names, true)
}

// ForceStopServices stops exactly the requested services without expanding
// dependents. It is the shutdown counterpart to ForceStartServices.

func (m *Manager) ForceStopServices(names []string) error {
	return m.stopServices(names, false)
}

func (m *Manager) stopServices(names []string, includeDependents bool) error {
	order, err := m.topologicalSort()
	if err != nil {
		order = m.configSnapshot().ServiceNames()
	}
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		if _, ok := m.GetService(name); !ok {
			return fmt.Errorf("service %q not found", name)
		}
		selected[name] = true
	}
	if includeDependents {
		for _, name := range names {
			for _, dependent := range m.findDependents(name) {
				selected[dependent] = true
			}
		}
	}

	var stopErrors []error
	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]
		if !selected[name] {
			continue
		}
		if svc, ok := m.GetService(name); ok && svc.Config.IsDetached() && !svc.CanStop() {
			continue
		}
		if err := m.StopService(name); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("%s: %w", name, err))
			svc, _ := m.GetService(name)
			if svc != nil {
				svc.AppendLog(fmt.Sprintf("[Kranz] Stop failed: %v", err))
			}
		}
	}

	return errors.Join(stopErrors...)
}

func (m *Manager) Shutdown() error {
	m.shuttingDown.Store(true)
	m.stopListenerDiscovery()
	m.stopAllStatusMonitors()
	m.stopAllDetachedLogs()
	m.actions.CancelActive()
	names := make([]string, 0)
	for _, svc := range m.Services() {
		if svc.Config.StopOnExitEnabled() {
			names = append(names, svc.Name)
		}
	}
	err := m.ForceStopServices(names)
	m.actions.Shutdown()
	return err
}

// RestartService restarts a service and all transitive dependents.
