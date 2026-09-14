package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The composition catalog under examples/composition is documentation that must
// keep working. These tests load it the same way the CLI and runtime do, so a
// change to composition semantics cannot silently invalidate the examples.
const compositionExamples = "../../examples/composition"

func composeExample(t *testing.T, name string, options LoadOptions) *Config {
	t.Helper()
	options.Directory = filepath.Join(compositionExamples, name)
	cfg, err := Compose(options)
	if err != nil {
		t.Fatalf("compose %s: %v", name, err)
	}
	return cfg
}

func requireUniqueOrder(t *testing.T, cfg *Config) {
	t.Helper()
	if len(cfg.ServiceOrder) != len(cfg.Services) {
		t.Fatalf("service order %v does not match %d services", cfg.ServiceOrder, len(cfg.Services))
	}
	seen := make(map[string]bool, len(cfg.ServiceOrder))
	for _, name := range cfg.ServiceOrder {
		if seen[name] {
			t.Fatalf("duplicate display name %q in %v", name, cfg.ServiceOrder)
		}
		seen[name] = true
	}
}

func TestCompositionCatalogRootExample(t *testing.T) {
	// The catalog directory has its own root so a bare run from inside it does
	// not fall through to discovery and pick up the broken negative configs.
	cfg := composeExample(t, ".", LoadOptions{})
	if cfg.Project != "Composition Catalog" {
		t.Fatalf("catalog project = %q", cfg.Project)
	}
	requireUniqueOrder(t, cfg)
	if len(cfg.Services) != 12 {
		t.Fatalf("catalog services = %d, want 12", len(cfg.Services))
	}
}

