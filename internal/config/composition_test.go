package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeCompositionFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestComposeKeepsSourcesAutonomousAndQualifiesOnlyCollisions(t *testing.T) {
	root := t.TempDir()
	rootConfig := filepath.Join(root, "kranz.yaml")
	writeCompositionFile(t, rootConfig, `
project: workspace
ui:
  theme: graphite
include:
  - glob: repos/*/kranz.yaml
services:
  gateway:
    command: ./gateway
defaults:
  shell: /bin/sh
`)
	writeCompositionFile(t, filepath.Join(root, "repos", "alpha", "kranz.yaml"), `
project: alpha-project
ui:
  theme: child-theme
defaults:
  dir: .
  shell: /bin/zsh
  env_files: [alpha.env]
services:
  api:
    command: ./serve
    actions:
      seed: {command: ./seed}
  worker:
    command: ./work
    depends_on: [api]
    before_start: [{service: api, action: seed}]
`)
	writeCompositionFile(t, filepath.Join(root, "repos", "alpha", "alpha.env"), "ALPHA_ONLY=yes\n")
	writeCompositionFile(t, filepath.Join(root, "repos", "beta", "kranz.yaml"), `
project: beta-project
defaults:
  dir: .
services:
  api:
    command: ./serve
`)

	cfg, err := Compose(LoadOptions{Directory: root, Sources: []string{rootConfig}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != "workspace" || cfg.UI.Theme != "graphite" {
		t.Fatalf("root metadata leaked or changed: %#v", cfg)
	}
	if got, want := cfg.ServiceNames(), []string{"gateway", "alpha/api", "worker", "beta/api"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("service order = %v, want %v", got, want)
	}
	alpha := cfg.Services["alpha/api"]
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if alpha.Shell != "/bin/zsh" || alpha.Dir != filepath.Join(canonicalRoot, "repos", "alpha") {
		t.Fatalf("alpha local defaults/path = %#v", alpha)
	}
	if cfg.Services["beta/api"].Shell == "/bin/zsh" {
		t.Fatal("defaults leaked between autonomous sources")
	}
	if got := alpha.EnvFiles; !reflect.DeepEqual(got, []string{filepath.Join(canonicalRoot, "repos", "alpha", "alpha.env")}) || len(cfg.Services["beta/api"].EnvFiles) != 0 {
		t.Fatalf("default env files were not materialized locally: alpha=%v beta=%v", got, cfg.Services["beta/api"].EnvFiles)
	}
	if got := cfg.Services["worker"].DependsOn; !reflect.DeepEqual(got, []string{"alpha/api"}) {
		t.Fatalf("local dependency = %v", got)
	}
	if got := cfg.Services["worker"].BeforeStart[0].Service; got != "alpha/api" {
		t.Fatalf("local prerequisite service = %q", got)
	}
	if cfg.ServiceMetadata["alpha/api"].ID == "" || cfg.ServiceMetadata["alpha/api"].ID == cfg.ServiceMetadata["beta/api"].ID {
		t.Fatalf("service IDs are not stable and distinct: %#v", cfg.ServiceMetadata)
	}
	foundResolvedDir := false
	for _, entry := range cfg.Provenance {
		if entry.FieldPath == "services.alpha/api.dir" && entry.Stage == StageDefault && entry.OriginalValue == "." && entry.EffectiveValue == alpha.Dir {
			foundResolvedDir = true
		}
	}
	if !foundResolvedDir {
		t.Fatalf("resolved directory provenance is missing: %#v", cfg.Provenance)
	}
}

func TestComposeDeduplicatesDiamondAndRejectsCycle(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: root
include:
  - path: left/kranz.yaml
  - path: right/kranz.yaml
services: {root: {command: root}}
`)
	writeCompositionFile(t, filepath.Join(root, "left", "kranz.yaml"), `project: left
include: [{path: ../shared/kranz.yaml}]
services: {left: {command: left}}
`)
	writeCompositionFile(t, filepath.Join(root, "right", "kranz.yaml"), `project: right
include: [{path: ../shared/kranz.yaml}]
services: {right: {command: right}}
`)
	shared := filepath.Join(root, "shared", "kranz.yaml")
	writeCompositionFile(t, shared, `project: shared
services: {shared: {command: shared}}
`)
	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 4 || len(cfg.Services) != 4 {
		t.Fatalf("diamond was not deduplicated: sources=%d services=%d", len(cfg.Sources), len(cfg.Services))
	}
	if len(cfg.CompositionDiagnostics) == 0 || cfg.CompositionDiagnostics[0].Code != "source_deduplicated" {
		t.Fatalf("missing dedupe diagnostic: %#v", cfg.CompositionDiagnostics)
	}
	writeCompositionFile(t, shared, `project: shared
include: [{path: ../kranz.yaml}]
services: {shared: {command: shared}}
`)
	_, err = Compose(LoadOptions{Directory: root})
	if err == nil || !strings.Contains(err.Error(), "config_include_cycle") || !strings.Contains(err.Error(), "shared/kranz.yaml") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestComposeDiscoveryDepthSymlinksAndVirtualRoot(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "one", "kranz.yaml"), `project: one
services: {one: {command: one}}
`)
	writeCompositionFile(t, filepath.Join(root, "deep", "two", "kranz.yaml"), `project: two
services: {two: {command: two}}
`)
	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != filepath.Base(root) || !reflect.DeepEqual(cfg.ServiceNames(), []string{"two", "one"}) {
		t.Fatalf("virtual project = %q services=%v", cfg.Project, cfg.ServiceNames())
	}

	rootConfig := filepath.Join(root, "root.yaml")
	writeCompositionFile(t, rootConfig, `project: bounded
include:
  - discover: {root: ., max_depth: 1}
services: {root: {command: root}}
`)
	bounded, err := Compose(LoadOptions{Directory: root, Sources: []string{rootConfig}})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := bounded.Services["two"]; found {
		t.Fatal("discovery exceeded max_depth")
	}
	if _, found := bounded.Services["one"]; !found {
		t.Fatal("discovery missed config inside max_depth")
	}

	if runtime.GOOS != "windows" {
		external := t.TempDir()
		writeCompositionFile(t, filepath.Join(external, "kranz.yaml"), `project: linked
services: {linked: {command: linked}}
`)
		if err := os.Symlink(external, filepath.Join(root, "linked")); err != nil {
			t.Fatal(err)
		}
		without, err := Compose(LoadOptions{Directory: root})
		if err != nil {
			t.Fatal(err)
		}
		if _, found := without.Services["linked"]; found {
			t.Fatal("default discovery followed a directory symlink")
		}
		with, err := Compose(LoadOptions{Directory: root, FollowSymlinks: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, found := with.Services["linked"]; !found {
			t.Fatal("follow-symlinks discovery missed linked config")
		}
	}
}

func TestComposeDiscoveryKeepsConventionalPerDirectoryPrecedence(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "repository")
	writeCompositionFile(t, filepath.Join(directory, "kranz.yaml"), "project: native\nservices: {native: {command: run}}\n")
	writeCompositionFile(t, filepath.Join(directory, "process-compose.yaml"), "name: compatible\nprocesses: {compat: {command: run}}\n")
	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg.Services["native"]; !exists {
		t.Fatal("native configuration was not discovered")
	}
	if _, exists := cfg.Services["compat"]; exists {
		t.Fatal("lower-priority conventional file in the same directory was also discovered")
	}
}

func TestComposeIncludeLimitOverrideAndProtected(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: root
include:
  - path: child/kranz.yaml
    max_depth: 0
overrides: [local.yaml]
protected:
  services:
    api:
      shell: /bin/protected
services:
  api:
    command: base
    env: {MODE: base}
`)
	writeCompositionFile(t, filepath.Join(root, "local.yaml"), `services:
  api:
    description: local
    env: {MODE: override}
`)
	writeCompositionFile(t, filepath.Join(root, "child", "kranz.yaml"), `project: child
include: [{path: grand/kranz.yaml}]
services: {worker: {command: worker}}
`)
	writeCompositionFile(t, filepath.Join(root, "child", "grand", "kranz.yaml"), `project: grand
services: {grand: {command: grand}}
`)
	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services["api"].Description != "local" || cfg.Services["api"].Env["MODE"] != "override" || cfg.Services["api"].Shell != "/bin/protected" {
		t.Fatalf("override/protected result = %#v", cfg.Services["api"])
	}
	if _, found := cfg.Services["grand"]; found {
		t.Fatal("include max_depth did not truncate grandchildren")
	}
	foundTruncation := false
	for _, diagnostic := range cfg.CompositionDiagnostics {
		foundTruncation = foundTruncation || diagnostic.Code == "include_depth_truncated"
	}
	if !foundTruncation {
		t.Fatalf("missing truncation diagnostic: %#v", cfg.CompositionDiagnostics)
	}
}

func TestComposeRejectsRemoteAndDistinguishesEmptyGlob(t *testing.T) {
	root := t.TempDir()
	if _, err := Compose(LoadOptions{Directory: root, Sources: []string{"https://example.invalid/config.yaml"}}); err == nil || !strings.Contains(err.Error(), "remote config") {
		t.Fatalf("remote error = %v", err)
	}
	if _, err := Compose(LoadOptions{Directory: root, Sources: []string{"missing/*.yaml"}}); err == nil || !strings.Contains(err.Error(), "config_glob_empty") {
		t.Fatalf("glob error = %v", err)
	}
	if _, err := Compose(LoadOptions{Directory: root, Sources: []string{"missing.yaml"}}); err == nil || strings.Contains(err.Error(), "config_glob_empty") || strings.Contains(err.Error(), root) {
		t.Fatalf("explicit error = %v", err)
	}
}

func TestComposeOverrideNullAndEmptyCollectionSemantics(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "kranz.yaml")
	writeCompositionFile(t, base, `project: patch
services:
  api:
    command: run-v1
    description: remove-me
    tags: [old]
    env: {FIRST: one, SECOND: two}
  worker:
    command: work
`)
	first := filepath.Join(root, "first.yaml")
	writeCompositionFile(t, first, `services:
  api:
    command: run-v2
    description: null
    tags: []
    env: {SECOND: changed}
`)
	second := filepath.Join(root, "second.yaml")
	writeCompositionFile(t, second, `services:
  api:
    env: {}
`)
	cfg, err := Compose(LoadOptions{Directory: root, Sources: []string{base}, Overrides: []string{first, second}})
	if err != nil {
		t.Fatal(err)
	}
	service := cfg.Services["api"]
	if service.Command != "run-v2" || service.StartAction().Command != "run-v2" || service.Description != "" || len(service.Tags) != 0 || len(service.Env) != 0 {
		t.Fatalf("typed patch semantics = %#v", service)
	}
	bad := filepath.Join(root, "bad.yaml")
	writeCompositionFile(t, bad, `services: {api: {env: [wrong]}}`)
	if _, err := Compose(LoadOptions{Directory: root, Sources: []string{base}, Overrides: []string{bad}}); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("incompatible type error = %v", err)
	}
	remove := filepath.Join(root, "remove.yaml")
	writeCompositionFile(t, remove, "services: {api: null}\n")
	removed, err := Compose(LoadOptions{Directory: root, Sources: []string{base}, Overrides: []string{remove}})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := removed.Services["api"]; exists {
		t.Fatal("null service override did not remove the service")
	}
	if _, exists := removed.ServiceMetadata["api"]; exists {
		t.Fatal("removed service retained stale identity metadata")
	}
}

