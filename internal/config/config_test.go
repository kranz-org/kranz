package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	cfg, err := Load("../../testdata/kranz.yaml")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Project != "Kranz Test" {
		t.Errorf("expected project 'Kranz Test', got '%s'", cfg.Project)
	}
	if cfg.UI.Theme != "dracula" || cfg.UI.Accent != "#BD93F9" || cfg.UI.Background != "terminal" || cfg.UI.ColorMode != "auto" {
		t.Errorf("project appearance = %#v", cfg.UI)
	}

	if len(cfg.Services) != 3 {
		t.Errorf("expected 3 services, got %d", len(cfg.Services))
	}

	// Check echo-server.
	svc, ok := cfg.Services["echo-server"]
	if !ok {
		t.Fatal("echo-server not found")
	}
	if svc.Command != "echo 'Echo server started' && sleep 3600" {
		t.Errorf("unexpected command: %s", svc.Command)
	}
	if len(svc.DependsOn) != 0 {
		t.Errorf("echo-server should have no deps, got %v", svc.DependsOn)
	}

	// Check web-api.
	web, ok := cfg.Services["web-api"]
	if !ok {
		t.Fatal("web-api not found")
	}
	if len(web.DependsOn) != 1 || web.DependsOn[0] != "echo-server" {
		t.Errorf("web-api should depend on echo-server, got %v", web.DependsOn)
	}
}

