// Package health runs independent readiness and liveness probes for services.
package health

import (
	"fmt"
	"sync"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/pkg/ringbuffer"
)

// Checker owns the monitoring goroutines for all configured services.
type Checker struct {
	services map[string]*ServiceHealth
	mu       sync.RWMutex
	stopCh   map[string]chan struct{}
}

// ServiceHealth stores the synchronized probe state for one service.
type ServiceHealth struct {
	mu         sync.RWMutex
	Ready      bool
	Alive      bool
	History    *ringbuffer.RingBuffer
	ReadySince time.Time
	LastCheck  time.Time
}

// IsReady returns the latest readiness result.
func (sh *ServiceHealth) IsReady() bool {
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	return sh.Ready
}

// IsAlive returns the latest liveness result.
func (sh *ServiceHealth) IsAlive() bool {
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	return sh.Alive
}

// GetReadySince returns when readiness last transitioned to success.
func (sh *ServiceHealth) GetReadySince() time.Time {
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	return sh.ReadySince
}

// GetLastCheck returns the time of the latest liveness probe.
func (sh *ServiceHealth) GetLastCheck() time.Time {
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	return sh.LastCheck
}

// setReady updates readiness while the caller holds mu.
func (sh *ServiceHealth) setReady(ready bool) {
	sh.Ready = ready
	if ready {
		sh.ReadySince = time.Now()
	}
}

// setAlive updates liveness and its timestamp while the caller holds mu.
func (sh *ServiceHealth) setAlive(alive bool) {
	sh.Alive = alive
	sh.LastCheck = time.Now()
}

// NewChecker creates an empty health monitor.
func NewChecker() *Checker {
	return &Checker{
		services: make(map[string]*ServiceHealth),
		stopCh:   make(map[string]chan struct{}),
	}
}

// StartMonitoring replaces any existing monitor for a service and starts its probes.
func (hc *Checker) StartMonitoring(name string, checkCfg *config.HealthCheckConfig) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	// Reconfiguration must not leave the previous monitor running.
	if ch, ok := hc.stopCh[name]; ok {
		close(ch)
	}

	health := &ServiceHealth{
		Ready:   false,
		Alive:   false,
		History: ringbuffer.New(50),
	}

	hc.services[name] = health

	if checkCfg == nil {
		// Missing probes are successful by definition.
		health.mu.Lock()
		health.setReady(true)
		health.setAlive(true)
		health.History.Write(formatEvent(time.Now(), "No health check configured — assuming healthy"))
		health.mu.Unlock()
		return
	}

	stopCh := make(chan struct{})
	hc.stopCh[name] = stopCh
	if checkCfg.Readiness == nil {
		health.mu.Lock()
		health.setReady(true)
		health.mu.Unlock()
	}
	if checkCfg.Liveness == nil {
		health.mu.Lock()
		health.setAlive(true)
		health.mu.Unlock()
	} else {
		// A running process is considered alive until the configured failure
		// threshold is reached. LastCheck remains zero until the first probe.
		health.mu.Lock()
		health.Alive = true
		health.mu.Unlock()
	}

	// Readiness runs until its first success.
	if checkCfg.Readiness != nil {
		go hc.runReadinessCheck(name, checkCfg.Readiness, health, stopCh)
	}

	// Liveness continues for the lifetime of the service.
	if checkCfg.Liveness != nil {
		go hc.runLivenessCheck(name, checkCfg.Liveness, health, stopCh)
	}
}

// runReadinessCheck probes until readiness succeeds or monitoring is cancelled.
func (hc *Checker) runReadinessCheck(name string, cfg *config.CheckConfig, health *ServiceHealth, stopCh chan struct{}) {
	if !waitInitialDelay(cfg.InitialDelay, stopCh) {
		return
	}
	ticker := time.NewTicker(checkInterval(cfg.Interval))
	defer ticker.Stop()

	for {
		err := executeCheck(name, cfg)
		now := time.Now()
		if err == nil {
			health.mu.Lock()
			health.setReady(true)
			health.History.Write(formatEvent(now, "Readiness passed ✓"))
			health.mu.Unlock()
			return
		}
		health.mu.Lock()
		health.History.Write(formatEvent(now, "Readiness failed: "+err.Error()))
		health.mu.Unlock()

		select {
		case <-stopCh:
			return
		case <-ticker.C:
		}
	}
}

// runLivenessCheck continuously updates liveness with failure-threshold semantics.
func (hc *Checker) runLivenessCheck(name string, cfg *config.CheckConfig, health *ServiceHealth, stopCh chan struct{}) {
	if !waitInitialDelay(cfg.InitialDelay, stopCh) {
		return
	}
	ticker := time.NewTicker(checkInterval(cfg.Interval))
	defer ticker.Stop()

	failCount := 0

	for {
		err := executeCheck(name, cfg)
		now := time.Now()
		health.mu.Lock()
		health.LastCheck = now
		if err == nil {
			failCount = 0
			health.Alive = true
			health.History.Write(formatEvent(now, "Liveness passed ✓"))
		} else {
			failCount++
			health.History.Write(formatEvent(now, "Liveness failed: "+err.Error()))
			if failCount >= failureThreshold(cfg.FailureThreshold) {
				health.Alive = false
				health.History.Write(formatEvent(now, fmt.Sprintf("UNHEALTHY: %d consecutive failures", failCount)))
			}
		}
		health.mu.Unlock()

		select {
		case <-stopCh:
			return
		case <-ticker.C:
		}
	}
}

func waitInitialDelay(delay time.Duration, stopCh <-chan struct{}) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-stopCh:
		return false
	}
}

func failureThreshold(value int) int {
	if value <= 0 {
		return 3
	}
	return value
}

func checkInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 5 * time.Second
	}
	return interval
}

// StopMonitoring cancels and removes one service monitor.
func (hc *Checker) StopMonitoring(name string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	if ch, ok := hc.stopCh[name]; ok {
		close(ch)
		delete(hc.stopCh, name)
	}
	delete(hc.services, name)
}

// GetHealth returns the synchronized health state for a service.
func (hc *Checker) GetHealth(name string) *ServiceHealth {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.services[name]
}

// StopAll cancels every active monitor.
func (hc *Checker) StopAll() {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	for name, ch := range hc.stopCh {
		close(ch)
		delete(hc.stopCh, name)
	}
	hc.services = make(map[string]*ServiceHealth)
}

// formatEvent creates a timestamped entry for the bounded health history.
func formatEvent(t time.Time, msg string) string {
	return t.Format("15:04:05") + "  " + msg
}
