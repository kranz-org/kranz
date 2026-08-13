package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// Starting services: one service, a selection, a tag, or everything, with the
// dependency graph expanded and prerequisites run before each start.

func (m *Manager) StartService(name string) error {
	return m.startService(context.Background(), name, false)
}

func (m *Manager) startService(ctx context.Context, name string, recovery bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if m.shuttingDown.Load() {
		return errors.New("application is shutting down; new processes are disabled")
	}
	svc, ok := m.GetService(name)
	if !ok {
		return fmt.Errorf("service %q not found", name)
	}
	svc.lifecycleMu.Lock()
	defer svc.lifecycleMu.Unlock()
	if svc.Config.IsDetached() {
		if alreadyRunning(svc.Status()) {
			return m.startDetachedService(ctx, svc)
		}
		if err := m.runPrerequisites(ctx, svc); err != nil {
			return err
		}
		return m.startDetachedService(ctx, svc)
	}

	if status := svc.Status(); status != config.StatusStopped {
		return fmt.Errorf("service %q is already running", name)
	}
	// Prerequisites run after dependencies are ready and before this service
	// starts, so a failed prerequisite leaves the service stopped rather than
	// starting it against an unprepared environment.
	if err := m.runPrerequisites(ctx, svc); err != nil {
		return err
	}
	svc.SetDesiredRunning(true)
	if !recovery {
		svc.ResetRestartCount()
	}
	if m.shuttingDown.Load() {
		svc.SetDesiredRunning(false)
		return errors.New("application is shutting down; new processes are disabled")
	}

	// Pre-flight port check
	m.mu.RLock()
	pc := m.portChecker
	m.mu.RUnlock()
	if pc != nil && len(svc.Config.Ports) > 0 {
		portsInfo, err := pc.CheckPorts(svc.Config.Ports)
		if err == nil {
			for _, port := range svc.Config.Ports {
				if info, ok := portsInfo[port]; ok && info != nil {
					owner := m.ManagedServiceForPID(info.PID)
					svc.SetDesiredRunning(false)
					return &PortConflictError{
						Service:      name,
						Port:         port,
						PID:          info.PID,
						Process:      info.Process,
						Command:      info.Command,
						OwnerService: owner,
						External:     owner == "",
					}
				}
			}
		}
	}

	svc.SetStatus(config.StatusStarting)
	svc.AppendLog("[Kranz] Starting")

	pm := NewProcessManager(1000)
	start := svc.Config.StartAction()
	if start == nil {
		svc.SetDesiredRunning(false)
		svc.SetStatus(config.StatusStopped)
		return fmt.Errorf("service %q has no start capability", name)
	}

	pid, err := pm.Start(ctx, start.Command, start.Dir, start.Env, start.Shell)
	if err != nil {
		svc.SetDesiredRunning(false)
		svc.SetStatus(config.StatusStopped)
		svc.AppendLog("[Kranz] Start failed: " + err.Error())
		return fmt.Errorf("start service %q: %w", name, err)
	}

	svc.SetPID(pid)
	svc.SetStatus(config.StatusRunning)
	svc.AppendLog(fmt.Sprintf("[Kranz] Started · PID %d", pid))
	svc.ResetNewLogCount()

	monitorStop := make(chan struct{})
	svc.setRuntime(pm, monitorStop)
	if svc.Config.PortDiscoveryEnabled() {
		m.ensureListenerDiscovery()
	}

	m.mu.RLock()
	hc := m.healthChecker
	m.mu.RUnlock()
	if hc != nil {
		hc.StartMonitoring(name, svc.Config.HealthCheck)
	}

	// Output monitoring owns process completion and recovery transitions.
	go m.monitorProcess(name, svc, pm, monitorStop)

	return nil
}

// alreadyRunning reports states in which a detached start is a no-op, so that
// prerequisites are not run again for a resource that is already up.

func alreadyRunning(status config.ServiceStatus) bool {
	return status == config.StatusRunning || status == config.StatusUnhealthy
}

