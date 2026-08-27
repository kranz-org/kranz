package app

import (
	"fmt"

	"github.com/kranz-org/kranz/internal/config"
)

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
