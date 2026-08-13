package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// The dependency graph: ordering, readiness gates, and the transitive sets a
// start or stop expands into.

func (m *Manager) waitForDependencyGroup(ctx context.Context, group []string, selected map[string]bool) error {
	for _, dependencyName := range group {
		if !selected[dependencyName] {
			continue
		}
		conditions := m.conditionsForDependency(dependencyName, selected)
		for _, condition := range conditions {
			if err := m.waitForDependencyCondition(ctx, dependencyName, condition); err != nil {
				m.handleSkippedDependents(dependencyName, selected)
				return err
			}
		}
	}
	return nil
}

func (m *Manager) conditionsForDependency(dependencyName string, selected map[string]bool) []config.DependencyCondition {
	seen := make(map[config.DependencyCondition]bool)
	var result []config.DependencyCondition
	for _, dependent := range m.Services() {
		if !selected[dependent.Name] || !containsName(dependent.Config.DependsOn, dependencyName) {
			continue
		}
		condition := config.DependencyHealthy
		if configured, ok := dependent.Config.DependencyConditions[dependencyName]; ok && configured.Condition != "" {
			condition = configured.Condition
		}
		if !seen[condition] {
			seen[condition] = true
			result = append(result, condition)
		}
	}
	return result
}

func (m *Manager) waitForDependencyCondition(ctx context.Context, name string, condition config.DependencyCondition) error {
	svc, ok := m.GetService(name)
	if !ok {
		return fmt.Errorf("dependency %s disappeared", name)
	}
	if condition == config.DependencyHealthy {
		if m.waitForReadiness(ctx, name, 30*time.Second) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("dependency %s did not become healthy within 30s", name)
	}
	var readyPattern *regexp.Regexp
	if condition == config.DependencyLogReady {
		var err error
		readyPattern, err = regexp.Compile(svc.Config.ReadyLogLine)
		if err != nil {
			return fmt.Errorf("dependency %s ready_log_line: %w", name, err)
		}
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	startedDeadline := time.NewTimer(30 * time.Second)
	defer startedDeadline.Stop()
	for {
		state := svc.GetState()
		switch condition {
		case config.DependencyStarted:
			if !state.StartedAt.IsZero() && state.Status != config.StatusStopped && state.Status != config.StatusUnknown {
				return nil
			}
		case config.DependencyCompleted:
			if state.Completed {
				return nil
			}
		case config.DependencyCompletedSuccessfully:
			if state.Completed {
				if m.successfulExit(svc.Config, state.ExitCode) {
					return nil
				}
				return fmt.Errorf("dependency %s completed with exit code %d", name, state.ExitCode)
			}
		case config.DependencyLogReady:
			for _, line := range svc.Logs.Lines() {
				if readyPattern.MatchString(line) {
					return nil
				}
			}
		default:
			return fmt.Errorf("dependency %s has unsupported condition %q", name, condition)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-startedDeadline.C:
			if condition == config.DependencyStarted {
				return fmt.Errorf("dependency %s did not start within 30s", name)
			}
			startedDeadline.Reset(24 * time.Hour)
		}
	}
}

func (m *Manager) handleSkippedDependents(dependencyName string, selected map[string]bool) {
	for _, dependent := range m.Services() {
		if selected[dependent.Name] && containsName(dependent.Config.DependsOn, dependencyName) && dependent.Config.Availability.ExitOnSkipped {
			m.requestProjectExit(1)
			return
		}
	}
}

func containsName(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// groupByDependencyLevel groups independent services for parallel readiness gating.

func (m *Manager) groupByDependencyLevel(order []string) [][]string {
	graph := m.configSnapshot().GetDependsOn()
	levels := make(map[string]int)

	// A service level is one more than its deepest dependency.
	for _, name := range order {
		level := 0
		for _, dep := range graph[name] {
			if levels[dep] >= level {
				level = levels[dep] + 1
			}
		}
		levels[name] = level
	}

	// Pre-size the level buckets from the deepest service.
	maxLevel := 0
	for _, lvl := range levels {
		if lvl > maxLevel {
			maxLevel = lvl
		}
	}

	// Services within one level have no ordering dependency on each other.
	groups := make([][]string, maxLevel+1)
	for i := range groups {
		groups[i] = make([]string, 0)
	}
	for _, name := range order {
		lvl := levels[name]
		groups[lvl] = append(groups[lvl], name)
	}

	return groups
}

// StartByTags starts services matching at least one tag and their dependencies.

func (m *Manager) expandWithDependencies(names []string) (map[string]bool, error) {
	selected := make(map[string]bool, len(names))
	var visit func(string) error
	visit = func(name string) error {
		if selected[name] {
			return nil
		}
		svc, ok := m.GetService(name)
		if !ok {
			return fmt.Errorf("service %q not found", name)
		}
		selected[name] = true
		for _, dependency := range svc.Config.DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return selected, nil
}

// Shutdown rejects new starts and stops every child process exactly once.

func (m *Manager) GetAffectedServices(name string) []string {
	order, err := m.topologicalSort()
	if err != nil {
		return []string{name}
	}
	set := map[string]bool{name: true}
	for _, dependent := range m.findDependents(name) {
		if svc, ok := m.GetService(dependent); ok && svc.Status() != config.StatusStopped {
			set[dependent] = true
		}
	}
	result := make([]string, 0, len(set))
	for _, serviceName := range order {
		if set[serviceName] {
			result = append(result, serviceName)
		}
	}
	return result
}

// topologicalSort orders services with Kahn's algorithm.

func (m *Manager) topologicalSort() ([]string, error) {
	cfg := m.configSnapshot()
	graph := cfg.GetDependsOn()
	names := cfg.ServiceNames()
	inDegree := make(map[string]int, len(names))
	dependents := make(map[string][]string, len(names))

	for _, name := range names {
		inDegree[name] = len(graph[name])
		for _, dependency := range graph[name] {
			dependents[dependency] = append(dependents[dependency], name)
		}
	}

	var queue []string
	for _, name := range names {
		if inDegree[name] == 0 {
			queue = append(queue, name)
		}
	}

	var result []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		for _, dependent := range dependents[node] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(result) != len(names) {
		return nil, errors.New("dependency cycle detected")
	}

	return result, nil
}

// findDependents returns every transitive dependent of a service.

func (m *Manager) findDependents(name string) []string {
	graph := m.configSnapshot().GetDependsOn()
	var result []string
	visited := make(map[string]bool)

	var dfs func(current string)
	dfs = func(current string) {
		for svcName, deps := range graph {
			for _, dep := range deps {
				if dep == current && !visited[svcName] {
					visited[svcName] = true
					result = append(result, svcName)
					dfs(svcName)
				}
			}
		}
	}

	dfs(name)
	return result
}

// monitorProcess drains output, observes completion, and applies recovery policy.
