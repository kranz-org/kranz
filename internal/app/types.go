// Package app is the shared application layer between Kranz's delivery
// surfaces. It owns the runtime (service.Manager, health.Checker, and
// port.Checker) and exposes it as value snapshots and plain operations, so a
// TUI, a CLI, and a future MCP adapter can drive the same lifecycle, action,
// and configuration contracts without any of them reaching into the runtime
// packages directly.
package app

import (
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/health"
	"github.com/kranz-org/kranz/internal/service"
)

// ResolveCheckTarget re-exports health.ResolveCheckTarget: it resolves a
// dynamic-port health check against a service's currently detected ports. It
// is a pure function over config values, not runtime state, but a delivery
// surface still reaches it through app rather than importing internal/health
// itself, to keep the boundary a single, checkable rule.
var ResolveCheckTarget = health.ResolveCheckTarget

// ActionResult, ActionStatus, ReloadResult, and PortConflictError are already
// plain value types in internal/service. Aliasing them here (rather than
// redeclaring the type) keeps their behaviour, including ActionStatus's
// String method, while letting every other package reference them through
// app instead of importing internal/service itself.
type (
	ActionResult      = service.ActionResult
	ActionStatus      = service.ActionStatus
	ReloadResult      = service.ReloadResult
	PortConflictError = service.PortConflictError
	ActionBusyError   = service.ActionBusyError
	ActionExitError   = service.ActionExitError
)

// Re-exported so a caller never has to import internal/service for a
// constant it needs to compare an ActionResult.Status against.
const (
	ActionReady     = service.ActionReady
	ActionRunning   = service.ActionRunning
	ActionSucceeded = service.ActionSucceeded
	ActionFailed    = service.ActionFailed
	ActionTimedOut  = service.ActionTimedOut
	ActionCancelled = service.ActionCancelled
)

var (
	ErrActionNotFound       = service.ErrActionNotFound
	ErrActionRunnerStopping = service.ErrActionRunnerStopping
	ErrInteractiveAction    = service.ErrInteractiveAction
)

// HealthSnapshot is a value copy of a service's readiness/liveness state.
// Observed is false until health monitoring has produced a first result for
// the service, mirroring health.Checker.GetHealth returning nil.
type HealthSnapshot struct {
	Observed   bool
	Ready      bool
	Alive      bool
	ReadySince time.Time
	LastCheck  time.Time
}

// ServiceSnapshot is a point-in-time, concurrency-safe copy of one service's
// configuration and runtime state. It intentionally carries no live pointer
// back into the runtime: every field is a value, so the same type can later
// travel across the IPC boundary a future stream adds without redesign.
type ServiceSnapshot struct {
	Name           string
	Config         config.Service
	State          config.ServiceState
	DetectedPorts  []int
	DesiredRunning bool
	StatusObserved bool
	CanStart       bool
	CanStop        bool
	Health         HealthSnapshot
}

// ServiceStartPlanned reports whether a service is running, starting, or
// queued to start — the same "counts as active for toggle purposes" test the
// dashboard's start/stop button and quit plan both need.
func ServiceStartPlanned(svc *ServiceSnapshot) bool {
	if svc == nil {
		return false
	}
	if svc.State.Status == config.StatusUnknown {
		return svc.DesiredRunning
	}
	return svc.State.Status != config.StatusStopped || svc.DesiredRunning
}

// ProjectSnapshot describes the currently loaded configuration and the
// health of its hot-reload pipeline.
type ProjectSnapshot struct {
	Name            string
	Version         string
	Source          config.SourceFormat
	ConfigPaths     []string
	WatchPaths      []string
	Generation      uint64
	LoadedAt        time.Time
	LastReloadError string
}

// ShutdownPlan describes what a full shutdown will do to every active
// service: which managed processes stop, which detached resources run their
// configured stop command, and which detached resources are left running
// because Kranz never owned their lifecycle.
type ShutdownPlan struct {
	Managed      []string
	DetachedStop []string
	DetachedKeep []string
}
