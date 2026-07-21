package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/health"
	"github.com/kranz-org/kranz/internal/port"
)

// Manager coordinates service lifecycles, dependencies, health, and recovery.
type Manager struct {
	services      map[string]*Service
	cfg           *config.Config
	mu            sync.RWMutex
	healthChecker *health.Checker
	portChecker   port.Checker
	shuttingDown  atomic.Bool
	exitRequested atomic.Bool
	exitCode      atomic.Int64
	reloadMu      sync.Mutex
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
			if err := m.StopService(name); err != nil {
				return result, fmt.Errorf("stop removed service %s: %w", name, err)
			}
			result.Removed = append(result.Removed, name)
			continue
		}
		if reflect.DeepEqual(svc.Config, incoming) {
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
		replacement.Logs = svc.Logs
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
	m.cfg = next
	m.mu.Unlock()
	sort.Strings(result.Added)

	if len(runningChanged) > 0 {
		if err := m.StartServices(runningChanged); err != nil {
			return result, fmt.Errorf("restart changed services: %w", err)
		}
		result.Restarted = append(result.Restarted, runningChanged...)
	}
	return result, nil
}

// NewManager creates stopped runtime services from configuration.
func NewManager(cfg *config.Config) *Manager {
	m := &Manager{
		services: make(map[string]*Service),
		cfg:      cfg,
	}

	for name, svcCfg := range cfg.Services {
		m.services[name] = NewService(name, svcCfg, 1000)
	}

	return m
}

// SetHealthChecker configures readiness and liveness monitoring.
func (m *Manager) SetHealthChecker(hc *health.Checker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthChecker = hc
}

// SetPortChecker configures pre-flight listener ownership checks.
func (m *Manager) SetPortChecker(pc port.Checker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.portChecker = pc
}