func TestLoadInvalidFile(t *testing.T) {
	_, err := Load("nonexistent.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestCommandNormalizesToLifecycleStartBeforeLayerMerge(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "kranz.yaml")
	overridePath := filepath.Join(directory, "kranz.local.yaml")
	base := "project: Lifecycle\nservices:\n  app:\n    command: npm run dev\n"
	override := "project: Lifecycle\nservices:\n  app:\n    lifecycle:\n      start:\n        timeout: 30s\n"
	if err := os.WriteFile(basePath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridePath, []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFiles([]string{basePath, overridePath})
	if err != nil {
		t.Fatal(err)
	}
	service := cfg.Services["app"]
	if service.Lifecycle.Start == nil || service.Lifecycle.Start.Command != "npm run dev" || service.Lifecycle.Start.Timeout != 30*time.Second {
		t.Fatalf("normalized lifecycle start = %#v", service.Lifecycle.Start)
	}
	if service.Command != service.Lifecycle.Start.Command {
		t.Fatalf("compatibility command = %q, start = %#v", service.Command, service.Lifecycle.Start)
	}
}

func TestNativeServiceRejectsCommandAndLifecycleStartInSameFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kranz.yaml")
	data := "project: Conflict\nservices:\n  app:\n    command: npm run dev\n    lifecycle:\n      start:\n        command: npm run debug\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "command conflicts with lifecycle.start") {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestLoadDetachedLifecycleDefaultsAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kranz.yaml")
	data := `project: Detached
services:
  stack:
    supervision: detached
    lifecycle:
      start:
        command: docker compose up -d
        timeout: 2m
      stop:
        command: docker compose down
      status:
        type: command
        command: docker compose ps -q
        interval: 5s
        running_exit_codes: [0]
        stopped_exit_codes: [3]
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	service := cfg.Services["stack"]
	if !service.IsDetached() || service.StopOnExitEnabled() || service.PortDiscoveryEnabled() {
		t.Fatalf("detached defaults = %#v", service)
	}
	if service.Lifecycle.Status == nil || service.Lifecycle.Status.StoppedInterval != 30*time.Second {
		t.Fatalf("status defaults = %#v", service.Lifecycle.Status)
	}
	if service.Lifecycle.Start.Dir == "" || service.Lifecycle.Stop.Shell == "" {
		t.Fatalf("lifecycle context was not inherited: %#v", service.Lifecycle)
	}
}

func TestLoadLifecyclePlayground(t *testing.T) {
	cfg, err := Load("../../examples/lifecycle/kranz.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Services) != 5 || !cfg.Services["remote-stack"].IsDetached() || cfg.Services["observed-resource"].Lifecycle.Status == nil {
		t.Fatalf("lifecycle playground was not loaded as expected: %#v", cfg.Services)
	}
	if start := cfg.Services["guarded-worker"].StartAction(); start == nil || !start.ConfirmationRequired() {
		t.Fatalf("guarded worker start = %#v", start)
	}
}

// TestLoadAnnotatedReferenceConfiguration keeps the documented reference file
// honest: docs/reference/kranz-yaml.md includes this exact file, so a field
// that stops loading or validating fails the build instead of misleading a
// reader.
func TestLoadAnnotatedReferenceConfiguration(t *testing.T) {
	cfg, err := Load("../../examples/reference/kranz.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	api := cfg.Services["api"]
	if len(api.BeforeStart) != 2 || len(api.Actions) != 3 {
		t.Fatalf("annotated api service = %#v", api)
	}
	if stack := cfg.Services["remote-stack"]; !stack.IsDetached() || stack.StopOnExitEnabled() {
		t.Fatalf("annotated detached service = %#v", stack)
	}
}

func TestLoadAllCanonicalExamples(t *testing.T) {
	paths := []string{
		"../../examples/native/kranz.yaml",
		"../../examples/lifecycle/kranz.yaml",
		"../../examples/full-stack/kranz.yaml",
		"../../examples/full-stack/process-compose.yaml",
		"../../examples/process-compose/process-compose.yaml",
		"../../examples/procfile/Procfile",
		"../../examples/runtime-ports/kranz.yaml",
		"../../examples/prerequisites/kranz.yaml",
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			if _, err := Load(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateUIBackgroundSource(t *testing.T) {
	base := &Config{Project: "Appearance", Services: map[string]Service{"app": {Command: "exit 0"}}}
	for _, source := range []string{"", "terminal", "theme"} {
		cfg := *base
		cfg.UI.Background = source
		if err := Validate(&cfg); err != nil {
			t.Errorf("background %q was rejected: %v", source, err)
		}
	}
	invalid := *base
	invalid.UI.Background = "automatic"
	if err := Validate(&invalid); err == nil || !strings.Contains(err.Error(), "ui.background") {
		t.Fatalf("invalid background source error = %v", err)
	}
}

func TestValidateUIColorMode(t *testing.T) {
	base := &Config{Project: "Appearance", Services: map[string]Service{"app": {Command: "exit 0"}}}
	for _, mode := range []string{"", "auto", "dark", "light"} {
		cfg := *base
		cfg.UI.ColorMode = mode
		if err := Validate(&cfg); err != nil {
			t.Errorf("color mode %q was rejected: %v", mode, err)
		}
	}
	invalid := *base
	invalid.UI.ColorMode = "system"
	if err := Validate(&invalid); err == nil || !strings.Contains(err.Error(), "ui.color_mode") {
		t.Fatalf("invalid color mode error = %v", err)
	}
}

func TestLoadFilesMergesUIColorModeFromLastLayer(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "kranz.yaml")
	overridePath := filepath.Join(directory, "kranz.local.yaml")
	base := "project: Layered\nui:\n  theme: cream\n  color_mode: light\nservices:\n  app:\n    command: exit 0\n"
	override := "project: Layered\nui:\n  color_mode: dark\nservices: {}\n"
	if err := os.WriteFile(basePath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridePath, []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFiles([]string{basePath, overridePath})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Theme != "cream" || cfg.UI.ColorMode != "dark" {
		t.Fatalf("merged appearance = %#v", cfg.UI)
	}
}

func TestLoadEnvSubstitution(t *testing.T) {
	t.Setenv("TEST_VAR", "test_value")

	tmpFile := filepath.Join(t.TempDir(), "kranz.yaml")
	content := "project: Test\nversion: \"1.0\"\nservices:\n  test:\n    command: echo ${TEST_VAR}\n"
	if err := os.WriteFile(tmpFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(tmpFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	svc := cfg.Services["test"]
	if svc.Command != "echo test_value" {
		t.Errorf("env substitution failed, got '%s'", svc.Command)
	}
}

func TestValidate(t *testing.T) {
	// Valid config.
	cfg := &Config{
		Project: "Test",
		Services: map[string]Service{
			"svc1": {Command: "echo hello"},
		},
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("valid config should not error: %v", err)
	}

	// Missing project.
	cfg2 := &Config{
		Services: map[string]Service{
			"svc1": {Command: "echo hello"},
		},
	}
	if err := Validate(cfg2); err == nil {
		t.Error("expected error for missing project")
	}

	// Missing services.
	cfg3 := &Config{
		Project: "Test",
	}
	if err := Validate(cfg3); err == nil {
		t.Error("expected error for missing services")
	}

	// Missing command.
	cfg4 := &Config{
		Project: "Test",
		Services: map[string]Service{
			"svc1": {},
		},
	}
	if err := Validate(cfg4); err == nil {
		t.Error("expected error for missing command")
	}
}

func TestValidateCycles(t *testing.T) {
	cfg := &Config{
		Project: "Test",
		Services: map[string]Service{
			"a": {Command: "echo a", DependsOn: []string{"b"}},
			"b": {Command: "echo b", DependsOn: []string{"a"}},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected cycle detection error")
	}
}

func TestValidateSelfDependency(t *testing.T) {
	cfg := &Config{
		Project: "Test",
		Services: map[string]Service{
			"a": {Command: "echo a", DependsOn: []string{"a"}},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for self-dependency")
	}
}

func TestValidateMissingDependency(t *testing.T) {
	cfg := &Config{
		Project: "Test",
		Services: map[string]Service{
			"a": {Command: "echo a", DependsOn: []string{"nonexistent"}},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for missing dependency")
	}
}

func TestValidateRequiresExplicitReadinessAndLivenessTypes(t *testing.T) {
	tests := []struct {
		name   string
		health *HealthCheckConfig
	}{
		{name: "empty healthcheck", health: &HealthCheckConfig{}},
		{name: "readiness without type", health: &HealthCheckConfig{Readiness: &CheckConfig{URL: "http://localhost/ready"}}},
		{name: "liveness without type", health: &HealthCheckConfig{Liveness: &CheckConfig{Port: 8080}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{Project: "Test", Services: map[string]Service{
				"api": {Command: "run", HealthCheck: test.health},
			}}
			if err := Validate(cfg); err == nil {
				t.Fatal("expected invalid healthcheck to be rejected")
			}
		})
	}
}

func TestValidateDetectedPortHealthChecks(t *testing.T) {
	index := 1
	negativeIndex := -1
	disabled := false
	tests := []struct {
		name    string
		service Service
		wantErr string
	}{
		{
			name: "tcp uses sole detected port",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckTCP, PortFrom: PortFromDetected,
			}}},
		},
		{
			name: "tcp omitted port uses sole detected port",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckTCP,
			}}},
		},
		{
			name: "tcp omitted port accepts selector",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckTCP, DetectedPortIndex: &index,
			}}},
		},
		{
			name: "http selects second detected port",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Liveness: &CheckConfig{
				Type: CheckHTTP, URL: "http://127.0.0.1/health", PortFrom: PortFromDetected, DetectedPortIndex: &index,
			}}},
		},
		{
			name: "http omitted url port uses sole detected port",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckHTTP, URL: "http://127.0.0.1/health",
			}}},
		},
		{
			name: "unknown dynamic source",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckTCP, PortFrom: "automatic",
			}}},
			wantErr: "unknown port_from",
		},
		{
			name: "dynamic and static ports conflict",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckTCP, Port: 8080, PortFrom: PortFromDetected,
			}}},
			wantErr: "cannot use both 'port' and 'port_from'",
		},
		{
			name: "selector requires dynamic source",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckTCP, Port: 8080, DetectedPortIndex: &index,
			}}},
			wantErr: "detected_port_index requires a detected port",
		},
		{
			name: "selector cannot be negative",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckTCP, PortFrom: PortFromDetected, DetectedPortIndex: &negativeIndex,
			}}},
			wantErr: "detected_port_index cannot be negative",
		},
		{
			name: "dynamic source rejects explicit http port",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckHTTP, URL: "http://127.0.0.1:8080/health", PortFrom: PortFromDetected,
			}}},
			wantErr: `write "http://127.0.0.1/health" instead`,
		},
		{
			name: "dynamic source suggests corrected ipv6 http url",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckHTTP, URL: "https://[::1]:8443/health?deep=1", PortFrom: PortFromDetected,
			}}},
			wantErr: `write "https://[::1]/health?deep=1" instead`,
		},
		{
			name: "command cannot use dynamic port",
			service: Service{Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckCommand, Command: "true", PortFrom: PortFromDetected,
			}}},
			wantErr: "command check cannot use port_from",
		},
		{
			name: "dynamic source requires discovery",
			service: Service{Command: "run", DetectPorts: &disabled, HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckTCP, PortFrom: PortFromDetected,
			}}},
			wantErr: "set detect_ports: true or use a static port",
		},
		{
			name: "omitted tcp port requires discovery",
			service: Service{Command: "run", DetectPorts: &disabled, HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckTCP,
			}}},
			wantErr: "set detect_ports: true or configure a static port",
		},
		{
			name: "omitted http url port requires discovery",
			service: Service{Command: "run", DetectPorts: &disabled, HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckHTTP, URL: "http://127.0.0.1/health",
			}}},
			wantErr: "set detect_ports: true or specify an explicit port in 'url'",
		},
		{
			name: "explicit static http port works without discovery",
			service: Service{Command: "run", DetectPorts: &disabled, HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
				Type: CheckHTTP, URL: "http://127.0.0.1:80/health",
			}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{Project: "Test", Services: map[string]Service{"api": test.service}}
			err := Validate(cfg)
			if test.wantErr == "" && err != nil {
				t.Fatalf("valid dynamic probe rejected: %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestValidateHTTPCheckURLs(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{name: "static http with explicit port", url: "http://127.0.0.1:8080/health"},
		{name: "dynamic http omits url port", url: "http://api.local/health"},
		{name: "https", url: "https://api.local/health?deep=1"},
		{name: "relative path", url: "/health", wantErr: "absolute URL with scheme and host"},
		{name: "missing scheme", url: "localhost:8080/health", wantErr: "absolute URL with scheme and host"},
		{name: "missing host", url: "http:///health", wantErr: "absolute URL with scheme and host"},
		{name: "unsupported scheme", url: "ftp://127.0.0.1/health", wantErr: `scheme must be "http" or "https"`},
		{name: "malformed port", url: "http://127.0.0.1:not-a-port/health", wantErr: "valid absolute URL"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{Project: "Test", Services: map[string]Service{
				"api": {Command: "run", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{
					Type: CheckHTTP, URL: test.url,
				}}},
			}}
			err := Validate(cfg)
			if test.wantErr == "" && err != nil {
				t.Fatalf("valid HTTP URL rejected: %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestLoadDetectedPortHealthCheckSelector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kranz.yaml")
	data := "project: Dynamic probe\nservices:\n  api:\n    command: run\n    healthcheck:\n      readiness:\n        type: tcp\n        port_from: detected\n        detected_port_index: 0\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	check := cfg.Services["api"].HealthCheck.Readiness
	if check.PortFrom != PortFromDetected || check.DetectedPortIndex == nil || *check.DetectedPortIndex != 0 {
		t.Fatalf("dynamic probe selector = %#v", check)
	}
}

func TestLoadTCPHealthCheckWithoutPortUsesDiscovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kranz.yaml")
	data := "project: Implicit dynamic probe\nservices:\n  im-widgets:\n    command: npm run dev\n    healthcheck:\n      readiness:\n        type: tcp\n        interval: 3s\n      liveness:\n        type: tcp\n        detected_port_index: 0\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	health := cfg.Services["im-widgets"].HealthCheck
	if !health.Readiness.UsesDetectedPort() || !health.Liveness.UsesDetectedPort() {
		t.Fatalf("tcp probes did not use discovery: %#v", health)
	}
}

func TestGetAllTags(t *testing.T) {
	cfg := &Config{
		Project: "Test",
		Services: map[string]Service{
			"a": {Command: "echo a", Tags: []string{"backend", "core"}},
			"b": {Command: "echo b", Tags: []string{"frontend", "core"}},
		},
	}

	tags := cfg.GetAllTags()
	if len(tags) != 3 {
		t.Errorf("expected 3 unique tags, got %d", len(tags))
	}
}

func TestGetServicesByTags(t *testing.T) {
	cfg := &Config{
		Project: "Test",
		Services: map[string]Service{
			"a": {Command: "echo a", Tags: []string{"backend"}},
			"b": {Command: "echo b", Tags: []string{"frontend"}},
			"c": {Command: "echo c", Tags: []string{"backend", "core"}},
		},
	}

	names := cfg.GetServicesByTags([]string{"backend"})
	if len(names) != 2 {
		t.Errorf("expected 2 backend services, got %d: %v", len(names), names)
	}
}

func TestServiceNames(t *testing.T) {
	cfg := &Config{
		Project: "Test",
		Services: map[string]Service{
			"b": {Command: "echo b"},
			"a": {Command: "echo a"},
			"c": {Command: "echo c"},
		},
	}

	names := cfg.ServiceNames()
	if len(names) != 3 {
		t.Errorf("expected 3 names, got %d", len(names))
	}
	if strings.Join(names, ",") != "a,b,c" {
		t.Errorf("service names are not deterministic: %v", names)
	}
}

func TestLoadProcessComposeCompatibilitySubset(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "process-compose.yaml")
	data := `
version: "0.5"
name: Compose Demo
environment:
  SHARED: project
processes:
  database:
    command: run-db
  api:
    command: run-api
    description: HTTP API
    working_dir: apps/api
    namespace: backend
    environment:
      - LOCAL=service
    depends_on:
      database:
        condition: process_healthy
    readiness_probe:
      http_get:
        host: 127.0.0.1
        scheme: http
        path: healthz
        port: 8080
        headers:
          X-Probe: kranz
        status_code: 204
      initial_delay_seconds: 2
      period_seconds: 7
      timeout_seconds: 3
      success_threshold: 2
      failure_threshold: 4
    liveness_probe:
      exec:
        command: test -f alive
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load process-compose config: %v", err)
	}
	if cfg.Source != SourceProcessCompose || cfg.Project != "Compose Demo" {
		t.Fatalf("source/project = %q/%q", cfg.Source, cfg.Project)
	}
	api := cfg.Services["api"]
	if api.Description != "HTTP API" || api.Dir != filepath.Join(directory, "apps/api") {
		t.Errorf("api metadata = %#v", api)
	}
	if len(api.Tags) != 1 || api.Tags[0] != "backend" || len(api.DependsOn) != 1 || api.DependsOn[0] != "database" {
		t.Errorf("api tags/dependencies = %v/%v", api.Tags, api.DependsOn)
	}
	if api.Env["SHARED"] != "project" || api.Env["LOCAL"] != "service" {
		t.Errorf("merged environment = %v", api.Env)
	}
	readiness := api.HealthCheck.Readiness
	if readiness.URL != "http://127.0.0.1:8080/healthz" || readiness.StatusCode != 204 || readiness.Headers["X-Probe"] != "kranz" {
		t.Errorf("readiness mapping = %#v", readiness)
	}
	if readiness.InitialDelay != 2*time.Second || readiness.Interval != 7*time.Second || readiness.Timeout != 3*time.Second || readiness.FailureThreshold != 4 {
		t.Errorf("readiness timing = %#v", readiness)
	}
	if api.HealthCheck.Liveness.Type != CheckCommand || api.HealthCheck.Liveness.Command != "test -f alive" {
		t.Errorf("liveness mapping = %#v", api.HealthCheck.Liveness)
	}
	if len(api.Ports) != 1 || api.Ports[0] != 8080 {
		t.Errorf("inferred ports = %v", api.Ports)
	}
	if !strings.Contains(strings.Join(cfg.Diagnostics, "\n"), "success_threshold") {
		t.Errorf("expected compatibility diagnostic, got %v", cfg.Diagnostics)
	}
}

