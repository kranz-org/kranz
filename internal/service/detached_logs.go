package service

import (
	"context"
	"fmt"

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
		result, _ := m.actions.runDefinition(ctx, id, action, func(entry CapturedOutput) {
			svc.AppendLogAtSource(entry.CapturedAt, entry.Source, entry.Text)
		})
		resultCh <- result
	}()
	result := <-resultCh
	if ctx.Err() == nil {
		svc.AppendLog(fmt.Sprintf("[Kranz] Detached log follower %s · exit %d", result.Status.String(), result.ExitCode))
	}
	m.logsMu.Lock()
	if m.detachedLogs[svc.Name] == follower {
		delete(m.detachedLogs, svc.Name)
	}
	m.logsMu.Unlock()
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
