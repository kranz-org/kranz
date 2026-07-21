package config

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type processComposeFile struct {
	Version     string                    `yaml:"version"`
	Name        string                    `yaml:"name"`
	Environment yaml.Node                 `yaml:"environment"`
	Processes   map[string]processCompose `yaml:"processes"`
}

type processCompose struct {
	Command          string                     `yaml:"command"`
	Description      string                     `yaml:"description"`
	WorkingDir       string                     `yaml:"working_dir"`
	Namespace        string                     `yaml:"namespace"`
	Environment      yaml.Node                  `yaml:"environment"`
	EnvFile          yaml.Node                  `yaml:"env_file"`
	DependsOn        yaml.Node                  `yaml:"depends_on"`
	ReadyLogLine     string                     `yaml:"ready_log_line"`
	ReadinessProbe   *processComposeProbe       `yaml:"readiness_probe"`
	LivenessProbe    *processComposeProbe       `yaml:"liveness_probe"`
	Disabled         bool                       `yaml:"disabled"`
	IsDisabled       string                     `yaml:"is_disabled"`
	IsDotenvDisabled bool                       `yaml:"is_dotenv_disabled"`
	Replicas         int                        `yaml:"replicas"`
	IsDaemon         bool                       `yaml:"is_daemon"`
	IsTTY            bool                       `yaml:"is_tty"`
	IsInteractive    bool                       `yaml:"is_interactive"`
	IsForeground     bool                       `yaml:"is_foreground"`
	Availability     processComposeAvailability `yaml:"availability"`
	Shutdown         processComposeShutdown     `yaml:"shutdown"`
	Schedule         yaml.Node                  `yaml:"schedule"`
	LogLocation      string                     `yaml:"log_location"`
	SuccessExitCodes []int                      `yaml:"success_exit_codes"`
}

type processComposeAvailability struct {
	Restart        string `yaml:"restart"`
	BackoffSeconds int    `yaml:"backoff_seconds"`
	MaxRestarts    int    `yaml:"max_restarts"`
	ExitOnEnd      bool   `yaml:"exit_on_end"`
	ExitOnSkipped  bool   `yaml:"exit_on_skipped"`
}

