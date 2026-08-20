package service

import (
	"context"
	"fmt"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func (m *Manager) startDetachedLogs(svc *Service) {
	if svc == nil || svc.Config.Lifecycle.Logs == nil || (svc.Status() != config.StatusRunning && svc.Status() != config.StatusUnhealthy) {
		return
	}
	m.logsMu.Lock()
	if _, exists := m.detachedLogs[svc.Name]; exists {
		m.logsMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	follower := &detachedLogFollower{cancel: cancel, done: make(chan struct{})}
	m.detachedLogs[svc.Name] = follower
	m.logsMu.Unlock()
	go m.followDetachedLogs(ctx, svc, *svc.Config.Lifecycle.Logs, follower)
}

func (m *Manager) followDetachedLogs(ctx context.Context, svc *Service, action config.Action, follower *detachedLogFollower) {
	defer close(follower.done)
	id := config.ActionID{OwnerKind: config.ActionOwnerLifecycle, Owner: svc.Name, Name: "logs"}
	resultCh := make(chan ActionResult, 1)
	go func() {
		result, _ := m.actions.RunDefinition(ctx, id, action)
		resultCh <- result
	}()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	stdoutOffset, stderrOffset := 0, 0
	appendSnapshot := func(result ActionResult) {
		for ; stdoutOffset < len(result.Stdout); stdoutOffset++ {
			svc.AppendLogAtSource(time.Now(), "stdout", result.Stdout[stdoutOffset])
		}
		for ; stderrOffset < len(result.Stderr); stderrOffset++ {
			svc.AppendLogAtSource(time.Now(), "stderr", result.Stderr[stderrOffset])
		}
	}
	for {
		select {
		case result := <-resultCh:
			appendSnapshot(result)
			if ctx.Err() == nil {
				svc.AppendLog(fmt.Sprintf("[Kranz] Detached log follower %s · exit %d", result.Status.String(), result.ExitCode))
			}
			m.logsMu.Lock()
			if m.detachedLogs[svc.Name] == follower {
				delete(m.detachedLogs, svc.Name)
			}
			m.logsMu.Unlock()
			return
		case <-ticker.C:
			if result, exists := m.actions.State(id); exists {
				appendSnapshot(result)
			}
		}
	}
}

func (m *Manager) stopDetachedLogs(name string) {
	m.logsMu.Lock()
	follower := m.detachedLogs[name]
	delete(m.detachedLogs, name)
	m.logsMu.Unlock()
	if follower != nil {
		follower.cancel()
		<-follower.done
	}
}

func (m *Manager) stopAllDetachedLogs() {
	m.logsMu.Lock()
	followers := make([]*detachedLogFollower, 0, len(m.detachedLogs))
	for name, follower := range m.detachedLogs {
		followers = append(followers, follower)
		delete(m.detachedLogs, name)
	}
	m.logsMu.Unlock()
	for _, follower := range followers {
		follower.cancel()
	}
	for _, follower := range followers {
		<-follower.done
	}
}

func (m *Manager) reconcileDetachedLogs() {
	for _, svc := range m.Services() {
		m.startDetachedLogs(svc)
	}
}
