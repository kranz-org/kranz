package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kranz-org/kranz/internal/app"
	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
	"github.com/kranz-org/kranz/internal/ui"
)

// TestRunTUIValidConfigStartsAndAttachesNormally is the PRD 3.1 unit test
// for "valid config": a bare launch in a project directory must start (or
// attach to) its runtime and open the dashboard exactly as before this
// feature — the runtime-picker fallback must never be consulted. It stops
// short of the interactive event loop itself (which needs a terminal;
// covered by manual verification) by replacing runDashboardProgram, so
// everything before it — config loading, background spawn, registry
// publish, dial, and Model construction — runs for real.
func TestRunTUIValidConfigStartsAndAttachesNormally(t *testing.T) {
	useHelperBackgroundRuntimes(t)
	directory := t.TempDir()
	name := fmt.Sprintf("valid-config-test-%d", os.Getpid())
	document := "project: " + name + "\nruntime:\n  name: " + name + "\nservices:\n  api:\n    command: sleep 60\n"
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runDown(kranzcli.GlobalOptions{Project: name, Output: kranzcli.OutputText}, nil, io.Discard)
	})

	dashboardStarted := false
	previousDashboard := runDashboardProgram
	runDashboardProgram = func(*tea.Program) (tea.Model, error) {
		dashboardStarted = true
		return nil, nil
	}
	defer func() { runDashboardProgram = previousDashboard }()
	pickerInvoked := false
	previousPicker := runRuntimePickerProgram
	runRuntimePickerProgram = func(*ui.RuntimePicker) error {
		pickerInvoked = true
		return nil
	}
	defer func() { runRuntimePickerProgram = previousPicker }()

	if err := runTUI(kranzcli.GlobalOptions{Directory: directory, Output: kranzcli.OutputText}); err != nil {
		t.Fatalf("runTUI with a valid config returned an error: %v", err)
	}
	if !dashboardStarted {
		t.Fatal("the ordinary dashboard program was never started")
	}
	if pickerInvoked {
		t.Fatal("the runtime picker must never run when a configuration was found")
	}

	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(context.Background(), name, version); err != nil {
		t.Fatalf("runtime never published to the registry: %v", err)
	}
}

// asCommandError unwraps err into *kranzcli.Error, failing the test if it is
// some other kind of error entirely.
func asCommandError(t *testing.T, err error) *kranzcli.Error {
	t.Helper()
	var commandError *kranzcli.Error
	if !errors.As(err, &commandError) {
		t.Fatalf("error %v (%T) is not a *kranzcli.Error", err, err)
	}
	return commandError
}

// TestRunTUIInvalidConfigIsNotMaskedByTheRuntimePicker is the PRD 3.1 unit
// test for "invalid config": a configuration that IS discovered but fails
// to load must return the existing invalid_config error unchanged, never
// the runtime-picker fallback (PRD acceptance criterion: "an existing
// config error is never turned into an offer to connect to an unrelated
// runtime").
func TestRunTUIInvalidConfigIsNotMaskedByTheRuntimePicker(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Even if local runtimes exist, a found-but-broken config must win.
	previousRegistry := openBareLaunchRegistry
	openBareLaunchRegistry = func() (*kranzruntime.Registry, error) {
		t.Fatal("the registry must never be consulted when a discovered config is merely invalid")
		return nil, nil
	}
	defer func() { openBareLaunchRegistry = previousRegistry }()

	err := runTUI(kranzcli.GlobalOptions{Directory: directory, Output: kranzcli.OutputText})
	if err == nil {
		t.Fatal("bare launch over an invalid config returned no error")
	}
	if code := asCommandError(t, err).Code; code != "invalid_config" {
		t.Fatalf("error code = %q, want invalid_config", code)
	}
}