type processComposeShutdown struct {
	Command        string `yaml:"command"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
	Signal         int    `yaml:"signal"`
	ParentOnly     bool   `yaml:"parent_only"`
}

type processComposeProbe struct {
	Exec                *processComposeExec `yaml:"exec"`
	HTTPGet             *processComposeHTTP `yaml:"http_get"`
	InitialDelaySeconds int                 `yaml:"initial_delay_seconds"`
	PeriodSeconds       int                 `yaml:"period_seconds"`
	TimeoutSeconds      int                 `yaml:"timeout_seconds"`
	SuccessThreshold    int                 `yaml:"success_threshold"`
	FailureThreshold    int                 `yaml:"failure_threshold"`
}

type processComposeExec struct {
	Command string `yaml:"command"`
}

type processComposeHTTP struct {
	Host       string            `yaml:"host"`
	Scheme     string            `yaml:"scheme"`
	Path       string            `yaml:"path"`
	Port       yaml.Node         `yaml:"port"`
	Headers    map[string]string `yaml:"headers"`
	StatusCode int               `yaml:"status_code"`
}

func loadProcessCompose(data []byte, path string) (*Config, error) {
	var source processComposeFile
	if err := yaml.Unmarshal(data, &source); err != nil {
		return nil, fmt.Errorf("parse Process Compose YAML: %w", err)
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	baseDir := filepath.Dir(absolutePath)
	project := strings.TrimSpace(source.Name)
	if project == "" {
		project = filepath.Base(baseDir)
	}
	globalEnv, err := parseProcessEnvironment(source.Environment)
	if err != nil {
		return nil, fmt.Errorf("process-compose environment: %w", err)
	}
	cfg := &Config{
		Project: project, Version: source.Version, Source: SourceProcessCompose,
		Defaults: Defaults{Dir: baseDir, Env: globalEnv}, Services: make(map[string]Service),
		Diagnostics: []string{"Loaded Process Compose compatibility mode; unsupported features are reported explicitly."},
	}

	for name, process := range source.Processes {
		if process.Replicas > 1 {
			return nil, fmt.Errorf("processes.%s.replicas: values above 1 are not supported yet", name)
		}
		if process.IsDaemon || process.IsTTY || process.IsInteractive || process.IsForeground {
			return nil, fmt.Errorf("processes.%s: daemon, TTY, interactive, and foreground modes are not supported yet", name)
		}
		if nodeConfigured(process.Schedule) {
			return nil, fmt.Errorf("processes.%s.schedule: scheduled processes are not supported", name)
		}
		environment, err := parseProcessEnvironment(process.Environment)
		if err != nil {
			return nil, fmt.Errorf("processes.%s.environment: %w", name, err)
		}
		disabled := process.Disabled
		disabledSet := process.Disabled
		if process.IsDisabled != "" {
			disabled, err = strconv.ParseBool(process.IsDisabled)
			if err != nil {
				return nil, fmt.Errorf("processes.%s.is_disabled: must be true or false", name)
			}
			disabledSet = true
		}
		dependsOn, dependencyConditions, err := parseProcessDependencies(process.DependsOn)
		if err != nil {
			return nil, fmt.Errorf("processes.%s.depends_on: %w", name, err)
		}
		dir := process.WorkingDir
		if dir != "" && !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		healthCheck, ports, probeWarnings, err := convertProcessProbes(process.ReadinessProbe, process.LivenessProbe)
		if err != nil {
			return nil, fmt.Errorf("processes.%s health probe: %w", name, err)
		}
		for _, warning := range probeWarnings {
			cfg.Diagnostics = append(cfg.Diagnostics, fmt.Sprintf("processes.%s: %s", name, warning))
		}
		tags := []string(nil)
		if process.Namespace != "" && process.Namespace != "default" {
			tags = []string{process.Namespace}
		}
		if process.LogLocation != "" {
			cfg.Diagnostics = append(cfg.Diagnostics, fmt.Sprintf("processes.%s.log_location: file logging is ignored", name))
		}
		envFiles, err := parseStringList(process.EnvFile)
		if err != nil {
			return nil, fmt.Errorf("processes.%s.env_file: %w", name, err)
		}
		availability := AvailabilityConfig{
			Restart:       process.Availability.Restart,
			Backoff:       time.Duration(process.Availability.BackoffSeconds) * time.Second,
			MaxRestarts:   process.Availability.MaxRestarts,
			ExitOnEnd:     process.Availability.ExitOnEnd,
			ExitOnSkipped: process.Availability.ExitOnSkipped,
		}
		shutdownTimeout := process.Shutdown.TimeoutSeconds
		shutdownSignal := process.Shutdown.Signal
		cfg.Services[name] = Service{
			Command: process.Command, Description: process.Description, Dir: cleanOptionalPath(dir),
			Ports: ports, Tags: tags, DependsOn: dependsOn, DependencyConditions: dependencyConditions,
			Env: environment, EnvFiles: envFiles, HealthCheck: healthCheck, ReadyLogLine: process.ReadyLogLine,
			Availability:     availability,
			Shutdown:         ShutdownConfig{Command: process.Shutdown.Command, Timeout: time.Duration(shutdownTimeout) * time.Second, Signal: shutdownSignal, ParentOnly: process.Shutdown.ParentOnly},
			SuccessExitCodes: append([]int(nil), process.SuccessExitCodes...), Disabled: disabled,
			DisableDotenv: process.IsDotenvDisabled, disabledSet: disabledSet,
		}
	}
	return cfg, nil
}

func cleanOptionalPath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func parseProcessEnvironment(node yaml.Node) (map[string]string, error) {
	result := make(map[string]string)
	if !nodeConfigured(node) {
		return result, nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		for index := 0; index+1 < len(node.Content); index += 2 {
			result[node.Content[index].Value] = node.Content[index+1].Value
		}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			name, value, found := strings.Cut(item.Value, "=")
			if !found || strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("entry %q must use NAME=value", item.Value)
			}
			result[name] = value
		}
	default:
		return nil, fmt.Errorf("must be a mapping or a list of NAME=value entries")
	}
	return result, nil
}

func parseProcessDependencies(node yaml.Node) ([]string, map[string]DependencyConfig, error) {
	if !nodeConfigured(node) {
		return nil, nil, nil
	}
	var dependencies []string
	conditions := make(map[string]DependencyConfig)
	switch node.Kind {
	case yaml.SequenceNode:
		for _, item := range node.Content {
			dependencies = append(dependencies, item.Value)
			conditions[item.Value] = DependencyConfig{Condition: DependencyStarted}
		}
	case yaml.MappingNode:
		for index := 0; index+1 < len(node.Content); index += 2 {
			name, value := node.Content[index].Value, node.Content[index+1]
			condition := "process_started"
			if value.Kind == yaml.ScalarNode && value.Value != "" {
				condition = value.Value
			} else if value.Kind == yaml.MappingNode {
				for child := 0; child+1 < len(value.Content); child += 2 {
					if value.Content[child].Value == "condition" {
						condition = value.Content[child+1].Value
					}
				}
			}
			switch DependencyCondition(condition) {
			case "", DependencyStarted, DependencyHealthy, DependencyCompleted, DependencyCompletedSuccessfully, DependencyLogReady:
				dependencies = append(dependencies, name)
				conditions[name] = DependencyConfig{Condition: DependencyCondition(condition)}
			default:
				return nil, nil, fmt.Errorf("condition %q is not supported", condition)
			}
		}
	default:
		return nil, nil, fmt.Errorf("must be a mapping or sequence")
	}
	return dependencies, conditions, nil
}

func parseStringList(node yaml.Node) ([]string, error) {
	if !nodeConfigured(node) {
		return nil, nil
	}
	if node.Kind == yaml.ScalarNode {
		return []string{node.Value}, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("must be a string or list of strings")
	}
	values := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		values = append(values, item.Value)
	}
	return values, nil
}

func convertProcessProbes(readiness, liveness *processComposeProbe) (*HealthCheckConfig, []int, []string, error) {
	if readiness == nil && liveness == nil {
		return nil, nil, nil, nil
	}
	result := &HealthCheckConfig{}
	ports := make([]int, 0, 2)
	warnings := make([]string, 0)
	var err error
	if readiness != nil {
		result.Readiness, err = convertProcessProbe(readiness)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("readiness: %w", err)
		}
		if readiness.SuccessThreshold > 1 {
			warnings = append(warnings, "readiness success_threshold above 1 is accepted as 1")
		}
		if result.Readiness.Type == CheckHTTP {
			ports = appendUniquePort(ports, result.Readiness.Port)
		}
	}
	if liveness != nil {
		result.Liveness, err = convertProcessProbe(liveness)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("liveness: %w", err)
		}
		if liveness.SuccessThreshold > 1 {
			warnings = append(warnings, "liveness success_threshold above 1 is accepted as 1")
		}
		if result.Liveness.Type == CheckHTTP {
			ports = appendUniquePort(ports, result.Liveness.Port)
		}
	}
	return result, ports, warnings, nil
}

func convertProcessProbe(probe *processComposeProbe) (*CheckConfig, error) {
	configured := 0
	if probe.Exec != nil {
		configured++
	}
	if probe.HTTPGet != nil {
		configured++
	}
	if configured != 1 {
		return nil, fmt.Errorf("exactly one of exec or http_get is required")
	}
	interval := probe.PeriodSeconds
	if interval <= 0 {
		interval = 10
	}
	timeout := probe.TimeoutSeconds
	if timeout <= 0 {
		timeout = 1
	}
	failureThreshold := probe.FailureThreshold
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	check := &CheckConfig{
		InitialDelay: time.Duration(max(0, probe.InitialDelaySeconds)) * time.Second,
		Interval:     time.Duration(interval) * time.Second, Timeout: time.Duration(timeout) * time.Second,
		FailureThreshold: failureThreshold,
	}
	if probe.Exec != nil {
		if strings.TrimSpace(probe.Exec.Command) == "" {
			return nil, fmt.Errorf("exec.command is required")
		}
		check.Type, check.Command = CheckCommand, probe.Exec.Command
		return check, nil
	}
	httpGet := probe.HTTPGet
	port, err := strconv.Atoi(httpGet.Port.Value)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("http_get.port must be between 1 and 65535")
	}
	host := httpGet.Host
	if host == "" {
		host = "127.0.0.1"
	}
	scheme := strings.ToLower(httpGet.Scheme)
	if scheme == "" {
		scheme = "http"
	}
	path := httpGet.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	check.Type = CheckHTTP
	check.Port = port
	check.Headers = httpGet.Headers
	check.StatusCode = httpGet.StatusCode
	if check.StatusCode == 0 {
		check.StatusCode = 200
	}
	check.URL = (&url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: path}).String()
	return check, nil
}

func appendUniquePort(ports []int, port int) []int {
	for _, current := range ports {
		if current == port {
			return ports
		}
	}
	return append(ports, port)
}

func nodeConfigured(node yaml.Node) bool {
	return node.Kind != 0 && !(node.Kind == yaml.ScalarNode && node.Value == "")
}
