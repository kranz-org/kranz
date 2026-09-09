package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
)

// withWizardResult exercises command integration without asking Bubble Tea to
// take over the test process terminal. The wizard model has focused state and
// rendering tests of its own.
func withWizardResult(t *testing.T, draft initDraft, approved bool) {
	t.Helper()
	previousStdin, previousTerminal, previousWizard := stdin, isTerminal, interactiveInitWizard
	stdin = strings.NewReader("")
	isTerminal = func() bool { return true }
	interactiveInitWizard = func(_ string, _ string, _ initOptions, _ io.Reader, _ io.Writer) (initDraft, bool, error) {
		return draft, approved, nil
	}
	t.Cleanup(func() {
		stdin, isTerminal, interactiveInitWizard = previousStdin, previousTerminal, previousWizard
	})
}

// withoutTerminal forces the non-interactive path.
func withoutTerminal(t *testing.T) {
	t.Helper()
	previousStdin, previousTerminal := stdin, isTerminal
	stdin = strings.NewReader("")
	isTerminal = func() bool { return false }
	t.Cleanup(func() { stdin, isTerminal = previousStdin, previousTerminal })
}

func TestInitWritesAValidConfigurationFromFlags(t *testing.T) {
	withoutTerminal(t)
	directory := t.TempDir()

	output := runInspection(t, directory, "init", "--name", "Demo", "--service", "api", "--command", "sleep 60", "--yes")
	if !strings.Contains(output, "Wrote kranz.yaml") {
		t.Fatalf("init output = %q", output)
	}

	cfg, err := config.LoadFiles([]string{filepath.Join(directory, "kranz.yaml")})
	if err != nil {
		t.Fatalf("written configuration does not load: %v", err)
	}
	if cfg.Project != "Demo" {
		t.Errorf("project = %q, want Demo", cfg.Project)
	}
	if cfg.Services["api"].Command != "sleep 60" {
		t.Errorf("api command = %q", cfg.Services["api"].Command)
	}
}

func TestInitCreatesPositionalProjectDirectory(t *testing.T) {
	withoutTerminal(t)
	base := t.TempDir()
	output := runInspection(t, base, "init", "shop", "--name", "Demo", "--service", "api", "--command", "sleep 60", "--yes")
	if !strings.Contains(output, "Wrote kranz.yaml") {
		t.Fatalf("init output = %q", output)
	}
	cfg, err := config.LoadFiles([]string{filepath.Join(base, "shop", "kranz.yaml")})
	if err != nil {
		t.Fatalf("configuration in positional directory does not load: %v", err)
	}
	if cfg.Project != "Demo" {
		t.Errorf("project = %q, want Demo", cfg.Project)
	}
}

