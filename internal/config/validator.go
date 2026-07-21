package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Validate checks project metadata, commands, dependencies, ports, and probes.
func Validate(cfg *Config) error {
	if cfg.Project == "" {
		return fmt.Errorf("field 'project' is required")
	}
	switch cfg.UI.Background {
	case "", UIBackgroundTerminal, UIBackgroundTheme:
	default:
		return fmt.Errorf("ui.background must be terminal or theme, got %q", cfg.UI.Background)
	}
	switch cfg.UI.ColorMode {
	case "", UIColorModeAuto, UIColorModeDark, UIColorModeLight:
	default:
		return fmt.Errorf("ui.color_mode must be auto, dark, or light, got %q", cfg.UI.ColorMode)
	}

	if len(cfg.Services) == 0 {
		return fmt.Errorf("section 'services' must contain at least one service")
	}

	svcNames := make(map[string]bool)
	for name := range cfg.Services {
		svcNames[name] = true
	}

	for name, svc := range cfg.Services {
		if svc.Command == "" {
			return fmt.Errorf("service %q: field 'command' is required", name)
		}

		// Validate references before cycle detection to produce actionable errors.
		for _, dep := range svc.DependsOn {
			if dep == name {
				return fmt.Errorf("service %q cannot depend on itself", name)
			}
			if !svcNames[dep] {
				return fmt.Errorf("service %q: dependency %q was not found", name, dep)
			}
		}
		for dependency, dependencyConfig := range svc.DependencyConditions {
			if !svcNames[dependency] {
				return fmt.Errorf("service %q: dependency condition refers to unknown service %q", name, dependency)
			}
			switch dependencyConfig.Condition {
			case "", DependencyStarted, DependencyHealthy, DependencyCompleted, DependencyCompletedSuccessfully, DependencyLogReady:
			default:
				return fmt.Errorf("service %q: dependency %q has unknown condition %q", name, dependency, dependencyConfig.Condition)
			}
			if dependencyConfig.Condition == DependencyLogReady && cfg.Services[dependency].ReadyLogLine == "" {
				return fmt.Errorf("service %q: dependency %q uses process_log_ready but has no ready_log_line", name, dependency)
			}
		}
		if svc.ReadyLogLine != "" {
			if svc.HealthCheck != nil && svc.HealthCheck.Readiness != nil {
				return fmt.Errorf("service %q: ready_log_line and readiness cannot be used together", name)
			}
			if _, err := regexp.Compile(svc.ReadyLogLine); err != nil {
				return fmt.Errorf("service %q: ready_log_line is not a valid regular expression: %w", name, err)
			}
		}
		switch svc.Availability.Restart {
		case "", "no", "always", "on_failure", "exit_on_failure":
		default:
			return fmt.Errorf("service %q: availability.restart must be no, always, on_failure, or exit_on_failure", name)
		}
		if svc.Availability.MaxRestarts < 0 || svc.Availability.Backoff < 0 {
			return fmt.Errorf("service %q: restart limits and backoff cannot be negative", name)
		}
		if svc.Shutdown.Signal < 0 || svc.Shutdown.Signal > 31 || svc.Shutdown.Timeout < 0 {
			return fmt.Errorf("service %q: shutdown signal must be 0..31 and timeout cannot be negative", name)
		}
		for _, code := range svc.SuccessExitCodes {
			if code < 0 || code > 255 {
				return fmt.Errorf("service %q: success exit code %d is outside 0..255", name, code)
			}
		}

		// Readiness and liveness are validated independently.
		if svc.HealthCheck != nil {
			if svc.HealthCheck.Readiness == nil && svc.HealthCheck.Liveness == nil {
				return fmt.Errorf("service %q: 'healthcheck' must define 'readiness', 'liveness', or both", name)
			}
			if err := validateCheckConfig(name, "readiness", svc.HealthCheck.Readiness); err != nil {
				return err
			}
			if err := validateCheckConfig(name, "liveness", svc.HealthCheck.Liveness); err != nil {
				return err
			}
		}
	}

	// Cycles would make lifecycle ordering impossible.
	if err := detectCycles(cfg); err != nil {
		return err
	}

	return nil
}

// validateCheckConfig validates one readiness or liveness probe.
func validateCheckConfig(svcName, checkName string, c *CheckConfig) error {
	if c == nil {
		return nil
	}

	switch c.Type {
	case CheckHTTP:
		if c.URL == "" {
			return fmt.Errorf("service %q: %s check of type 'http' requires field 'url'", svcName, checkName)
		}
	case CheckTCP:
		if c.Port == 0 {
			return fmt.Errorf("service %q: %s check of type 'tcp' requires field 'port'", svcName, checkName)
		}
	case CheckCommand:
		if c.Command == "" {
			return fmt.Errorf("service %q: %s check of type 'command' requires field 'command'", svcName, checkName)
		}
	case "":
		return fmt.Errorf("service %q: %s check requires field 'type' (allowed: http, tcp, command)", svcName, checkName)
	default:
		return fmt.Errorf("service %q: %s check has unknown type %q (allowed: http, tcp, command)", svcName, checkName, c.Type)
	}

	return nil
}

// detectCycles finds dependency cycles with a three-color depth-first search.
func detectCycles(cfg *Config) error {
	// Build a compact adjacency list from validated service references.
	graph := make(map[string][]string)
	for name, svc := range cfg.Services {
		graph[name] = svc.DependsOn
	}

	// States: 0 is unseen, 1 is on the active path, and 2 is complete.
	state := make(map[string]int)
	for name := range graph {
		state[name] = 0
	}

	var dfs func(node string, path []string) error
	dfs = func(node string, path []string) error {
		state[node] = 1 // active path
		path = append(path, node)

		for _, dep := range graph[node] {
			switch state[dep] {
			case 1:
				// A back edge points to the cycle segment in the active path.
				cycleStart := -1
				for i, n := range path {
					if n == dep {
						cycleStart = i
						break
					}
				}
				cycle := path[cycleStart:]
				cycle = append(cycle, dep)
				return fmt.Errorf("dependency cycle detected: %s", strings.Join(cycle, " → "))
			case 0:
				if err := dfs(dep, path); err != nil {
					return err
				}
			}
		}

		state[node] = 2 // complete
		return nil
	}

	for name := range graph {
		if state[name] == 0 {
			if err := dfs(name, nil); err != nil {
				return err
			}
		}
	}

	return nil
}