func TestComposeStableIdentitySurvivesDisplayNameCollision(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "alpha", "kranz.yaml")
	second := filepath.Join(root, "beta", "kranz.yaml")
	writeCompositionFile(t, first, "project: alpha\nservices: {api: {command: alpha}}\n")

	before, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	stableID := before.ServiceMetadata["api"].ID
	writeCompositionFile(t, second, "project: beta\nservices: {api: {command: beta}}\n")
	after, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := after.ServiceMetadata["alpha/api"].ID; got != stableID {
		t.Fatalf("stable ID changed with display name: got %q want %q", got, stableID)
	}
	if after.ServiceMetadata["beta/api"].ID == stableID {
		t.Fatal("colliding services share a stable ID")
	}

	duplicateDir := filepath.Join(root, "group", "api")
	writeCompositionFile(t, filepath.Join(duplicateDir, "kranz.yaml"), "project: nested\nservices: {api: {command: nested}}\n")
	qualified, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range qualified.ServiceNames() {
		if strings.Contains(name, "api/api") {
			t.Fatalf("directory and service name were duplicated: %q", name)
		}
	}
}

func TestComposeChecksCyclesBeyondIncludeTruncation(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: root
include: [{path: child/kranz.yaml, max_depth: 0}]
services: {root: {command: root}}
`)
	writeCompositionFile(t, filepath.Join(root, "child", "kranz.yaml"), `project: child
include: [{path: grand/kranz.yaml}]
services: {child: {command: child}}
`)
	writeCompositionFile(t, filepath.Join(root, "child", "grand", "kranz.yaml"), `project: grand
include: [{path: ../kranz.yaml}]
services: {grand: {command: grand}}
`)
	if _, err := Compose(LoadOptions{Directory: root}); err == nil || !strings.Contains(err.Error(), "config_include_cycle") {
		t.Fatalf("cycle beyond truncation was not rejected: %v", err)
	}
}

func TestComposeProvenanceIsFieldScopedProtectedAndRedacted(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "kranz.yaml")
	override := filepath.Join(root, "local.yaml")
	writeCompositionFile(t, base, `project: root
overrides: [local.yaml]
protected:
  services:
    api:
      shell: /bin/protected
      env: {API_TOKEN: protected-secret, MODE: base}
      disabled: false
      tags: []
services:
  api:
    command: run
    description: base
    env: {API_TOKEN: base-secret, MODE: base}
    disabled: true
    tags: [base]
`)
	writeCompositionFile(t, override, `services:
  api:
    description: local
`)
	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	overrideFields := map[string]bool{}
	protected := map[string]FieldProvenance{}
	for _, entry := range cfg.Provenance {
		switch entry.Stage {
		case StageOverride:
			overrideFields[entry.FieldPath] = true
		case StageProtected:
			protected[entry.FieldPath] = entry
		}
		if strings.Contains(fmt.Sprint(entry.OriginalValue), "secret") || strings.Contains(fmt.Sprint(entry.EffectiveValue), "secret") {
			t.Fatalf("provenance leaked a secret: %#v", entry)
		}
	}
	if !overrideFields["services.api.description"] || overrideFields["services.api.command"] {
		t.Fatalf("override provenance is not field scoped: %v", overrideFields)
	}
	if cfg.Services["api"].Disabled || len(cfg.Services["api"].Tags) != 0 {
		t.Fatalf("protected zero/empty values were not applied: %#v", cfg.Services["api"])
	}
	if !protected["services.api.shell"].ProtectedRejection || !protected["services.api.env.API_TOKEN"].ProtectedRejection {
		t.Fatalf("changed protected fields were not explained: %#v", protected)
	}
	if protected["services.api.env.MODE"].ProtectedRejection {
		t.Fatalf("identical protected value produced a false rejection: %#v", protected["services.api.env.MODE"])
	}
}

func TestComposeOuterProtectedWinsOverNestedProtected(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: root
include: [{path: child/kranz.yaml}]
protected:
  services:
    api: {shell: /bin/outer}
services: {gateway: {command: gateway}}
`)
	writeCompositionFile(t, filepath.Join(root, "child", "kranz.yaml"), `project: child
protected:
  project: leaked
  ui: {theme: leaked}
  services:
    api: {shell: /bin/inner}
services: {api: {command: api}}
`)
	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["api"].Shell; got != "/bin/outer" {
		t.Fatalf("outer protected value lost: %q", got)
	}
	if cfg.Project != "root" || cfg.UI.Theme == "leaked" {
		t.Fatalf("nested protected metadata leaked into the root: project=%q ui=%#v", cfg.Project, cfg.UI)
	}
	var stages []string
	for _, entry := range cfg.Provenance {
		if entry.FieldPath == "services.api.shell" && entry.Stage == StageProtected {
			stages = append(stages, entry.ValueSourceID)
		}
	}
	if len(stages) != 2 || stages[0] == stages[1] {
		t.Fatalf("protected precedence chain = %v", stages)
	}
}

