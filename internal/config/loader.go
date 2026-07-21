package config

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Load parses one configuration file and expands ${VAR} environment values.
func Load(path string) (*Config, error) {
	return LoadFiles([]string{path})
}

// LoadFiles loads and merges configuration files from left to right. Later
// files override scalar fields and merge environment/dependency maps while
// preserving the remainder of an existing service definition.
func LoadFiles(paths []string) (*Config, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one configuration file is required")
	}
	var merged *Config
	basePath := paths[0]
	for _, path := range paths {
		cfg, err := loadFile(path, basePath)
		if err != nil {
			return nil, err
		}
		if merged == nil {
			merged = cfg
		} else if err := mergeConfig(merged, cfg); err != nil {
			return nil, fmt.Errorf("merge %s: %w", path, err)
		}
		merged.Paths = append(merged.Paths, path)
	}
	merged.Defaults.Env = mergeStringMap(merged.dotenvEnv, merged.Defaults.Env)
	if err := applyDefaults(merged); err != nil {
		return nil, err
	}
	if err := Validate(merged); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return merged, nil
}

func loadFile(path, basePath string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	format, err := detectFormat(data)
	if err != nil {
		return nil, err
	}
	dotenvPath := filepath.Join(filepath.Dir(basePath), ".env")
	dotenv, err := readDotEnv(dotenvPath)
	if err != nil {
		return nil, fmt.Errorf("read .env for %s: %w", path, err)
	}
	expanded := []byte(os.Expand(string(data), func(name string) string {
		if value, ok := os.LookupEnv(name); ok {
			return value
		}
		return dotenv[name]
	}))
	var cfg *Config
	switch format {
	case SourceProcessCompose:
		// Process Compose resolves paths in every override relative to the
		// first file in the merge set.
		cfg, err = loadProcessCompose(expanded, basePath)
	default:
		cfg, err = loadNative(expanded)
	}
	if err != nil {
		return nil, err
	}

	cfg.explicitEnv = mergeStringMap(nil, cfg.Defaults.Env)
	cfg.dotenvEnv = mergeStringMap(nil, dotenv)
	cfg.WatchPaths = appendUniqueString(cfg.WatchPaths, dotenvPath)
	return cfg, nil
}

func mergeConfig(base, override *Config) error {
	if base.Source != override.Source {
		return fmt.Errorf("cannot mix %s and %s formats", base.Source, override.Source)
	}
	if override.Project != "" {
		base.Project = override.Project
	}
	if override.Version != "" {
		base.Version = override.Version
	}
	if override.UI.Theme != "" {
		base.UI.Theme = override.UI.Theme
	}
	if override.UI.Accent != "" {
		base.UI.Accent = override.UI.Accent
	}
	if override.UI.Background != "" {
		base.UI.Background = override.UI.Background
	}
	if override.UI.ColorMode != "" {
		base.UI.ColorMode = override.UI.ColorMode
	}
	mergeDefaults(&base.Defaults, override.Defaults)
	if base.Services == nil {
		base.Services = make(map[string]Service)
	}
	for name, incoming := range override.Services {
		if current, exists := base.Services[name]; exists {
			base.Services[name] = mergeService(current, incoming, base.Source == SourceProcessCompose)
		} else {
			base.Services[name] = incoming
		}
	}
	base.Diagnostics = append(base.Diagnostics, override.Diagnostics...)
	base.dotenvEnv = mergeStringMap(base.dotenvEnv, override.dotenvEnv)
	base.explicitEnv = mergeStringMap(base.explicitEnv, override.explicitEnv)
	for _, path := range override.WatchPaths {
		base.WatchPaths = appendUniqueString(base.WatchPaths, path)
	}
	return nil
}

func mergeDefaults(base *Defaults, override Defaults) {
	if override.Dir != "" {
		base.Dir = override.Dir
	}
	if override.Shell != "" {
		base.Shell = override.Shell
	}
	base.Env = mergeStringMap(base.Env, override.Env)
	if len(override.EnvFiles) > 0 {
		base.EnvFiles = append([]string(nil), override.EnvFiles...)
	}
}

