package config

import (
	"errors"
	"fmt"
	"io/fs"
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

func TestComposeExpandsTruncatedSubtreeFromLaterEdge(t *testing.T) {
	// B is reached first through an edge with max_depth: 0, so its own services
	// are exported but its include of C is cut off. A reaches B again with no
	// budget, and must expand the subtree the first edge could not. The reverse
	// declaration order must reach the same service set, because include order
	// must not change whether a file is considered fully processed.
	orders := []struct {
		name     string
		includes string
	}{
		{"truncated-first", "  - {path: b/kranz.yaml, max_depth: 0}\n  - {path: a/kranz.yaml}\n"},
		{"expanded-first", "  - {path: a/kranz.yaml}\n  - {path: b/kranz.yaml, max_depth: 0}\n"},
	}
	var sets [2]map[string]bool
	for index, order := range orders {
		root := t.TempDir()
		writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), "project: root\ninclude:\n"+order.includes+"services: {root: {command: root}}\n")
		writeCompositionFile(t, filepath.Join(root, "a", "kranz.yaml"), "project: a\ninclude: [{path: ../b/kranz.yaml}]\nservices: {a: {command: a}}\n")
		writeCompositionFile(t, filepath.Join(root, "b", "kranz.yaml"), "project: b\ninclude: [{path: c/kranz.yaml}]\nservices: {b: {command: b}}\n")
		writeCompositionFile(t, filepath.Join(root, "b", "c", "kranz.yaml"), "project: c\nservices: {c: {command: c}}\n")

		cfg, err := Compose(LoadOptions{Directory: root, Sources: []string{filepath.Join(root, "kranz.yaml")}})
		if err != nil {
			t.Fatalf("%s: %v", order.name, err)
		}
		if len(cfg.ServiceOrder) != len(cfg.Services) {
			t.Fatalf("%s: duplicate display names: %v", order.name, cfg.ServiceOrder)
		}
		names := make(map[string]bool, len(cfg.Services))
		for _, name := range cfg.ServiceNames() {
			names[name] = true
		}
		if !names["c"] {
			t.Fatalf("%s: truncated subtree of b was not expanded through a: %v", order.name, cfg.ServiceNames())
		}
		sets[index] = names
	}
	if !reflect.DeepEqual(sets[0], sets[1]) {
		t.Fatalf("include order changed the effective services: %v vs %v", sets[0], sets[1])
	}
}

