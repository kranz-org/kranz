package main

import (
	"context"
	"errors"

	"github.com/kranz-org/kranz/internal/app"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

// executePlanWithApproval routes CLI mutations through the same supervisor-owned,
// plan-bound, one-shot token machinery as MCP. Callers decide whether this CLI
// invocation supplied an explicit approval; without one the original
// confirmation_required result remains fail-closed.
func executePlanWithApproval(client *kranzruntime.Client, request app.PlanRequest, confirmed bool) (app.OperationResult, error) {
	result, err := client.ExecutePlan(context.Background(), request, "")
	var required *app.ConfirmationRequiredError
	if !errors.As(err, &required) {
		return result, err
	}
	if !confirmed {
		return result, err
	}
	return client.ExecutePlan(context.Background(), request, required.Plan.ConfirmationToken)
}