func TestCompositionWorkspaceExample(t *testing.T) {
	cfg := composeExample(t, "workspace", LoadOptions{})
	requireUniqueOrder(t, cfg)
	workspaceRoot, err := filepath.Abs(filepath.Join(compositionExamples, "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot, err = filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Services) != 12 {
		t.Fatalf("services = %d, want 12: %v", len(cfg.Services), cfg.ServiceNames())
	}
	if got := cfg.Services["proc-web"].Dir; got != filepath.Join(workspaceRoot, "leaves", "procfile") {
		t.Fatalf("Procfile leaf dir = %q", got)
	}
	if got := cfg.Services["compose-worker"].Dir; got != filepath.Join(workspaceRoot, "leaves", "process-compose", "jobs") {
		t.Fatalf("process-compose leaf dir = %q", got)
	}

	// Outer protected beats the nested protected declared by checkout.
	if got := cfg.Services["checkout/api"].Env["TLS_MODE"]; got != "required" {
		t.Fatalf("root protected lost: TLS_MODE=%q", got)
	}
	// The catalog's own override layer applied before export.
	catalog := cfg.Services["catalog/api"]
	if catalog.Env["FEATURE_FLAG"] != "catalog-local" || len(catalog.Ports) != 1 || catalog.Ports[0] != 18121 {
		t.Fatalf("catalog local override = %#v", catalog)
	}
	// Root-local override and dotenv apply to the root service only.
	if got := cfg.Services["root-task"].Env["ROOT_LOCAL_OVERRIDE"]; got != "applied" {
		t.Fatalf("root local override missing: %q", got)
	}
	if _, present := cfg.Services["root-task"].Env["ROOT_TASK_ONLY"]; present {
		t.Fatal("null override did not delete ROOT_TASK_ONLY")
	}
	if _, leaked := cfg.Services["root-task"].Env["CATALOG_SCOPE"]; leaked {
		t.Fatal("catalog defaults leaked into the root source")
	}
	if _, leaked := cfg.Services["services/worker"].Env["CATALOG_SCOPE"]; leaked {
		t.Fatal("catalog defaults leaked into a sibling source")
	}
	if _, leaked := cfg.Services["catalog/api"].Env["WORKER_LOCAL"]; leaked {
		t.Fatal("worker defaults leaked into the catalog source")
	}
	// A short dependency resolves within its declaring source before global
	// lookup, while a root-level reference can name a qualified effective
	// service explicitly.
	if got := cfg.Services["checkout/worker"].DependsOn; len(got) != 1 || got[0] != "checkout/api" {
		t.Fatalf("checkout local dependency = %v", got)
	}
	if got := cfg.Services["root-task"].DependsOn; len(got) != 1 || got[0] != "checkout/api" {
		t.Fatalf("root qualified dependency = %v", got)
	}
	if got := cfg.Services["services/worker"].DependsOn; len(got) != 1 || got[0] != "log-collector" {
		t.Fatalf("unique cross-source dependency = %v", got)
	}
	// The diamond shared by platform and services/worker was deduplicated.
	foundDedupe := false
	for _, diagnostic := range cfg.CompositionDiagnostics {
		foundDedupe = foundDedupe || diagnostic.Code == "source_deduplicated"
	}
	if !foundDedupe {
		t.Fatalf("missing dedupe diagnostic: %#v", cfg.CompositionDiagnostics)
	}
}

func TestCompositionWorkspaceOverridesExample(t *testing.T) {
	cfg := composeExample(t, "workspace", LoadOptions{Overrides: []string{
		"overrides/10-ports.yaml",
		"overrides/20-debug.yaml",
	}})
	catalog := cfg.Services["catalog/api"]
	if catalog.Env["FEATURE_FLAG"] != "beta" || catalog.Env["DEBUG"] != "1" {
		t.Fatalf("ordered overrides = %#v", catalog.Env)
	}
	if len(catalog.Ports) != 1 || catalog.Ports[0] != 18111 {
		t.Fatalf("override sequence did not replace: %v", catalog.Ports)
	}
	if _, present := cfg.Services["root-task"].Env["ROOT_LOCAL_OVERRIDE"]; present {
		t.Fatal("second override layer did not delete the earlier value")
	}
}

func TestCompositionGraphDepthExample(t *testing.T) {
	cfg := composeExample(t, "graph-depth", LoadOptions{})
	if _, present := cfg.Services["level3-service"]; present {
		t.Fatal("include.max_depth did not truncate level3")
	}
	if _, present := cfg.Services["level2-service"]; !present {
		t.Fatal("include.max_depth truncated one level too early")
	}
	found := false
	for _, diagnostic := range cfg.CompositionDiagnostics {
		if diagnostic.Code == "include_depth_truncated" && diagnostic.Message == "level1/level2/kranz.yaml" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing truncation diagnostic: %#v", cfg.CompositionDiagnostics)
	}
}

func TestCompositionVirtualRootExample(t *testing.T) {
	cfg := composeExample(t, "virtual-root", LoadOptions{})
	if cfg.Project != "virtual-root" {
		t.Fatalf("virtual project name = %q", cfg.Project)
	}
	if len(cfg.Services) != 2 {
		t.Fatalf("virtual root services = %d, want 2", len(cfg.Services))
	}
	hasVirtual := false
	for _, source := range cfg.Sources {
		hasVirtual = hasVirtual || source.Kind == SourceVirtualRoot
	}
	if !hasVirtual {
		t.Fatalf("virtual root source missing: %#v", cfg.Sources)
	}
}

func TestCompositionSymlinkExample(t *testing.T) {
	if _, err := os.Lstat(filepath.Join(compositionExamples, "symlinks", "explicit-link.yaml")); err != nil {
		t.Skipf("example symlink unavailable: %v", err)
	}
	defaultCfg := composeExample(t, "symlinks", LoadOptions{})
	if len(defaultCfg.Services) != 2 {
		t.Fatalf("default symlink services = %d, want 2", len(defaultCfg.Services))
	}
	for _, diagnostic := range defaultCfg.CompositionDiagnostics {
		if diagnostic.Code == "source_deduplicated" {
			t.Fatal("default discovery followed a symlink")
		}
	}
	followed := composeExample(t, "symlinks", LoadOptions{FollowSymlinks: true})
	if len(followed.Services) != 2 {
		t.Fatalf("opt-in symlink services = %d, want 2", len(followed.Services))
	}
	found := false
	for _, diagnostic := range followed.CompositionDiagnostics {
		found = found || diagnostic.Code == "source_deduplicated"
	}
	if !found {
		t.Fatalf("opt-in discovery did not deduplicate the linked file: %#v", followed.CompositionDiagnostics)
	}
}

func TestCompositionNegativeExamples(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"cycle", "config_include_cycle"},
		{"empty-glob", "config_glob_empty"},
		{"remote", "remote config sources are not supported"},
		{"missing-explicit", "explicit config"},
		{"type-mismatch", "incompatible"},
		{"forbidden-override", "cannot declare defaults"},
		{"ambiguous-dependency", "reference \"api\" is ambiguous"},
	}
	for _, testCase := range cases {
		_, err := Compose(LoadOptions{Directory: filepath.Join(compositionExamples, "negatives", testCase.name)})
		if err == nil {
			t.Fatalf("%s: expected an error", testCase.name)
		}
		if !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s: error %q does not contain %q", testCase.name, err, testCase.want)
		}
		if home := os.Getenv("HOME"); home != "" && strings.Contains(err.Error(), home) {
			t.Fatalf("%s: error leaked an absolute home path: %v", testCase.name, err)
		}
	}
}
