package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Every composition list takes more than one entry: several include selectors
// of each kind, several file-local override layers, and several global
// override layers. Sources merge in declaration order and each later layer
// wins over the one before it.
func TestComposeAcceptsSeveralIncludesGlobsAndOverrides(t *testing.T) {
	root := t.TempDir()
	service := func(name string) string {
		return "services:\n  " + name + ":\n    command: sleep 60\n"
	}
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: multi
include:
  - path: a/kranz.yaml
  - path: b/kranz.yaml
  - glob: svc/*/kranz.yaml
  - glob: web/*/kranz.yaml
  - discover:
      root: repos
      max_depth: 3
  - discover:
      root: libs
overrides:
  - ov/first.yaml
  - ov/second.yaml
services:
  root:
    command: sleep 60
    env: {A: "0"}
`)
	writeCompositionFile(t, filepath.Join(root, "a", "kranz.yaml"), service("a-svc"))
	writeCompositionFile(t, filepath.Join(root, "b", "kranz.yaml"), service("b-svc"))
	writeCompositionFile(t, filepath.Join(root, "svc", "one", "kranz.yaml"), service("one"))
	writeCompositionFile(t, filepath.Join(root, "svc", "two", "kranz.yaml"), service("two"))
	writeCompositionFile(t, filepath.Join(root, "web", "x", "kranz.yaml"), service("wx"))
	writeCompositionFile(t, filepath.Join(root, "web", "y", "kranz.yaml"), service("wy"))
	writeCompositionFile(t, filepath.Join(root, "repos", "r1", "p", "kranz.yaml"), service("repo"))
	writeCompositionFile(t, filepath.Join(root, "libs", "l1", "kranz.yaml"), service("lib"))
	writeCompositionFile(t, filepath.Join(root, "ov", "first.yaml"), "services:\n  root:\n    env: {A: \"1\"}\n")
	writeCompositionFile(t, filepath.Join(root, "ov", "second.yaml"), "services:\n  root:\n    env: {A: \"2\", B: x}\n")
	writeCompositionFile(t, filepath.Join(root, "cli", "one.yaml"), "services:\n  one:\n    env: {CLI: \"1\"}\n")
	writeCompositionFile(t, filepath.Join(root, "cli", "two.yaml"), "services:\n  one:\n    env: {CLI: \"2\"}\n  two:\n    ports: [9000]\n")

	cfg, err := Compose(LoadOptions{Directory: root, Overrides: []string{"cli/one.yaml", "cli/two.yaml"}})
	if err != nil {
		t.Fatal(err)
	}

	type row struct {
		path string
		kind ConfigSourceKind
	}
	got := make([]row, 0, len(cfg.Sources))
	for _, source := range cfg.Sources {
		got = append(got, row{source.DisplayPath, source.Kind})
	}
	want := []row{
		{"kranz.yaml", SourceExplicit},
		{"ov/first.yaml", SourceOverride},
		{"ov/second.yaml", SourceOverride},
		{"a/kranz.yaml", SourceNestedInclude},
		{"b/kranz.yaml", SourceNestedInclude},
		{"svc/one/kranz.yaml", SourceGlob},
		{"svc/two/kranz.yaml", SourceGlob},
		{"web/x/kranz.yaml", SourceGlob},
		{"web/y/kranz.yaml", SourceGlob},
		{"repos/r1/p/kranz.yaml", SourceDiscovery},
		{"libs/l1/kranz.yaml", SourceDiscovery},
		{"cli/one.yaml", SourceOverride},
		{"cli/two.yaml", SourceOverride},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sources in merge order:\ngot  %v\nwant %v", got, want)
	}

	for _, name := range []string{"root", "a-svc", "b-svc", "one", "two", "wx", "wy", "repo", "lib"} {
		if _, ok := cfg.Services[name]; !ok {
			t.Errorf("service %q from a repeated selector is missing", name)
		}
	}
	if env := cfg.Services["root"].Env; env["A"] != "2" || env["B"] != "x" {
		t.Errorf("file-local override layers did not apply in order: root env = %v", env)
	}
	if env := cfg.Services["one"].Env; env["CLI"] != "2" {
		t.Errorf("global override layers did not apply in order: one env = %v", env)
	}
	if ports := cfg.Services["two"].Ports; !reflect.DeepEqual(ports, []int{9000}) {
		t.Errorf("second global layer lost its own field: two ports = %v", ports)
	}

	// Each layer is attributed to the layer it replaced, not only to the file
	// that first defined the value.
	replaced := map[string]string{}
	for _, contribution := range cfg.SourceMap().Contributions {
		for _, override := range contribution.Overrides {
			replaced[override.Field] = replaced[override.Field] + override.ReplacedSource + ";"
		}
	}
	for field, want := range map[string]string{
		"root.env.A":  "kranz.yaml;ov/first.yaml;",
		"one.env.CLI": ";cli/one.yaml;",
	} {
		if got := replaced[field]; !sameEntries(got, want) {
			t.Errorf("%s replaced %q, want %q", field, got, want)
		}
	}
}

// A file matched by an exact path and again by a later glob is loaded once,
// under the selector that reached it first, so its services do not collide.
func TestComposeLoadsAFileSelectedTwiceOnce(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: dup
include:
  - path: svc/one/kranz.yaml
  - glob: svc/*/kranz.yaml
services:
  root: {command: sleep 60}
`)
	writeCompositionFile(t, filepath.Join(root, "svc", "one", "kranz.yaml"), "services:\n  one: {command: sleep 60}\n")
	writeCompositionFile(t, filepath.Join(root, "svc", "two", "kranz.yaml"), "services:\n  two: {command: sleep 60}\n")

	cfg, err := Compose(LoadOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]ConfigSourceKind{}
	for _, source := range cfg.Sources {
		if _, seen := kinds[source.DisplayPath]; seen {
			t.Fatalf("%s was loaded twice: %+v", source.DisplayPath, cfg.Sources)
		}
		kinds[source.DisplayPath] = source.Kind
	}
	if kinds["svc/one/kranz.yaml"] != SourceNestedInclude || kinds["svc/two/kranz.yaml"] != SourceGlob {
		t.Fatalf("selector attribution = %v", kinds)
	}
	if len(cfg.Services) != 3 {
		t.Fatalf("services = %v", cfg.Services)
	}
}

// Several selectors are several list entries; one entry naming two selectors
// is ambiguous and rejected.
func TestComposeRejectsTwoSelectorsInOneIncludeEntry(t *testing.T) {
	root := t.TempDir()
	writeCompositionFile(t, filepath.Join(root, "kranz.yaml"), `project: both
include:
  - path: a/kranz.yaml
    glob: a/*.yaml
services:
  root: {command: sleep 60}
`)
	writeCompositionFile(t, filepath.Join(root, "a", "kranz.yaml"), "services:\n  a: {command: sleep 60}\n")
	if _, err := Compose(LoadOptions{Directory: root}); err == nil || !strings.Contains(err.Error(), "exactly one of path, glob, or discover") {
		t.Fatalf("two selectors in one entry: err = %v", err)
	}
}

// sameEntries compares two ";"-terminated lists regardless of order.
func sameEntries(got, want string) bool {
	split := func(value string) map[string]int {
		counts := map[string]int{}
		for _, entry := range strings.SplitAfter(value, ";") {
			if entry != "" {
				counts[entry]++
			}
		}
		return counts
	}
	return reflect.DeepEqual(split(got), split(want))
}
