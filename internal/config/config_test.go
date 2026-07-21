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
	os.Setenv("TEST_VAR", "test_value")
	defer os.Unsetenv("TEST_VAR")

	// Create a temporary config.
	tmpFile := "../../testdata/test_env.yaml"
	content := "project: Test\nversion: \"1.0\"\nservices:\n  test:\n    command: echo ${TEST_VAR}\n"
	os.WriteFile(tmpFile, []byte(content), 0644)
	defer os.Remove(tmpFile)

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
