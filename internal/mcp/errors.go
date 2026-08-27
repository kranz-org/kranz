package mcp

import (
	"context"
	"errors"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

func causalError(err error) *CausalError {
	if err == nil {
		return nil
	}
	var logErr *app.LogQueryError
	if errors.As(err, &logErr) {
		return &CausalError{Code: logErr.Code, Message: logErr.Message, Hint: logErr.Hint, Details: map[string]any{"selector": logErr.Selector}}
	}
	var runDelete *app.RunDeleteError
	if errors.As(err, &runDelete) {
		hint := "Use runs to inspect retained absolute run identities."
		if runDelete.Code == "confirmation_required" {
			hint = "Repeat the same call with confirm set to true."
		}
		return &CausalError{Code: runDelete.Code, Message: runDelete.Error(), Hint: hint, Details: map[string]any{"target": runTargetName(runDelete.Target), "run": runDelete.Run}}
	}
	var confirmationRequired *app.ConfirmationRequiredError
	if errors.As(err, &confirmationRequired) {
		return &CausalError{Code: "confirmation_required", Message: confirmationRequired.Error(), Hint: "Review the resolved plan, then repeat the same tool call with its confirmation_token.", Details: map[string]any{"plan": confirmationRequired.Plan, "confirmation_token": confirmationRequired.Plan.ConfirmationToken}}
	}
	var confirmation *app.ConfirmationError
	if errors.As(err, &confirmation) {
		return &CausalError{Code: confirmation.Code, Message: confirmation.Message, Hint: "Request a fresh plan before retrying."}
	}
	var waitErr *app.WaitError
	if errors.As(err, &waitErr) {
		services := make([]map[string]any, 0, len(waitErr.Services))
		for _, service := range waitErr.Services {
			services = append(services, map[string]any{"name": service.Name, "state": service.State, "health": service.Health, "desired_running": service.DesiredRunning})
		}
		return &CausalError{Code: waitErr.Code, Message: waitErr.Message, Details: map[string]any{"services": services}}
	}
	var evicted *app.ActionRunEvictedError
	if errors.As(err, &evicted) {
		return &CausalError{Code: "action_run_evicted", Message: evicted.Error(), Details: map[string]any{"owner": evicted.ID.Owner, "action": evicted.ID.Name, "run": evicted.Run, "oldest_retained_run": evicted.Oldest}}
	}
	var busy *app.ActionBusyError
	if errors.As(err, &busy) {
		return &CausalError{Code: "action_busy", Message: busy.Error(), Details: map[string]any{"requested": actionAddress(busy.Requested), "running": actionAddress(busy.Running)}}
	}
	var exit *app.ActionExitError
	if errors.As(err, &exit) {
		return &CausalError{Code: "action_failed", Message: exit.Error(), Details: map[string]any{"action": actionAddress(exit.ID), "exit_code": exit.ExitCode}}
	}
	var prerequisite *service.PrerequisiteError
	if errors.As(err, &prerequisite) {
		return &CausalError{Code: "prerequisite_failed", Message: prerequisite.Error(),
			Hint:    "Run the prerequisite action on its own to read its output, then fix what it checks.",
			Details: map[string]any{"service": prerequisite.Service, "action": actionAddress(prerequisite.Action), "run": prerequisite.Run}}
	}
	var portConflict *app.PortConflictError
	if errors.As(err, &portConflict) {
		return &CausalError{Code: "port_conflict", Message: portConflict.Error(), Details: map[string]any{"service": portConflict.Service, "port": portConflict.Port, "pid": portConflict.PID, "process": portConflict.Process, "command": portConflict.Command, "owner_service": portConflict.OwnerService, "external": portConflict.External}}
	}
	switch {
	case errors.Is(err, app.ErrActionNotFound):
		return &CausalError{Code: "action_not_found", Message: err.Error(), Hint: "Use action_list to inspect exact OWNER/ACTION identifiers."}
	case errors.Is(err, app.ErrActionRunNotFound):
		return &CausalError{Code: "action_run_not_found", Message: err.Error()}
	case errors.Is(err, app.ErrInteractiveAction):
		return &CausalError{Code: "interactive_action", Message: err.Error(), Hint: "Run this exact OWNER/ACTION from the Kranz TUI or terminal CLI."}
	case errors.Is(err, context.DeadlineExceeded):
		return &CausalError{Code: "wait_timeout", Message: err.Error()}
	case errors.Is(err, context.Canceled):
		return &CausalError{Code: "cancelled", Message: err.Error()}
	default:
		return &CausalError{Code: "operation_failed", Message: err.Error()}
	}
}

func actionAddress(id config.ActionID) string { return id.Owner + "/" + id.Name }
