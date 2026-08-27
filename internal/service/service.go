package service

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/pkg/ringbuffer"
)

// Service is the synchronized runtime representation of one configured service.
type Service struct {
	Config config.Service
	Name   string

	State   config.ServiceState
	stateMu sync.RWMutex

	stream            *logStream
	catalog           *RunCatalog
	nextRunProvenance RunProvenance

	// journal records this service's transitions for readers that need what
	// happened rather than what is. It is nil for services constructed outside
	// a Manager, and Journal.Record tolerates that.
	journal *Journal

	// HealthHistory is bounded separately from process output.
	HealthHistory *ringbuffer.RingBuffer

	// lifecycleMu serializes start, stop, and restart for this service.
	lifecycleMu         sync.Mutex
	runtimeMu           sync.RWMutex
	process             *ProcessManager
	monitorStop         chan struct{}
	runtimeGeneration   uint64
	lifecycleGeneration uint64
	detectedPorts       []int
	desiredRunning      atomic.Bool
	statusObserved      atomic.Bool
}

func (s *Service) setRuntime(process *ProcessManager, monitorStop chan struct{}) uint64 {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.runtimeGeneration++
	s.process = process
	s.monitorStop = monitorStop
	s.detectedPorts = nil
	return s.runtimeGeneration
}

func (s *Service) runtime() (*ProcessManager, chan struct{}) {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.process, s.monitorStop
}

func (s *Service) discoveryTarget() (int, uint64, bool) {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if s.process == nil {
		return 0, s.runtimeGeneration, false
	}
	pid := s.process.PID()
	return pid, s.runtimeGeneration, pid > 0
}

func (s *Service) clearRuntime(process *ProcessManager) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.process == process {
		s.process = nil
		s.monitorStop = nil
		s.runtimeGeneration++
		s.detectedPorts = nil
	}
}

// DetectedPorts returns a copy of the current runtime listener ports.
func (s *Service) DetectedPorts() []int {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return append([]int(nil), s.detectedPorts...)
}

func (s *Service) updateDetectedPortsJournalled(generation uint64, ports []int) bool {
	previous := s.DetectedPorts()
	if !s.updateDetectedPorts(generation, ports) {
		return false
	}
	current := s.DetectedPorts()
	if slices.Equal(previous, current) {
		return true
	}
	s.journal.Record(Transition{Kind: TransitionServicePorts, Service: s.Name, Run: s.Run(), Ports: current,
		From: formatPorts(previous), To: formatPorts(current), Summary: s.Name + " ports " + formatPorts(previous) + " -> " + formatPorts(current)})
	return true
}

func formatPorts(ports []int) string {
	if len(ports) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		parts = append(parts, strconv.Itoa(port))
	}
	return strings.Join(parts, ",")
}

func (s *Service) updateDetectedPorts(generation uint64, ports []int) bool {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.process == nil || s.runtimeGeneration != generation {
		return false
	}

	ordered := make([]int, 0, len(ports))
	for _, portNumber := range ports {
		if portNumber >= 1 && portNumber <= 65535 {
			ordered = append(ordered, portNumber)
		}
	}
	sort.Ints(ordered)
	unique := ordered[:0]
	for _, portNumber := range ordered {
		if len(unique) == 0 || unique[len(unique)-1] != portNumber {
			unique = append(unique, portNumber)
		}
	}
	s.detectedPorts = unique
	return true
}

// NewService creates a stopped runtime service from configuration.
func NewService(name string, cfg config.Service, logBufSize int) *Service {
	if logBufSize <= 0 {
		logBufSize = defaultLogBufferSize
	}
	status := config.StatusStopped
	if cfg.IsDetached() {
		status = config.StatusUnknown
	}
	return &Service{
		Name:          name,
		Config:        cfg,
		stream:        newLogStream(logBufSize),
		HealthHistory: ringbuffer.New(50),
		State: config.ServiceState{
			Status: status,
		},
	}
}

// SetJournal attaches the runtime journal this service records into.
func (s *Service) SetJournal(journal *Journal) { s.journal = journal }

func (s *Service) SetRunCatalog(catalog *RunCatalog) {
	s.catalog = catalog
	s.stream.SetCatalog(catalog, ServiceRunTarget(s.Name))
}

func (s *Service) SetNextRunProvenance(provenance RunProvenance) {
	s.stateMu.Lock()
	s.nextRunProvenance = normalizeRunProvenance(provenance)
	s.stateMu.Unlock()
}

