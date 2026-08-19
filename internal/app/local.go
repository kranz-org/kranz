package app

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/health"
	"github.com/kranz-org/kranz/internal/port"
	"github.com/kranz-org/kranz/internal/service"
)

// Options configures the runtime a Local wires up. A zero value is the
// production default: real health and port checkers.
type Options struct {
	PortChecker     port.Checker
	HealthChecker   *health.Checker
	ListenerScanner port.ListenerScanner
}

// Local implements API directly over a service.Manager living in this
// process. It is the only place in Kranz that constructs a Manager, a
// health.Checker, and a port.Checker, and it owns the configuration
// hot-reload pipeline that used to live in the TUI.
type Local struct {
	manager       *service.Manager
	healthChecker *health.Checker

	portMu      sync.RWMutex
	portChecker port.Checker

	// cfgMu guards every field a config reload replaces atomically: the
	// effective configuration, the reload bookkeeping, and the watched-file
	// stamps used to detect a change cheaply.
	cfgMu          sync.RWMutex
	cfg            *config.Config
	configPaths    []string
	watchPaths     []string
	generation     uint64
	loadedAt       time.Time
	lastReloadErr  string
	reloadBusy     bool
	lastConfigScan time.Time
	stamps         map[string]configStamp
}

// NewLocal constructs the runtime for one project and starts it observing
// health and ports. configPaths is the set of files Reload re-reads; it
// defaults to cfg.Paths when empty, matching how Kranz discovered the
// configuration in the first place.
func NewLocal(cfg *config.Config, configPaths []string, opts Options) *Local {
	manager := service.NewManager(cfg)

	healthChecker := opts.HealthChecker
	if healthChecker == nil {
		healthChecker = health.NewChecker()
	}
	portChecker := opts.PortChecker
	if portChecker == nil {
		portChecker = port.NewChecker()
	}
	listenerScanner := opts.ListenerScanner
	if listenerScanner == nil {
		listenerScanner = port.NewListenerScanner()
	}
	manager.SetHealthChecker(healthChecker)
	manager.SetPortChecker(portChecker)
	manager.SetListenerScanner(listenerScanner)

	paths := append([]string(nil), configPaths...)
	if len(paths) == 0 {
		paths = append([]string(nil), cfg.Paths...)
	}
	watchPaths := watchedConfigPaths(paths, cfg.WatchPaths)
	stamps, _ := readConfigStamps(watchPaths)

	return &Local{
		manager:       manager,
		healthChecker: healthChecker,
		portChecker:   portChecker,
		cfg:           cfg,
		configPaths:   paths,
		watchPaths:    watchPaths,
		generation:    1,
		loadedAt:      time.Now(),
		stamps:        stamps,
	}
}

var _ API = (*Local)(nil)

func (l *Local) Config() *config.Config {
	l.cfgMu.RLock()
	defer l.cfgMu.RUnlock()
	return l.cfg
}

func (l *Local) Project() ProjectSnapshot {
	l.cfgMu.RLock()
	defer l.cfgMu.RUnlock()
	return ProjectSnapshot{
		Name:            l.cfg.Project,
		Source:          l.cfg.Source,
		ConfigPaths:     append([]string(nil), l.configPaths...),
		WatchPaths:      append([]string(nil), l.watchPaths...),
		Generation:      l.generation,
		LoadedAt:        l.loadedAt,
		LastReloadError: l.lastReloadErr,
	}
}

func (l *Local) snapshotOf(svc *service.Service) *ServiceSnapshot {
	snapshot := &ServiceSnapshot{
		Name:           svc.Name,
		Config:         svc.Config,
		State:          svc.GetState(),
		DetectedPorts:  svc.DetectedPorts(),
		DesiredRunning: svc.DesiredRunning(),
		StatusObserved: svc.LifecycleStatusObserved(),
		CanStart:       svc.CanStart(),
		CanStop:        svc.CanStop(),
	}
	if l.healthChecker != nil {
		if h := l.healthChecker.GetHealth(svc.Name); h != nil {
			snapshot.Health = HealthSnapshot{
				Observed:   true,
				Ready:      h.IsReady(),
				Alive:      h.IsAlive(),
				ReadySince: h.GetReadySince(),
				LastCheck:  h.GetLastCheck(),
			}
		}
	}
	return snapshot
}

func (l *Local) Services() []*ServiceSnapshot {
	services := l.manager.Services()
	snapshots := make([]*ServiceSnapshot, len(services))
	for i, svc := range services {
		snapshots[i] = l.snapshotOf(svc)
	}
	return snapshots
}

func (l *Local) Service(name string) (*ServiceSnapshot, bool) {
	svc, ok := l.manager.GetService(name)
	if !ok {
		return nil, false
	}
	return l.snapshotOf(svc), true
}

func (l *Local) Tags() []string {
	return l.Config().GetAllTags()
}

func (l *Local) ManagedServiceForPID(pid int) string {
	return l.manager.ManagedServiceForPID(pid)
}

