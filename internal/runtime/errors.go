package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

// VersionMismatchError distinguishes protocol incompatibility from transport
// failures such as timeout, EOF, or a malformed frame. Registry-aware clients
// use this classification to report an incompatible live session instead of
// incorrectly calling it unreachable.
type VersionMismatchError struct {
	Message        string
	ServerProtocol int
	ServerVersion  string
}

func (e *VersionMismatchError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("runtime protocol %d from Kranz %s is incompatible", e.ServerProtocol, e.ServerVersion)
}

// encodeError turns a Go error into the wire shape. app.PortConflictError is
// the one error type internal/ui reconstructs with errors.As to drive the
// port-conflict modal (see internal/ui/model_lifecycle.go), so it is the one
// error kind that carries structured fields instead of just text.
func encodeError(err error) errorPayload {
	var runDelete *app.RunDeleteError
	if errors.As(err, &runDelete) {
		target := runDelete.Target
		return errorPayload{Kind: errorRunDelete, Code: runDelete.Code, Message: runDelete.Error(), RunTarget: &target, Run: runDelete.Run}
	}
	var required *app.ConfirmationRequiredError
	if errors.As(err, &required) {
		return errorPayload{Kind: errorConfirmationRequired, Code: "confirmation_required", Message: required.Error(), Plan: &required.Plan}
	}
	var confirmation *app.ConfirmationError
	if errors.As(err, &confirmation) {
		return errorPayload{Kind: errorConfirmation, Code: confirmation.Code, Message: confirmation.Message}
	}
	var waitErr *app.WaitError
	if errors.As(err, &waitErr) {
		return errorPayload{Kind: errorWait, Code: waitErr.Code, Message: waitErr.Message, Services: waitErr.Services}
	}
	var logQuery *app.LogQueryError
	if errors.As(err, &logQuery) {
		return errorPayload{Kind: errorLogQuery, Code: logQuery.Code, Message: logQuery.Message, Hint: logQuery.Hint, Selector: logQuery.Selector}
	}
	var evicted *app.ActionRunEvictedError
	if errors.As(err, &evicted) {
		return errorPayload{Kind: errorActionRunEvicted, Message: evicted.Error(), ActionOwner: evicted.ID.Owner, ActionName: evicted.ID.Name, Run: evicted.Run, OldestRun: evicted.Oldest}
	}
	var busy *app.ActionBusyError
	if errors.As(err, &busy) {
		return errorPayload{Kind: errorActionBusy, Message: busy.Error(), ActionOwner: busy.Requested.Owner, ActionName: busy.Requested.Name, RunningActionOwner: busy.Running.Owner, RunningActionName: busy.Running.Name}
	}
	var exit *app.ActionExitError
	if errors.As(err, &exit) {
		return errorPayload{Kind: errorActionExit, Message: exit.Error(), ActionOwner: exit.ID.Owner, ActionName: exit.ID.Name, ActionExitCode: exit.ExitCode}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errorPayload{Kind: errorActionTimedOut, Message: err.Error()}
	}
	if errors.Is(err, context.Canceled) {
		return errorPayload{Kind: errorActionCancelled, Message: err.Error()}
	}
	if errors.Is(err, app.ErrActionNotFound) {
		return errorPayload{Kind: errorActionNotFound, Message: err.Error()}
	}
	if errors.Is(err, app.ErrActionRunNotFound) {
		return errorPayload{Kind: errorActionRunNotFound, Message: err.Error()}
	}
	// A prerequisite failure keeps its structure across the wire: which service
	// did not start, which action gated it, and which run of that action.
	// Flattening it to text is what made an attached client report a specific,
	// answerable failure as a generic one.
	var prerequisite *service.PrerequisiteError
	if errors.As(err, &prerequisite) {
		return errorPayload{Kind: errorPrerequisite, Message: prerequisite.Error(), Service: prerequisite.Service,
			ActionOwner: prerequisite.Action.Owner, ActionName: prerequisite.Action.Name, Run: prerequisite.Run,
			PrerequisiteLabel: prerequisite.Label, PrerequisiteCause: fmt.Sprint(prerequisite.Cause), Code: string(prerequisite.Action.OwnerKind)}
	}
	var conflict *app.PortConflictError
	if errors.As(err, &conflict) {
		return errorPayload{
			Kind:         errorPortConflict,
			Message:      conflict.Error(),
			Service:      conflict.Service,
			Port:         conflict.Port,
			PID:          conflict.PID,
			Process:      conflict.Process,
			Command:      conflict.Command,
			OwnerService: conflict.OwnerService,
			External:     conflict.External,
		}
	}
	return errorPayload{Kind: errorGeneric, Message: err.Error()}
}