func mergeService(base, override Service, mergeDependencies bool) Service {
	if override.Command != "" {
		base.Command = override.Command
	}
	if override.Description != "" {
		base.Description = override.Description
	}
	if override.Dir != "" {
		base.Dir = override.Dir
	}
	if override.Shell != "" {
		base.Shell = override.Shell
	}
	if len(override.Ports) > 0 {
		base.Ports = append([]int(nil), override.Ports...)
	}
	if len(override.Tags) > 0 {
		base.Tags = append([]string(nil), override.Tags...)
	}
	if len(override.DependsOn) > 0 {
		if mergeDependencies {
			for _, dependency := range override.DependsOn {
				base.DependsOn = appendUniqueString(base.DependsOn, dependency)
			}
		} else {
			base.DependsOn = append([]string(nil), override.DependsOn...)
		}
	}
	if len(override.DependencyConditions) > 0 {
		if !mergeDependencies || base.DependencyConditions == nil {
			base.DependencyConditions = make(map[string]DependencyConfig)
		}
		for dependency, condition := range override.DependencyConditions {
			base.DependencyConditions[dependency] = condition
		}
	}
	base.Env = mergeStringMap(base.Env, override.Env)
	if len(override.EnvFiles) > 0 {
		base.EnvFiles = append([]string(nil), override.EnvFiles...)
	}
	if override.HealthCheck != nil {
		base.HealthCheck = override.HealthCheck
	}
	if override.ReadyLogLine != "" {
		base.ReadyLogLine = override.ReadyLogLine
	}
	if override.Availability != (AvailabilityConfig{}) {
		if override.Availability.Restart != "" {
			base.Availability.Restart = override.Availability.Restart
		}
		if override.Availability.Backoff != 0 {
			base.Availability.Backoff = override.Availability.Backoff
		}
		if override.Availability.MaxRestarts != 0 {
			base.Availability.MaxRestarts = override.Availability.MaxRestarts
		}
		if override.Availability.ExitOnEnd {
			base.Availability.ExitOnEnd = true
		}
		if override.Availability.ExitOnSkipped {
			base.Availability.ExitOnSkipped = true
		}
	}
	if override.Shutdown != (ShutdownConfig{}) {
		if override.Shutdown.Command != "" {
			base.Shutdown.Command = override.Shutdown.Command
		}
		if override.Shutdown.Timeout != 0 {
			base.Shutdown.Timeout = override.Shutdown.Timeout
		}
		if override.Shutdown.Signal != 0 {
			base.Shutdown.Signal = override.Shutdown.Signal
		}
		if override.Shutdown.ParentOnly {
			base.Shutdown.ParentOnly = true
		}
	}
	if len(override.SuccessExitCodes) > 0 {
		base.SuccessExitCodes = append([]int(nil), override.SuccessExitCodes...)
	}
	if override.disabledSet || override.Disabled {
		base.Disabled = override.Disabled
		base.disabledSet = true
	}
	if override.DisableDotenv {
		base.DisableDotenv = true
	}
	return base
}

func mergeStringMap(base, override map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range override {
		result[key] = value
	}
	return result
}

func loadNative(data []byte) (*Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse Kranz YAML: %w", err)
	}
	cfg.Source = SourceKranz
	return &cfg, nil
}

func detectFormat(data []byte) (SourceFormat, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return "", fmt.Errorf("parse YAML: %w", err)
	}
	if len(document.Content) == 0 || len(document.Content[0].Content) == 0 {
		return "", fmt.Errorf("configuration is empty")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", fmt.Errorf("configuration root must be a mapping")
	}
	hasServices, hasProcesses := false, false
	for index := 0; index+1 < len(root.Content); index += 2 {
		switch root.Content[index].Value {
		case "services":
			hasServices = true
		case "processes":
			hasProcesses = true
		}
	}
	if hasServices && hasProcesses {
		return "", fmt.Errorf("configuration cannot contain both 'services' and 'processes'")
	}
	if hasProcesses {
		return SourceProcessCompose, nil
	}
	return SourceKranz, nil
}