// SetStatus atomically updates lifecycle status and transition timestamps, and
// records the transition. Every lifecycle path funnels through here, so the
// journal cannot miss a change some other path made.
func (s *Service) SetStatus(status config.ServiceStatus) {
	s.stateMu.Lock()
	previous := s.State.Status
	s.State.Status = status
	if status == config.StatusStarting || status == config.StatusStopping {
		s.lifecycleGeneration++
	}
	if status == config.StatusStarting {
		// A start opens a new numbered run, and the log stream numbers the
		// lines it captures with it. That is what makes "the logs of run 3" an
		// address rather than a time range a reader has to reconstruct.
		s.State.Run = s.stream.BeginRun()
		s.State.StartedAt = time.Now()
		s.State.Completed = false
		s.State.ExitCode = 0
		s.State.ExitError = ""
		s.State.Cause = nil
		provenance := normalizeRunProvenance(s.nextRunProvenance)
		s.nextRunProvenance = RunProvenance{}
		s.catalog.Begin(RunSummary{Target: ServiceRunTarget(s.Name), Run: s.State.Run, Status: status.String(),
			StartedAt: s.State.StartedAt, Surface: provenance.Surface, ClientLabel: provenance.ClientLabel,
			StartReason: provenance.StartReason})
	}
	if status == config.StatusRunning && s.State.StartedAt.IsZero() {
		s.State.StartedAt = time.Now()
	}
	transition := Transition{
		Kind:    TransitionServiceState,
		Service: s.Name,
		From:    previous.String(),
		To:      status.String(),
		Run:     s.State.Run,
		PID:     s.State.PID,
		Cause:   s.State.Cause,
		Summary: s.Name + " " + status.String(),
	}
	if (status == config.StatusStopped || status == config.StatusUnknown) && s.State.Completed {
		exit := s.State.ExitCode
		transition.ExitCode = &exit
	}
	s.stateMu.Unlock()
	s.catalog.Update(ServiceRunTarget(s.Name), transition.Run, status.String(), transition.PID, transition.Cause)
	if (status == config.StatusStopped || status == config.StatusUnknown) && transition.ExitCode != nil {
		s.catalog.Finish(ServiceRunTarget(s.Name), transition.Run, status.String(), time.Now(), *transition.ExitCode, transition.Cause)
	}
	if previous != status {
		s.journal.Record(transition)
	}
}

// SetCause records the structured reason for the current state. Callers set it
// before the status change it explains, so the transition carries the cause
// with it instead of a reader having to correlate two records by time.
func (s *Service) SetCause(cause *config.StateCause) {
	s.stateMu.Lock()
	if cause != nil && cause.At.IsZero() {
		cause.At = time.Now()
	}
	s.State.Cause = cause
	run := s.State.Run
	s.stateMu.Unlock()
	s.catalog.Update(ServiceRunTarget(s.Name), run, "", -1, cause)
}

// Run returns the current execution number of this service.
func (s *Service) Run() uint32 {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.State.Run
}

// LifecycleGeneration identifies results started before the latest transition.
func (s *Service) LifecycleGeneration() uint64 {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.lifecycleGeneration
}

// CanStart reports whether the configured start capability applies now.
func (s *Service) CanStart() bool {
	status := s.Status()
	if !s.Config.IsDetached() {
		return status == config.StatusStopped
	}
	return s.Config.Lifecycle.Start != nil && (status == config.StatusStopped || status == config.StatusUnknown)
}

// CanStop reports whether the current lifecycle can be explicitly stopped.
func (s *Service) CanStop() bool {
	status := s.Status()
	if !s.Config.IsDetached() {
		return status != config.StatusStopped && status != config.StatusUnknown
	}
	return s.Config.Lifecycle.Stop != nil && (status == config.StatusRunning || status == config.StatusUnhealthy || (status == config.StatusUnknown && s.DesiredRunning()))
}

// SetDesiredRunning records whether lifecycle policy expects the service to run.
func (s *Service) SetDesiredRunning(value bool) { s.desiredRunning.Store(value) }

// DesiredRunning reports whether lifecycle policy expects the service to run.
func (s *Service) DesiredRunning() bool { return s.desiredRunning.Load() }

// LifecycleStatusObserved reports whether a configured detached status probe
// has completed at least once in this session.
func (s *Service) LifecycleStatusObserved() bool { return s.statusObserved.Load() }

