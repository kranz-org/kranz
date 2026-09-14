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
	discoveryWake        chan struct{}
	discoveryMu          sync.Mutex
	discoveryCancel      context.CancelFunc
	discoveryDone        chan struct{}
	shuttingDown         atomic.Bool
	exitRequested        atomic.Bool
	exitCode             atomic.Int64
	reloadMu             sync.Mutex
	pendingReload        []PendingChange
	pendingDesired       *config.Config
	statusMu             sync.Mutex
	statusMonitors       map[string]*statusMonitor
	logsMu               sync.Mutex
	detachedLogs         map[string]*detachedLogFollower
	prereqMu             sync.Mutex
	prereqSatisfied      map[config.ActionID]bool
	prereqRuns           map[config.ActionID]*prereqRun
	journal              *Journal
	runs                 *RunCatalog
}

// Journal returns the runtime transition journal shared by every service and
// action this manager owns.
func (m *Manager) Journal() *Journal { return m.journal }

func (m *Manager) RunSummaries(target RunTarget) []RunSummary { return m.runs.List(target) }

func (m *Manager) AllRunSummaries() []RunSummary { return m.runs.All() }

func (m *Manager) RunRetentionBoundaries() []RunRetentionBoundary { return m.runs.Boundaries() }

// DeleteRun removes one completed catalog record and the retained data owned
// by its producer. The transition journal remains an immutable audit trail.
func (m *Manager) DeleteRun(target RunTarget, run uint32) (RunSummary, error) {
	deleted, err := m.runs.Delete(target, run)
	if err != nil {
		return RunSummary{}, err
	}
	if target.Kind == RunKindAction {
		m.actions.DeleteRun(target.Action, run)
	} else if svc, ok := m.GetService(target.Name); ok {
		svc.DeleteRunLogs(run)
	}
	return deleted, nil
}

// RecordConfigReload marks a new configuration generation inside every
// continuing service run. A reload never cycles a running process, so every
// service that has a run and is not stopped keeps its process and receives the
// marker; an explicit restart reports its own lifecycle result instead.
func (m *Manager) RecordConfigReload(generation uint64) {
	for _, svc := range m.Services() {
		if svc.Run() == 0 || svc.Status() == config.StatusStopped {
			continue
		}
		svc.AppendLog(fmt.Sprintf("[Kranz] Config reloaded · generation %d · %s#%d", generation, svc.Name, svc.Run()))
	}
}

// newService constructs a service already attached to this manager's journal,
// so no construction path can produce a service whose changes go unrecorded.
func (m *Manager) newService(name string, cfg config.Service) *Service {
	svc := NewService(name, cfg, 1000)
	svc.SetJournal(m.journal)
	svc.SetRunCatalog(m.runs)
	return svc
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
// Under the pending policy ApplyConfig never cycles a running process, so a
// change to a running service is reported as pending rather than as a restart.
type ReloadResult struct {
	Added   []string
	Removed []string
	Updated []string
	Pending []PendingChange
}

// PendingChange is a desired configuration change that was deliberately not
// applied because it would detach, replace, or rename a running process.
type PendingChange struct {
	ServiceID   string `json:"service_id"`
	Name        string `json:"name"`
	DesiredName string `json:"desired_name,omitempty"`
	Kind        string `json:"kind"`
	Reason      string `json:"reason"`
}

type pendingAdoptionSnapshot struct {
	cfg            *config.Config
	services       map[string]*Service
	pendingReload  []PendingChange
	pendingDesired *config.Config
}

func (m *Manager) PendingChanges() []PendingChange {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]PendingChange(nil), m.pendingReload...)
}

func (m *Manager) PendingChangeFor(name string) (PendingChange, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, change := range m.pendingReload {
		if change.Name == name {
			return change, true
		}
	}
	return PendingChange{}, false
}

func (m *Manager) ServiceIdentity(name string) config.EffectiveService {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return serviceIdentity(m.cfg, name)
}