func TestComposeKeepsTruncationDistinctFromDeduplication(t *testing.T) {
	// Both edges to b carry max_depth: 0, so b can never expand. The result
	// must report truncation, not an ordinary deduplication that would imply
	// the file's subtree had been processed.
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), "project: root\ninclude:\n  - {path: b/kranz.yaml, max_depth: 0}\n  - {path: a/kranz.yaml, max_depth: 0}\nservices: {root: {command: root}}\n")
	writeCompositionFile(t, filepath.Join(root, "a", "kranz.yaml"), "project: a\ninclude: [{path: ../b/kranz.yaml}]\nservices: {a: {command: a}}\n")
	writeCompositionFile(t, filepath.Join(root, "b", "kranz.yaml"), "project: b\ninclude: [{path: c/kranz.yaml}]\nservices: {b: {command: b}}\n")
	writeCompositionFile(t, filepath.Join(root, "b", "c", "kranz.yaml"), "project: c\nservices: {c: {command: c}}\n")
	cfg, err := Compose(LoadOptions{Directory: root, Sources: []string{filepath.Join(root, "kranz.yaml")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := cfg.Services["c"]; found {
		t.Fatal("a budgetless graph must not export the truncated subtree")
	}
	truncated, deduplicated := false, false
	for _, diagnostic := range cfg.CompositionDiagnostics {
		switch diagnostic.Code {
		case "include_depth_truncated":
			truncated = true
		case "source_deduplicated":
			deduplicated = true
		}
	}
	if !truncated {
		t.Fatalf("missing truncation diagnostic: %#v", cfg.CompositionDiagnostics)
	}
	if deduplicated {
		t.Fatalf("truncation was masked as deduplication: %#v", cfg.CompositionDiagnostics)
	}
}

func TestComposeDisplayNamesDoNotDependOnAbsoluteDirectory(t *testing.T) {
	// Two colliding services live directly in the composition root. Their
	// relative directory is ".", which contributes no prefix, so the allocator
	// must fall back to a directory-independent suffix instead of the on-disk
	// base name of the root. The same structure must name services identically
	// wherever it is placed.
	load := func(t *testing.T) ([]string, string) {
		t.Helper()
		root := t.TempDir()
		writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), "project: p\ninclude:\n  - {path: one.yaml}\n  - {path: two.yaml}\n")
		writeCompositionFile(t, filepath.Join(root, "one.yaml"), "services: {api: {command: one}}\n")
		writeCompositionFile(t, filepath.Join(root, "two.yaml"), "services: {api: {command: two}}\n")
		cfg, err := Compose(LoadOptions{Directory: root, Sources: []string{filepath.Join(root, "kranz.yaml")}})
		if err != nil {
			t.Fatal(err)
		}
		return cfg.ServiceNames(), filepath.Base(root)
	}
	first, firstName := load(t)
	second, secondName := load(t)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("display names depend on the absolute directory: %v vs %v", first, second)
	}
	for _, name := range first {
		if strings.Contains(name, firstName) || strings.Contains(name, secondName) {
			t.Fatalf("display name leaked the on-disk directory name: %v", first)
		}
	}
}

func TestComposeRejectsNegativeDiscoverMaxDepth(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), "project: p\ninclude:\n  - discover: {root: ., max_depth: -1}\nservices: {root: {command: root}}\n")
	_, err := Compose(LoadOptions{Directory: root, Sources: []string{filepath.Join(root, "kranz.yaml")}})
	if err == nil || !strings.Contains(err.Error(), "discover.max_depth cannot be negative") {
		t.Fatalf("negative discover.max_depth error = %v", err)
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

func TestComposeAcceptsProcfileAndProcessComposeAsTerminalLeaves(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: Mixed
include:
  - path: proc/Procfile
  - path: compatible/process-compose.yaml
services:
  root: {command: run-root}
`)
	writeCompositionFile(t, filepath.Join(root, "proc", "Procfile"), "web: run-web\n")
	writeCompositionFile(t, filepath.Join(root, "compatible", "process-compose.yaml"), `name: Compatible
processes:
  worker:
    command: run-worker
    working_dir: jobs
`)

	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root", "web", "worker"} {
		if _, ok := cfg.Services[name]; !ok {
			t.Errorf("service %q from mixed-format composition is missing", name)
		}
	}
	if got := cfg.Services["web"].Dir; got != filepath.Join(canonicalRoot, "proc") {
		t.Errorf("Procfile service dir = %q", got)
	}
	if got := cfg.Services["worker"].Dir; got != filepath.Join(canonicalRoot, "compatible", "jobs") {
		t.Errorf("process-compose service dir = %q", got)
	}
	sourceIDs := map[string]string{}
	for _, source := range cfg.Sources {
		if source.DisplayPath == "proc/Procfile" {
			sourceIDs["procfile"] = source.ID
		}
		if source.DisplayPath == "compatible/process-compose.yaml" {
			sourceIDs["process-compose"] = source.ID
		}
	}
	if cfg.ServiceMetadata["web"].SourceID != sourceIDs["procfile"] || cfg.ServiceMetadata["worker"].SourceID != sourceIDs["process-compose"] {
		t.Fatalf("leaf source attribution: sources=%v metadata=%v", sourceIDs, cfg.ServiceMetadata)
	}
}

func TestProcessComposeLeafRejectsNativeCompositionDirectives(t *testing.T) {
	root := t.TempDir()
	for _, field := range []string{"include", "overrides"} {
		body := "processes:\n  worker: {command: run}\n" + field + ":\n  - child.yaml\n"
		path := filepath.Join(root, "process-compose.yaml")
		writeCompositionFile(t, path, body)
		_, err := Compose(LoadOptions{Directory: root, Sources: []string{path}})
		if err == nil || !strings.Contains(err.Error(), "terminal composition leaf") || !strings.Contains(err.Error(), field) {
			t.Fatalf("%s directive: err = %v", field, err)
		}
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

func TestComposeDisplayNamesStayGloballyUnique(t *testing.T) {
	// A service literally named "alpha/api" and the qualified name derived for
	// `api` under repos/alpha are the same string. The allocator must widen one
	// of them, or two services would share a display name and the ordered
	// ServiceOrder would hold duplicates.
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: unique
include:
  - glob: repos/*/kranz.yaml
services:
  alpha/api:
    command: literal
`)
	writeCompositionFile(t, filepath.Join(root, "repos", "alpha", "kranz.yaml"), `services: {api: {command: served}}
`)
	writeCompositionFile(t, filepath.Join(root, "repos", "beta", "kranz.yaml"), `services: {api: {command: served}}
`)
	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ServiceOrder) != len(cfg.Services) {
		t.Fatalf("ServiceOrder has duplicates: order=%v services=%d", cfg.ServiceOrder, len(cfg.Services))
	}
	seen := make(map[string]bool, len(cfg.ServiceOrder))
	for _, name := range cfg.ServiceOrder {
		if seen[name] {
			t.Fatalf("duplicate display name %q in %v", name, cfg.ServiceOrder)
		}
		seen[name] = true
	}
	if !seen["alpha/api"] {
		t.Fatalf("literal service name was not preserved: %v", cfg.ServiceOrder)
	}
}

