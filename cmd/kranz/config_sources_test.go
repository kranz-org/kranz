package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

// configSourcesProject composes a root file with one included file, so the
// command has a hierarchy, per-source services, and an override to report.
// Paths are fictional and relative, per the repository's privacy rules.
func configSourcesProject(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	root := `project: Sources
include:
  - path: services/api/kranz.yaml
services:
  root:
    command: sleep 60
    depends_on: [root-db]
  root-db:
    command: sleep 60
    ports: [5432]
`
	included := `project: API
services:
  api:
    command: sleep 60
    ports: [8080]
    actions:
      migrate:
        command: echo migrate
`
	if err := os.MkdirAll(filepath.Join(directory, "services", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(root), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "services", "api", "kranz.yaml"), []byte(included), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

// The command is a config-to-service map, not a second dump of the effective
// configuration: it names the files, the services each defined, and the fields
// each overrode, and omits service detail such as ports or actions.
func TestConfigSourcesListsFilesInResolutionOrderWithContributions(t *testing.T) {
	output := runInspection(t, configSourcesProject(t), "config", "sources")

	for _, expected := range []string{
		"2 sources · 3 services · 0 overrides · resolved order; field writes shown below",
		"● kranz.yaml  (explicit)",
		"│   services: root, root-db",
		// The include hierarchy is a tree, not a flat indent, and the row order
		// is the resolution order, so rows are not numbered. An include is not
		// tagged, and a kranz.yaml base name is not repeated.
		"└─● services/api\n",
		"      services: api",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("config sources output is missing %q:\n%s", expected, output)
		}
	}
	for _, unexpected := range []string{"ports:", "5432", "8080", "depends:", "actions:", "→"} {
		if strings.Contains(output, unexpected) {
			t.Errorf("config sources still shows service detail %q:\n%s", unexpected, output)
		}
	}

	rootOrder := strings.Index(output, "kranz.yaml")
	includedOrder := strings.Index(output, "services/api")
	if rootOrder < 0 || includedOrder < 0 || includedOrder < rootOrder {
		t.Fatalf("resolution order is not top to bottom: root=%d included=%d", rootOrder, includedOrder)
	}
}

// An override layer that names the file it replaced is the whole reason the
// command exists: "who won, and over whom".
func TestConfigSourcesNamesTheSourceAnOverrideReplaced(t *testing.T) {
	directory := configSourcesProject(t)
	override := filepath.Join(directory, "local.yaml")
	if err := os.WriteFile(override, []byte("services:\n  api:\n    ports: [9090]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := execute([]string{"-C", directory, "--override", "local.yaml", "config", "sources"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "override") {
		t.Fatalf("override source is not listed:\n%s", output)
	}
	if !strings.Contains(output, "overrides services/api: api.ports") {
		t.Fatalf("override attribution is missing:\n%s", output)
	}

	// The reverse direction names the same winner from the service's side.
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"-C", directory, "--override", "local.yaml", "config", "sources", "--by-service"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--by-service exit = %d, stderr = %q", code, stderr.String())
	}
	for _, expected := range []string{
		"3 services · 1 overridden",
		"api            services/api/kranz.yaml",
		"  ↳ override",
		"local.yaml",
		"overrides services/api/kranz.yaml: ports",
		"root-db        kranz.yaml",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("--by-service output is missing %q:\n%s", expected, stdout.String())
		}
	}
}

// The CLI prints the dashboard's config map without colour: both come from
// internal/sourceview, so the text here is the layout the TUI shows.
func TestConfigSourcesByServiceListsDefiningFiles(t *testing.T) {
	output := runInspection(t, configSourcesProject(t), "config", "sources", "--by-service")
	for _, expected := range []string{
		"3 services · 0 overridden · defining file, then later overrides",
		"root      kranz.yaml",
		"root-db   kranz.yaml",
		"api       services/api/kranz.yaml",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("--by-service output is missing %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "\x1b[") {
		t.Errorf("--by-service output carries escape sequences:\n%q", output)
	}
}

func TestConfigSourcesByServiceFormatTemplate(t *testing.T) {
	directory := configSourcesProject(t)
	if err := os.WriteFile(filepath.Join(directory, "local.yaml"), []byte("services:\n  api:\n    ports: [9090]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := execute([]string{"-C", directory, "--override", "local.yaml", "config", "sources", "--by-service", "--format", "{{.Service}}|{{.DefinedIn}}|{{.Overrides}}"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	output := stdout.String()
	for _, expected := range []string{
		"root|kranz.yaml|",
		"api|services/api/kranz.yaml|local.yaml: api.ports (was services/api/kranz.yaml)",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("--by-service --format output is missing %q:\n%s", expected, output)
		}
	}
}

func TestConfigSourcesJSONCarriesStructuredProvenance(t *testing.T) {
	type sourceJSON struct {
		DisplayPath  string `json:"display_path"`
		Kind         string `json:"kind"`
		Depth        int    `json:"depth"`
		Contribution struct {
			Services []string `json:"services"`
		} `json:"contribution"`
	}
	type document struct {
		Sources      []sourceJSON `json:"sources"`
		ServiceOrder []string     `json:"service_order"`
	}

	var stdout, stderr bytes.Buffer
	code := execute([]string{"-C", configSourcesProject(t), "--output", "json", "config", "sources"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	decoded := decodeJSONData[document](t, stdout.Bytes())
	if len(decoded.Sources) != 2 {
		t.Fatalf("sources = %+v", decoded.Sources)
	}
	if decoded.Sources[0].DisplayPath != "kranz.yaml" || decoded.Sources[0].Kind != "explicit" {
		t.Fatalf("first source = %+v", decoded.Sources[0])
	}
	if decoded.Sources[1].DisplayPath != "services/api/kranz.yaml" || decoded.Sources[1].Depth != 1 {
		t.Fatalf("included source = %+v", decoded.Sources[1])
	}
	if len(decoded.Sources[1].Contribution.Services) != 1 || decoded.Sources[1].Contribution.Services[0] != "api" {
		t.Fatalf("included contribution = %+v", decoded.Sources[1].Contribution)
	}
	if len(decoded.ServiceOrder) != 3 {
		t.Fatalf("service order = %v", decoded.ServiceOrder)
	}
}

func TestConfigSourcesFormatTemplate(t *testing.T) {
	output := runInspection(t, configSourcesProject(t), "config", "sources", "--format", "{{.Path}}|{{.Kind}}")
	if !strings.Contains(output, "kranz.yaml|explicit") || !strings.Contains(output, "services/api/kranz.yaml|include") {
		t.Fatalf("formatted sources = %q", output)
	}
}

func TestConfigSourcesRejectsUnknownArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := execute([]string{"-C", configSourcesProject(t), "config", "sources", "extra"}, &stdout, &stderr)
	if code != kranzcli.ExitUsage {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestConfigSourcesIsDiscoverableInHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"config", "--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "sources") {
		t.Fatalf("config help does not list the sources command:\n%s", stdout.String())
	}
}
