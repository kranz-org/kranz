// Package main provides the Kranz command-line entry point.
// Kranz is a terminal process orchestrator for local development environments.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/settings"
	"github.com/kranz-org/kranz/internal/ui"
)

// version, commit, and buildTime are injected with -ldflags by release builds.
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		var requested requestedExitError
		if errors.As(err, &requested) {
			os.Exit(requested.code)
		}
		fmt.Fprintf(os.Stderr, "Kranz error: %v\n", err)
		os.Exit(1)
	}
}

type requestedExitError struct{ code int }

// Error satisfies error while the concrete type carries the requested code.
func (e requestedExitError) Error() string {
	return fmt.Sprintf("project requested exit code %d", e.code)
}

func run() (runErr error) {
	if output, handled, err := commandInformation(os.Args[1:]); err != nil {
		return err
	} else if handled {
		fmt.Print(output)
		return nil
	}
	cfgPaths, err := configPaths(os.Args[1:])
	if err != nil {
		return err
	}

	// Resolve and load every native or Process Compose configuration layer.
	cfg, err := config.LoadFiles(cfgPaths)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
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
	model := ui.NewModelWithOptions(cfg, version, ui.ModelOptions{
		Settings: userSettings, SettingsPath: settingsPath, ConfigPaths: cfgPaths,
		DarkBackground: &darkBackground,
	})
	defer func() {
		runErr = errors.Join(runErr, model.Shutdown())
	}()

	// Cell-motion tracking enables clicks without reporting passive mouse motion.
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithReportFocus(),
	)

	// An external signal first exits Bubble Tea; the deferred shutdown above then
	// synchronously stops every managed process group before the command returns.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	runDone := make(chan struct{})
	defer close(runDone)
	go func() {
		select {
		case <-signals:
			p.Quit()
		case <-runDone:
		}
	}()

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	if code := model.RequestedExitCode(); code != 0 {
		return requestedExitError{code: code}
	}
	return nil
}

func commandInformation(args []string) (output string, handled bool, err error) {
	if len(args) == 0 {
		return "", false, nil
	}
	switch args[0] {
	case "--version", "-v":
		if len(args) != 1 {
			return "", false, errors.New("--version does not accept additional arguments")
		}
		return fmt.Sprintf("kranz %s (commit %s, built %s)\n",
			strings.TrimPrefix(version, "v"), commit, buildTime), true, nil
	case "--help", "-h":
		if len(args) != 1 {
			return "", false, errors.New("--help does not accept additional arguments")
		}
		return `Kranz — a local service orchestrator with a terminal UI.

Usage:
  kranz
  kranz [CONFIG ...]
  kranz -f CONFIG [-f OVERRIDE ...]

Options:
  -f, --config PATH  Load a configuration layer (repeatable)
  -h, --help         Show this help
  -v, --version      Show version and build metadata
`, true, nil
	default:
		return "", false, nil
	}
}

func configPaths(args []string) ([]string, error) {
	if len(args) == 0 {
		return config.DiscoverFiles(".")
	}
	var paths []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-f" || arg == "--config":
			index++
			if index >= len(args) {
				return nil, fmt.Errorf("%s requires a path", arg)
			}
			paths = append(paths, args[index])
		case strings.HasPrefix(arg, "--config="):
			paths = append(paths, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown option %s", arg)
		default:
			paths = append(paths, arg)
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("no configuration files provided")
	}
	return paths, nil
}
