package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
)

// withWizardInput drives the interactive path deterministically, without a
// terminal, by replacing the two seams init reads the user through.
func withWizardInput(t *testing.T, answers string) {
	t.Helper()
	previousStdin, previousTerminal := stdin, isTerminal
	stdin = strings.NewReader(answers)
	isTerminal = func() bool { return true }
	t.Cleanup(func() { stdin, isTerminal = previousStdin, previousTerminal })
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

	output := runInspection(t, directory, "init", "--project", "Demo", "--service", "api", "--command", "sleep 60", "--yes")
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

// --project is consumed by the global runtime selector before init sees it, so
// init has to read the project name from there or silently ignore the flag its
// own reference documents.
func TestInitReadsProjectNameFromTheGlobalFlag(t *testing.T) {
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

// The wizard exists so the user approves a file they have read. Declining the
// write must leave the directory untouched.
func TestInitWizardDeclineWritesNothing(t *testing.T) {
	withWizardInput(t, "Interactive\napi\nsleep 60\nn\n")
	directory := t.TempDir()

	output := runInspection(t, directory, "init")
	if !strings.Contains(output, "Nothing was written") {
		t.Errorf("output = %q", output)
	}
	if _, err := os.Stat(filepath.Join(directory, "kranz.yaml")); !os.IsNotExist(err) {
		t.Error("declining the wizard still wrote a file")
	}
}

func TestInitWizardAcceptsAnswersAndPreviewsTheFile(t *testing.T) {
	withWizardInput(t, "Interactive\nweb\nnpm start\ny\n")
	directory := t.TempDir()

	output := runInspection(t, directory, "init")
	// The preview has to show the actual content, not a summary of it.
	if !strings.Contains(output, "project: Interactive") || !strings.Contains(output, "npm start") {
		t.Errorf("preview does not show the file:\n%s", output)
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

	runInspection(t, directory, "init", "--yes")

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

// Discovering what a project can do must not have the side effects of doing it,
// so scripts are read from the manifest and never executed.
func TestInitOffersPackageScriptsAsActions(t *testing.T) {
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
	actions := cfg.Services["web"].Actions
	for _, name := range []string{"build", "test"} {
		if actions[name].Command != "npm run "+name {
			t.Errorf("action %q = %q", name, actions[name].Command)
		}
	}
}

func TestInitRejectsUnknownArguments(t *testing.T) {
	withoutTerminal(t)
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", t.TempDir(), "init", "--turbo"}, &stdout, &stderr); code != kranzcli.ExitUsage {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}
