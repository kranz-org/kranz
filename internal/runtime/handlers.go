package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// handlerFunc decodes a request body, calls into local, and returns a value
// dispatch will marshal as the response body.
type handlerFunc func(ctx context.Context, local *app.Local, body json.RawMessage) (any, error)

// handler adapts a typed app.Local call into a handlerFunc, so each method's
// registration below states only what it decodes and what it calls — the
// JSON plumbing is identical for every method and lives here once.
func handler[Req any, Resp any](fn func(ctx context.Context, local *app.Local, req Req) (Resp, error)) handlerFunc {
	return func(ctx context.Context, local *app.Local, body json.RawMessage) (any, error) {
		var req Req
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, fmt.Errorf("decode request: %w", err)
			}
		}
		return fn(ctx, local, req)
	}
}

// handlers is the server's method dispatch table. It is built once at
// package init from app.API's exact surface as of this stream; adding a
// method to app.API without adding it here fails loudly at first use
// (dispatch returns "unknown method"), not silently.
var handlers = map[string]handlerFunc{
	methodProject: handler(func(_ context.Context, l *app.Local, _ emptyRequest) (app.ProjectSnapshot, error) {
		return l.Project(), nil
	}),
	methodConfig: handler(func(_ context.Context, l *app.Local, _ emptyRequest) (*config.Config, error) {
		return l.Config(), nil
	}),
	methodReload: handler(func(_ context.Context, l *app.Local, req reloadRequest) (app.ReloadResult, error) {
		return l.Reload(req.Force)
	}),
	methodAcknowledgeExternalWrite: handler(func(_ context.Context, l *app.Local, _ emptyRequest) (emptyResponse, error) {
		l.AcknowledgeExternalWrite()
		return emptyResponse{}, nil
	}),
	methodServices: handler(func(_ context.Context, l *app.Local, _ emptyRequest) (servicesResponse, error) {
		return servicesResponse{Services: l.Services()}, nil
	}),
	methodService: handler(func(_ context.Context, l *app.Local, req nameRequest) (serviceResponse, error) {
		svc, ok := l.Service(req.Name)
		return serviceResponse{Service: svc, Ok: ok}, nil
	}),
	methodTags: handler(func(_ context.Context, l *app.Local, _ emptyRequest) (tagsResponse, error) {
		return tagsResponse{Tags: l.Tags()}, nil
	}),
	methodManagedServiceForPID: handler(func(_ context.Context, l *app.Local, req pidRequest) (serviceNameResponse, error) {
		return serviceNameResponse{Name: l.ManagedServiceForPID(req.PID)}, nil
	}),
	methodStartConfirmationNames: handler(func(_ context.Context, l *app.Local, req startConfirmationNamesRequest) (namesResponse, error) {
		return namesResponse{Names: l.StartConfirmationNames(req.Names, req.IncludeDependencies)}, nil
	}),
	methodRequiresStopConfirmation: handler(func(_ context.Context, l *app.Local, req namesRequest) (boolResponse, error) {
		return boolResponse{Value: l.RequiresStopConfirmation(req.Names)}, nil
	}),
	methodAffectedServices: handler(func(_ context.Context, l *app.Local, req nameRequest) (namesResponse, error) {
		return namesResponse{Names: l.AffectedServices(req.Name)}, nil
	}),
	methodShutdownPlan: handler(func(_ context.Context, l *app.Local, _ emptyRequest) (shutdownPlanResponse, error) {
		return shutdownPlanResponse{Plan: l.ShutdownPlan()}, nil
	}),
	methodStartServicesContext: handler(func(ctx context.Context, l *app.Local, req namesRequest) (emptyResponse, error) {
		return emptyResponse{}, l.StartServicesContext(ctx, req.Names)
	}),
	methodStopServices: handler(func(_ context.Context, l *app.Local, req namesRequest) (emptyResponse, error) {
		return emptyResponse{}, l.StopServices(req.Names)
	}),
	methodForceStopServices: handler(func(_ context.Context, l *app.Local, req namesRequest) (emptyResponse, error) {
		return emptyResponse{}, l.ForceStopServices(req.Names)
	}),
	methodForceStartServices: handler(func(_ context.Context, l *app.Local, req namesRequest) (emptyResponse, error) {
		return emptyResponse{}, l.ForceStartServices(req.Names)
	}),
	methodStopAll: handler(func(_ context.Context, l *app.Local, _ emptyRequest) (emptyResponse, error) {
		return emptyResponse{}, l.StopAll()
	}),
	methodRestartAll: handler(func(_ context.Context, l *app.Local, _ emptyRequest) (emptyResponse, error) {
		return emptyResponse{}, l.RestartAll()
	}),
	methodRestartService: handler(func(_ context.Context, l *app.Local, req nameRequest) (emptyResponse, error) {
		return emptyResponse{}, l.RestartService(req.Name)
	}),
	methodRestartServices: handler(func(_ context.Context, l *app.Local, req namesRequest) (emptyResponse, error) {
		return emptyResponse{}, l.RestartServices(req.Names)
	}),
	methodHasRunningServices: handler(func(_ context.Context, l *app.Local, _ emptyRequest) (boolResponse, error) {
		return boolResponse{Value: l.HasRunningServices()}, nil
	}),
	methodProjectExitRequested: handler(func(_ context.Context, l *app.Local, _ emptyRequest) (projectExitRequestedResponse, error) {
		requested, code := l.ProjectExitRequested()
		return projectExitRequestedResponse{Requested: requested, Code: code}, nil
	}),
	methodShutdown: handler(func(_ context.Context, l *app.Local, _ emptyRequest) (emptyResponse, error) {
		return emptyResponse{}, l.Shutdown()
	}),
	methodRunAction: handler(func(ctx context.Context, l *app.Local, req actionIDRequest) (app.ActionResult, error) {
		return l.RunAction(ctx, req.ID)
	}),
	methodActionState: handler(func(_ context.Context, l *app.Local, req actionIDRequest) (actionStateResponse, error) {
		result, ok := l.ActionState(req.ID)
		return actionStateResponse{Result: result, Ok: ok}, nil
	}),
	methodCancelAction: handler(func(_ context.Context, l *app.Local, req actionIDRequest) (cancelActionResponse, error) {
		return cancelActionResponse{Cancelled: l.CancelAction(req.ID)}, nil
	}),
	methodAcquireInteractiveAction: handler(func(_ context.Context, l *app.Local, req actionIDRequest) (acquireInteractiveActionResponse, error) {
		action, lease, err := l.AcquireInteractiveAction(req.ID)
		return acquireInteractiveActionResponse{Action: action, Lease: lease}, err
	}),
	methodCompleteInteractiveAction: handler(func(_ context.Context, l *app.Local, req completeInteractiveActionRequest) (app.ActionResult, error) {
		var execErr error
		if req.ExecErr != "" {
			execErr = fmt.Errorf("%s", req.ExecErr)
		}
		return l.CompleteInteractiveAction(req.ID, req.Lease, execErr, req.ExitCode, req.PID)
	}),
	methodLogs: handler(func(_ context.Context, l *app.Local, req nameRequest) (logsResponse, error) {
		return logsResponse{Entries: l.Logs(req.Name)}, nil
	}),
	methodClearLogs: handler(func(_ context.Context, l *app.Local, req nameRequest) (emptyResponse, error) {
		l.ClearLogs(req.Name)
		return emptyResponse{}, nil
	}),
	methodMarkLogsRead: handler(func(_ context.Context, l *app.Local, req nameRequest) (emptyResponse, error) {
		l.MarkLogsRead(req.Name)
		return emptyResponse{}, nil
	}),
	methodHealthHistory: handler(func(_ context.Context, l *app.Local, req nameRequest) (healthHistoryResponse, error) {
		return healthHistoryResponse{Lines: l.HealthHistory(req.Name)}, nil
	}),
	methodInspectPorts: handler(func(_ context.Context, l *app.Local, req inspectPortsRequest) (inspectPortsResponse, error) {
		details, err := l.InspectPorts(req.Ports)
		return inspectPortsResponse{Details: details}, err
	}),
	methodReleaseExternalPort: handler(func(_ context.Context, l *app.Local, req releaseExternalPortRequest) (releaseExternalPortResponse, error) {
		alreadyFree, err := l.ReleaseExternalPort(req.Port, req.ExpectedPID)
		return releaseExternalPortResponse{AlreadyFree: alreadyFree}, err
	}),
	methodSetServiceStatusForTest: handler(func(_ context.Context, l *app.Local, req setServiceStatusForTestRequest) (emptyResponse, error) {
		l.SetServiceStatusForTest(req.Name, req.Status)
		return emptyResponse{}, nil
	}),
	methodSetServiceStateForTest: handler(func(_ context.Context, l *app.Local, req setServiceStateForTestRequest) (emptyResponse, error) {
		l.SetServiceStateForTest(req.Name, req.State)
		return emptyResponse{}, nil
	}),
	methodSetServiceDesiredRunningForTest: handler(func(_ context.Context, l *app.Local, req setServiceDesiredRunningForTestRequest) (emptyResponse, error) {
		l.SetServiceDesiredRunningForTest(req.Name, req.DesiredRunning)
		return emptyResponse{}, nil
	}),
	methodAppendLogForTest: handler(func(_ context.Context, l *app.Local, req appendLogForTestRequest) (emptyResponse, error) {
		l.AppendLogForTest(req.Name, req.Line)
		return emptyResponse{}, nil
	}),
	methodAppendLogAtForTest: handler(func(_ context.Context, l *app.Local, req appendLogAtForTestRequest) (emptyResponse, error) {
		l.AppendLogAtForTest(req.Name, req.Timestamp, req.Line)
		return emptyResponse{}, nil
	}),
}