func TestProcessComposeRejectsUnsafeUnsupportedLifecycleFeatures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process-compose.yaml")
	data := "processes:\n  worker:\n    command: run\n    replicas: 2\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "replicas") {
		t.Fatalf("unsupported replicas error = %v", err)
	}
}

func TestDiscoverPrefersNativeConfigAndFindsProcessComposeFallback(t *testing.T) {
	directory := t.TempDir()
	composePath := filepath.Join(directory, "process-compose.yaml")
	if err := os.WriteFile(composePath, []byte("processes: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Discover(directory); err != nil || got != composePath {
		t.Fatalf("Discover process-compose = %q, %v", got, err)
	}
	nativePath := filepath.Join(directory, "kranz.yaml")
	if err := os.WriteFile(nativePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Discover(directory); err != nil || got != nativePath {
		t.Fatalf("Discover native = %q, %v", got, err)
	}
}

func TestDiscoverFilesIncludesConventionalProcessComposeOverride(t *testing.T) {
	directory := t.TempDir()
	base := filepath.Join(directory, "process-compose.yaml")
	override := filepath.Join(directory, "process-compose.override.yaml")
	for _, path := range []string{base, override} {
		if err := os.WriteFile(path, []byte("processes: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := DiscoverFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != base+","+override {
		t.Fatalf("discovered files = %v", paths)
	}
}

func TestNativeConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kranz.yaml")
	data := "project: Test\nunknown: value\nservices:\n  app:\n    command: run\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "field unknown") {
		t.Fatalf("unknown-field error = %v", err)
	}
}

func TestPortDiscoveryEffectiveDefaultMatrix(t *testing.T) {
	enabled := true
	disabled := false
	tests := []struct {
		name    string
		service Service
		want    bool
	}{
		{name: "no ports defaults on", service: Service{}, want: true},
		{name: "configured ports default off", service: Service{Ports: []int{8080}}, want: false},
		{name: "explicit opt in with configured ports", service: Service{Ports: []int{8080}, DetectPorts: &enabled}, want: true},
		{name: "explicit opt out without ports", service: Service{DetectPorts: &disabled}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.service.PortDiscoveryEnabled(); got != tt.want {
				t.Fatalf("PortDiscoveryEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectPortsYAMLAndMergePreserveExplicitFalse(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "base.yaml")
	overridePath := filepath.Join(directory, "override.yaml")
	base := "project: Test\nservices:\n  api:\n    command: run\n    ports: [8080]\n    detect_ports: true\n"
	override := "services:\n  api:\n    detect_ports: false\n"
	if err := os.WriteFile(basePath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridePath, []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFiles([]string{basePath, overridePath})
	if err != nil {
		t.Fatal(err)
	}
	service := cfg.Services["api"]
	if service.DetectPorts == nil || *service.DetectPorts || service.PortDiscoveryEnabled() {
		t.Fatalf("merged detect_ports = %v, effective=%v", service.DetectPorts, service.PortDiscoveryEnabled())
	}
}

func TestLoadFilesMergesProcessComposeOverridesFromBaseDirectory(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "process-compose.yaml")
	overrideDirectory := filepath.Join(directory, "overrides")
	if err := os.MkdirAll(overrideDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(overrideDirectory, "dev.yaml")
	base := `
name: Merge Demo
processes:
  db:
    command: sleep 60
  api:
    command: run-api
    working_dir: apps/api
    environment:
      BASE: one
    depends_on:
      db:
        condition: process_started
    availability:
      restart: always
    shutdown:
      timeout_seconds: 20
  keep-dir:
    command: run-keep
    working_dir: apps/keep
    disabled: true
`
	override := `
processes:
  api:
    command: run-api --debug
    working_dir: apps/debug
    environment:
      BASE: two
      EXTRA: yes
    availability:
      max_restarts: 2
    shutdown:
      parent_only: true
  keep-dir:
    is_disabled: "false"
    environment:
      DEBUG: yes
  worker:
    command: run-worker
    depends_on:
      api:
        condition: process_completed
`
	if err := os.WriteFile(basePath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridePath, []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFiles([]string{basePath, overridePath})
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	api := cfg.Services["api"]
	if api.Command != "run-api --debug" || api.Dir != filepath.Join(directory, "apps/debug") {
		t.Fatalf("merged api command/dir = %q/%q", api.Command, api.Dir)
	}
	if api.Env["BASE"] != "two" || api.Env["EXTRA"] != "yes" {
		t.Fatalf("merged api environment = %v", api.Env)
	}
	if len(api.DependsOn) != 1 || api.DependsOn[0] != "db" {
		t.Fatalf("merged dependencies = %v", api.DependsOn)
	}
	if api.Availability.Restart != "always" || api.Availability.MaxRestarts != 2 || api.Availability.Backoff != time.Second {
		t.Fatalf("merged availability = %#v", api.Availability)
	}
	if api.Shutdown.Timeout != 20*time.Second || !api.Shutdown.ParentOnly || api.Shutdown.Signal != 15 {
		t.Fatalf("merged shutdown = %#v", api.Shutdown)
	}
	if keep := cfg.Services["keep-dir"]; keep.Dir != filepath.Join(directory, "apps/keep") || keep.Env["DEBUG"] != "yes" || keep.Disabled {
		t.Fatalf("env-only override reset working directory: %#v", keep)
	}
	if len(cfg.Paths) != 2 {
		t.Fatalf("config paths = %v", cfg.Paths)
	}
}

func TestDotenvAndServiceEnvFilesAreLoadedAndWatched(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "kranz.yaml")
	dotenvPath := filepath.Join(directory, ".env")
	serviceEnvPath := filepath.Join(directory, "service.env")
	data := `
project: Env Demo
defaults:
  dir: ` + directory + `
services:
  api:
    command: echo ${APP_NAME}
    env_files: [service.env]
    env:
      SHARED: explicit
  isolated:
    command: echo isolated
    is_dotenv_disabled: true
`
	if err := os.WriteFile(dotenvPath, []byte("APP_NAME=from-dotenv\nGLOBAL=yes\nSHARED=global\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serviceEnvPath, []byte("LOCAL=loaded\nSHARED=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	api := cfg.Services["api"]
	if api.Command != "echo from-dotenv" || api.Env["GLOBAL"] != "yes" || api.Env["LOCAL"] != "loaded" || api.Env["SHARED"] != "explicit" {
		t.Fatalf("resolved service = command %q env %v", api.Command, api.Env)
	}
	if isolated := cfg.Services["isolated"]; isolated.Env["GLOBAL"] != "" || isolated.Env["APP_NAME"] != "" {
		t.Fatalf("dotenv-disabled service received dotenv values: %v", isolated.Env)
	}
	joined := strings.Join(cfg.WatchPaths, "\n")
	if !strings.Contains(joined, dotenvPath) || !strings.Contains(joined, serviceEnvPath) {
		t.Fatalf("watch paths = %v", cfg.WatchPaths)
	}
}

func TestProcessComposeMapsLifecycleAndDependencyConditions(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "process-compose.yaml")
	data := `
name: Lifecycle Demo
processes:
  started:
    command: sleep 1
  healthy:
    command: sleep 1
    readiness_probe:
      exec:
        command: exit 0
  completed:
    command: exit 2
  successful:
    command: exit 7
    success_exit_codes: [7]
  logged:
    command: echo READY
    ready_log_line: READY
  app:
    command: sleep 1
    depends_on:
      started: {condition: process_started}
      healthy: {condition: process_healthy}
      completed: {condition: process_completed}
      successful: {condition: process_completed_successfully}
      logged: {condition: process_log_ready}
    availability:
      restart: on_failure
      backoff_seconds: 2
      max_restarts: 4
      exit_on_skipped: true
    shutdown:
      command: stop-app
      timeout_seconds: 6
      parent_only: true
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	app := cfg.Services["app"]
	for dependency, condition := range map[string]DependencyCondition{
		"started": DependencyStarted, "healthy": DependencyHealthy, "completed": DependencyCompleted,
		"successful": DependencyCompletedSuccessfully, "logged": DependencyLogReady,
	} {
		if got := app.DependencyConditions[dependency].Condition; got != condition {
			t.Errorf("%s condition = %q, want %q", dependency, got, condition)
		}
	}
	if app.Availability.Restart != "on_failure" || app.Availability.Backoff != 2*time.Second || app.Availability.MaxRestarts != 4 || !app.Availability.ExitOnSkipped {
		t.Fatalf("availability = %#v", app.Availability)
	}
	if app.Shutdown.Command != "stop-app" || app.Shutdown.Timeout != 6*time.Second || !app.Shutdown.ParentOnly {
		t.Fatalf("shutdown = %#v", app.Shutdown)
	}
	if cfg.Services["started"].Shutdown.Signal != 15 || cfg.Services["started"].Shutdown.Timeout != 10*time.Second {
		t.Fatalf("Process Compose shutdown defaults = %#v", cfg.Services["started"].Shutdown)
	}
}

func TestValidateRejectsIncompatibleOrInvalidReadyLog(t *testing.T) {
	for _, serviceConfig := range []Service{
		{Command: "run", ReadyLogLine: "["},
		{Command: "run", ReadyLogLine: "ready", HealthCheck: &HealthCheckConfig{Readiness: &CheckConfig{Type: CheckCommand, Command: "true"}}},
	} {
		cfg := &Config{Project: "Test", Services: map[string]Service{"api": serviceConfig}}
		if err := Validate(cfg); err == nil {
			t.Fatalf("invalid ready_log_line config was accepted: %#v", serviceConfig)
		}
	}
}

func TestLoadNormalizesServiceActionsAndActionGroups(t *testing.T) {
	directory := t.TempDir()
	serviceDir := filepath.Join(directory, "service")
	actionDir := filepath.Join(directory, "action")
	groupDir := filepath.Join(directory, "infra")
	for _, path := range []string{serviceDir, actionDir, groupDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(serviceDir, "service.env"): "SERVICE_FILE=yes\nSHARED=service-file\n",
		filepath.Join(actionDir, "action.env"):   "ACTION_FILE=yes\nSHARED=action-file\n",
		filepath.Join(groupDir, "group.env"):     "GROUP_FILE=yes\nSHARED=group-file\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(directory, "kranz.yaml")
	data := `
project: Actions
defaults:
  shell: /bin/zsh
  env:
    GLOBAL: inherited
services:
  app:
    command: run-app
    dir: ` + serviceDir + `
    env_files: [service.env]
    env:
      SERVICE_ONLY: yes
    actions:
      build-launcher:
        command: npm run build:launcher
        description: Build launcher
        timeout: 45s
        confirm: true
      migrate:
        command: npm run migrate
        dir: ` + actionDir + `
        env_files: [action.env]
        env:
          SHARED: action-explicit
action_groups:
  remote-infra:
    description: Remote stack
    dir: ` + groupDir + `
    env_files: [group.env]
    actions:
      up:
        command: ssh host docker-compose up -d
      console:
        command: ssh -t host shell
        interactive: true
`
	if err := os.WriteFile(configPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	build := cfg.Services["app"].Actions["build-launcher"]
	if build.Dir != serviceDir || build.Shell != "/bin/zsh" || build.Timeout != 45*time.Second || !build.ConfirmationRequired() {
		t.Fatalf("normalized service action = %#v", build)
	}
	if build.Env["GLOBAL"] != "inherited" || build.Env["SERVICE_FILE"] != "yes" || build.Env["SERVICE_ONLY"] != "yes" {
		t.Fatalf("inherited service action env = %v", build.Env)
	}
	migrate := cfg.Services["app"].Actions["migrate"]
	if migrate.Env["ACTION_FILE"] != "yes" || migrate.Env["SHARED"] != "action-explicit" {
		t.Fatalf("action env precedence = %v", migrate.Env)
	}
	group := cfg.ActionGroups["remote-infra"]
	up := group.Actions["up"]
	if group.Dir != groupDir || up.Dir != groupDir || up.Shell != "/bin/zsh" {
		t.Fatalf("normalized action group = group %#v action %#v", group, up)
	}
	if up.Env["GLOBAL"] != "inherited" || up.Env["GROUP_FILE"] != "yes" || up.Env["SHARED"] != "group-file" {
		t.Fatalf("inherited action group env = %v", up.Env)
	}
	if !group.Actions["console"].InteractiveEnabled() {
		t.Fatal("interactive flag was not preserved")
	}
	for path := range files {
		if !containsString(cfg.WatchPaths, path) {
			t.Fatalf("action env file %s missing from watch paths %v", path, cfg.WatchPaths)
		}
	}
}

func TestLoadFilesMergesActionsByOwnerAndName(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "kranz.yaml")
	overridePath := filepath.Join(directory, "kranz.local.yaml")
	base := `
project: Layered Actions
services:
  app:
    command: run
    actions:
      migrate:
        command: migrate
        description: Base description
        timeout: 10s
        confirm: true
action_groups:
  infra:
    dir: ./infra
    actions:
      up:
        command: up
`
	override := `
project: Layered Actions
services:
  app:
    actions:
      migrate:
        description: Local description
        confirm: false
      seed:
        command: seed
action_groups:
  infra:
    shell: /bin/zsh
    actions:
      up:
        timeout: 30s
      down:
        command: down
`
	for path, content := range map[string]string{basePath: base, overridePath: override} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := LoadFiles([]string{basePath, overridePath})
	if err != nil {
		t.Fatal(err)
	}
	migrate := cfg.Services["app"].Actions["migrate"]
	if migrate.Command != "migrate" || migrate.Description != "Local description" || migrate.Timeout != 10*time.Second || migrate.ConfirmationRequired() || migrate.Confirm == nil {
		t.Fatalf("merged service action = %#v", migrate)
	}
	if cfg.Services["app"].Actions["seed"].Command != "seed" {
		t.Fatalf("new service action missing: %#v", cfg.Services["app"].Actions)
	}
	group := cfg.ActionGroups["infra"]
	if group.Dir != "./infra" || group.Shell != "/bin/zsh" || group.Actions["up"].Timeout != 30*time.Second || group.Actions["down"].Command != "down" {
		t.Fatalf("merged action group = %#v", group)
	}
}

func TestValidateActionsAndActionOnlyProjects(t *testing.T) {
	valid := &Config{
		Project: "Operations",
		ActionGroups: map[string]ActionGroup{
			"database": {Actions: map[string]Action{"migrate": {Command: "migrate"}}},
		},
	}
	if err := Validate(valid); err != nil {
		t.Fatalf("action-only project was rejected: %v", err)
	}

	tests := []struct {
		name    string
		config  *Config
		wantErr string
	}{
		{
			name:    "empty project",
			config:  &Config{Project: "Empty"},
			wantErr: "service or action group",
		},
		{
			name:    "empty group",
			config:  &Config{Project: "Empty group", ActionGroups: map[string]ActionGroup{"infra": {}}},
			wantErr: "must contain at least one action",
		},
		{
			name: "missing command",
			config: &Config{Project: "Missing", Services: map[string]Service{"app": {
				Command: "run", Actions: map[string]Action{"broken": {}},
			}}},
			wantErr: "field 'command' is required",
		},
		{
			name: "negative timeout",
			config: &Config{Project: "Timeout", ActionGroups: map[string]ActionGroup{"ops": {
				Actions: map[string]Action{"broken": {Command: "run", Timeout: -time.Second}},
			}}},
			wantErr: "timeout cannot be negative",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.config)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validation error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestNativeConfigRejectsUnknownActionFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kranz.yaml")
	data := `
project: Unknown Action Field
action_groups:
  ops:
    actions:
      deploy:
        command: deploy
        title: Deploy
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "field title not found") {
		t.Fatalf("unknown action field error = %v", err)
	}
}

func TestActionIDsAndResolutionAreDeterministicAndUnambiguous(t *testing.T) {
	cfg := &Config{
		Project: "Action IDs",
		Services: map[string]Service{
			"web": {Command: "run", Actions: map[string]Action{
				"test":           {Command: "web-test"},
				"build:launcher": {Command: "build"},
			}},
		},
		ActionGroups: map[string]ActionGroup{
			"ops": {Actions: map[string]Action{
				"test": {Command: "ops-test"},
			}},
		},
	}
	want := []ActionID{
		{OwnerKind: ActionOwnerService, Owner: "web", Name: "build:launcher"},
		{OwnerKind: ActionOwnerService, Owner: "web", Name: "test"},
		{OwnerKind: ActionOwnerGroup, Owner: "ops", Name: "test"},
	}
	got := cfg.ActionIDs()
	if len(got) != len(want) {
		t.Fatalf("action ids = %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("action id %d = %#v, want %#v", index, got[index], want[index])
		}
		action, exists := cfg.ResolveAction(got[index])
		if !exists || action.Command == "" {
			t.Fatalf("resolve %#v = %#v, %v", got[index], action, exists)
		}
	}
	serviceTest, _ := cfg.ResolveAction(want[1])
	groupTest, _ := cfg.ResolveAction(want[2])
	if serviceTest.Command != "web-test" || groupTest.Command != "ops-test" {
		t.Fatalf("same-name actions collided: service %#v group %#v", serviceTest, groupTest)
	}
	if _, exists := cfg.ResolveAction(ActionID{OwnerKind: "unknown", Owner: "ops", Name: "test"}); exists {
		t.Fatal("unknown owner kind resolved")
	}
}

func boolPointer(value bool) *bool { return &value }

func TestLoadPrerequisitesResolveScopeAndRunPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kranz.yaml")
	data := `project: Prerequisites
action_groups:
  infra:
    actions:
      up:
        command: docker compose up -d
services:
  api:
    command: npm run dev
    actions:
      migrate:
        command: npm run migrate
    before_start:
      - action: migrate
      - group: infra
        action: up
        run: always
  web:
    command: npm run dev
    before_start:
      - service: api
        action: migrate
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	api := cfg.Services["api"]
	if len(api.BeforeStart) != 2 {
		t.Fatalf("api before_start = %#v", api.BeforeStart)
	}
	// An unqualified reference resolves against the declaring service.
	if id := api.BeforeStart[0].ActionID("api"); id.OwnerKind != ActionOwnerService || id.Owner != "api" || id.Name != "migrate" {
		t.Fatalf("own action reference = %#v", id)
	}
	if api.BeforeStart[0].RunPolicy() != PrerequisiteOnce {
		t.Fatalf("default run policy = %q, want once", api.BeforeStart[0].RunPolicy())
	}
	if id := api.BeforeStart[1].ActionID("api"); id.OwnerKind != ActionOwnerGroup || id.Owner != "infra" {
		t.Fatalf("group reference = %#v", id)
	}
	if api.BeforeStart[1].RunPolicy() != PrerequisiteAlways {
		t.Fatalf("explicit run policy = %q, want always", api.BeforeStart[1].RunPolicy())
	}
	web := cfg.Services["web"]
	if id := web.BeforeStart[0].ActionID("web"); id.Owner != "api" || id.Name != "migrate" {
		t.Fatalf("cross-service reference = %#v", id)
	}
}

func TestValidateRejectsUnusablePrerequisites(t *testing.T) {
	base := func(prerequisite Prerequisite) *Config {
		return &Config{
			Project: "Prerequisites",
			Services: map[string]Service{
				"api": {
					Command: "npm run dev",
					Actions: map[string]Action{
						"migrate":  {Command: "npm run migrate"},
						"console":  {Command: "npm run console", Interactive: boolPointer(true)},
						"deployed": {Command: "true"},
					},
					BeforeStart: []Prerequisite{prerequisite},
				},
			},
		}
	}
	cases := []struct {
		name         string
		prerequisite Prerequisite
		want         string
	}{
		{"unknown action", Prerequisite{Action: "missing"}, "was not found"},
		{"unknown group", Prerequisite{Group: "nope", Action: "up"}, "was not found"},
		{"missing action name", Prerequisite{Service: "api"}, "field 'action' is required"},
		{"both scopes", Prerequisite{Service: "api", Group: "infra", Action: "migrate"}, "not both"},
		{"unknown run policy", Prerequisite{Action: "migrate", Run: "sometimes"}, "run must be once or always"},
		{"interactive action", Prerequisite{Action: "console"}, "interactive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(base(tc.prerequisite))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestMergeReplacesPrerequisiteSequence(t *testing.T) {
	directory := t.TempDir()
	base := filepath.Join(directory, "kranz.yaml")
	override := filepath.Join(directory, "kranz.local.yaml")
	baseData := `project: Prerequisites
services:
  api:
    command: npm run dev
    actions:
      migrate:
        command: npm run migrate
      seed:
        command: npm run seed
    before_start:
      - action: migrate
`
	overrideData := `services:
  api:
    before_start:
      - action: seed
`
	if err := os.WriteFile(base, []byte(baseData), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte(overrideData), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFiles([]string{base, override})
	if err != nil {
		t.Fatal(err)
	}
	prerequisites := cfg.Services["api"].BeforeStart
	if len(prerequisites) != 1 || prerequisites[0].Action != "seed" {
		t.Fatalf("merged before_start = %#v, want the override sequence only", prerequisites)
	}
}