// Services returns runtime services in stable configuration order.
func (m *Manager) Services() []*Service {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := m.cfg.ServiceNames()
	sort.Strings(names)

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
func (m *Manager) StartService(name string) error {
	return m.startService(name, false)
}

func (m *Manager) startService(name string, recovery bool) error {
	if m.shuttingDown.Load() {
		return errors.New("application is shutting down; new processes are disabled")
	}
	svc, ok := m.GetService(name)
	if !ok {
		return fmt.Errorf("service %q not found", name)
	}
	svc.lifecycleMu.Lock()
	defer svc.lifecycleMu.Unlock()

	if status := svc.Status(); status != config.StatusStopped {
		return fmt.Errorf("service %q is already running", name)
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

	pm := NewProcessManager(1000)

	ctx := context.Background()
	pid, err := pm.Start(ctx, svc.Config.Command, svc.Config.Dir, svc.Config.Env, svc.Config.Shell)
	if err != nil {
		svc.SetDesiredRunning(false)
		svc.SetStatus(config.StatusStopped)
		return fmt.Errorf("start service %q: %w", name, err)
	}

	svc.SetPID(pid)
	svc.SetStatus(config.StatusRunning)
	svc.ResetNewLogCount()

	monitorStop := make(chan struct{})
	svc.setRuntime(pm, monitorStop)

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

// StopService gracefully stops one service and releases its process group.
func (m *Manager) StopService(name string) error {
	svc, ok := m.GetService(name)
	if !ok {
		return fmt.Errorf("service %q not found", name)
	}
	svc.lifecycleMu.Lock()
	defer svc.lifecycleMu.Unlock()
	svc.SetDesiredRunning(false)

	pm, monitorStop := svc.runtime()
	if svc.Status() == config.StatusStopped && pm == nil {
		return nil
	}

	svc.SetStatus(config.StatusStopping)

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
	return stopErr
}

// StartAll starts every enabled service in dependency order.
func (m *Manager) StartAll() error {
	return m.StartServices(m.configSnapshot().ServiceNames())
}

// StartAllContext starts all services and lets callers cancel readiness waits.
func (m *Manager) StartAllContext(ctx context.Context) error {
	return m.StartServicesContext(ctx, m.configSnapshot().ServiceNames())
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
		if svc.Status() != config.StatusStopped {
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
		if !ok || svc.Status() != config.StatusStopped {
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
		if state.Status == config.StatusStopped && state.StartedAt.Equal(intent.startedAt) {
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
		if svc != nil && svc.Status() != config.StatusStopped {
			continue
		}
		if err := m.StartService(name); err != nil {
			startErrors = append(startErrors, fmt.Errorf("%s: %w", name, err))
			if svc, _ := m.GetService(name); svc != nil {
				svc.AppendLog(fmt.Sprintf("[Kranz] Start failed: %v", err))
			}
		}
	}
	return startErrors, nil
}

func (m *Manager) waitForDependencyGroup(ctx context.Context, group []string, selected map[string]bool) error {
	for _, dependencyName := range group {
		if !selected[dependencyName] {
			continue
		}
		conditions := m.conditionsForDependency(dependencyName, selected)
		for _, condition := range conditions {
			if err := m.waitForDependencyCondition(ctx, dependencyName, condition); err != nil {
				m.handleSkippedDependents(dependencyName, selected)
				return err
			}
		}
	}
	return nil
}

func (m *Manager) conditionsForDependency(dependencyName string, selected map[string]bool) []config.DependencyCondition {
	seen := make(map[config.DependencyCondition]bool)
	var result []config.DependencyCondition
	for _, dependent := range m.Services() {
		if !selected[dependent.Name] || !containsName(dependent.Config.DependsOn, dependencyName) {
			continue
		}
		condition := config.DependencyHealthy
		if configured, ok := dependent.Config.DependencyConditions[dependencyName]; ok && configured.Condition != "" {
			condition = configured.Condition
		}
		if !seen[condition] {
			seen[condition] = true
			result = append(result, condition)
		}
	}
	return result
}

func (m *Manager) waitForDependencyCondition(ctx context.Context, name string, condition config.DependencyCondition) error {
	svc, ok := m.GetService(name)
	if !ok {
		return fmt.Errorf("dependency %s disappeared", name)
	}
	if condition == config.DependencyHealthy {
		if m.waitForReadiness(ctx, name, 30*time.Second) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("dependency %s did not become healthy within 30s", name)
	}
	var readyPattern *regexp.Regexp
	if condition == config.DependencyLogReady {
		var err error
		readyPattern, err = regexp.Compile(svc.Config.ReadyLogLine)
		if err != nil {
			return fmt.Errorf("dependency %s ready_log_line: %w", name, err)
		}
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	startedDeadline := time.NewTimer(30 * time.Second)
	defer startedDeadline.Stop()
	for {
		state := svc.GetState()
		switch condition {
		case config.DependencyStarted:
			if !state.StartedAt.IsZero() {
				return nil
			}
		case config.DependencyCompleted:
			if state.Completed {
				return nil
			}
		case config.DependencyCompletedSuccessfully:
			if state.Completed {
				if m.successfulExit(svc.Config, state.ExitCode) {
					return nil
				}
				return fmt.Errorf("dependency %s completed with exit code %d", name, state.ExitCode)
			}
		case config.DependencyLogReady:
			for _, line := range svc.Logs.Lines() {
				if readyPattern.MatchString(line) {
					return nil
				}
			}
		default:
			return fmt.Errorf("dependency %s has unsupported condition %q", name, condition)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-startedDeadline.C:
			if condition == config.DependencyStarted {
				return fmt.Errorf("dependency %s did not start within 30s", name)
			}
			startedDeadline.Reset(24 * time.Hour)
		}
	}
}

func (m *Manager) handleSkippedDependents(dependencyName string, selected map[string]bool) {
	for _, dependent := range m.Services() {
		if selected[dependent.Name] && containsName(dependent.Config.DependsOn, dependencyName) && dependent.Config.Availability.ExitOnSkipped {
			m.requestProjectExit(1)
			return
		}
	}
}

func containsName(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// groupByDependencyLevel groups independent services for parallel readiness gating.
func (m *Manager) groupByDependencyLevel(order []string) [][]string {
	graph := m.configSnapshot().GetDependsOn()
	levels := make(map[string]int)

	// A service level is one more than its deepest dependency.
	for _, name := range order {
		level := 0
		for _, dep := range graph[name] {
			if levels[dep] >= level {
				level = levels[dep] + 1
			}
		}
		levels[name] = level
	}

	// Pre-size the level buckets from the deepest service.
	maxLevel := 0
	for _, lvl := range levels {
		if lvl > maxLevel {
			maxLevel = lvl
		}
	}

	// Services within one level have no ordering dependency on each other.
	groups := make([][]string, maxLevel+1)
	for i := range groups {
		groups[i] = make([]string, 0)
	}
	for _, name := range order {
		lvl := levels[name]
		groups[lvl] = append(groups[lvl], name)
	}

	return groups
}

// StartByTags starts services matching at least one tag and their dependencies.
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
func (m *Manager) StopAll() error {
	return m.StopServices(m.configSnapshot().ServiceNames())
}

// StopServices stops only the requested services, in reverse dependency order.
func (m *Manager) StopServices(names []string) error {
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

	var stopErrors []error
	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]
		if !selected[name] {
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

func (m *Manager) expandWithDependencies(names []string) (map[string]bool, error) {
	selected := make(map[string]bool, len(names))
	var visit func(string) error
	visit = func(name string) error {
		if selected[name] {
			return nil
		}
		svc, ok := m.GetService(name)
		if !ok {
			return fmt.Errorf("service %q not found", name)
		}
		selected[name] = true
		for _, dependency := range svc.Config.DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return selected, nil
}

// Shutdown rejects new starts and stops every child process exactly once.
func (m *Manager) Shutdown() error {
	m.shuttingDown.Store(true)
	return m.StopAll()
}

// RestartService restarts a service and all transitive dependents.
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
func (m *Manager) GetAffectedServices(name string) []string {
	order, err := m.topologicalSort()
	if err != nil {
		return []string{name}
	}
	set := map[string]bool{name: true}
	for _, dependent := range m.findDependents(name) {
		if svc, ok := m.GetService(dependent); ok && svc.Status() != config.StatusStopped {
			set[dependent] = true
		}
	}
	result := make([]string, 0, len(set))
	for _, serviceName := range order {
		if set[serviceName] {
			result = append(result, serviceName)
		}
	}
	return result
}

// topologicalSort orders services with Kahn's algorithm.
func (m *Manager) topologicalSort() ([]string, error) {
	cfg := m.configSnapshot()
	graph := cfg.GetDependsOn()
	names := cfg.ServiceNames()
	inDegree := make(map[string]int, len(names))
	dependents := make(map[string][]string, len(names))

	for _, name := range names {
		inDegree[name] = len(graph[name])
		for _, dependency := range graph[name] {
			dependents[dependency] = append(dependents[dependency], name)
		}
	}

	var queue []string
	for _, name := range names {
		if inDegree[name] == 0 {
			queue = append(queue, name)
		}
	}

	var result []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		for _, dependent := range dependents[node] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(result) != len(names) {
		return nil, errors.New("dependency cycle detected")
	}

	return result, nil
}

// findDependents returns every transitive dependent of a service.
func (m *Manager) findDependents(name string) []string {
	graph := m.configSnapshot().GetDependsOn()
	var result []string
	visited := make(map[string]bool)

	var dfs func(current string)
	dfs = func(current string) {
		for svcName, deps := range graph {
			for _, dep := range deps {
				if dep == current && !visited[svcName] {
					visited[svcName] = true
					result = append(result, svcName)
					dfs(svcName)
				}
			}
		}
	}

	dfs(name)
	return result
}

// monitorProcess drains output, observes completion, and applies recovery policy.
func (m *Manager) monitorProcess(name string, svc *Service, pm *ProcessManager, cancelCh chan struct{}) {
	// Drain process buffers frequently enough to keep the UI responsive.
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-cancelCh:
			m.drainProcessLogs(svc, pm)
			return
		case <-pm.Done():
			m.drainProcessLogs(svc, pm)
			waitErr := pm.Wait()
			exitCode := pm.ExitCode()
			m.mu.RLock()
			hc := m.healthChecker
			m.mu.RUnlock()
			if hc != nil {
				hc.StopMonitoring(name)
			}
			svc.lifecycleMu.Lock()
			shouldEvaluate := false
			if current, _ := svc.runtime(); current == pm && svc.Status() != config.StatusStopping {
				svc.clearRuntime(pm)
				svc.SetStatus(config.StatusStopped)
				svc.SetPID(0)
				svc.RecordExit(exitCode, waitErr)
				shouldEvaluate = true
				if m.successfulExit(svc.Config, exitCode) {
					svc.AppendLog(fmt.Sprintf("[Kranz] Service %s completed with exit code %d", name, exitCode))
				} else {
					svc.AppendLog(fmt.Sprintf("[Kranz] Service %s failed with exit code %d", name, exitCode))
				}
			}
			svc.lifecycleMu.Unlock()
			if shouldEvaluate {
				m.handleNaturalExit(name, svc, exitCode)
			}
			return
		case <-ticker.C:
			m.drainProcessLogs(svc, pm)
		}
	}
}

func (m *Manager) successfulExit(svc config.Service, exitCode int) bool {
	if exitCode == 0 {
		return true
	}
	for _, code := range svc.SuccessExitCodes {
		if code == exitCode {
			return true
		}
	}
	return false
}

func (m *Manager) handleNaturalExit(name string, svc *Service, exitCode int) {
	availability := svc.Config.Availability
	success := m.successfulExit(svc.Config, exitCode)
	restart := availability.Restart == "always" || (availability.Restart == "on_failure" && !success)
	state := svc.GetState()
	restartAllowed := availability.MaxRestarts == 0 || state.RestartCount < availability.MaxRestarts
	if restart && svc.DesiredRunning() && !m.shuttingDown.Load() && restartAllowed {
		attempt := svc.IncrementRestartCount()
		backoff := availability.Backoff
		if backoff <= 0 {
			backoff = time.Second
		}
		svc.AppendLog(fmt.Sprintf("[Kranz] Restarting in %s (attempt %d)", backoff, attempt))
		go func() {
			timer := time.NewTimer(backoff)
			defer timer.Stop()
			<-timer.C
			if !svc.DesiredRunning() || m.shuttingDown.Load() {
				return
			}
			if err := m.startService(name, true); err != nil {
				svc.AppendLog("[Kranz] Automatic restart failed: " + err.Error())
			}
		}()
		return
	}
	if restart && !restartAllowed {
		svc.AppendLog(fmt.Sprintf("[Kranz] Restart limit reached (%d)", availability.MaxRestarts))
	}
	svc.SetDesiredRunning(false)
	if availability.ExitOnEnd || (availability.Restart == "exit_on_failure" && !success) {
		svc.AppendLog("[Kranz] Project stop requested by availability policy")
		requestedCode := exitCode
		if success {
			requestedCode = 0
		}
		m.requestProjectExit(requestedCode)
	}
}

func (m *Manager) requestProjectExit(code int) {
	if !m.exitRequested.CompareAndSwap(false, true) {
		return
	}
	m.exitCode.Store(int64(code))
	go func() { _ = m.StopAll() }()
}

// ProjectExitRequested reports whether an availability policy requested the
// whole Kranz session to terminate, together with its intended exit code.
func (m *Manager) ProjectExitRequested() (bool, int) {
	return m.exitRequested.Load(), int(m.exitCode.Load())
}

func (m *Manager) drainProcessLogs(svc *Service, pm *ProcessManager) {
	for _, line := range pm.Stdout().Drain() {
		svc.AppendLog(line)
	}
	for _, line := range pm.Stderr().Drain() {
		svc.AppendLog(line)
	}
}

// HasRunningServices reports whether any managed service is active or transitioning.
func (m *Manager) HasRunningServices() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, svc := range m.services {
		status := svc.Status()
		if status == config.StatusRunning || status == config.StatusStarting {
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
type PortConflictError struct {
	Service      string
	Port         int
	PID          int
	Process      string
	Command      string
	OwnerService string
	External     bool
}

// Error returns a concise description of the conflicting listener.
func (e *PortConflictError) Error() string {
	return fmt.Sprintf("port %d is occupied by PID %d (%s)", e.Port, e.PID, e.Process)
}

// ManagedServiceForPID returns the Kranz service that owns pid. A service may
// launch the actual listener as a child of its shell, so ownership is matched
// by process group as well as by the recorded leader PID.
func (m *Manager) ManagedServiceForPID(pid int) string {
	if pid <= 0 {
		return ""
	}
	for _, svc := range m.Services() {
		leader := svc.PID()
		if leader > 0 && sameProcessGroup(leader, pid) {
			return svc.Name
		}
	}
	return ""
}

// waitForReadiness blocks until readiness succeeds, times out, or is cancelled.
func (m *Manager) waitForReadiness(ctx context.Context, name string, timeout time.Duration) bool {
	svc, ok := m.GetService(name)
	if !ok {
		return false
	}
	if svc.Config.HealthCheck == nil || svc.Config.HealthCheck.Readiness == nil {
		return svc.Status() != config.StatusStopped
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
