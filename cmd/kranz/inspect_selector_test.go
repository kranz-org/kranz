package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

// collidingSelectorDirectory builds two autonomous sources that both declare a
// service named `api`. Composition qualifies the display names to `a/api` and
// `b/api`, which makes a bare `api` selector ambiguous — exactly the collisions
// composition makes ordinary.
func collidingSelectorDirectory(t *testing.T) (directory, first, second string) {
	t.Helper()
	directory = t.TempDir()
	first = filepath.Join(directory, "a", "kranz.yaml")
	second = filepath.Join(directory, "b", "kranz.yaml")
	for path, project := range map[string]string{first: "Alpha", second: "Beta"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("project: "+project+"\nservices:\n  api:\n    command: sleep 60\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory, first, second
}

// config explain and services info must accept a display name, a stable ID, and
// report ambiguity the same way the runtime commands do, instead of collapsing
// every non-exact key into a generic not-found.
func TestConfigExplainAndServicesInfoShareTheRuntimeSelectorResolver(t *testing.T) {
	directory, first, second := collidingSelectorDirectory(t)
	composeArgs := []string{"-C", directory, "-f", first, "-f", second}

	for _, command := range [][]string{
		append(append([]string{}, composeArgs...), "services", "info", "api"),
		append(append([]string{}, composeArgs...), "config", "explain", "api"),
	} {
		var stdout, stderr bytes.Buffer
		if code := execute(command, &stdout, &stderr); code != kranzcli.ExitUsage {
			t.Fatalf("%v exit = %d, want usage; stderr = %q", command, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "Choose one of: a/api, b/api") {
			t.Fatalf("%v did not offer the ambiguous choices: %q", command, stderr.String())
		}
	}

	// The qualified display name the diagnostics recommend is accepted.
	var stdout, stderr bytes.Buffer
	if code := execute(append(append([]string{}, composeArgs...), "services", "info", "a/api"), &stdout, &stderr); code != 0 {
		t.Fatalf("services info a/api exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Service:     a/api") {
		t.Fatalf("services info a/api output = %q", stdout.String())
	}

	// The stable ID survives a rename precisely because it is not the display
	// name, so it has to resolve here too.
	options := kranzcli.GlobalOptions{Directory: directory, ConfigPaths: []string{first, second}}
	cfg, _, err := loadProject(options)
	if err != nil {
		t.Fatal(err)
	}
	id := cfg.ServiceMetadata["a/api"].ID
	if id == "" {
		t.Fatal("composition produced no stable ID for a/api")
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute(append(append([]string{}, composeArgs...), "services", "info", id), &stdout, &stderr); code != 0 {
		t.Fatalf("services info %s exit = %d, stderr = %q", id, code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Service:     a/api") {
		t.Fatalf("stable ID did not resolve to its service: %q", stdout.String())
	}

	// A selector that matches nothing stays a not-found, not an ambiguity.
	stdout.Reset()
	stderr.Reset()
	if code := execute(append(append([]string{}, composeArgs...), "services", "info", "missing"), &stdout, &stderr); code != kranzcli.ExitNotFound {
		t.Fatalf("missing selector exit = %d, want not found; stderr = %q", code, stderr.String())
	}
}

// A selector that names nothing keeps the released service_not_found code in
// `config explain` and `services info`; only ambiguity earns a selector code.
// Both commands share resolveSingleService, so their JSON error.code is locked
// here.
func TestConfigExplainAndServicesInfoReportMissingServiceAsNotFound(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "kranz.yaml")
	contents := "project: Demo\nservices:\n  api:\n    command: sleep 60\n"
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"--output=json", "-C", directory, "-f", configPath}

	for _, command := range [][]string{
		append(append([]string{}, base...), "services", "info", "missing"),
		append(append([]string{}, base...), "config", "explain", "missing"),
	} {
		var stdout, stderr bytes.Buffer
		if code := execute(command, &stdout, &stderr); code != kranzcli.ExitNotFound {
			t.Fatalf("%v exit = %d, want not found; stderr = %q", command, code, stderr.String())
		}
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("%v error envelope = %q: %v", command, stdout.String(), err)
		}
		if envelope.Error.Code != "service_not_found" {
			t.Fatalf("%v error.code = %q, want service_not_found", command, envelope.Error.Code)
		}
	}
}
