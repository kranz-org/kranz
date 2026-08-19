// Package main provides the Kranz command-line entry point.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kranz-org/kranz/internal/app"
	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/settings"
	"github.com/kranz-org/kranz/internal/ui"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() { os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr)) }

func execute(args []string, stdout, stderr io.Writer) int {
	tree := kranzcli.DefaultTree()
	invocation, err := kranzcli.Parse(tree, args)
	if err != nil {
		return kranzcli.WriteError(stdout, stderr, kranzcli.RequestedOutput(args), err)
	}

	if invocation.Help {
		output, helpErr := kranzcli.Help(tree, invocation.CommandPath)
		if helpErr != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, helpErr)
		}
		if _, err := fmt.Fprint(stdout, output); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	}
	if invocation.Command() == "version" {
		return writeVersion(stdout, stderr, invocation.Globals.Output)
	}
	if invocation.Command() != "" {
		err := &kranzcli.Error{
			Code: "not_implemented", Message: fmt.Sprintf("command %q is not implemented yet", invocation.Command()),
			ExitCode: kranzcli.ExitUsage,
		}
		return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
	}
	if invocation.Globals.Output != kranzcli.OutputText {
		err := &kranzcli.Error{Code: "invalid_output", Message: "the TUI requires text output", ExitCode: kranzcli.ExitUsage}
		return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
	}
	if invocation.Globals.Project != "" {
		err := &kranzcli.Error{
			Code: "invalid_arguments", Message: "-p requires attach or another runtime command",
			Hint: "Use `kranz -p " + invocation.Globals.Project + " attach`.", ExitCode: kranzcli.ExitUsage,
		}
		return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
	}

	if err := runTUI(invocation.Globals); err != nil {
		var requested requestedExitError
		if errors.As(err, &requested) {
			return requested.code
		}
		return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
	}
	return 0
}

func writeVersion(stdout, stderr io.Writer, format kranzcli.OutputFormat) int {
	metadata := struct {
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildTime string `json:"build_time"`
	}{Version: strings.TrimPrefix(version, "v"), Commit: commit, BuildTime: buildTime}
	if format == kranzcli.OutputJSON {
		if err := kranzcli.WriteJSON(stdout, metadata); err != nil {
			return kranzcli.WriteError(stdout, stderr, format, err)
		}
		return 0
	}
	if _, err := fmt.Fprintf(stdout, "kranz %s (commit %s, built %s)\n", metadata.Version, metadata.Commit, metadata.BuildTime); err != nil {
		return kranzcli.WriteError(stdout, stderr, format, err)
	}
	return 0
}

type requestedExitError struct{ code int }

func (e requestedExitError) Error() string {
	return fmt.Sprintf("project requested exit code %d", e.code)
}

func runTUI(options kranzcli.GlobalOptions) (runErr error) {
	originalDirectory, err := os.Getwd()
	if err != nil {
		return &kranzcli.Error{Code: "directory", Message: "determine working directory", ExitCode: kranzcli.ExitConfig, Cause: err}
	}
	if err := os.Chdir(options.Directory); err != nil {
		return &kranzcli.Error{Code: "directory", Message: fmt.Sprintf("change directory to %s", options.Directory), ExitCode: kranzcli.ExitConfig, Cause: err}
	}
	defer func() { runErr = errors.Join(runErr, os.Chdir(originalDirectory)) }()

	cfgPaths := options.ConfigPaths
	if len(cfgPaths) == 0 {
		cfgPaths, err = config.DiscoverFiles(".")
		if err != nil {
			return &kranzcli.Error{Code: "config_not_found", Message: "discover configuration", ExitCode: kranzcli.ExitConfig, Cause: err}
		}
	}
	cfg, err := config.LoadFiles(cfgPaths)
	if err != nil {
		return &kranzcli.Error{Code: "invalid_config", Message: "load configuration", ExitCode: kranzcli.ExitConfig, Cause: err}
	}

	settingsPath, settingsPathErr := settings.DefaultPath()
	if settingsPathErr != nil {
		fmt.Fprintf(os.Stderr, "Kranz settings warning: %v\n", settingsPathErr)
	}
	userSettings, settingsErr := settings.Load(settingsPath)
	if settingsErr != nil {
		fmt.Fprintf(os.Stderr, "Kranz settings warning: %v\n", settingsErr)
		userSettings = settings.Settings{}
	}

	darkBackground := lipgloss.HasDarkBackground()
	application := app.NewLocal(cfg, cfgPaths, app.Options{})
	model := ui.NewModelWithOptions(cfg, version, ui.ModelOptions{
		Settings: userSettings, SettingsPath: settingsPath, ConfigPaths: cfgPaths,
		DarkBackground: &darkBackground, App: application,
	})
	defer func() { runErr = errors.Join(runErr, model.Shutdown()) }()

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	runDone := make(chan struct{})
	defer close(runDone)
	go func() {
		select {
		case <-signals:
			program.Quit()
		case <-runDone:
		}
	}()

	if _, err := program.Run(); err != nil {
		return &kranzcli.Error{Code: "tui", Message: "run TUI", ExitCode: kranzcli.ExitInternal, Cause: err}
	}
	if code := model.RequestedExitCode(); code != 0 {
		return requestedExitError{code: code}
	}
	return nil
}