// TestRunBareWithoutConfigNoRuntimeReturnsClearError is the PRD 3.1 unit
// test for "no config without runtimes": a directory with neither a
// configuration nor any locally registered runtime must print a clear
// terminal error and exit non-zero — and, by construction (no tea.Program
// is ever created on this path), never touch the alternate screen.
func TestRunBareWithoutConfigNoRuntimeReturnsClearError(t *testing.T) {
	directory := t.TempDir()
	previousRegistry := openBareLaunchRegistry
	openBareLaunchRegistry = func() (*kranzruntime.Registry, error) {
		return kranzruntime.NewRegistry(t.TempDir()) // isolated and empty
	}
	defer func() { openBareLaunchRegistry = previousRegistry }()
	pickerInvoked := false
	previousPicker := runRuntimePickerProgram
	runRuntimePickerProgram = func(*ui.RuntimePicker) error {
		pickerInvoked = true
		return nil
	}
	defer func() { runRuntimePickerProgram = previousPicker }()

	err := runTUI(kranzcli.GlobalOptions{Directory: directory, Output: kranzcli.OutputText})
	if err == nil {
		t.Fatal("bare launch with neither config nor runtime returned no error")
	}
	commandError := asCommandError(t, err)
	if commandError.ExitCode != kranzcli.ExitConfig {
		t.Fatalf("exit code = %d, want ExitConfig (%d)", commandError.ExitCode, kranzcli.ExitConfig)
	}
	if commandError.Code != "config_not_found" {
		t.Fatalf("error code = %q, want config_not_found", commandError.Code)
	}
	if commandError.Error() == "" {
		t.Fatal("error message is empty")
	}
	if pickerInvoked {
		t.Fatal("the runtime picker must not run when the registry is empty")
	}
}

// TestRunBareWithoutConfigOpensPickerWhenARuntimeExists is the PRD 3.1/
// Scenario C unit test for "no config with runtimes": with at least one
// runtime registered, the bare launch must open the picker instead of
// failing outright. It drives the picker headlessly through its own
// exported Update/Init methods (via the injected program runner) rather
// than a real terminal, then has it quit without choosing, which proves the
// routing decision without needing a live dashboard.
func TestRunBareWithoutConfigOpensPickerWhenARuntimeExists(t *testing.T) {
	directory := t.TempDir()
	registry, err := kranzruntime.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	runtimeName := fmt.Sprintf("picker-test-%d", os.Getpid())
	rt := startPickerTestRuntime(t, registry, runtimeName)
	defer rt.stop()

	previousRegistry := openBareLaunchRegistry
	openBareLaunchRegistry = func() (*kranzruntime.Registry, error) { return registry, nil }
	defer func() { openBareLaunchRegistry = previousRegistry }()

	pickerInvoked := false
	previousPicker := runRuntimePickerProgram
	runRuntimePickerProgram = func(picker *ui.RuntimePicker) error {
		pickerInvoked = true
		// Drive discovery once, synchronously, the same way Bubble Tea's own
		// loop would feed the result of Init()'s command back in — then quit
		// without selecting anything.
		if cmd := picker.Init(); cmd != nil {
			if msg := cmd(); msg != nil {
				picker.Update(msg)
			}
		}
		picker.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		return nil
	}
	defer func() { runRuntimePickerProgram = previousPicker }()

	err = runTUI(kranzcli.GlobalOptions{Directory: directory, Output: kranzcli.OutputText})
	if !pickerInvoked {
		t.Fatal("the runtime picker was never invoked even though a runtime is registered")
	}
	if err != nil {
		t.Fatalf("quitting the picker without a choice should return no error, got %v", err)
	}
}

// pickerTestRuntime is a minimal real supervisor registered in an isolated
// registry, just enough for the picker to discover and for a would-be
// selection to dial.
type pickerTestRuntime struct {
	stop func()
}

func startPickerTestRuntime(t *testing.T, registry *kranzruntime.Registry, name string) *pickerTestRuntime {
	t.Helper()
	directory := t.TempDir()
	document := "project: " + name + "\nservices: {}\n"
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := registry.Acquire(name)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	metadata, err := session.Prepare(name, "test", "background", directory)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	cfg := &config.Config{Project: name, Services: map[string]config.Service{}}
	local := app.NewLocal(cfg, []string{filepath.Join(directory, "kranz.yaml")}, app.Options{SessionID: metadata.ID})
	supervisor := kranzruntime.NewSupervisor(local)
	if err := supervisor.Listen(metadata.Socket); err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := session.Publish(); err != nil {
		t.Fatalf("publish: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- supervisor.Serve() }()
	return &pickerTestRuntime{stop: func() {
		_ = supervisor.Close()
		<-serveErr
		_ = session.Close()
		_ = local.Shutdown()
	}}
}