func (s *Service) markLifecycleStatusObserved() { s.statusObserved.Store(true) }

// RecordExit stores the most recent process completion result. An unsuccessful
// exit is also the cause of the stop that follows it, so the reason survives
// past the moment the status becomes stopped.
func (s *Service) RecordExit(code int, err error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.State.Completed = true
	s.State.ExitCode = code
	if err != nil {
		s.State.ExitError = err.Error()
	} else {
		s.State.ExitError = ""
	}
	if code != 0 || err != nil {
		exit := code
		cause := &config.StateCause{Type: "exited", ExitCode: &exit, At: time.Now(), PID: s.State.PID}
		if err != nil {
			cause.Message = err.Error()
		}
		s.State.Cause = cause
	}
}

// ResetRestartCount clears the availability-policy restart counter.
func (s *Service) ResetRestartCount() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.State.RestartCount = 0
}

// IncrementRestartCount increments and returns the restart counter.
func (s *Service) IncrementRestartCount() int {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.State.RestartCount++
	return s.State.RestartCount
}

// Status returns the current lifecycle status.
func (s *Service) Status() config.ServiceStatus {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.State.Status
}

// SetPID updates the owned process ID.
func (s *Service) SetPID(pid int) {
	s.stateMu.Lock()
	s.State.PID = pid
	run := s.State.Run
	s.stateMu.Unlock()
	s.catalog.Update(ServiceRunTarget(s.Name), run, "", pid, nil)
}

// PID returns the owned process ID, or zero while stopped.
func (s *Service) PID() int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.State.PID
}

// SetReadyAt records when readiness succeeded.
func (s *Service) SetReadyAt(t time.Time) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.State.ReadyAt = t
}

// IncrementFailedChecks increments the consecutive health failure count.
func (s *Service) IncrementFailedChecks() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.State.FailedChecks++
}

// ResetFailedChecks clears the consecutive health failure count.
func (s *Service) ResetFailedChecks() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.State.FailedChecks = 0
}

// FailedChecks returns the consecutive health failure count.
func (s *Service) FailedChecks() int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.State.FailedChecks
}

// IncrementNewLogCount increments unread log lines for the UI.
func (s *Service) IncrementNewLogCount() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.State.NewLogCount++
}

// ResetNewLogCount marks every captured line as read.
func (s *Service) ResetNewLogCount() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.State.NewLogCount = 0
}

// NewLogCount returns the unread line count.
func (s *Service) NewLogCount() int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.State.NewLogCount
}

// AppendLog captures one line with the current time and marks it unread.
func (s *Service) AppendLog(line string) {
	s.AppendLogAtSource(time.Now(), "kranz", line)
}

// AppendLogAt records a log line with the time Kranz received it.
func (s *Service) AppendLogAt(timestamp time.Time, line string) {
	s.AppendLogAtSource(timestamp, "kranz", line)
}

// AppendLogAtSource records a source-aware line with a stable sequence cursor.
func (s *Service) AppendLogAtSource(timestamp time.Time, source, line string) {
	s.stream.Append(timestamp, source, line)
	s.IncrementNewLogCount()
}

// LogEntries returns an aligned snapshot of log text and capture timestamps.
func (s *Service) LogEntries() []config.LogEntry { return s.stream.Entries() }

// LogLines returns the buffered log text without its capture metadata.
func (s *Service) LogLines() []string { return s.stream.Lines() }

// CopyLogHistoryFrom preserves the logical service buffer across a hot reload.
func (s *Service) CopyLogHistoryFrom(previous *Service) { s.stream.CopyFrom(previous.stream) }

// ClearLogs discards this service's buffered logs and their metadata.
func (s *Service) ClearLogs() { s.stream.Clear() }

// DeleteRunLogs removes retained output for one completed execution.
func (s *Service) DeleteRunLogs(run uint32) { s.stream.DeleteRun(run) }

// SetState replaces the complete mutable state.
func (s *Service) SetState(state config.ServiceState) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.State = state
}

// GetState returns a copy of the mutable state.
func (s *Service) GetState() config.ServiceState {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.State
}

// RestoreState copies lifecycle state when a detached service definition is
// hot-reloaded. Detached services have no owned process runtime to preserve.
func (s *Service) RestoreState(state config.ServiceState, desiredRunning bool) {
	s.stateMu.Lock()
	s.State = state
	s.stateMu.Unlock()
	s.desiredRunning.Store(desiredRunning)
}