func (m *Manager) startDetachedService(ctx context.Context, svc *Service) error {
	start := svc.Config.Lifecycle.Start
	if start == nil {
		return fmt.Errorf("service %q has no start capability", svc.Name)
	}
	status := svc.Status()
	if status == config.StatusRunning || status == config.StatusUnhealthy {
		return nil
	}
	if status != config.StatusStopped && status != config.StatusUnknown {
		return fmt.Errorf("service %q is already running", svc.Name)
	}
	if status == config.StatusUnknown && svc.Config.Lifecycle.Status != nil {
		failures := 0
		m.reconcileDetachedStatus(ctx, svc, &failures)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if observed := svc.Status(); observed == config.StatusRunning || observed == config.StatusUnhealthy {
			return nil
		}
	}
	svc.SetDesiredRunning(true)
	svc.SetStatus(config.StatusStarting)
	svc.AppendLog("[Kranz] Starting detached service")
	id := lifecycleActionID(svc.Name, "start")
	result, err := m.actions.RunDefinition(ctx, id, *start)
	m.appendLifecycleResult(svc, "start", result)
	if err != nil {
		svc.SetDesiredRunning(false)
		// A detached start can mutate external state before failing, timing out,
		// or being canceled. Never claim it is stopped without observing that.
		svc.SetStatus(config.StatusUnknown)
		return fmt.Errorf("start detached service %q: %w", svc.Name, err)
	}
	svc.SetPID(0)
	svc.SetStatus(config.StatusRunning)
	m.mu.RLock()
	hc := m.healthChecker
	m.mu.RUnlock()
	if hc != nil {
		hc.StartMonitoring(svc.Name, svc.Config.HealthCheck)
	}
	m.startDetachedLogs(svc)
	m.wakeStatusMonitor(svc.Name)
	return nil
}

// StopService gracefully stops one service and releases its process group.

func (m *Manager) StartAll() error {
	return m.StartServices(m.enabledServiceNames())
}

// enabledServiceNames lists the services a "start everything" operation may
// touch. A disabled service is excluded: it remains startable by name, which is
// the difference between hidden and manual.

func (m *Manager) enabledServiceNames() []string {
	cfg := m.configSnapshot()
	names := make([]string, 0, len(cfg.Services))
	for _, name := range cfg.ServiceNames() {
		if !cfg.Services[name].Disabled {
			names = append(names, name)
		}
	}
	return names
}

// StartAllContext starts all services and lets callers cancel readiness waits.

func (m *Manager) StartAllContext(ctx context.Context) error {
	return m.StartServicesContext(ctx, m.enabledServiceNames())
}

// StartServices starts the requested services and any dependencies they require.
// Services outside that dependency closure are left untouched.

func (m *Manager) StartServices(names []string) error {
	return m.StartServicesContext(context.Background(), names)
}

// ForceStartServices starts exactly the requested stopped services without
// expanding or waiting for dependencies. Normal port and process ownership
// checks still apply.

func (m *Manager) ForceStartServices(names []string) error {
	unique := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		if _, ok := m.GetService(name); !ok {
			return fmt.Errorf("service %q not found", name)
		}
		seen[name] = true
		unique = append(unique, name)
	}

	var startErrors []error
	for _, name := range unique {
		svc, _ := m.GetService(name)
		if !svc.CanStart() {
			continue
		}
		if err := m.StartService(name); err != nil {
			wrapped := fmt.Errorf("%s: %w", name, err)
			startErrors = append(startErrors, wrapped)
			svc.AppendLog(fmt.Sprintf("[Kranz] Force start failed: %v", err))
		}
	}
	return errors.Join(startErrors...)
}

// StartServicesContext starts a dependency closure and stops launching new
// processes as soon as the context is canceled.

func (m *Manager) StartServicesContext(ctx context.Context, names []string) error {
	order, err := m.topologicalSort()
	if err != nil {
		return err
	}
	selected, err := m.expandWithDependencies(names)
	if err != nil {
		return err
	}
	queued := m.queuePendingStarts(selected)
	defer m.clearPendingStarts(queued)

	groups := m.groupByDependencyLevel(order)
	var startErrors []error

	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return errors.Join(errors.Join(startErrors...), err)
		}
		groupErrors, err := m.startDependencyGroup(ctx, group, selected)
		startErrors = append(startErrors, groupErrors...)
		if err != nil {
			return errors.Join(errors.Join(startErrors...), err)
		}
		if err := m.waitForDependencyGroup(ctx, group, selected); err != nil {
			return errors.Join(errors.Join(startErrors...), err)
		}
	}

	return errors.Join(startErrors...)
}

type pendingStartIntent struct {
	name      string
	startedAt time.Time
}

// queuePendingStarts exposes the complete dependency closure before the first
// dependency gate blocks. DesiredRunning is already the lifecycle source of
// truth, so the UI can render this intent without inventing another state.

func (m *Manager) queuePendingStarts(selected map[string]bool) []pendingStartIntent {
	queued := make([]pendingStartIntent, 0, len(selected))
	for name := range selected {
		svc, ok := m.GetService(name)
		if !ok || !svc.CanStart() {
			continue
		}
		startedAt := svc.GetState().StartedAt
		svc.SetDesiredRunning(true)
		queued = append(queued, pendingStartIntent{name: name, startedAt: startedAt})
	}
	return queued
}

