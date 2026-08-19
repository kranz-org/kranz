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
	methodProject                         = "project"
	methodConfig                          = "config"
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
	methodStartServicesContext            = "startServicesContext"
	methodStopServices                    = "stopServices"
	methodForceStopServices               = "forceStopServices"
	methodForceStartServices              = "forceStartServices"
	methodStopAll                         = "stopAll"
	methodRestartAll                      = "restartAll"
	methodRestartService                  = "restartService"
	methodHasRunningServices              = "hasRunningServices"
	methodProjectExitRequested            = "projectExitRequested"
	methodShutdown                        = "shutdown"
	methodRunAction                       = "runAction"
	methodActionState                     = "actionState"
	methodCancelAction                    = "cancelAction"
	methodAcquireInteractiveAction        = "acquireInteractiveAction"
	methodCompleteInteractiveAction       = "completeInteractiveAction"
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

type actionIDRequest struct {
	ID config.ActionID `json:"id"`
}

type reloadRequest struct {
	Force bool `json:"force"`
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
