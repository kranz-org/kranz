package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
	client, err := kranzruntime.Dial(record.Socket, version)
	if err != nil {
		return classifyRuntimeError(err)
	}
	defer func() { runErr = errors.Join(runErr, client.Close()) }()
	cfg := client.Config()
	if cfg == nil {
		return errors.New("runtime returned no effective configuration")
	}
	return runAttachedTUI(client, cfg)
}

func runAttachedTUI(client *kranzruntime.Client, cfg *config.Config) (runErr error) {
	settingsPath, settingsPathErr := settings.DefaultPath()
	if settingsPathErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Kranz settings warning: %v\n", settingsPathErr)
	}
	userSettings, settingsErr := settings.Load(settingsPath)
	if settingsErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Kranz settings warning: %v\n", settingsErr)
		userSettings = settings.Settings{}
	}
	darkBackground := lipgloss.HasDarkBackground()
	model := ui.NewModelWithOptions(cfg, version, ui.ModelOptions{Settings: userSettings, SettingsPath: settingsPath, ConfigPaths: cfg.Paths, DarkBackground: &darkBackground, App: client, DetachOnExit: true})
	defer func() { runErr = errors.Join(runErr, model.Shutdown()) }()
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-signals:
			program.Quit()
		case <-client.Done():
			program.Quit()
		case <-done:
		}
	}()
	if _, err := program.Run(); err != nil {
		return &kranzcli.Error{Code: "tui", Message: "run attached TUI", ExitCode: kranzcli.ExitInternal, Cause: err}
	}
	if code := model.RequestedExitCode(); code != 0 {
		return requestedExitError{code: code}
	}
	return nil
}
