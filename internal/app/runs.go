package app

import (
	"errors"
	"fmt"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

// RunDeleteError is stable across local and runtime-backed APIs so delivery
// adapters can give the same causal response for an invalid destructive call.
type RunDeleteError struct {
	Code    string
	Target  RunTarget
	Run     uint32
	Message string
}

func (e *RunDeleteError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("cannot delete %s#%d", runTargetName(e.Target), e.Run)
}

func (l *Local) DeleteRun(target RunTarget, run uint32) (RunSummary, error) {
	deleted, err := l.manager.DeleteRun(target, run)
	if err == nil {
		return deleted, nil
	}
	code, message := "run_delete_failed", fmt.Sprintf("cannot delete %s#%d", runTargetName(target), run)
	switch {
	case errors.Is(err, service.ErrRunActive):
		code, message = "run_active", fmt.Sprintf("%s#%d is still running", runTargetName(target), run)
	case errors.Is(err, service.ErrRunNotFound):
		code, message = "run_not_found", fmt.Sprintf("%s#%d is not retained", runTargetName(target), run)
	}
	return RunSummary{}, &RunDeleteError{Code: code, Target: target, Run: run, Message: message}
}

func (l *Local) ExportRun(target RunTarget, run uint32) (RunExport, error) {
	var summary RunSummary
	found := false
	for _, candidate := range l.manager.RunSummaries(target) {
		if candidate.Run == run {
			summary, found = candidate, true
			break
		}
	}
	if !found {
		return RunExport{}, fmt.Errorf("run %s#%d is not retained", runTargetName(target), run)
	}
	var entries []config.LogEntry
	if target.Kind == RunKindService {
		service, ok := l.manager.GetService(target.Name)
		if !ok {
			return RunExport{}, fmt.Errorf("service %q not found", target.Name)
		}
		entries = service.LogEntries()
	} else {
		entries = l.manager.ActionLogs(target.Action)
	}
	filtered := make([]config.LogEntry, 0)
	for _, entry := range entries {
		if entry.Run == run {
			filtered = append(filtered, entry)
		}
	}
	var retention RunRetentionBoundary
	for _, boundary := range l.manager.RunRetentionBoundaries() {
		if boundary.Target == target {
			retention = boundary
			break
		}
	}
	return RunExport{Summary: summary, Retention: retention, Entries: filtered}, nil
}

func runTargetName(target RunTarget) string {
	if target.Kind == RunKindService {
		return target.Name
	}
	return target.Action.Owner + "/" + target.Action.Name
}