func (m *Manager) clearPendingStarts(queued []pendingStartIntent) {
	for _, intent := range queued {
		svc, ok := m.GetService(intent.name)
		if !ok {
			continue
		}
		state := svc.GetState()
		if (state.Status == config.StatusStopped || state.Status == config.StatusUnknown) && state.StartedAt.Equal(intent.startedAt) {
			svc.SetDesiredRunning(false)
		}
	}
}

func (m *Manager) startDependencyGroup(ctx context.Context, group []string, selected map[string]bool) ([]error, error) {
	var startErrors []error
	for _, name := range group {
		if !selected[name] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return startErrors, err
		}
		svc, _ := m.GetService(name)
		if svc != nil && !svc.CanStart() {
			continue
		}
		if err := m.startService(ctx, name, false); err != nil {
			startErrors = append(startErrors, fmt.Errorf("%s: %w", name, err))
			if svc, _ := m.GetService(name); svc != nil {
				svc.AppendLog(fmt.Sprintf("[Kranz] Start failed: %v", err))
			}
		}
	}
	return startErrors, nil
}

func (m *Manager) StartByTags(tags []string) error {
	names := m.configSnapshot().GetServicesByTags(tags)
	if len(names) == 0 {
		return fmt.Errorf("no services match tags %v", tags)
	}

	var startErrors []error
	for _, name := range names {
		if err := m.StartService(name); err != nil {
			startErrors = append(startErrors, fmt.Errorf("%s: %w", name, err))
			svc, _ := m.GetService(name)
			if svc != nil {
				svc.AppendLog(fmt.Sprintf("[Kranz] Start failed: %v", err))
			}
		}
	}
	return errors.Join(startErrors...)
}

// StopAll stops every service in reverse dependency order.

func (m *Manager) RestartService(name string) error {
	// Hold reloadMu for the whole operation so a concurrent config reload
	// cannot swap or remove services mid-restart out from under this plan.
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if _, ok := m.GetService(name); !ok {
		return fmt.Errorf("service %q not found", name)
	}

	order, err := m.topologicalSort()
	if err != nil {
		return err
	}
	affectedSet := map[string]bool{name: true}
	for _, dependent := range m.findDependents(name) {
		if svc, ok := m.GetService(dependent); ok && svc.Status() != config.StatusStopped {
			affectedSet[dependent] = true
		}
	}
	var affected []string
	for _, serviceName := range order {
		if affectedSet[serviceName] {
			affected = append(affected, serviceName)
		}
	}

	// Stop dependents before their dependencies.
	for i := len(affected) - 1; i >= 0; i-- {
		if err := m.StopService(affected[i]); err != nil {
			return fmt.Errorf("stop service %q: %w", affected[i], err)
		}
	}

	// Start dependencies before their dependents.
	for _, n := range affected {
		if err := m.StartService(n); err != nil {
			return fmt.Errorf("start service %q: %w", n, err)
		}
	}

	return nil
}

// RestartAll restarts only services that were active when the operation began.

func (m *Manager) RestartAll() error {
	// See RestartService: block a concurrent reload for the whole operation.
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	order, err := m.topologicalSort()
	if err != nil {
		return err
	}
	running := make(map[string]bool)
	for _, name := range order {
		if svc, ok := m.GetService(name); ok && svc.Status() != config.StatusStopped {
			running[name] = true
		}
	}
	for i := len(order) - 1; i >= 0; i-- {
		if running[order[i]] {
			if err := m.StopService(order[i]); err != nil {
				return err
			}
		}
	}
	for _, name := range order {
		if running[name] {
			if err := m.StartService(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetAffectedServices returns the restart target followed by transitive dependents.

func (m *Manager) waitForReadiness(ctx context.Context, name string, timeout time.Duration) bool {
	svc, ok := m.GetService(name)
	if !ok {
		return false
	}
	if svc.Config.HealthCheck == nil || svc.Config.HealthCheck.Readiness == nil {
		status := svc.Status()
		return status == config.StatusRunning || status == config.StatusUnhealthy
	}

	m.mu.RLock()
	hc := m.healthChecker
	m.mu.RUnlock()

	if hc == nil {
		return true // Without a checker there is no readiness gate.
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-ticker.C:
		}
		if m.shuttingDown.Load() {
			return false
		}
		if svc, ok := m.GetService(name); !ok || svc.Status() == config.StatusStopped {
			return false
		}
		health := hc.GetHealth(name)
		if health != nil && health.IsReady() {
			return true
		}
	}
}
