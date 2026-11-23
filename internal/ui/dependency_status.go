package ui

import (
	"fmt"
	"strings"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

type dependencyServiceStatus struct {
	indicator string
	detail    string
}

func (status dependencyServiceStatus) render(name string) string {
	label := status.indicator + " " + name
	if status.detail != "" {
		label += " (" + status.detail + ")"
	}
	return label
}

// dependencyStatuses uses the same polled runtime snapshots as the service
// list. Probe labels describe observed results, including the time before the
// first check, and do not stand in for the service's process state.
func (m *Model) dependencyStatuses() map[string]dependencyServiceStatus {
	statuses := make(map[string]dependencyServiceStatus, len(m.runtimeServices))
	for _, svc := range m.runtimeServices {
		statuses[svc.Name] = m.dependencyStatusSnapshot(svc)
	}
	return statuses
}

func (m *Model) dependencyStatusSnapshot(svc *app.ServiceSnapshot) dependencyServiceStatus {
	visual := m.serviceVisualState(svc)
	status := dependencyServiceStatus{indicator: serviceStatusIndicator(visual)}
	var details []string
	if visual == visualStopped {
		status.indicator = StoppedBadgeStyle.Render("○")
		if svc.State.Completed {
			details = append(details, ContextBarStyle.Render(fmt.Sprintf("exit %d", svc.State.ExitCode)))
		}
	} else {
		details = append(details, strings.ToLower(serviceStatusLabel(svc.State.Status, visual)))
	}

	checks := svc.Config.HealthCheck
	if checks != nil && svc.State.Status != config.StatusStopped && svc.State.Status != config.StatusUnknown {
		if checks.Readiness != nil {
			switch {
			case svc.Health.Readiness.LastAttempt.IsZero():
				details = append(details, StartingBadgeStyle.Render("readiness pending"))
			case svc.Health.Ready:
				details = append(details, RunningBadgeStyle.Render("ready"))
			default:
				details = append(details, StartingBadgeStyle.Render("not ready"))
			}
		}
		if checks.Liveness != nil {
			switch {
			case svc.Health.Liveness.LastAttempt.IsZero():
				details = append(details, StartingBadgeStyle.Render("liveness pending"))
			case !svc.Health.Alive:
				details = append(details, FailedBadgeStyle.Render("liveness failed"))
			case svc.Health.Liveness.ConsecutiveFailures > 0:
				details = append(details, StartingBadgeStyle.Render("liveness degraded"))
			default:
				details = append(details, RunningBadgeStyle.Render("live"))
			}
		}
	}
	if svc.ReloadState == "pending_restart" {
		details = append(details, StartingBadgeStyle.Render("restart pending"))
	}
	status.detail = strings.Join(details, ", ")
	return status
}
