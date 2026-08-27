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
	// RedactedConfig returns a detached effective config safe for delivery to
	// structured clients.
	RedactedConfig() (*config.Config, error)
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
	// Plan and ExecutePlan expose versioned resolved lifecycle/action plans and
	// supervisor-owned one-shot confirmations.
	Plan(request PlanRequest) (OperationPlan, error)
	ExecutePlan(ctx context.Context, request PlanRequest, confirmationToken string) (OperationResult, error)
	// Wait blocks inside the application layer until all selected services
	// satisfy one supported condition or ctx ends.
	Wait(ctx context.Context, request WaitRequest) (WaitResult, error)
	// Changes returns the recorded runtime transitions after a cursor. It
	// answers what happened, which the difference between two status snapshots
	// cannot: a service that restarted and came back looks unchanged in a diff.
	Changes(query ChangeQuery) (ChangeResult, error)
	// Graph returns the declared dependency and prerequisite structure of the
	// project with live service state folded in.
	Graph() Graph
	// Preflight checks the loaded configuration against the filesystem and the
	// ports it declares, without touching running services.
	Preflight() PreflightResult

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
	ForceStartServicesContext(ctx context.Context, names []string) error
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
	// ActionResult returns a current or retained action execution. Positive
	// run values are absolute and negative values are offsets from newest.
	ActionResult(id config.ActionID, run int) (ActionResult, error)
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
	AcquireInteractiveActionContext(ctx context.Context, id config.ActionID) (config.Action, string, error)
	// CompleteInteractiveAction finishes an AcquireInteractiveAction lease
	// with the outcome the caller observed running the command: the error
	// tea.ExecProcess reported, if any, plus the exit code and PID read
	// from the command's ProcessState.
	CompleteInteractiveAction(id config.ActionID, lease string, execErr error, exitCode, pid int) (ActionResult, error)
	// Runs returns the bounded run catalog independently from retained output.
	Runs() []RunSummary
	RunRetention() []RunRetentionBoundary
	ExportRun(target RunTarget, run uint32) (RunExport, error)
	DeleteRun(target RunTarget, run uint32) (RunSummary, error)

	// Logs returns the current buffered log entries for a service.
	Logs(name string) []config.LogEntry
	// ActionLogs returns the buffered log entries of one action. Lifecycle
	// actions have none: their output belongs to the service they act on.
	ActionLogs(id config.ActionID) []config.LogEntry
	// QueryLogs resolves selectors and applies run/source/since/tail/cursor
	// semantics in the application layer shared by every delivery adapter.
	QueryLogs(query LogQuery) (LogResult, error)
	// ClearLogStreams resolves the same service/tag/action targets as QueryLogs
	// and clears only those bounded buffers.
	ClearLogStreams(selectors []string, withActions bool) ([]string, error)
	// ClearActionLogs discards one action's buffered logs.
	ClearActionLogs(id config.ActionID)
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