func TestComposeHandlesLargeDeterministicWorkspace(t *testing.T) {
	root := t.TempDir()
	for source := 0; source < 100; source++ {
		var body strings.Builder
		fmt.Fprintf(&body, "project: source-%03d\nservices:\n", source)
		for service := 0; service < 10; service++ {
			fmt.Fprintf(&body, "  service-%03d-%02d: {command: run}\n", source, service)
		}
		writeCompositionFile(t, filepath.Join(root, fmt.Sprintf("source-%03d", source), "kranz.yaml"), body.String())
	}
	first, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Services) != 1000 || !reflect.DeepEqual(first.ServiceNames(), second.ServiceNames()) {
		t.Fatalf("large composition is incomplete or nondeterministic: %d services", len(first.Services))
	}
}

func TestComposePreservesConventionalProcessComposeOverride(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "process-compose.yaml"), `name: Compatible
processes:
  api:
    command: run-api
    environment: {MODE: base}
`)
	writeCompositionFile(t, filepath.Join(root, "process-compose.override.yaml"), `processes:
  api:
    command: run-api --debug
    environment: {MODE: debug}
`)
	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services["api"].Command != "run-api --debug" || cfg.Services["api"].Env["MODE"] != "debug" {
		t.Fatalf("conventional override was not preserved: %#v", cfg.Services["api"])
	}
	foundOverride := false
	for _, source := range cfg.Sources {
		foundOverride = foundOverride || source.Kind == SourceOverride
	}
	if !foundOverride {
		t.Fatalf("conventional override source is missing: %#v", cfg.Sources)
	}
}

