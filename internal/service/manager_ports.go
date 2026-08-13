package service

import (
	"context"
	"strings"
	"time"
)

// Runtime port discovery and the ownership questions it answers.

type listenerDiscoveryTarget struct {
	service    *Service
	leaderPID  int
	generation uint64
}

func (m *Manager) ensureListenerDiscovery() {
	m.discoveryMu.Lock()
	defer m.discoveryMu.Unlock()
	if m.listenerScanner == nil || m.discoveryCancel != nil || m.shuttingDown.Load() {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.discoveryCancel = cancel
	m.discoveryDone = done
	interval := m.listenerScanInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	go func() {
		defer close(done)
		m.refreshDetectedPorts(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.refreshDetectedPorts(ctx)
			}
		}
	}()
}

func (m *Manager) stopListenerDiscovery() {
	m.discoveryMu.Lock()
	cancel := m.discoveryCancel
	done := m.discoveryDone
	m.discoveryCancel = nil
	m.discoveryDone = nil
	m.discoveryMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (m *Manager) refreshDetectedPorts(ctx context.Context) {
	m.discoveryMu.Lock()
	scanner := m.listenerScanner
	m.discoveryMu.Unlock()
	if scanner == nil {
		return
	}

	targets := make(map[string]listenerDiscoveryTarget)
	for _, svc := range m.Services() {
		if !svc.Config.PortDiscoveryEnabled() {
			continue
		}
		leaderPID, generation, running := svc.discoveryTarget()
		if running {
			targets[svc.Name] = listenerDiscoveryTarget{
				service: svc, leaderPID: leaderPID, generation: generation,
			}
		}
	}
	if len(targets) == 0 {
		return
	}

	listeners, err := scanner.Snapshot(ctx)
	if err != nil {
		return
	}
	portsByService := make(map[string][]int, len(targets))
	for _, listener := range listeners {
		if !strings.EqualFold(listener.Protocol, "tcp") || listener.PID < 1 {
			continue
		}
		for name, target := range targets {
			if sameProcessGroup(target.leaderPID, listener.PID) {
				portsByService[name] = append(portsByService[name], listener.Port)
				break
			}
		}
	}
	for name, target := range targets {
		target.service.updateDetectedPorts(target.generation, portsByService[name])
	}
}

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