func TestComposeOverrideExpandsDotenvWithSameFallbackAsBase(t *testing.T) {
	// The base file and its override must expand ${VAR} identically: process
	// environment first, then the .env beside the file being read. A bare
	// os.ExpandEnv in the override path would silently turn a dotenv-only
	// variable into an empty string.
	root := t.TempDir()
	base := filepath.Join(root, "kranz.yaml")
	writeCompositionFile(t, base, "project: p\nservices:\n  api:\n    command: run\n    env: {MODE: base, HOST_VALUE: base}\n")
	patchDir := filepath.Join(root, "patches")
	writeCompositionFile(t, filepath.Join(patchDir, "local.yaml"), "services:\n  api:\n    env: {MODE: ${PATCH_ONLY}, HOST_VALUE: ${PATCH_HOST_ONLY}}\n")
	writeCompositionFile(t, filepath.Join(patchDir, ".env"), "PATCH_ONLY=from-patch-dotenv\nPATCH_HOST_ONLY=from-dotenv\n")
	t.Setenv("PATCH_HOST_ONLY", "from-host")

	cfg, err := Compose(LoadOptions{Directory: root, Sources: []string{base}, Overrides: []string{"patches/local.yaml"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["api"].Env["MODE"]; got != "from-patch-dotenv" {
		t.Fatalf("override dotenv fallback = %q, want from-patch-dotenv", got)
	}
	if got := cfg.Services["api"].Env["HOST_VALUE"]; got != "from-host" {
		t.Fatalf("process environment did not win over dotenv: %q", got)
	}
}

func TestComposeErrorsRedactPathsOutsideCompositionRoot(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(canonicalRoot)
	source := filepath.Join(root, "kranz.yaml")

	// A discovery root outside the composition root is a legitimate ../shared
	// reference, but the diagnostic must not print the absolute path.
	writeCompositionFile(t, source, "project: p\ninclude:\n  - discover: {root: ../kranz-missing-discovery}\nservices: {root: {command: root}}\n")
	_, err = Compose(LoadOptions{Directory: root, Sources: []string{source}})
	if err == nil {
		t.Fatal("expected an error for a missing discovery root")
	}
	if strings.Contains(err.Error(), parent) {
		t.Fatalf("discovery error leaked an absolute path: %v", err)
	}
	if !strings.Contains(err.Error(), "../kranz-missing-discovery") {
		t.Fatalf("discovery error lost the relative path: %v", err)
	}

	// A missing explicit include outside the root takes the same treatment.
	writeCompositionFile(t, source, "project: p\ninclude:\n  - path: ../kranz-missing-explicit.yaml\nservices: {root: {command: root}}\n")
	_, err = Compose(LoadOptions{Directory: root, Sources: []string{source}})
	if err == nil {
		t.Fatal("expected an error for a missing include")
	}
	if strings.Contains(err.Error(), parent) {
		t.Fatalf("include error leaked an absolute path: %v", err)
	}
	if !strings.Contains(err.Error(), "../kranz-missing-explicit.yaml") {
		t.Fatalf("include error lost the relative path: %v", err)
	}
}

func TestComposeProtectedRemapsNestedReferencesUnderCollision(t *testing.T) {
	// The root and the child both declare db, so the allocator renames them to
	// db@1 and child/db. A protected layer in the child that references db, and
	// its action group, must be rewritten with the same allocator mapping or the
	// effective graph would gain a dangling reference and a duplicate group.
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: root
include: [{path: child/kranz.yaml}]
action_groups:
  ops: {actions: {deploy: {command: root-deploy}}}
services:
  db: {command: root-db}
`)
	writeCompositionFile(t, filepath.Join(root, "child", "kranz.yaml"), `project: child
protected:
  services:
    api:
      depends_on: [db]
  action_groups:
    ops:
      description: protected-child
services:
  api: {command: api}
  db: {command: child-db}
action_groups:
  ops: {actions: {deploy: {command: child-deploy}}}
`)
	cfg, err := Compose(LoadOptions{Directory: root, Sources: []string{filepath.Join(root, "kranz.yaml")}})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["api"].DependsOn; !reflect.DeepEqual(got, []string{"child/db"}) {
		t.Fatalf("protected dependency was not remapped: %v", got)
	}
	if _, duplicate := cfg.ActionGroups["ops"]; duplicate {
		t.Fatalf("protected action group reintroduced the raw name: %v", cfg.ActionGroups)
	}
	if got := cfg.ActionGroups["child/ops"].Description; got != "protected-child" {
		t.Fatalf("protected action group was not applied to the qualified group: %#v", cfg.ActionGroups)
	}
}

// TestComposeErrConfigNotFoundSurvivesPathSanitization guards the fallback
// routing in the CLI and TUI: an empty discovery must stay identifiable with
// errors.Is after Compose rewrites the message, while a discovered but invalid
// file must not be reported as "not found" (PRD 6.3).
func TestComposeErrConfigNotFoundSurvivesPathSanitization(t *testing.T) {
	root := t.TempDir()
	_, err := Compose(LoadOptions{Directory: root})
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("empty discovery error = %v, want ErrConfigNotFound", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("not-found error leaked the absolute root: %v", err)
	}
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), "not: [valid")
	_, err = Compose(LoadOptions{Directory: root})
	if err == nil || errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("invalid discovered config = %v, want a distinct error", err)
	}
}

// TestComposeIncludeMatchesAreCachedWithinBuild proves the boundary cycle pass
// cannot repeat a discovery the export pass already resolved. After the first
// resolution the discovered directory is removed; a cached second resolution
// still returns the same matches instead of walking (and failing on) the
// missing directory.
func TestComposeIncludeMatchesAreCachedWithinBuild(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "repos", "alpha", "kranz.yaml"), "project: alpha\nservices: {alpha: {command: run}}\n")
	composer := newComposer(root, false, NewSourceCache())
	spec := IncludeSpec{Discover: &DiscoverySpec{Root: "repos"}}
	first, kind, discovery, resolvedRoot, err := composer.includeMatches(spec, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || kind != SourceDiscovery || discovery == nil || resolvedRoot == "" {
		t.Fatalf("first resolution = %#v kind=%q root=%q", first, kind, resolvedRoot)
	}
	if err := os.RemoveAll(filepath.Join(root, "repos")); err != nil {
		t.Fatal(err)
	}
	second, _, _, _, err := composer.includeMatches(spec, root)
	if err != nil {
		t.Fatalf("cached resolution walked the removed directory: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("cached resolution = %#v, want %#v", second, first)
	}
}

// TestComposeChecksCyclesThroughTruncatedDiscovery keeps cycle detection
// independent of include.max_depth: a cycle that runs through a discovery node
// the depth limit stopped exporting must still abort the build.
func TestComposeChecksCyclesThroughTruncatedDiscovery(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: root
include:
  - discover: {root: repos}
    max_depth: 0
services: {root: {command: root}}
`)
	writeCompositionFile(t, filepath.Join(root, "repos", "child", "kranz.yaml"), `project: child
include: [{path: ../../kranz.yaml}]
services: {child: {command: child}}
`)
	if _, err := Compose(LoadOptions{Directory: root}); err == nil || !strings.Contains(err.Error(), "config_include_cycle") {
		t.Fatalf("cycle through a truncated discovery node was not rejected: %v", err)
	}
}

// TestComposeProtectedDoesNotCarryDefaults pins the chosen semantics: a
// protected layer pins service values, so a defaults section there is dropped
// rather than copied into the effective config where no service would ever see
// it (defaults are applied before protected runs).
func TestComposeProtectedDoesNotCarryDefaults(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: root
protected:
  defaults:
    env: {LEAK: "yes"}
  services:
    api:
      shell: /bin/protected
services:
  api: {command: run}
`)
	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services["api"].Shell != "/bin/protected" {
		t.Fatalf("protected service value was not applied: %#v", cfg.Services["api"])
	}
	if _, leaked := cfg.Services["api"].Env["LEAK"]; leaked {
		t.Fatalf("protected defaults leaked into a service: %#v", cfg.Services["api"].Env)
	}
	if _, leaked := cfg.Defaults.Env["LEAK"]; leaked {
		t.Fatalf("protected defaults were carried into the effective config: %#v", cfg.Defaults.Env)
	}
}

// TestComposeWatchesPatchDotenvFiles guards live reload: expandPatchEnvironment
// reads .env beside a patch, so that file is a real input and must be watched
// for both source-local and external override layers.
func TestComposeWatchesPatchDotenvFiles(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "kranz.yaml")
	writeCompositionFile(t, base, `project: p
overrides: [patches/local.yaml]
services: {api: {command: run}}
`)
	writeCompositionFile(t, filepath.Join(root, "patches", "local.yaml"), "services: {api: {description: local}}\n")
	writeCompositionFile(t, filepath.Join(root, "external", "layer.yaml"), "services: {api: {shell: /bin/sh}}\n")

	cfg, err := Compose(LoadOptions{Directory: root, Sources: []string{"kranz.yaml"}, Overrides: []string{"external/layer.yaml"}})
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(canonicalRoot, "patches", ".env"),
		filepath.Join(canonicalRoot, "external", ".env"),
	} {
		if !containsString(cfg.WatchPaths, path) {
			t.Fatalf("patch dotenv %q is not watched: %#v", path, cfg.WatchPaths)
		}
	}
}

// TestComposePatchDotenvErrorsNameTheFile pins the diagnostic for a malformed
// patch dotenv: the message must name the offending file while staying redacted
// to the composition-relative path.
func TestComposePatchDotenvErrorsNameTheFile(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "kranz.yaml")
	writeCompositionFile(t, base, "project: p\nservices: {api: {command: run}}\n")
	writeCompositionFile(t, filepath.Join(root, "patches", "local.yaml"), "services: {api: {env: {MODE: ${PATCH_ONLY}}}}\n")
	writeCompositionFile(t, filepath.Join(root, "patches", ".env"), "BROKEN_LINE_WITHOUT_EQUALS\n")

	_, err := Compose(LoadOptions{Directory: root, Sources: []string{base}, Overrides: []string{"patches/local.yaml"}})
	if err == nil {
		t.Fatal("expected an error for an invalid patch dotenv")
	}
	if !strings.Contains(err.Error(), "patches/.env") {
		t.Fatalf("patch dotenv error lost the file name: %v", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("patch dotenv error leaked the absolute root: %v", err)
	}
}

// TestComposeSanitizedPathErrorsKeepTheirCause proves path redaction is not
// allowed to flatten the error chain: errors.As must still reach the original
// *fs.PathError, and the message stays relative to the composition root.
func TestComposeSanitizedPathErrorsKeepTheirCause(t *testing.T) {
	root := t.TempDir()
	_, err := Compose(LoadOptions{Directory: root, Sources: []string{"missing.yaml"}})
	if err == nil {
		t.Fatal("expected an error for a missing explicit source")
	}
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("sanitized error lost its *fs.PathError cause: %v", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("sanitized error leaked the absolute root: %v", err)
	}
}

// TestComposeReportsSilentServiceRename covers a unique raw name that the
// global allocator renames only because it collides with a qualified name from
// another source. Without a diagnostic the effective graph silently drops the
// name the author wrote.
func TestComposeReportsSilentServiceRename(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: literal
include:
  - path: repos/alpha/kranz.yaml
  - path: repos/beta/kranz.yaml
  - path: literal/kranz.yaml
`)
	writeCompositionFile(t, filepath.Join(root, "repos", "alpha", "kranz.yaml"), "services: {api: {command: alpha}}\n")
	writeCompositionFile(t, filepath.Join(root, "repos", "beta", "kranz.yaml"), "services: {api: {command: beta}}\n")
	// The literal name equals the qualified name the allocator gives alpha's api.
	writeCompositionFile(t, filepath.Join(root, "literal", "kranz.yaml"), "services: {\"alpha/api\": {command: literal}}\n")

	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	display := ""
	for name, metadata := range cfg.ServiceMetadata {
		if metadata.SourceName == "alpha/api" {
			display = name
		}
	}
	if display == "" || display == "alpha/api" {
		t.Fatalf("literal service was not qualified: %q", display)
	}
	found := false
	for _, diagnostic := range cfg.CompositionDiagnostics {
		if diagnostic.Code == "display_name_qualified" && strings.Contains(diagnostic.Message, "alpha/api -> "+display) {
			found = true
		}
	}
	if !found {
		t.Fatalf("silent rename %q -> %q has no diagnostic: %#v", "alpha/api", display, cfg.CompositionDiagnostics)
	}
}

// TestComposeProtectedProvenanceSurvivesRename pins that a protected service
// whose raw name is qualified by the allocator still records StageProtected
// provenance against the effective display name.
func TestComposeProtectedProvenanceSurvivesRename(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: root
include: [{path: child/kranz.yaml}]
services: {db: {command: root-db}}
`)
	writeCompositionFile(t, filepath.Join(root, "child", "kranz.yaml"), `project: child
protected:
  services:
    db: {shell: /bin/protected}
services:
  api: {command: api}
  db: {command: child-db, shell: /bin/child}
`)
	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	display := ""
	for name, metadata := range cfg.ServiceMetadata {
		if metadata.SourceName == "db" && strings.HasPrefix(name, "child/") {
			display = name
		}
	}
	if display == "" {
		t.Fatalf("child db was not qualified: %#v", cfg.ServiceMetadata)
	}
	found := false
	for _, entry := range cfg.Provenance {
		if entry.FieldPath == "services."+display+".shell" && entry.Stage == StageProtected {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing protected provenance for renamed service %q: %#v", display, cfg.Provenance)
	}
	if cfg.Services[display].Shell != "/bin/protected" {
		t.Fatalf("protected value was not applied to %q: %#v", display, cfg.Services[display])
	}
}