// StartConfirmationNames mirrors the dependency walk the dashboard used to
// perform itself: it visits names (and, when includeDependencies is true,
// their transitive config.Service.DependsOn) depth-first, collecting every
// startable service whose start action requires confirmation in the order
// its dependencies are visited before it.
func (l *Local) StartConfirmationNames(names []string, includeDependencies bool) []string {
	cfg := l.Config()
	selected := make(map[string]bool)
	confirmed := make([]string, 0, len(names))
	var visit func(string)
	visit = func(name string) {
		if selected[name] {
			return
		}
		selected[name] = true
		if includeDependencies {
			for _, dependency := range cfg.Services[name].DependsOn {
				visit(dependency)
			}
		}
		snapshot, ok := l.Service(name)
		if !ok || !snapshot.CanStart {
			return
		}
		if start := snapshot.Config.StartAction(); start != nil && start.ConfirmationRequired() {
			confirmed = append(confirmed, name)
		}
	}
	for _, name := range names {
		visit(name)
	}
	return confirmed
}

func (l *Local) RequiresStopConfirmation(names []string) bool {
	for _, name := range names {
		if snapshot, ok := l.Service(name); ok && snapshot.CanStop {
			return true
		}
	}
	return false
}

func (l *Local) AffectedServices(name string) []string {
	return l.manager.GetAffectedServices(name)
}

func (l *Local) ShutdownPlan() ShutdownPlan {
	var plan ShutdownPlan
	for _, svc := range l.Services() {
		if !ServiceStartPlanned(svc) {
			continue
		}
		switch {
		case !svc.Config.IsDetached():
			plan.Managed = append(plan.Managed, svc.Name)
		case svc.Config.StopOnExitEnabled():
			plan.DetachedStop = append(plan.DetachedStop, svc.Name)
		default:
			plan.DetachedKeep = append(plan.DetachedKeep, svc.Name)
		}
	}
	return plan
}

func (l *Local) StartServicesContext(ctx context.Context, names []string) error {
	return l.manager.StartServicesContext(ctx, names)
}

func (l *Local) StopServices(names []string) error {
	return l.manager.StopServices(names)
}

func (l *Local) ForceStopServices(names []string) error {
	return l.manager.ForceStopServices(names)
}

func (l *Local) ForceStartServices(names []string) error {
	return l.manager.ForceStartServices(names)
}

func (l *Local) StopAll() error {
	return l.manager.StopAll()
}

func (l *Local) RestartAll() error {
	return l.manager.RestartAll()
}

func (l *Local) RestartService(name string) error {
	return l.manager.RestartService(name)
}

func (l *Local) HasRunningServices() bool {
	return l.manager.HasRunningServices()
}

func (l *Local) ProjectExitRequested() (bool, int) {
	return l.manager.ProjectExitRequested()
}

func (l *Local) Shutdown() error {
	l.healthChecker.StopAll()
	return l.manager.Shutdown()
}

func (l *Local) RunAction(ctx context.Context, id config.ActionID) (ActionResult, error) {
	return l.manager.RunAction(ctx, id)
}

func (l *Local) ActionState(id config.ActionID) (ActionResult, bool) {
	return l.manager.ActionState(id)
}

func (l *Local) CancelAction(id config.ActionID) bool {
	return l.manager.CancelAction(id)
}

func (l *Local) PrepareInteractiveAction(id config.ActionID) (*exec.Cmd, func(error) ActionResult, error) {
	return l.manager.PrepareInteractiveAction(id)
}

func (l *Local) Logs(name string) []config.LogEntry {
	svc, ok := l.manager.GetService(name)
	if !ok {
		return nil
	}
	return svc.LogEntries()
}

func (l *Local) ClearLogs(name string) {
	svc, ok := l.manager.GetService(name)
	if !ok {
		return
	}
	svc.ClearLogs()
	svc.ResetNewLogCount()
}

func (l *Local) MarkLogsRead(name string) {
	svc, ok := l.manager.GetService(name)
	if !ok {
		return
	}
	svc.ResetNewLogCount()
}

func (l *Local) HealthHistory(name string) []string {
	if l.healthChecker == nil {
		return nil
	}
	h := l.healthChecker.GetHealth(name)
	if h == nil {
		return nil
	}
	return h.History.Lines()
}

func (l *Local) InspectPorts(ports []int) (map[int]*config.PortInfo, error) {
	l.portMu.RLock()
	checker := l.portChecker
	l.portMu.RUnlock()
	return checker.CheckPorts(ports)
}

func (l *Local) ReleaseExternalPort(portNumber, expectedPID int) (bool, error) {
	l.portMu.RLock()
	checker := l.portChecker
	l.portMu.RUnlock()
	info, err := checker.CheckPort(portNumber)
	if err != nil {
		return false, err
	}
	if info == nil {
		return true, nil
	}
	if info.PID != expectedPID {
		return false, fmt.Errorf("port %d owner changed from PID %d to PID %d; refusing to stop it", portNumber, expectedPID, info.PID)
	}
	if owner := l.manager.ManagedServiceForPID(info.PID); owner != "" {
		return false, fmt.Errorf("port %d is now owned by Kranz service %s; refusing to stop it as external", portNumber, owner)
	}
	return false, port.TerminateExternalPID(expectedPID, 3*time.Second)
}

func (l *Local) SetPortChecker(checker port.Checker) {
	l.portMu.Lock()
	l.portChecker = checker
	l.portMu.Unlock()
	l.manager.SetPortChecker(checker)
}