func (m *Manager) Config() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// ApplyConfig reconciles a validated desired configuration without silently
// cycling a running process. Unsafe changes remain on the accepted runtime
// snapshot and are exposed as pending until the user explicitly restarts.
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

	m.mu.RLock()
	currentNames := make([]string, 0, len(m.services))
	for name := range m.services {
		currentNames = append(currentNames, name)
	}
	m.mu.RUnlock()
	sort.Strings(currentNames)

	m.mu.RLock()
	currentConfig := m.cfg
	m.mu.RUnlock()
	accepted := cloneManagerConfig(next)
	nextByID := make(map[string]string, len(next.Services))
	for name := range next.Services {
		nextByID[serviceIdentity(next, name).ID] = name
	}
	handled := make(map[string]bool, len(next.Services))
	pending := make([]PendingChange, 0)

	for _, name := range currentNames {
		svc, _ := m.GetService(name)
		identity := serviceIdentity(currentConfig, name)
		nextName, exists := nextByID[identity.ID]
		// Name matching is only a compatibility path for legacy, uncomposed
		// configurations. Once a service has source-backed identity, a different
		// service reusing its display name must never inherit the old process.
		if !exists && identity.SourceID == "" {
			nextName = name
			_, exists = next.Services[name]
		}
		incoming := next.Services[nextName]
		wasRunning := svc.Status() != config.StatusStopped || svc.DesiredRunning()
		if !exists {
			if wasRunning {
				accepted.Services[name] = svc.Config
				accepted.ServiceOrder = managerAppendUnique(accepted.ServiceOrder, name)
				accepted.ServiceMetadata[name] = identity
				retainManagerSource(accepted, currentConfig, identity.SourceID)
				retainManagerProvenance(accepted, currentConfig, identity.ID)
				pending = append(pending, PendingChange{ServiceID: identity.ID, Name: name, Kind: "remove", Reason: "running service keeps its accepted snapshot until an explicit restart"})
				continue
			}
			result.Removed = append(result.Removed, name)
			continue
		}
		handled[nextName] = true
		if sameManagedServiceConfig(svc.Config, incoming) && name == nextName {
			continue
		}
		// A detached resource lives outside Kranz. When its accepted definition
		// declares no stop operation the ordinary restart path cannot cycle it,
		// so a pending update would never be confirmable. Reload the definition
		// in place, retaining observed state, instead of leaving dead pending.
		// The gate is definition-based rather than CanStop: CanStop also encodes
		// the current status, so a stopped or not-yet-observed resource that
		// still declares a stop command would hot-apply a change that a restart
		// could have confirmed.
		if svc.Config.IsDetached() && incoming.IsDetached() && svc.Config.Lifecycle.Stop == nil {
			replacement := m.newService(nextName, incoming)
			replacement.CopyLogHistoryFrom(svc)
			replacement.HealthHistory = svc.HealthHistory
			replacement.RestoreState(svc.GetState(), svc.DesiredRunning())
			m.mu.Lock()
			delete(m.services, name)
			m.services[nextName] = replacement
			m.mu.Unlock()
			if name != nextName {
				// The clone already carries the desired definition. Drop the old
				// display name only when it still refers to this identity, so a
				// different service reusing that name keeps its own entry.
				if acceptedIdentity, ok := accepted.ServiceMetadata[name]; ok && acceptedIdentity.ID == identity.ID {
					delete(accepted.Services, name)
					delete(accepted.ServiceMetadata, name)
				}
				accepted.ServiceOrder = managerAppendUnique(accepted.ServiceOrder, nextName)
				retainManagerSource(accepted, currentConfig, identity.SourceID)
				retainManagerProvenance(accepted, currentConfig, identity.ID)
			}
			result.Updated = append(result.Updated, nextName)
			continue
		}
		if wasRunning {
			delete(accepted.Services, nextName)
			delete(accepted.ServiceMetadata, nextName)
			accepted.Services[name] = svc.Config
			accepted.ServiceMetadata[name] = identity
			retainManagerSource(accepted, currentConfig, identity.SourceID)
			retainManagerProvenance(accepted, currentConfig, identity.ID)
			accepted.ServiceOrder = managerAppendUnique(accepted.ServiceOrder, name)
			kind := "update"
			if name != nextName {
				kind = "rename"
			}
			pending = append(pending, PendingChange{ServiceID: identity.ID, Name: name, DesiredName: nextName, Kind: kind, Reason: "running service keeps its accepted snapshot until an explicit restart"})
			continue
		}
		replacement := m.newService(nextName, incoming)
		// Keep the visible history across a hot reload without mutating the
		// configuration object observed by process-monitor goroutines.
		replacement.CopyLogHistoryFrom(svc)
		replacement.HealthHistory = svc.HealthHistory
		m.mu.Lock()
		delete(m.services, name)
		m.services[nextName] = replacement
		m.mu.Unlock()
		result.Updated = append(result.Updated, nextName)
	}

	m.mu.Lock()
	for _, name := range result.Removed {
		delete(m.services, name)
	}
	for _, name := range next.ServiceNames() {
		if handled[name] {
			continue
		}
		svcConfig := next.Services[name]
		if _, exists := m.services[name]; !exists {
			m.services[name] = m.newService(name, svcConfig)
			result.Added = append(result.Added, name)
		} else {
			identity := serviceIdentity(next, name)
			removeManagerProvenance(accepted, identity.ID)
			pending = append(pending, PendingChange{ServiceID: identity.ID, Name: name, Kind: "add", Reason: "display name is owned by a running service snapshot"})
		}
	}
	previous := m.cfg
	m.cfg = accepted
	m.pendingReload = append([]PendingChange(nil), pending...)
	if len(pending) > 0 {
		m.pendingDesired = cloneManagerConfig(next)
	} else {
		m.pendingDesired = nil
	}
	m.mu.Unlock()
	m.forgetChangedPrerequisites(previous, accepted)
	m.actions.ApplyConfig(accepted)
	m.reconcileStatusMonitors(accepted)
	m.reconcileDetachedLogs()
	reconcileBackground = false
	sort.Strings(result.Added)
	result.Pending = append([]PendingChange(nil), pending...)
	return result, nil
}