func TestInitRejectsMoreThanOneDirectory(t *testing.T) {
	withoutTerminal(t)
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", t.TempDir(), "init", "one", "two", "--yes"}, &stdout, &stderr); code != kranzcli.ExitUsage {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestInitJSONWritesOneUsefulEnvelopeWithoutHumanPreview(t *testing.T) {
	withoutTerminal(t)
	directory := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := execute([]string{
		"-C", directory, "--output=json", "init",
		"--name", "Demo", "--service", "api", "--command", "sleep 60", "--yes",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 || !json.Valid(stdout.Bytes()) {
		t.Fatalf("stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
	var envelope struct {
		SchemaVersion int        `json:"schema_version"`
		Data          initResult `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != kranzcli.SchemaVersion ||
		!envelope.Data.Written ||
		envelope.Data.Project != "Demo" ||
		len(envelope.Data.Services) != 1 ||
		envelope.Data.Services[0] != "api" ||
		envelope.Data.Actions != 0 {
		t.Fatalf("init envelope = %#v", envelope)
	}
	if envelope.Data.Path != filepath.Join(directory, "kranz.yaml") {
		t.Errorf("path = %q, want absolute target", envelope.Data.Path)
	}
	if strings.Contains(stdout.String(), "----------") || strings.Contains(stdout.String(), "Wrote kranz.yaml") {
		t.Errorf("JSON contains human preview: %s", stdout.String())
	}
}

// -p/--project was the original spelling and is still accepted for scripts,
// while --name is the unambiguous init-local option shown in help.
func TestInitPreservesGlobalProjectNameCompatibility(t *testing.T) {
	withoutTerminal(t)
	directory := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", directory, "--project", "Named", "init", "--service", "api", "--command", "sleep 60", "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	cfg, err := config.LoadFiles([]string{filepath.Join(directory, "kranz.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != "Named" {
		t.Errorf("project = %q, want Named", cfg.Project)
	}
}

func TestInitRejectsAmbiguousProjectNames(t *testing.T) {
	withoutTerminal(t)
	var stdout, stderr bytes.Buffer
	code := execute([]string{"-C", t.TempDir(), "-p", "Legacy", "init", "--name", "Current", "--service", "api", "--command", "sleep 60", "--yes"}, &stdout, &stderr)
	if code != kranzcli.ExitUsage || !strings.Contains(stderr.String(), "both --name and -p/--project") {
		t.Fatalf("exit=%d stdout/stderr=%q/%q", code, stdout.String(), stderr.String())
	}
}

// A service with no command cannot start, and a configuration that cannot start
// is not a useful thing to have written.
func TestInitRefusesToWriteAServiceWithNoCommand(t *testing.T) {
	withoutTerminal(t)
	var stdout, stderr bytes.Buffer
	directory := t.TempDir()
	if code := execute([]string{"-C", directory, "init", "--yes"}, &stdout, &stderr); code != kranzcli.ExitUsage {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(directory, "kranz.yaml")); !os.IsNotExist(err) {
		t.Error("init wrote a file it had rejected")
	}
}

func TestInitDoesNotOverwriteWithoutConsent(t *testing.T) {
	withoutTerminal(t)
	directory := t.TempDir()
	existing := filepath.Join(directory, "kranz.yaml")
	original := "project: Original\nservices:\n  api:\n    command: sleep 1\n"
	if err := os.WriteFile(existing, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := execute([]string{"-C", directory, "init", "--service", "other", "--command", "sleep 2"}, &stdout, &stderr)
	if code != kranzcli.ExitConflict {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Errorf("init overwrote the file it refused to overwrite:\n%s", data)
	}
}

func TestInitYesDoesNotImplyOverwrite(t *testing.T) {
	withoutTerminal(t)
	directory := t.TempDir()
	existing := filepath.Join(directory, "kranz.yaml")
	original := "project: Original\nservices:\n  api:\n    command: sleep 1\n"
	if err := os.WriteFile(existing, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := execute([]string{"-C", directory, "init", "--service", "other", "--command", "sleep 2", "--yes"}, &stdout, &stderr)
	if code != kranzcli.ExitConflict {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("--yes overwrote the existing file:\n%s", data)
	}
}

func TestInitForceAllowsNonInteractiveOverwrite(t *testing.T) {
	withoutTerminal(t)
	directory := t.TempDir()
	existing := filepath.Join(directory, "kranz.yaml")
	if err := os.WriteFile(existing, []byte("project: Original\nservices:\n  api:\n    command: sleep 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runInspection(t, directory, "init", "--name", "Replacement", "--service", "other", "--command", "sleep 2", "--yes", "--force")
	cfg, err := config.LoadFiles([]string{existing})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != "Replacement" {
		t.Fatalf("project = %q, want Replacement", cfg.Project)
	}
}

// The wizard exists so the user approves a file they have read. Declining the
// write must leave the directory untouched.
func TestInitWizardDeclineWritesNothing(t *testing.T) {
	directory := t.TempDir()
	withWizardResult(t, newInitDraft(directory, initOptions{}), false)

	output := runInspection(t, directory, "init")
	if !strings.Contains(output, "Nothing was written") {
		t.Errorf("output = %q", output)
	}
	if _, err := os.Stat(filepath.Join(directory, "kranz.yaml")); !os.IsNotExist(err) {
		t.Error("declining the wizard still wrote a file")
	}
}

func TestInitWizardAcceptsAnswersAndPreviewsTheFile(t *testing.T) {
	directory := t.TempDir()
	draft := newInitDraft(directory, initOptions{})
	draft.Project = "Interactive"
	draft.Services = []initServiceDraft{{Name: "web", Dir: ".", Command: "npm start"}}
	withWizardResult(t, draft, true)

	output := runInspection(t, directory, "init")
	if !strings.Contains(output, "Wrote kranz.yaml") {
		t.Errorf("output does not report the saved draft:\n%s", output)
	}
	cfg, err := config.LoadFiles([]string{filepath.Join(directory, "kranz.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != "Interactive" || cfg.Services["web"].Command != "npm start" {
		t.Errorf("wizard answers were not used: %+v", cfg.Services)
	}
}

// Importing re-renders through the loader, which expands `command` into
// `lifecycle.start` and resolves directories against the project. Writing that
// back unchanged produces a file that is both rejected as a conflict and tied
// to one machine's absolute paths.
func TestInitImportProducesAPortableLoadableFile(t *testing.T) {
	withoutTerminal(t)
	directory := t.TempDir()
	procfile := "web: node server.js\nworker: node worker.js\n"
	if err := os.WriteFile(filepath.Join(directory, "Procfile"), []byte(procfile), 0o600); err != nil {
		t.Fatal(err)
	}

	runInspection(t, directory, "init", "--from", "Procfile", "--yes")

	target := filepath.Join(directory, "kranz.yaml")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), directory) {
		t.Errorf("import baked an absolute project path into the file:\n%s", data)
	}
	cfg, err := config.LoadFiles([]string{target})
	if err != nil {
		t.Fatalf("imported configuration does not load: %v", err)
	}
	for _, name := range []string{"web", "worker"} {
		if cfg.Services[name].Command == "" {
			t.Errorf("imported service %q lost its command", name)
		}
	}
}

func TestInitImportRejectsAMissingSource(t *testing.T) {
	withoutTerminal(t)
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", t.TempDir(), "init", "--from", "nope.yaml", "--yes"}, &stdout, &stderr); code != kranzcli.ExitNotFound {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

// Discovery is a separate future workflow. Init must not infer authoring intent
// from what happens to be present in a package registry.
func TestInitIgnoresPackageScripts(t *testing.T) {
	withoutTerminal(t)
	directory := t.TempDir()
	manifest := `{"name":"web","scripts":{"build":"vite build","test":"vitest"}}`
	if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	runInspection(t, directory, "init", "--service", "web", "--command", "npm run dev", "--yes")

	cfg, err := config.LoadFiles([]string{filepath.Join(directory, "kranz.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	if actions := cfg.Services["web"].Actions; len(actions) != 0 {
		t.Fatalf("init inferred package scripts as actions: %#v", actions)
	}
}

func TestInitRejectsUnknownArguments(t *testing.T) {
	withoutTerminal(t)
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", t.TempDir(), "init", "--turbo"}, &stdout, &stderr); code != kranzcli.ExitUsage {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}
