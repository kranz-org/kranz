package service

import (
	"sort"
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

	Logs            *ringbuffer.RingBuffer
	logMu           sync.RWMutex
	logTimes        []time.Time
	logSources      []string
	logSequences    []uint64
	logTimeWrite    int
	logTimeCount    int
	nextLogSequence uint64

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
		logBufSize = 1000
	}
	status := config.StatusStopped
	if cfg.IsDetached() {
		status = config.StatusUnknown
	}
	return &Service{
		Name:          name,
		Config:        cfg,
		Logs:          ringbuffer.New(logBufSize),
		logTimes:      make([]time.Time, logBufSize),
		logSources:    make([]string, logBufSize),
		logSequences:  make([]uint64, logBufSize),
		HealthHistory: ringbuffer.New(50),
		State: config.ServiceState{
			Status: status,
		},
	}
}

// SetStatus atomically updates lifecycle status and transition timestamps.
func (s *Service) SetStatus(status config.ServiceStatus) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.State.Status = status
	if status == config.StatusStarting || status == config.StatusStopping {
		s.lifecycleGeneration++
	}
	if status == config.StatusStarting {
		s.State.StartedAt = time.Now()
		s.State.Completed = false
		s.State.ExitCode = 0
		s.State.ExitError = ""
	}
	if status == config.StatusRunning && s.State.StartedAt.IsZero() {
		s.State.StartedAt = time.Now()
	}
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

// RecordExit stores the most recent process completion result.
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
	defer s.stateMu.Unlock()
	s.State.PID = pid
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
	s.logMu.Lock()
	if source == "" {
		source = "unknown"
	}
	s.nextLogSequence++
	s.Logs.Write(line)
	s.logTimes[s.logTimeWrite] = timestamp
	s.logSources[s.logTimeWrite] = source
	s.logSequences[s.logTimeWrite] = s.nextLogSequence
	s.logTimeWrite = (s.logTimeWrite + 1) % len(s.logTimes)
	if s.logTimeCount < len(s.logTimes) {
		s.logTimeCount++
	}
	s.logMu.Unlock()
	s.IncrementNewLogCount()
}

// LogEntries returns an aligned snapshot of log text and capture timestamps.
func (s *Service) LogEntries() []config.LogEntry {
	s.logMu.RLock()
	defer s.logMu.RUnlock()
	lines := s.Logs.Lines()
	count := min(len(lines), s.logTimeCount)
	entries := make([]config.LogEntry, 0, count)
	// The metadata rings are written in lockstep with the text buffer, so one
	// index walked back from the write cursor addresses all of them. Rebuilding
	// a separate ordered copy per field invites the fields to disagree.
	for index := range count {
		metadataIndex := (s.logTimeWrite - count + index + len(s.logTimes)) % len(s.logTimes)
		entries = append(entries, config.LogEntry{
			Sequence:  s.logSequences[metadataIndex],
			Timestamp: s.logTimes[metadataIndex],
			Source:    s.logSources[metadataIndex],
			Raw:       lines[len(lines)-count+index],
		})
	}
	return entries
}

// CopyLogHistoryFrom preserves the logical service buffer across a hot reload.
func (s *Service) CopyLogHistoryFrom(previous *Service) {
	previous.logMu.RLock()
	defer previous.logMu.RUnlock()
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.Logs = previous.Logs
	s.logTimes = append(s.logTimes[:0], previous.logTimes...)
	s.logSources = append(s.logSources[:0], previous.logSources...)
	s.logSequences = append(s.logSequences[:0], previous.logSequences...)
	s.logTimeWrite = previous.logTimeWrite
	s.logTimeCount = previous.logTimeCount
	s.nextLogSequence = previous.nextLogSequence
}

// ClearLogs atomically clears both log text and its timestamp metadata.
func (s *Service) ClearLogs() {
	s.logMu.Lock()
	s.Logs.Clear()
	clear(s.logTimes)
	clear(s.logSources)
	clear(s.logSequences)
	s.logTimeWrite = 0
	s.logTimeCount = 0
	s.logMu.Unlock()
}

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
