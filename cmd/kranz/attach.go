package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
	"github.com/kranz-org/kranz/internal/settings"
	"github.com/kranz-org/kranz/internal/ui"
)

func runAttach(options kranzcli.GlobalOptions, args []string) (runErr error) {
	if len(args) > 0 {
		return &kranzcli.Error{Code: "invalid_arguments", Message: "attach does not accept arguments", ExitCode: kranzcli.ExitUsage}
	}
	record, err := resolveSession(options)
	if err != nil {
		return err
	}
	originalDirectory, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(record.Directory); err != nil {
		return fmt.Errorf("open runtime directory %s: %w", record.Directory, err)
	}
	defer func() { runErr = errors.Join(runErr, os.Chdir(originalDirectory)) }()
	client, err := kranzruntime.DialWithIdentity(record.Socket, version,
		kranzruntime.ClientIdentity{Surface: "tui", Label: "Kranz attach"})
	if err != nil {
		return classifyRuntimeError(err)
	}
	defer func() { runErr = errors.Join(runErr, client.Close()) }()
	cfg := client.Config()
	if cfg == nil {
		return errors.New("runtime returned no effective configuration")
	}
	return runAttachedTUI(client, cfg, record, options)
}

func runAttachedTUI(client *kranzruntime.Client, cfg *config.Config, record kranzruntime.SessionRecord, options kranzcli.GlobalOptions) (runErr error) {
	settingsPath, settingsPathErr := settings.DefaultPath()
	if settingsPathErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Kranz settings warning: %v\n", settingsPathErr)
	}
	userSettings, settingsErr := settings.Load(settingsPath)
	if settingsErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Kranz settings warning: %v\n", settingsErr)
		userSettings = settings.Settings{}
	}
	registry, registryErr := kranzruntime.DefaultRegistry()
	if registryErr != nil {
		registry = nil
	}
	darkBackground := lipgloss.HasDarkBackground()
	programReady := make(chan struct{})
	focusReported := make(chan struct{})
	var focusOnce sync.Once
	model := ui.NewModelWithOptions(cfg, version, ui.ModelOptions{Settings: userSettings, SettingsPath: settingsPath, ConfigPaths: cfg.Paths, DarkBackground: &darkBackground, App: client, DetachOnExit: true, ProgramReady: func() { close(programReady) },
		FocusReported: func() { focusOnce.Do(func() { close(focusReported) }) },
		Registry:      registry, SessionRecord: record, RestartRuntime: makeRestartRuntime(options)})
	defer func() {
		runErr = errors.Join(runErr, model.Shutdown())
		// Shutdown() only ever asks the runtime to stop something; it never
		// closes the socket. Close whatever session is current when this
		// returns, not the connection this function originally dialed — a
		// switch may have retired that one long ago and left it as the only
		// thing still referencing the live connection.
		runErr = errors.Join(runErr, model.CloseCurrentConnection())
	}()
	program := tea.NewProgram(model, dashboardProgramOptions()...)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	done := make(chan struct{})
	defer close(done)
	go runMouseWatchdog(program, mouseWatchdogConfig{
		interval: mouseRecoveryInterval,
		backstop: mouseRecoveryBackstop,
		ready:    programReady,
		relaxed:  focusReported,
		done:     done,
	})
	go func() {
		select {
		case <-signals:
			program.Quit()
		case <-done:
		}
	}()
	if _, err := runDashboardProgram(program); err != nil {
		return &kranzcli.Error{Code: "tui", Message: "run attached TUI", ExitCode: kranzcli.ExitInternal, Cause: err}
	}
	if code := model.RequestedExitCode(); code != 0 {
		return requestedExitError{code: code}
	}
	return nil
}

// runDashboardProgram runs the dashboard's Bubble Tea event loop. It exists
// as a variable so a test can replace the real interactive loop (which
// needs a terminal) while still exercising every routing decision around
// it — config loading, runtime resolution, dialing, and model construction.
var runDashboardProgram = func(program *tea.Program) (tea.Model, error) { return program.Run() }

// openBareLaunchRegistry opens the registry a bare launch with no
// discovered configuration probes for local runtimes to offer through the
// picker (PRD 3.1, Scenario C). It exists as a variable so a test can point
// it at an isolated temp registry instead of this machine's real one.
var openBareLaunchRegistry = kranzruntime.DefaultRegistry

// runRuntimePickerProgram runs the bare-launch runtime picker's Bubble Tea
// event loop. Like runDashboardProgram, it is a variable so a test can drive
// picker.Update directly (simulating a selection or a quit) instead of
// needing a real terminal.
var runRuntimePickerProgram = func(picker *ui.RuntimePicker) error {
	_, err := tea.NewProgram(picker, dashboardProgramOptions()...).Run()
	return err
}

// runtimeRegistryProbeTimeout bounds the registry query a bare launch makes
// before deciding between the ordinary "no configuration" error and the
// runtime picker (PRD 3.1: "a bounded-timeout registry query before the
// full-screen TUI starts").
const runtimeRegistryProbeTimeout = 2 * time.Second

// runBareWithoutConfig handles a bare `kranz` launch where automatic
// discovery found no configuration in the working directory (PRD 3.1,
// Scenario C). It is reached only when discovery itself found nothing to
// load, never when a discovered or explicitly named configuration exists
// but fails to parse — that error is returned unchanged by the caller and
// never reaches here, so an existing project's broken config is never
// misreported as "no project here".
func runBareWithoutConfig(options kranzcli.GlobalOptions) error {
	notFound := &kranzcli.Error{
		Code:     "config_not_found",
		Message:  "no Kranz configuration was found in this directory, and no local Kranz runtime is running",
		Hint:     "Run from a project directory, pass -f PATH, or start a runtime elsewhere first.",
		ExitCode: kranzcli.ExitConfig,
	}
	registry, err := openBareLaunchRegistry()
	if err != nil {
		return notFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), runtimeRegistryProbeTimeout)
	records, err := registry.List(ctx, version)
	cancel()
	if err != nil || len(records) == 0 {
		return notFound
	}

	picker := ui.NewRuntimePicker(registry, version)
	if err := runRuntimePickerProgram(picker); err != nil {
		return &kranzcli.Error{Code: "tui", Message: "run runtime picker", ExitCode: kranzcli.ExitInternal, Cause: err}
	}
	record, chosen := picker.Selected()
	if !chosen {
		return nil
	}
	client, err := kranzruntime.DialWithIdentity(record.Socket, version,
		kranzruntime.ClientIdentity{Surface: "tui", Label: "Kranz dashboard"})
	if err != nil {
		return classifyRuntimeError(err)
	}
	defer func() { _ = client.Close() }()
	cfg := client.Config()
	if cfg == nil {
		return errors.New("runtime returned no effective configuration")
	}
	return runAttachedTUI(client, cfg, record, options)
}
