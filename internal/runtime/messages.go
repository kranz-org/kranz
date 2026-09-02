package runtime

import (
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// This file lists every app.API method as one RPC method name plus its
// request/response payload shapes. Pairing them here keeps client.go (which
// encodes a call) and supervisor.go (which decodes and dispatches it) from
// drifting apart on a shape only one side remembers.

const (
	methodHello                           = "hello"
	methodClients                         = "clients"
	methodProject                         = "project"
	methodConfig                          = "config"
	methodRedactedConfig                  = "redactedConfig"
	methodReload                          = "reload"
	methodAcknowledgeExternalWrite        = "acknowledgeExternalWrite"
	methodServices                        = "services"
	methodService                         = "service"
	methodTags                            = "tags"
	methodManagedServiceForPID            = "managedServiceForPID"
	methodStartConfirmationNames          = "startConfirmationNames"
	methodRequiresStopConfirmation        = "requiresStopConfirmation"
	methodAffectedServices                = "affectedServices"
	methodShutdownPlan                    = "shutdownPlan"
	methodPlan                            = "plan"
	methodExecutePlan                     = "executePlan"
	methodWait                            = "wait"
	methodChanges                         = "changes"
	methodGraph                           = "graph"
	methodPreflight                       = "preflight"
	methodStartServicesContext            = "startServicesContext"
	methodStopServices                    = "stopServices"
	methodForceStopServices               = "forceStopServices"
	methodForceStartServices              = "forceStartServices"
	methodStopAll                         = "stopAll"
	methodRestartAll                      = "restartAll"
	methodRestartService                  = "restartService"
	methodRestartServices                 = "restartServices"
	methodHasRunningServices              = "hasRunningServices"
	methodProjectExitRequested            = "projectExitRequested"
	methodShutdown                        = "shutdown"
	methodRunAction                       = "runAction"
	methodActionState                     = "actionState"
	methodActionStates                    = "actionStates"
	methodActionResult                    = "actionResult"
	methodCancelAction                    = "cancelAction"
	methodAcquireInteractiveAction        = "acquireInteractiveAction"
	methodCompleteInteractiveAction       = "completeInteractiveAction"
	methodRuns                            = "runs"
	methodRunRetention                    = "runRetention"
	methodExportRun                       = "exportRun"
	methodDeleteRun                       = "deleteRun"
	methodActionLogs                      = "actionLogs"
	methodQueryLogs                       = "queryLogs"
	methodClearLogStreams                 = "clearLogStreams"
	methodClearActionLogs                 = "clearActionLogs"
	methodLogs                            = "logs"
	methodClearLogs                       = "clearLogs"
	methodMarkLogsRead                    = "markLogsRead"
	methodHealthHistory                   = "healthHistory"
	methodInspectPorts                    = "inspectPorts"
	methodReleaseExternalPort             = "releaseExternalPort"
	methodSetServiceStatusForTest         = "setServiceStatusForTest"
	methodSetServiceStateForTest          = "setServiceStateForTest"
	methodSetServiceDesiredRunningForTest = "setServiceDesiredRunningForTest"
	methodAppendLogForTest                = "appendLogForTest"
	methodAppendLogAtForTest              = "appendLogAtForTest"
)

// ClientInfo describes one connection a runtime is serving. Several agents,
// a TUI, and a CLI can share one runtime, and after v0.11.0 an MCP process is
// no longer a registry entry of its own, so this is the only place a
// connected client is visible.
type ClientInfo struct {
	Surface     string    `json:"surface"`
	Label       string    `json:"label"`
	PID         int       `json:"pid"`
	Version     string    `json:"version"`
	ConnectedAt time.Time `json:"connected_at"`
}

type clientsResponse struct {
	Clients []ClientInfo `json:"clients"`
}

type emptyRequest struct{}
type emptyResponse struct{}

type nameRequest struct {
	Name string `json:"name"`
}

type namesRequest struct {
	Names []string `json:"names"`
}

type namesResponse struct {
	Names []string `json:"names"`
}

type clearLogStreamsRequest struct {
	Selectors   []string `json:"selectors"`
	WithActions bool     `json:"with_actions"`
}

type actionIDRequest struct {
	ID config.ActionID `json:"id"`
}

type actionResultRequest struct {
	ID  config.ActionID `json:"id"`
	Run int             `json:"run"`
}

type exportRunRequest struct {
	Target app.RunTarget `json:"target"`
	Run    uint32        `json:"run"`
}

type reloadRequest struct {
	Force bool `json:"force"`
}

type executePlanRequest struct {
	Request           app.PlanRequest `json:"request"`
	ConfirmationToken string          `json:"confirmation_token,omitempty"`
}

type serviceResponse struct {
	Service *app.ServiceSnapshot `json:"service"`
	Ok      bool                 `json:"ok"`
}

type servicesResponse struct {
	Services []*app.ServiceSnapshot `json:"services"`
}

type tagsResponse struct {
	Tags []string `json:"tags"`
}

type pidRequest struct {
	PID int `json:"pid"`
}

type serviceNameResponse struct {
	Name string `json:"name"`
}

type startConfirmationNamesRequest struct {
	Names               []string `json:"names"`
	IncludeDependencies bool     `json:"includeDependencies"`
}

type boolResponse struct {
	Value bool `json:"value"`
}

type shutdownPlanResponse struct {
	Plan app.ShutdownPlan `json:"plan"`
}

type projectExitRequestedResponse struct {
	Requested bool `json:"requested"`
	Code      int  `json:"code"`
}

type actionStatesResponse struct {
	States []app.ActionResult `json:"states"`
}

type actionStateResponse struct {
	Result app.ActionResult `json:"result"`
	Ok     bool             `json:"ok"`
}

type cancelActionResponse struct {
	Cancelled bool `json:"cancelled"`
}

type acquireInteractiveActionResponse struct {
	Action config.Action `json:"action"`
	Lease  string        `json:"lease"`
}

type completeInteractiveActionRequest struct {
	ID       config.ActionID `json:"id"`
	Lease    string          `json:"lease"`
	ExecErr  string          `json:"execErr,omitempty"`
	ExitCode int             `json:"exitCode"`
	PID      int             `json:"pid"`
}

type logsResponse struct {
	Entries []config.LogEntry `json:"entries"`
}

type healthHistoryResponse struct {
	Lines []string `json:"lines"`
}

type inspectPortsRequest struct {
	Ports []int `json:"ports"`
}

type inspectPortsResponse struct {
	Details map[int]*config.PortInfo `json:"details"`
}

type releaseExternalPortRequest struct {
	Port        int `json:"port"`
	ExpectedPID int `json:"expectedPID"`
}

type releaseExternalPortResponse struct {
	AlreadyFree bool `json:"alreadyFree"`
}

type setServiceStatusForTestRequest struct {
	Name   string               `json:"name"`
	Status config.ServiceStatus `json:"status"`
}

type setServiceStateForTestRequest struct {
	Name  string              `json:"name"`
	State config.ServiceState `json:"state"`
}

type setServiceDesiredRunningForTestRequest struct {
	Name           string `json:"name"`
	DesiredRunning bool   `json:"desiredRunning"`
}

type appendLogForTestRequest struct {
	Name string `json:"name"`
	Line string `json:"line"`
}

type appendLogAtForTestRequest struct {
	Name      string    `json:"name"`
	Timestamp time.Time `json:"timestamp"`
	Line      string    `json:"line"`
}
