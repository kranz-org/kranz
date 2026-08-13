package service

import (
	"context"
	"fmt"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// Supervision of processes Kranz owns: output, exit, recovery, and the exit
// codes a project can request.

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
					svc.AppendLog(fmt.Sprintf("[Kranz] Process exited · code %d", exitCode))
				} else {
					svc.AppendLog(fmt.Sprintf("[Kranz] Process failed · exit code %d", exitCode))
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
		svc.AppendLog(fmt.Sprintf("[Kranz] Restart scheduled · attempt %d · in %s", attempt, backoff))
		go func() {
			timer := time.NewTimer(backoff)
			defer timer.Stop()
			<-timer.C
			if !svc.DesiredRunning() || m.shuttingDown.Load() {
				return
			}
			if err := m.startService(context.Background(), name, true); err != nil {
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
