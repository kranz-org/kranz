package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Runtime port discovery and the ownership questions it answers.

type listenerDiscoveryTarget struct {
	service    *Service
	leaderPID  int
	generation uint64
}

const (
	// listenerScanCeiling bounds how far the discovery cadence backs off once a
	// project has settled. Taking a listener snapshot shells out to lsof on
	// macOS and ss on Linux, which costs tens of milliseconds of CPU every
	// time, so a permanently fast cadence is the single most expensive thing an
	// otherwise idle project does.
	listenerScanCeiling = 30 * time.Second
)

func (m *Manager) ensureListenerDiscovery() {
	m.discoveryMu.Lock()
	if m.listenerScanner == nil || m.shuttingDown.Load() {
		m.discoveryMu.Unlock()
		return
	}
	if m.discoveryCancel != nil {
		// Discovery is already running, and a service has just started: its
		// ports are about to appear, so pull the cadence back to responsive.
		wake := m.discoveryWake
		m.discoveryMu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	m.discoveryCancel = cancel
	m.discoveryDone = done
	m.discoveryWake = wake
	interval := m.listenerScanInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	m.discoveryMu.Unlock()

	go func() {
		defer close(done)
		// Scan often while the answer is still moving, then back off while it
		// keeps coming back the same. Anything that can change the answer — a
		// service starting, a port appearing or going away — resets the cadence,
		// so the responsive case stays responsive and only the quiet case gets
		// cheap.
		signature := m.refreshDetectedPorts(ctx)
		cadence := interval
		ticker := time.NewTicker(cadence)
		defer ticker.Stop()
		reset := func(next time.Duration) {
			if next == cadence {
				return
			}
			cadence = next
			ticker.Reset(cadence)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				reset(interval)
				signature = m.refreshDetectedPorts(ctx)
			case <-ticker.C:
				current := m.refreshDetectedPorts(ctx)
				if current == signature {
					reset(min(2*cadence, listenerScanCeiling))
					continue
				}
				signature = current
				reset(interval)
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
	m.discoveryWake = nil
	m.discoveryMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// refreshDetectedPorts updates every running service's detected ports and
// returns a signature of what it found. An unchanged signature is what lets the
// discovery loop slow down.
func (m *Manager) refreshDetectedPorts(ctx context.Context) string {
	m.discoveryMu.Lock()
	scanner := m.listenerScanner
	m.discoveryMu.Unlock()
	if scanner == nil {
		return ""
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
		return ""
	}

	listeners, err := scanner.Snapshot(ctx)
	if err != nil {
		return ""
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
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	var signature strings.Builder
	for _, name := range names {
		target := targets[name]
		ports := portsByService[name]
		sort.Ints(ports)
		target.service.updateDetectedPortsJournalled(target.generation, ports)
		fmt.Fprintf(&signature, "%s/%d/%d/%v;", name, target.leaderPID, target.generation, ports)
	}
	return signature.String()
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
