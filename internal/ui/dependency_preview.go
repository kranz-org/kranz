package ui

import (
	"fmt"
	"slices"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

type dependencyPreview struct {
	Requested      []string
	Steps          []dependencyPreviewStep
	Notes          []string
	restartTargets []string
}

type dependencyPreviewStep struct {
	Action     string
	Waves      [][]string
	Sequential bool
}

// Preview uses the targets captured when the modal opened and the current
// cached runtime snapshots. Redrawing the modal needs no RPC.
func (m *Model) previewDependencyOperation(operation string, selectedOnly bool) (dependencyPreview, error) {
	names := m.dependencyModal.roots
	if m.mode != ModeDependencies {
		names = m.dependencyTargetNames()
	}
	preview := dependencyPreview{Requested: names}
	if len(preview.Requested) == 0 {
		return preview, nil
	}

	services := make(map[string]*app.ServiceSnapshot, len(m.runtimeServices))
	for _, snapshot := range m.runtimeServices {
		services[snapshot.Name] = snapshot
	}
	removed := make(map[string]bool)
	for _, change := range m.runtimePending {
		if change.Kind == "remove" {
			removed[change.Name] = true
		}
	}
	for _, name := range preview.Requested {
		if _, ok := m.cfg.Services[name]; !ok || removed[name] {
			return preview, fmt.Errorf("service %q is no longer configured", name)
		}
		if services[name] == nil {
			return preview, fmt.Errorf("status is unavailable for service %q", name)
		}
	}

	startable := func(name string) bool {
		return services[name] != nil && services[name].CanStart
	}
	if selectedOnly && operation == "start" {
		ordered := m.orderSelectedStarts(preview.Requested)
		preview.addSequence("Start", matchingDependencyTargets(ordered, startable))
		return preview, nil
	}

	operationTargets := preview.Requested
	if operation == "restart" {
		// Restart in the viewer applies to active roots. The supervisor's
		// restart API also accepts stopped roots, so filter those before both
		// expanding dependents and passing the targets to the supervisor.
		preview.restartTargets = matchingDependencyTargets(preview.Requested, func(name string) bool {
			snapshot := services[name]
			switch snapshot.State.Status {
			case config.StatusStopped, config.StatusStopping:
				return false
			case config.StatusUnknown:
				return snapshot.DesiredRunning
			default:
				return true
			}
		})
		operationTargets = preview.restartTargets
		if len(operationTargets) == 0 {
			return preview, nil
		}
		if len(operationTargets) < len(preview.Requested) {
			preview.Notes = append(preview.Notes, "Inactive selected services are skipped.")
		}
	}

	order, waves, err := app.ServiceDependencyOrder(m.cfg)
	if err != nil {
		return preview, err
	}
	requested := nameSet(operationTargets)
	active := func(name string) bool {
		snapshot := services[name]
		return snapshot != nil && snapshot.State.Status != config.StatusStopped
	}

	switch operation {
	case "start":
		targets := m.dependencyClosure(preview.Requested)
		step := dependencyPreviewStep{Action: "Start"}
		for _, level := range waves {
			names := matchingDependencyTargets(level, func(name string) bool { return targets[name] && startable(name) })
			if len(names) > 0 {
				step.Waves = append(step.Waves, names)
			}
		}
		if len(step.Waves) > 0 {
			preview.Steps = append(preview.Steps, step)
		}
	case "stop", "restart":
		targets := requested
		if !selectedOnly {
			targets = m.dependentClosure(operationTargets)
		}
		if operation == "restart" {
			// Match the supervisor's expansion of active dependents.
			targets = nameSet(matchingDependencyTargets(order, func(name string) bool {
				return requested[name] || targets[name] && active(name)
			}))
			for _, name := range order {
				snapshot := services[name]
				if !targets[name] || removed[name] || snapshot == nil || !snapshot.Config.IsDetached() {
					continue
				}
				if active(name) && snapshot.Config.Lifecycle.Stop == nil {
					return preview, fmt.Errorf("cannot restart because service %q has no stop capability", name)
				}
				if snapshot.Config.Lifecycle.Start == nil && snapshot.ReloadState != "pending_restart" {
					return preview, fmt.Errorf("cannot restart because service %q has no start capability", name)
				}
			}
		}
		toStop := matchingDependencyTargets(order, func(name string) bool {
			if !targets[name] {
				return false
			}
			snapshot := services[name]
			if snapshot == nil || (!active(name) && !snapshot.DesiredRunning) {
				return false
			}
			if snapshot.Config.IsDetached() && operation == "restart" {
				return active(name) && (!removed[name] || snapshot.CanStop)
			}
			if snapshot.Config.IsDetached() && !snapshot.CanStop {
				preview.Notes = append(preview.Notes, fmt.Sprintf("%s cannot currently be stopped.", name))
				return false
			}
			return true
		})
		slices.Reverse(toStop)
		preview.addSequence("Stop", toStop)
		if operation == "restart" {
			toRemove := matchingDependencyTargets(order, func(name string) bool {
				return targets[name] && removed[name]
			})
			for _, name := range toRemove {
				if snapshot := services[name]; snapshot != nil && active(name) && snapshot.Config.IsDetached() && !snapshot.CanStop {
					preview.Notes = append(preview.Notes, fmt.Sprintf("%s will be removed from configuration while its external resource is left as-is.", name))
				}
			}
			preview.addSequence("Remove from configuration", toRemove)
			toStart := matchingDependencyTargets(order, func(name string) bool {
				return targets[name] && !removed[name]
			})
			for _, name := range toStart {
				if snapshot := services[name]; snapshot != nil && snapshot.ReloadState == "pending_restart" {
					preview.Notes = append(preview.Notes, fmt.Sprintf("%s has configuration changes pending restart.", name))
				}
			}
			preview.addSequence("Start", toStart)
		}
	default:
		return preview, fmt.Errorf("unknown lifecycle operation %q", operation)
	}
	return preview, nil
}

func (preview dependencyPreview) runnable(err error) bool {
	return err == nil && len(preview.Steps) > 0
}

func (preview *dependencyPreview) addSequence(action string, names []string) {
	if len(names) > 0 {
		preview.Steps = append(preview.Steps, dependencyPreviewStep{
			Action: action, Waves: [][]string{names}, Sequential: true,
		})
	}
}

func matchingDependencyTargets(order []string, matches func(string) bool) []string {
	var targets []string
	for _, name := range order {
		if matches(name) {
			targets = append(targets, name)
		}
	}
	return targets
}

// dependencyPlanLinks shows declared edges between the services in a plan
// section. These edges also describe exact-target plans, whose execution
// does not necessarily wait for the corresponding readiness gates.
func (m *Model) dependencyPlanLinks(names []string) map[string][]string {
	visible := nameSet(names)
	links := make(map[string][]string)
	for _, name := range names {
		for _, dependency := range m.cfg.Services[name].DependsOn {
			if visible[dependency] && !slices.Contains(links[name], dependency) {
				links[name] = append(links[name], dependency)
			}
		}
	}
	return links
}
