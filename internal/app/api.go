package app

import (
	"context"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/port"
)

// API is the shared contract between Kranz's delivery surfaces (today the
// TUI; a future CLI and MCP adapter reuse it) and the runtime that owns
// process-supervised services. Local implements it directly over
// service.Manager; a later stream adds an IPC client implementation with an
// identical surface.
type API interface {
	// Project describes the currently loaded configuration.
	Project() ProjectSnapshot
	// Config returns the effective configuration. Callers must treat it as
	// read-only: it is the same value the runtime is using.
	Config() *config.Config
	// Reload re-reads the configuration from disk if any watched path
	// changed since the last successful load (or unconditionally, if
	// force is true), and reconciles it into the running services.
	Reload(force bool) (ReloadResult, error)
	// AcknowledgeExternalWrite re-stamps the watched configuration paths
	// without reloading. Call it right after writing to one of them (for
	// example, saving a theme to the project file) so the next Reload does
	// not treat that write as an external change worth reconciling.
	AcknowledgeExternalWrite()

	// Services returns every configured service in stable declaration
	// order.
	Services() []*ServiceSnapshot
	// Service returns one service by name.
	Service(name string) (*ServiceSnapshot, bool)
	// Tags returns every unique configured service tag.
	Tags() []string
	// ManagedServiceForPID returns the Kranz service that owns pid, or ""
	// if no configured service owns it.
	ManagedServiceForPID(pid int) string

	// StartConfirmationNames returns, in dependency order, every service
	// among names (and, if includeDependencies is true, their transitive
	// dependencies) whose start action requires confirmation.
	StartConfirmationNames(names []string, includeDependencies bool) []string
	// RequiresStopConfirmation reports whether stopping every one of names
	// would stop at least one service capable of being stopped.
	RequiresStopConfirmation(names []string) bool
	// AffectedServices returns name followed by its transitive dependents
	// that are not already stopped, in dependency order.
	AffectedServices(name string) []string
	// ShutdownPlan describes what a full shutdown will do right now.
	ShutdownPlan() ShutdownPlan

	// StartServicesContext starts names and their dependencies, honoring
	// ctx cancellation while it waits on dependency readiness gates.
	StartServicesContext(ctx context.Context, names []string) error
	// StopServices stops names and every transitive dependent.
	StopServices(names []string) error
	// ForceStopServices stops exactly names, without expanding dependents.
	ForceStopServices(names []string) error
	// ForceStartServices starts exactly names, without expanding or
	// waiting on dependencies.
	ForceStartServices(names []string) error
	// StopAll stops every configured service in reverse dependency order.
	StopAll() error
	// RestartAll restarts every service that was active when called.
	RestartAll() error
	// RestartService restarts name and its transitive dependents.
	RestartService(name string) error
	// RestartServices restarts names and their active transitive dependents in
	// one config generation.
	RestartServices(names []string) error
	// HasRunningServices reports whether any service is running, starting,
	// stopping, or unhealthy.
	HasRunningServices() bool
	// ProjectExitRequested reports whether an availability policy has
	// asked the whole project to exit, and with what code.
	ProjectExitRequested() (bool, int)
	// Shutdown stops every service that should stop on exit and releases
	// the runtime. It is idempotent from the caller's perspective only if
	// the caller itself guards against calling it twice; Local does not
	// re-guard an already-shut-down runtime.
	Shutdown() error

	// RunAction runs a non-interactive action to completion.
	RunAction(ctx context.Context, id config.ActionID) (ActionResult, error)
	// ActionState returns the last known result for id, if any action
	// with that identity has ever run or is configured.
	ActionState(id config.ActionID) (ActionResult, bool)
	// CancelAction cancels a running action, reporting whether one was
	// running to cancel.
	CancelAction(id config.ActionID) bool
	// AcquireInteractiveAction reserves an interactive action's execution
	// slot and returns its resolved definition plus a lease token. The
	// caller builds and runs the command itself, with direct terminal
	// access — see BuildInteractiveCommand — and reports the outcome
	// through CompleteInteractiveAction. Splitting the reservation this way
	// (rather than handing back a live *exec.Cmd, as Manager still does)
	// lets an IPC-backed API implementation support interactive actions:
	// neither a process handle nor a closure over one survives the wire.
	AcquireInteractiveAction(id config.ActionID) (config.Action, string, error)
	// CompleteInteractiveAction finishes an AcquireInteractiveAction lease
	// with the outcome the caller observed running the command: the error
	// tea.ExecProcess reported, if any, plus the exit code and PID read
	// from the command's ProcessState.
	CompleteInteractiveAction(id config.ActionID, lease string, execErr error, exitCode, pid int) (ActionResult, error)

	// Logs returns the current buffered log entries for a service.
	Logs(name string) []config.LogEntry
	// ClearLogs discards a service's buffered logs and resets its unread
	// count.
	ClearLogs(name string)
	// MarkLogsRead resets a service's unread log count without discarding
	// its logs.
	MarkLogsRead(name string)
	// HealthHistory returns the recorded readiness/liveness event lines
	// for a service, oldest first.
	HealthHistory(name string) []string

	// InspectPorts checks the current owner, if any, of each port.
	InspectPorts(ports []int) (map[int]*config.PortInfo, error)
	// ReleaseExternalPort verifies a port is still held by expectedPID and,
	// if so, terminates that external process. It refuses to act if the
	// port's owner has changed, including to a Kranz-managed service.
	// alreadyFree reports the port had no owner at all, which the caller
	// should treat as success without attributing it to this call.
	ReleaseExternalPort(portNumber, expectedPID int) (alreadyFree bool, err error)

	// SetPortChecker replaces the port checker. It exists for tests that
	// need a deterministic port checker; production callers should not
	// need it.
	SetPortChecker(checker port.Checker)

	// The three methods below exist for tests that need a service in a
	// specific runtime state, or with specific log content, without
	// spawning and observing a real process. Production delivery surfaces
	// should never call them: state changes production code, from a
	// TUI or a CLI, always earns by going through the lifecycle methods.
	SetServiceStatusForTest(name string, status config.ServiceStatus)
	SetServiceStateForTest(name string, state config.ServiceState)
	SetServiceDesiredRunningForTest(name string, desiredRunning bool)
	AppendLogForTest(name, line string)
	AppendLogAtForTest(name string, timestamp time.Time, line string)
}