func retainManagerSource(target, current *config.Config, sourceID string) {
	if sourceID == "" || target == nil || current == nil {
		return
	}
	for _, source := range target.Sources {
		if source.ID == sourceID {
			return
		}
	}
	for _, source := range current.Sources {
		if source.ID == sourceID {
			target.Sources = append(target.Sources, source)
			return
		}
	}
}

func retainManagerProvenance(target, current *config.Config, serviceID string) {
	removeManagerProvenance(target, serviceID)
	for _, entry := range current.Provenance {
		if entry.ServiceID == serviceID {
			target.Provenance = append(target.Provenance, entry)
		}
	}
}

func removeManagerProvenance(target *config.Config, serviceID string) {
	if target == nil || serviceID == "" {
		return
	}
	filtered := target.Provenance[:0]
	for _, entry := range target.Provenance {
		if entry.ServiceID != serviceID {
			filtered = append(filtered, entry)
		}
	}
	target.Provenance = filtered
}

func cloneManagerConfig(source *config.Config) *config.Config {
	clone := *source
	clone.Services = make(map[string]config.Service, len(source.Services))
	for name, service := range source.Services {
		clone.Services[name] = service
	}
	clone.ServiceOrder = append([]string(nil), source.ServiceOrder...)
	clone.ServiceMetadata = make(map[string]config.EffectiveService, len(source.ServiceMetadata))
	for name, metadata := range source.ServiceMetadata {
		clone.ServiceMetadata[name] = metadata
	}
	// Copy slice fields that reconciliation rewrites in place. A bare header
	// copy would let removeManagerProvenance filter through the source's
	// backing array, silently rewriting the desired configuration it came from.
	clone.Provenance = append([]config.FieldProvenance(nil), source.Provenance...)
	clone.Sources = append([]config.ConfigSource(nil), source.Sources...)
	clone.Diagnostics = append([]string(nil), source.Diagnostics...)
	clone.CompositionDiagnostics = append([]config.CompositionDiagnostic(nil), source.CompositionDiagnostics...)
	return &clone
}

func serviceIdentity(cfg *config.Config, name string) config.EffectiveService {
	if cfg != nil {
		if identity, ok := cfg.ServiceMetadata[name]; ok {
			return identity
		}
	}
	return config.EffectiveService{ID: name, SourceName: name, DisplayName: name}
}

func managerAppendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// adoptPendingStopped installs desired definitions only after the ordinary
// restart plan has stopped every affected process. Adoption is matched by the
// immutable ServiceID, never by display name: a rename and an add can share one
// display name, and name-keyed bookkeeping would let either adoption erase the
// other. The caller holds reloadMu.
func (m *Manager) adoptPendingStopped(names []string) []string {
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		selected[name] = true
	}
	m.mu.Lock()
	if m.pendingDesired == nil || len(m.pendingReload) == 0 {
		m.mu.Unlock()
		return names
	}
	accepted := cloneManagerConfig(m.cfg)
	// Resolve running services by identity before any map entry is removed, so
	// a pending change still finds the process it describes when two services
	// currently share a display name.
	servicesByID := make(map[string]*Service, len(m.services))
	for name := range m.services {
		servicesByID[serviceIdentity(accepted, name).ID] = m.services[name]
	}
	remaining := make([]PendingChange, 0, len(m.pendingReload))
	// adopted groups every desired name produced for one selected runtime slot,
	// so a rename and an add under the same old name both keep a service.
	adopted := make(map[string][]string)
	seenDesired := make(map[string]bool)
	for _, change := range m.pendingReload {
		if !selected[change.Name] {
			remaining = append(remaining, change)
			continue
		}
		old := servicesByID[change.ServiceID]
		oldName := change.Name
		if old != nil {
			oldName = old.Name
		}
		if old != nil && m.services[oldName] == old {
			delete(m.services, oldName)
		}
		if acceptedIdentity, ok := accepted.ServiceMetadata[oldName]; ok && acceptedIdentity.ID == change.ServiceID {
			delete(accepted.Services, oldName)
			delete(accepted.ServiceMetadata, oldName)
		}
		removeManagerProvenance(accepted, change.ServiceID)
		if change.Kind == "remove" {
			continue
		}
		desiredName := change.DesiredName
		if desiredName == "" {
			desiredName = change.Name
		}
		desiredService, exists := m.pendingDesired.Services[desiredName]
		if !exists {
			continue
		}
		replacement := m.newService(desiredName, desiredService)
		if old != nil {
			replacement.CopyLogHistoryFrom(old)
			replacement.HealthHistory = old.HealthHistory
		}
		m.services[desiredName] = replacement
		accepted.Services[desiredName] = desiredService
		desiredIdentity := m.pendingDesired.ServiceMetadata[desiredName]
		accepted.ServiceMetadata[desiredName] = desiredIdentity
		retainManagerProvenance(accepted, m.pendingDesired, desiredIdentity.ID)
		if !seenDesired[desiredName] {
			seenDesired[desiredName] = true
			adopted[change.Name] = append(adopted[change.Name], desiredName)
		}
	}
	translated := make([]string, 0, len(names))
	seenTranslated := make(map[string]bool)
	for _, name := range names {
		candidates := adopted[name]
		if len(candidates) == 0 {
			if _, exists := m.services[name]; exists {
				candidates = []string{name}
			}
		}
		for _, candidate := range candidates {
			if seenTranslated[candidate] {
				continue
			}
			seenTranslated[candidate] = true
			translated = append(translated, candidate)
		}
	}
	previous := m.cfg
	if len(remaining) == 0 {
		accepted = cloneManagerConfig(m.pendingDesired)
	} else {
		accepted.ServiceOrder = reconcileManagerOrder(m.pendingDesired.ServiceOrder, accepted.Services)
	}
	m.cfg = accepted
	m.pendingReload = remaining
	if len(remaining) == 0 {
		m.pendingDesired = nil
	}
	m.mu.Unlock()
	m.forgetChangedPrerequisites(previous, accepted)
	m.actions.ApplyConfig(accepted)
	m.reconcileStatusMonitors(accepted)
	m.reconcileDetachedLogs()
	return translated
}

func (m *Manager) capturePendingAdoption() (*pendingAdoptionSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.pendingDesired == nil || len(m.pendingReload) == 0 {
		return nil, false
	}
	services := make(map[string]*Service, len(m.services))
	for name, service := range m.services {
		services[name] = service
	}
	return &pendingAdoptionSnapshot{
		cfg:            cloneManagerConfig(m.cfg),
		services:       services,
		pendingReload:  append([]PendingChange(nil), m.pendingReload...),
		pendingDesired: cloneManagerConfig(m.pendingDesired),
	}, true
}

func (m *Manager) restorePendingAdoption(snapshot *pendingAdoptionSnapshot, started []string) {
	if snapshot == nil {
		return
	}
	for index := len(started) - 1; index >= 0; index-- {
		_ = m.StopService(started[index])
	}
	m.mu.Lock()
	m.cfg = snapshot.cfg
	m.services = snapshot.services
	m.pendingReload = snapshot.pendingReload
	m.pendingDesired = snapshot.pendingDesired
	m.mu.Unlock()
	m.actions.ApplyConfig(snapshot.cfg)
	m.reconcileStatusMonitors(snapshot.cfg)
	m.reconcileDetachedLogs()
}

func reconcileManagerOrder(preferred []string, services map[string]config.Service) []string {
	result := make([]string, 0, len(services))
	seen := make(map[string]bool, len(services))
	for _, name := range preferred {
		if _, ok := services[name]; ok && !seen[name] {
			result = append(result, name)
			seen[name] = true
		}
	}
	var rest []string
	for name := range services {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(result, rest...)
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
		journal:              NewJournal(defaultJournalSize),
		runs:                 NewRunCatalog(defaultRunCatalogSize),
	}
	m.actions.SetJournal(m.journal)
	m.actions.SetRunCatalog(m.runs)

	for name, svcCfg := range cfg.Services {
		m.services[name] = m.newService(name, svcCfg)
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