func TestComposeCacheReusesUnchangedSourcesAndInvalidatesDotenv(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "kranz.yaml")
	dotenv := filepath.Join(root, ".env")
	writeCompositionFile(t, path, "project: cache\nservices: {api: {command: run}}\n")
	writeCompositionFile(t, dotenv, "KRANZ_COMPOSITION_CACHE_TEST_VALUE=one\n")
	cache := NewSourceCache()
	options := LoadOptions{Directory: root, Cache: cache}
	first, err := Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	hits := cache.hits
	cache.mu.Unlock()
	if hits == 0 || first.Services["api"].Env["KRANZ_COMPOSITION_CACHE_TEST_VALUE"] != "one" || second.Services["api"].Env["KRANZ_COMPOSITION_CACHE_TEST_VALUE"] != "one" {
		t.Fatalf("unchanged source was not reused safely: hits=%d", hits)
	}
	writeCompositionFile(t, dotenv, "KRANZ_COMPOSITION_CACHE_TEST_VALUE=two\n")
	touch := time.Now().Add(time.Second)
	if err := os.Chtimes(dotenv, touch, touch); err != nil {
		t.Fatal(err)
	}
	third, err := Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	if got := third.Services["api"].Env["KRANZ_COMPOSITION_CACHE_TEST_VALUE"]; got != "two" {
		t.Fatalf("dotenv cache was not invalidated: %q", got)
	}
}
