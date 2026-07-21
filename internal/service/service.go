package service

import (
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

	Logs         *ringbuffer.RingBuffer
	logMu        sync.RWMutex
	logTimes     []time.Time
	logTimeWrite int
	logTimeCount int

	// HealthHistory is bounded separately from process output.
	HealthHistory *ringbuffer.RingBuffer

	// lifecycleMu serializes start, stop, and restart for this service.
	lifecycleMu    sync.Mutex
	runtimeMu      sync.RWMutex
	process        *ProcessManager
	monitorStop    chan struct{}
	desiredRunning atomic.Bool
}

func (s *Service) setRuntime(process *ProcessManager, monitorStop chan struct{}) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.process = process
	s.monitorStop = monitorStop
}

func (s *Service) runtime() (*ProcessManager, chan struct{}) {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.process, s.monitorStop
}

func (s *Service) clearRuntime(process *ProcessManager) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.process == process {
		s.process = nil
		s.monitorStop = nil
	}
}

// NewService creates a stopped runtime service from configuration.
func NewService(name string, cfg config.Service, logBufSize int) *Service {
	if logBufSize <= 0 {
		logBufSize = 1000
	}
	return &Service{
		Name:          name,
		Config:        cfg,
		Logs:          ringbuffer.New(logBufSize),
		logTimes:      make([]time.Time, logBufSize),
		HealthHistory: ringbuffer.New(50),
		State: config.ServiceState{
			Status: config.StatusStopped,
		},
	}
}

// SetStatus atomically updates lifecycle status and transition timestamps.
func (s *Service) SetStatus(status config.ServiceStatus) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.State.Status = status
	if status == config.StatusStarting {
		s.State.StartedAt = time.Now()
		s.State.Completed = false
		s.State.ExitCode = 0
		s.State.ExitError = ""
	}
}

// SetDesiredRunning records whether lifecycle policy expects the service to run.
func (s *Service) SetDesiredRunning(value bool) { s.desiredRunning.Store(value) }

// DesiredRunning reports whether lifecycle policy expects the service to run.
func (s *Service) DesiredRunning() bool { return s.desiredRunning.Load() }

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
	s.AppendLogAt(time.Now(), line)
}

// AppendLogAt records a log line with the time Kranz received it.
func (s *Service) AppendLogAt(timestamp time.Time, line string) {
	s.logMu.Lock()
	s.Logs.Write(line)
	s.logTimes[s.logTimeWrite] = timestamp
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
	times := make([]time.Time, 0, s.logTimeCount)
	if s.logTimeCount < len(s.logTimes) {
		times = append(times, s.logTimes[:s.logTimeCount]...)
	} else {
		times = append(times, s.logTimes[s.logTimeWrite:]...)
		times = append(times, s.logTimes[:s.logTimeWrite]...)
	}
	count := min(len(lines), len(times))
	entries := make([]config.LogEntry, 0, count)
	for index := range count {
		line := lines[len(lines)-count+index]
		entries = append(entries, config.LogEntry{Timestamp: times[len(times)-count+index], Raw: line})
	}
	return entries
}

// ClearLogs atomically clears both log text and its timestamp metadata.
func (s *Service) ClearLogs() {
	s.logMu.Lock()
	s.Logs.Clear()
	clear(s.logTimes)
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
