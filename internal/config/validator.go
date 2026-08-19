package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Validate checks project metadata, commands, actions, dependencies, ports, and probes.
func Validate(cfg *Config) error {
	if cfg.Project == "" {
		return fmt.Errorf("field 'project' is required")
	}
	if cfg.Runtime.Name != "" {
		if err := ValidateRuntimeName(cfg.Runtime.Name); err != nil {
			return fmt.Errorf("runtime.name: %w", err)
		}
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

	if len(cfg.Services) == 0 && len(cfg.ActionGroups) == 0 {
		return fmt.Errorf("configuration must contain at least one service or action group")
	}

	svcNames := make(map[string]bool)
	for name := range cfg.Services {
		svcNames[name] = true
	}

	for name, svc := range cfg.Services {
		if err := validateServiceLifecycle(name, svc); err != nil {
			return err
		}
		if err := validateActions(fmt.Sprintf("service %q", name), svc.Actions); err != nil {
			return err
		}
		if err := validatePrerequisites(cfg, name, svc); err != nil {
			return err
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
			if err := validateCheckConfig(name, "readiness", svc.HealthCheck.Readiness, svc.PortDiscoveryEnabled()); err != nil {
				return err
			}
			if err := validateCheckConfig(name, "liveness", svc.HealthCheck.Liveness, svc.PortDiscoveryEnabled()); err != nil {
				return err
			}
		}
	}
	for name, group := range cfg.ActionGroups {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("action group name cannot be empty")
		}
		if len(group.Actions) == 0 {
			return fmt.Errorf("action group %q must contain at least one action", name)
		}
		if err := validateActions(fmt.Sprintf("action group %q", name), group.Actions); err != nil {
			return err
		}
	}

	// Cycles would make lifecycle ordering impossible.
	if err := detectCycles(cfg); err != nil {
		return err
	}

	return nil
}

func validateServiceLifecycle(name string, svc Service) error {
	mode := svc.SupervisionMode()
	if mode != SupervisionProcess && mode != SupervisionDetached {
		return fmt.Errorf("service %q: supervision must be process or detached, got %q", name, svc.Supervision)
	}
	start := svc.StartAction()
	if mode == SupervisionProcess && (start == nil || strings.TrimSpace(start.Command) == "") {
		return fmt.Errorf("service %q: command or lifecycle.start is required for process supervision", name)
	}
	if mode == SupervisionDetached && start == nil && svc.Lifecycle.Stop == nil && svc.Lifecycle.Status == nil {
		return fmt.Errorf("service %q: detached supervision requires lifecycle.start, lifecycle.stop, or lifecycle.status", name)
	}
	for role, action := range map[string]*Action{"start": start, "stop": svc.Lifecycle.Stop, "logs": svc.Lifecycle.Logs} {
		if action == nil {
			continue
		}
		if strings.TrimSpace(action.Command) == "" {
			return fmt.Errorf("service %q lifecycle.%s: field 'command' is required", name, role)
		}
		if action.Timeout < 0 {
			return fmt.Errorf("service %q lifecycle.%s: timeout cannot be negative", name, role)
		}
		if action.InteractiveEnabled() {
			return fmt.Errorf("service %q lifecycle.%s: interactive execution is not supported", name, role)
		}
	}
	if mode == SupervisionProcess {
		if svc.Lifecycle.Stop != nil || svc.Lifecycle.Status != nil || svc.Lifecycle.Logs != nil {
			return fmt.Errorf("service %q: lifecycle.stop, status, and logs require detached supervision", name)
		}
		if svc.StopOnExit != nil && !*svc.StopOnExit {
			return fmt.Errorf("service %q: stop_on_exit: false requires detached supervision", name)
		}
		return nil
	}
	if svc.DetectPorts != nil && *svc.DetectPorts {
		return fmt.Errorf("service %q: detect_ports: true is not supported with detached supervision", name)
	}
	if svc.Availability.Restart != "" && svc.Availability.Restart != "no" {
		return fmt.Errorf("service %q: availability.restart is not supported with detached supervision", name)
	}
	if svc.Lifecycle.Status != nil {
		status := svc.Lifecycle.Status
		if err := validateCheckConfig(name, "lifecycle.status", &status.CheckConfig, false); err != nil {
			return err
		}
		if status.Type != CheckCommand {
			return fmt.Errorf("service %q: lifecycle.status currently supports only type command", name)
		}
		if status.StoppedInterval < 0 {
			return fmt.Errorf("service %q: lifecycle.status stopped_interval cannot be negative", name)
		}
		if err := validateStatusExitCodes(name, status); err != nil {
			return err
		}
	}
	return nil
}

func validateStatusExitCodes(name string, status *LifecycleStatusConfig) error {
	seen := make(map[int]string)
	for kind, codes := range map[string][]int{"running": status.RunningExitCodes, "stopped": status.StoppedExitCodes} {
		for _, code := range codes {
			if code < 0 || code > 255 {
				return fmt.Errorf("service %q: lifecycle.status %s exit code %d is outside 0..255", name, kind, code)
			}
			if previous, exists := seen[code]; exists {
				return fmt.Errorf("service %q: lifecycle.status exit code %d is both %s and %s", name, code, previous, kind)
			}
			seen[code] = kind
		}
	}
	return nil
}