// decodeError reconstructs every error whose identity is part of a delivery
// contract. Truly generic supervisor failures still cross as text only.
func decodeError(payload errorPayload) error {
	if payload.OperationResult != nil {
		result := *payload.OperationResult
		payload.OperationResult = nil
		return &app.OperationExecutionError{Result: result, Cause: decodeError(payload)}
	}
	switch payload.Kind {
	case errorVersionMismatch:
		return &VersionMismatchError{
			Message: payload.Message, ServerProtocol: payload.ServerProtocol, ServerVersion: payload.ServerVersion,
		}
	case errorRunDelete:
		var target app.RunTarget
		if payload.RunTarget != nil {
			target = *payload.RunTarget
		}
		return &app.RunDeleteError{Code: payload.Code, Message: payload.Message, Target: target, Run: payload.Run}
	case errorPortConflict:
		return &app.PortConflictError{
			Service:      payload.Service,
			Port:         payload.Port,
			PID:          payload.PID,
			Process:      payload.Process,
			Command:      payload.Command,
			OwnerService: payload.OwnerService,
			External:     payload.External,
		}
	case errorActionNotFound:
		return fmt.Errorf("%w: %s", app.ErrActionNotFound, payload.Message)
	case errorActionRunNotFound:
		return fmt.Errorf("%w: %s", app.ErrActionRunNotFound, payload.Message)
	case errorActionRunEvicted:
		return &app.ActionRunEvictedError{ID: config.ActionID{Owner: payload.ActionOwner, Name: payload.ActionName}, Run: payload.Run, Oldest: payload.OldestRun}
	case errorActionBusy:
		return &app.ActionBusyError{Requested: config.ActionID{Owner: payload.ActionOwner, Name: payload.ActionName}, Running: config.ActionID{Owner: payload.RunningActionOwner, Name: payload.RunningActionName}}
	case errorActionExit:
		return &app.ActionExitError{ID: config.ActionID{Owner: payload.ActionOwner, Name: payload.ActionName}, ExitCode: payload.ActionExitCode}
	case errorActionTimedOut:
		return fmt.Errorf("%s: %w", payload.Message, context.DeadlineExceeded)
	case errorActionCancelled:
		return fmt.Errorf("%s: %w", payload.Message, context.Canceled)
	case errorLogQuery:
		return &app.LogQueryError{Code: payload.Code, Message: payload.Message, Hint: payload.Hint, Selector: payload.Selector}
	case errorConfirmationRequired:
		if payload.Plan == nil {
			return errors.New(payload.Message)
		}
		return &app.ConfirmationRequiredError{Plan: *payload.Plan}
	case errorConfirmation:
		return &app.ConfirmationError{Code: payload.Code, Message: payload.Message}
	case errorWait:
		return &app.WaitError{Code: payload.Code, Message: payload.Message, Services: payload.Services}
	case errorPrerequisite:
		return &service.PrerequisiteError{
			Service: payload.Service,
			Action:  config.ActionID{OwnerKind: config.ActionOwnerKind(payload.Code), Owner: payload.ActionOwner, Name: payload.ActionName},
			Run:     payload.Run, Label: payload.PrerequisiteLabel, Cause: errors.New(payload.PrerequisiteCause),
		}
	default:
		return errors.New(payload.Message)
	}
}