// Discover finds a supported project config without confusing Docker Compose files.
func Discover(directory string) (string, error) {
	if directory == "" {
		directory = "."
	}
	for _, name := range []string{"kranz.yaml", "kranz.yml", "process-compose.yaml", "process-compose.yml"} {
		path := filepath.Join(directory, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("no kranz.yaml or process-compose.yaml found in %s", directory)
}

// DiscoverFiles returns the primary project configuration and the conventional
// Process Compose override file when one is present.
func DiscoverFiles(directory string) ([]string, error) {
	primary, err := Discover(directory)
	if err != nil {
		return nil, err
	}
	result := []string{primary}
	base := filepath.Base(primary)
	if base != "process-compose.yaml" && base != "process-compose.yml" {
		return result, nil
	}
	extension := filepath.Ext(base)
	override := filepath.Join(filepath.Dir(primary), "process-compose.override"+extension)
	if info, statErr := os.Stat(override); statErr == nil && !info.IsDir() {
		result = append(result, override)
	}
	return result, nil
}

// applyDefaults fills values omitted by individual services.
func applyDefaults(cfg *Config) error {
	defDir := cfg.Defaults.Dir
	if defDir == "" {
		defDir = "."
	}
	defShell := cfg.Defaults.Shell
	if defShell == "" {
		defShell = "/bin/bash"
	}

	for name, svc := range cfg.Services {
		if svc.Dir == "" {
			svc.Dir = defDir
		}
		if svc.Shell == "" {
			svc.Shell = defShell
		}
		if cfg.Source == SourceProcessCompose {
			if svc.Availability.Restart != "" && svc.Availability.Backoff == 0 {
				svc.Availability.Backoff = time.Second
			}
			if svc.Shutdown.Timeout == 0 {
				svc.Shutdown.Timeout = 10 * time.Second
			}
			if svc.Shutdown.Signal == 0 {
				svc.Shutdown.Signal = 15
			}
		}
		// Service-specific variables override project defaults.
		fileEnv, err := loadServiceEnvFiles(cfg, svc)
		if err != nil {
			return fmt.Errorf("service %q env files: %w", name, err)
		}
		svc.Env = mergeStringMap(fileEnv, svc.Env)
		for k, v := range cfg.Defaults.Env {
			if svc.DisableDotenv {
				if _, fromDotenv := cfg.dotenvEnv[k]; fromDotenv {
					if _, explicit := cfg.explicitEnv[k]; !explicit {
						continue
					}
				}
			}
			if _, ok := svc.Env[k]; !ok {
				svc.Env[k] = v
			}
		}
		for dependency := range svc.DependencyConditions {
			if !containsString(svc.DependsOn, dependency) {
				svc.DependsOn = append(svc.DependsOn, dependency)
			}
		}
		for _, path := range resolvedServiceEnvFiles(cfg, svc) {
			cfg.WatchPaths = appendUniqueString(cfg.WatchPaths, path)
		}
		sort.Strings(svc.DependsOn)
		// Expand host environment references after all layers have been merged.
		for k, v := range svc.Env {
			svc.Env[k] = os.ExpandEnv(v)
		}

		// Apply timing defaults independently to readiness and liveness probes.
		if svc.HealthCheck != nil {
			applyCheckDefaults(svc.HealthCheck.Readiness)
			applyCheckDefaults(svc.HealthCheck.Liveness)
		}

		// Map iteration returns a copy, so store the normalized service explicitly.
		cfg.Services[name] = svc
	}
	return nil
}

func loadServiceEnvFiles(cfg *Config, svc Service) (map[string]string, error) {
	files := resolvedServiceEnvFiles(cfg, svc)
	result := make(map[string]string)
	for _, path := range files {
		values, err := readDotEnv(path)
		if err != nil {
			return nil, err
		}
		result = mergeStringMap(result, values)
	}
	return result, nil
}

func resolvedServiceEnvFiles(cfg *Config, svc Service) []string {
	files := append(append([]string(nil), cfg.Defaults.EnvFiles...), svc.EnvFiles...)
	for index, path := range files {
		if filepath.IsAbs(path) {
			continue
		}
		base := svc.Dir
		if base == "" {
			base = cfg.Defaults.Dir
		}
		files[index] = filepath.Clean(filepath.Join(base, path))
	}
	return files
}

func readDotEnv(path string) (map[string]string, error) {
	values := make(map[string]string)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid entry %q", line)
		}
		value = strings.TrimSpace(value)
		if unquoted, unquoteErr := strconv.Unquote(value); unquoteErr == nil {
			value = unquoted
		}
		values[strings.TrimSpace(key)] = value
	}
	return values, scanner.Err()
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendUniqueString(values []string, value string) []string {
	if containsString(values, value) {
		return values
	}
	return append(values, value)
}

// applyCheckDefaults fills probe timing values that were not configured.
func applyCheckDefaults(c *CheckConfig) {
	if c == nil {
		return
	}
	if c.Interval == 0 {
		c.Interval = 5 * time.Second
	}
	if c.Timeout == 0 {
		c.Timeout = 2 * time.Second
	}
	if c.FailureThreshold == 0 {
		c.FailureThreshold = 3
	}
}

// ServiceNames returns service names in stable configuration order.
func (cfg *Config) ServiceNames() []string {
	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetAllTags returns every unique service tag.
func (cfg *Config) GetAllTags() []string {
	tagSet := make(map[string]struct{})
	for _, svc := range cfg.Services {
		for _, tag := range svc.Tags {
			tagSet[tag] = struct{}{}
		}
	}
	tags := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		tags = append(tags, tag)
	}
	return tags
}

// GetServicesByTags returns services matching at least one requested tag.
func (cfg *Config) GetServicesByTags(tags []string) []string {
	tagSet := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		tagSet[strings.ToLower(t)] = struct{}{}
	}

	var names []string
	for name, svc := range cfg.Services {
		for _, t := range svc.Tags {
			if _, ok := tagSet[strings.ToLower(t)]; ok {
				names = append(names, name)
				break
			}
		}
	}
	return names
}

// GetDependsOn returns the direct dependency adjacency list.
func (cfg *Config) GetDependsOn() map[string][]string {
	deps := make(map[string][]string)
	for name, svc := range cfg.Services {
		deps[name] = svc.DependsOn
	}
	return deps
}