func validatePrerequisites(cfg *Config, name string, svc Service) error {
	for index, prerequisite := range svc.BeforeStart {
		position := fmt.Sprintf("service %q before_start[%d]", name, index)
		if prerequisite.Service != "" && prerequisite.Group != "" {
			return fmt.Errorf("%s: set either service or group, not both", position)
		}
		if strings.TrimSpace(prerequisite.Action) == "" {
			return fmt.Errorf("%s: field 'action' is required", position)
		}
		switch prerequisite.RunPolicy() {
		case PrerequisiteOnce, PrerequisiteAlways:
		default:
			return fmt.Errorf("%s: run must be once or always, got %q", position, prerequisite.Run)
		}
		id := prerequisite.ActionID(name)
		action, exists := cfg.ResolveAction(id)
		if !exists {
			return fmt.Errorf("%s: %s was not found", position, prerequisite.String(name))
		}
		// A prerequisite runs unattended while a start is already in flight, so
		// it cannot be one of the actions that takes over the terminal.
		if action.InteractiveEnabled() {
			return fmt.Errorf("%s: %s is interactive and cannot be a prerequisite", position, prerequisite.String(name))
		}
	}
	return nil
}

func validateActions(owner string, actions map[string]Action) error {
	for name, action := range actions {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%s: action name cannot be empty", owner)
		}
		if strings.TrimSpace(action.Command) == "" {
			return fmt.Errorf("%s action %q: field 'command' is required", owner, name)
		}
		if action.Timeout < 0 {
			return fmt.Errorf("%s action %q: timeout cannot be negative", owner, name)
		}
	}
	return nil
}

// validateCheckConfig validates one readiness or liveness probe.
func validateCheckConfig(svcName, checkName string, c *CheckConfig, portDiscoveryEnabled bool) error {
	if c == nil {
		return nil
	}
	usesDetectedPort := c.UsesDetectedPort()
	if c.PortFrom != "" && c.PortFrom != PortFromDetected {
		return fmt.Errorf("service %q: %s check has unknown port_from %q (allowed: detected)", svcName, checkName, c.PortFrom)
	}
	if c.PortFrom != "" && c.Port != 0 {
		return fmt.Errorf("service %q: %s check cannot use both 'port' and 'port_from'; remove one of them", svcName, checkName)
	}
	if c.DetectedPortIndex != nil && !usesDetectedPort {
		return fmt.Errorf("service %q: %s check: detected_port_index requires a detected port; omit 'port' for a tcp check or set port_from: detected", svcName, checkName)
	}
	if c.DetectedPortIndex != nil && *c.DetectedPortIndex < 0 {
		return fmt.Errorf("service %q: %s check: detected_port_index cannot be negative", svcName, checkName)
	}
	if usesDetectedPort && !portDiscoveryEnabled {
		if c.PortFrom == "" && c.Type == CheckTCP {
			return fmt.Errorf("service %q: %s tcp check omits 'port' and therefore uses runtime discovery, but port discovery is disabled; set detect_ports: true or configure a static port", svcName, checkName)
		}
		if c.PortFrom == "" && c.Type == CheckHTTP {
			return fmt.Errorf("service %q: %s http check URL omits a port and therefore uses runtime discovery, but port discovery is disabled; set detect_ports: true or specify an explicit port in 'url'", svcName, checkName)
		}
		return fmt.Errorf("service %q: %s check uses port_from: detected, but port discovery is disabled; set detect_ports: true or use a static port", svcName, checkName)
	}

	switch c.Type {
	case CheckHTTP:
		if c.URL == "" {
			return fmt.Errorf("service %q: %s check of type 'http' requires field 'url'", svcName, checkName)
		}
		parsed, err := validateHTTPURL(svcName, checkName, c.URL)
		if err != nil {
			return err
		}
		if usesDetectedPort {
			if parsed.Port() != "" {
				return fmt.Errorf("service %q: %s dynamic http check must not specify a port in url; write %q instead of %q", svcName, checkName, urlWithoutPort(parsed), c.URL)
			}
		}
	case CheckTCP:
		// An omitted port means the sole detected runtime listener. Multiple
		// listeners still require detected_port_index.
	case CheckCommand:
		if c.PortFrom != "" {
			return fmt.Errorf("service %q: %s command check cannot use port_from", svcName, checkName)
		}
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

func validateHTTPURL(svcName, checkName, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("service %q: %s http check requires a valid absolute URL: %w", svcName, checkName, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("service %q: %s http check requires an absolute URL with scheme and host, got %q", svcName, checkName, rawURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("service %q: %s http check URL scheme must be %q or %q, got %q", svcName, checkName, "http", "https", parsed.Scheme)
	}
	return parsed, nil
}

func urlWithoutPort(parsed *url.URL) string {
	corrected := *parsed
	host := parsed.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	corrected.Host = host
	return corrected.String()
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
