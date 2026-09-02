// Package main provides the Kranz command-line entry point.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kranz-org/kranz/internal/app"
	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
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
		if containsMCPCommand(args) {
			_, _ = fmt.Fprintln(stderr, err)
			return kranzcli.ExitUsage
		}
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
	// A command whose grammar is reserved but whose execution a later feature
	// stream still has to attach is refused from the tree itself, so help and
	// dispatch can never disagree about what this build supports.
	if len(invocation.CommandPath) > 0 {
		if command, resolveErr := tree.Resolve(invocation.CommandPath); resolveErr == nil && command.IsPlanned() {
			err := &kranzcli.Error{
				Code:     "not_implemented",
				Message:  fmt.Sprintf("command %q is not implemented yet", invocation.Command()),
				Hint:     "It is planned for v0.8.0. Run `kranz --help` for the commands this build supports.",
				ExitCode: kranzcli.ExitUsage,
			}
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
	}

	if invocation.Command() == "version" {
		return writeVersion(stdout, stderr, invocation.Globals.Output)
	}
	if invocation.Command() == "mcp" {
		attachOnly := false
		for _, arg := range invocation.Args {
			if arg == "--attach-only" {
				attachOnly = true
				continue
			}
			_, _ = fmt.Fprintf(stderr, "Kranz MCP: unknown mcp option or argument %q\n", arg)
			return kranzcli.ExitUsage
		}
		if err := runMCP(invocation.Globals, attachOnly, stdout, stderr); err != nil {
			_, _ = fmt.Fprintf(stderr, "Kranz MCP: %v\n", err)
			return 1
		}
		return 0
	}
	// The inspection commands describe a project from its configuration and
	// never touch a runtime, so they are dispatched before anything that
	// resolves a session.
	switch invocation.Command() {
	case "config check":
		if err := runConfigCheck(invocation.Globals, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "init":
		if err := runInit(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "config show":
		if err := runConfigShow(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "config explain":
		if err := runConfigExplain(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "doctor":
		if err := runDoctor(invocation.Globals, stdout); err != nil {
			var requested requestedExitError
			if errors.As(err, &requested) {
				return requested.code
			}
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "list":
		if err := runList(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "info":
		if err := runInfo(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "plan":
		if err := runPlan(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "runs list":
		if err := runRuns(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "runs delete":
		if err := runRunsDelete(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "graph":
		if err := runGraph(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "ports":
		if err := runPorts(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "logs show":
		if err := runLogs(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "logs clear":
		if err := runLogsClear(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "completion":
		if len(invocation.Args) != 1 {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, &kranzcli.Error{
				Code:     "invalid_arguments",
				Message:  "completion takes exactly one shell",
				Hint:     "Run `kranz completion bash`, `kranz completion zsh`, or `kranz completion fish`.",
				ExitCode: kranzcli.ExitUsage,
			})
		}
		script, err := kranzcli.Completion(tree, invocation.Args[0])
		if err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		if _, err := fmt.Fprint(stdout, script); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "action list":
		if err := runActionList(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "action info":
		if err := runActionInfo(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "action run":
		if err := runActionRun(invocation.Globals, invocation.Args, stdout); err != nil {
			var requested requestedExitError
			if errors.As(err, &requested) {
				return requested.code
			}
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "port inspect":
		if err := runPortInspect(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	}

	if invocation.Command() == "ps" {
		if len(invocation.Args) != 0 {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, &kranzcli.Error{Code: "invalid_arguments", Message: "ps does not accept arguments", ExitCode: kranzcli.ExitUsage})
		}
		return runPS(invocation.Globals, stdout, stderr)
	}
	if invocation.Command() == "clients" {
		if len(invocation.Args) != 0 {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, &kranzcli.Error{Code: "invalid_arguments", Message: "clients does not accept arguments", ExitCode: kranzcli.ExitUsage})
		}
		return runClients(invocation.Globals, stdout, stderr)
	}
	if invocation.Command() == "up" {
		if err := runUp(invocation.Globals, invocation.Args, stdout); err != nil {
			var requested requestedExitError
			if errors.As(err, &requested) {
				return requested.code
			}
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	}
	if invocation.Command() == "status" {
		if err := runStatus(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	}
	if command := invocation.Command(); command == "start" || command == "stop" || command == "restart" || command == "reload" {
		if err := runLifecycle(invocation.Globals, command, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	}
	if invocation.Command() == "down" {
		if err := runDown(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	}
	if invocation.Command() == "attach" {
		if invocation.Globals.Output != kranzcli.OutputText {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, &kranzcli.Error{Code: "invalid_output", Message: "attach requires text output", ExitCode: kranzcli.ExitUsage})
		}
		if err := runAttach(invocation.Globals, invocation.Args); err != nil {
			var requested requestedExitError
			if errors.As(err, &requested) {
				return requested.code
			}
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
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

func containsMCPCommand(args []string) bool {
	for _, arg := range args {
		if arg == "mcp" {
			return true
		}
	}
	return false
}

func runPS(options kranzcli.GlobalOptions, stdout, stderr io.Writer) int {
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return kranzcli.WriteError(stdout, stderr, options.Output, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	records, err := registry.List(ctx, version)
	if err != nil {
		return kranzcli.WriteError(stdout, stderr, options.Output, err)
	}
	if options.Project != "" {
		filtered := records[:0]
		for _, record := range records {
			if record.Name == options.Project || record.ID == options.Project || strings.HasPrefix(record.ID, options.Project) {
				filtered = append(filtered, record)
			}
		}
		records = filtered
	}
	if options.Output == kranzcli.OutputJSON {
		if err := kranzcli.WriteJSON(stdout, records); err != nil {
			return kranzcli.WriteError(stdout, stderr, options.Output, err)
		}
		return 0
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	// MODE is gone: with the MCP adapter no longer a registry entry, the only
	// values left describe how a runtime was launched, not what it is. CLIENTS
	// answers the question the row could not: who is working in this project.
	_, _ = fmt.Fprintln(w, "ID\tNAME\tPROJECT\tSERVICES\tCLIENTS\tSTATE\tUPTIME")
	for _, record := range records {
		// A bare total says nothing about whether the project is actually up.
		// An unreachable runtime reports "-" rather than a count it cannot know.
		services := "-"
		if record.Services != nil && record.Running != nil {
			services = fmt.Sprintf("%d/%d", *record.Running, *record.Services)
		}
		clients := "-"
		if record.Clients != nil {
			clients = strconv.Itoa(*record.Clients)
		}
		id := record.ID
		if len(id) > 8 {
			id = id[:8]
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", id, record.Name, record.Project, services, clients, record.State, shortDuration(time.Since(record.StartedAt)))
	}
	if err := w.Flush(); err != nil {
		return kranzcli.WriteError(stdout, stderr, options.Output, err)
	}
	return 0
}

// shortDuration renders an age the way a person reads one: the largest unit
// that still says something, never a run of trailing zero units. Every command
// that shows an age uses it, so `ps` and `status` cannot disagree about what
// eight minutes looks like.
func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
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
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return fmt.Errorf("open runtime registry: %w", err)
	}
	lookupCtx, cancelLookup := context.WithTimeout(context.Background(), 2*time.Second)
	record, lookupErr := registry.Resolve(lookupCtx, cfg.RuntimeName(), version)
	cancelLookup()
	if lookupErr == nil {
		client, dialErr := kranzruntime.DialWithIdentity(record.Socket, version,
			kranzruntime.ClientIdentity{Surface: "tui", Label: "Kranz dashboard"})
		if dialErr != nil {
			return classifyRuntimeError(dialErr)
		}
		defer func() { runErr = errors.Join(runErr, client.Close()) }()
		activeConfig := client.Config()
		if activeConfig == nil {
			return errors.New("runtime returned no effective configuration")
		}
		return runAttachedTUI(client, activeConfig)
	}
	var missingRuntime *kranzruntime.SessionNotFoundError
	if !errors.As(lookupErr, &missingRuntime) {
		return classifyRuntimeError(lookupErr)
	}
	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve runtime directory: %w", err)
	}
	session, err := registry.Acquire(cfg.RuntimeName())
	if err != nil {
		var conflict *kranzruntime.SessionConflictError
		if errors.As(err, &conflict) {
			return &kranzcli.Error{Code: "runtime_conflict", Message: conflict.Error(), Hint: "Use `kranz -p " + cfg.RuntimeName() + " attach` or `kranz -p " + cfg.RuntimeName() + " down`.", ExitCode: kranzcli.ExitConflict}
		}
		return fmt.Errorf("acquire runtime session: %w", err)
	}
	defer func() { runErr = errors.Join(runErr, session.Close()) }()
	metadata, err := session.Prepare(cfg.Project, version, "tui", directory)
	if err != nil {
		return fmt.Errorf("prepare runtime session: %w", err)
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

	// The runtime always speaks the socket protocol, even for an ordinary
	// foreground `kranz` with no other client. The published registry session
	// lets ps and later attach/down clients discover this same supervisor.
	local := app.NewLocal(cfg, cfgPaths, app.Options{SessionID: metadata.ID})
	supervisor := kranzruntime.NewSupervisor(local)
	if err := supervisor.Listen(metadata.Socket); err != nil {
		return fmt.Errorf("start runtime supervisor: %w", err)
	}
	if err := session.Publish(); err != nil {
		_ = supervisor.Close()
		return fmt.Errorf("publish runtime session: %w", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- supervisor.Serve() }()
	defer func() {
		// Make the old session undiscoverable before attached clients receive
		// EOF. A supervised MCP bridge may restart immediately on disconnect;
		// it must see a free registry entry rather than stale locked metadata.
		runErr = errors.Join(runErr, session.Close())
		runErr = errors.Join(runErr, supervisor.Close())
		if err := <-serveErr; err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()

	client, err := kranzruntime.DialWithIdentity(metadata.Socket, version,
		kranzruntime.ClientIdentity{Surface: "tui", Label: "Kranz dashboard"})
	if err != nil {
		return fmt.Errorf("connect to runtime: %w", err)
	}
	defer func() { runErr = errors.Join(runErr, client.Close()) }()
	stopOwnership := make(chan struct{})
	ownershipDone := make(chan struct{})
	go func() {
		defer close(ownershipDone)
		// Ownership is registry safety metadata, not UI animation. A one-second
		// cadence keeps force-down evidence current without four full service
		// snapshot RPCs per second while the runtime is idle.
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			_ = session.UpdateOwnership(client.Services())
			select {
			case <-ticker.C:
			case <-stopOwnership:
				return
			}
		}
	}()
	defer func() { close(stopOwnership); <-ownershipDone }()

	darkBackground := lipgloss.HasDarkBackground()
	programReady := make(chan struct{})
	focusReported := make(chan struct{})
	var focusOnce sync.Once
	model := ui.NewModelWithOptions(cfg, version, ui.ModelOptions{
		Settings: userSettings, SettingsPath: settingsPath, ConfigPaths: cfgPaths,
		DarkBackground: &darkBackground, App: client, ProgramReady: func() { close(programReady) },
		FocusReported: func() { focusOnce.Do(func() { close(focusReported) }) },
	})
	var remoteDown atomic.Bool
	defer func() {
		if !remoteDown.Load() {
			runErr = errors.Join(runErr, model.Shutdown())
		}
	}()

	program := tea.NewProgram(model, dashboardProgramOptions()...)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	runDone := make(chan struct{})
	defer close(runDone)
	go runMouseWatchdog(program, mouseWatchdogConfig{
		interval: mouseRecoveryInterval,
		backstop: mouseRecoveryBackstop,
		ready:    programReady,
		relaxed:  focusReported,
		done:     runDone,
	})
	go func() {
		select {
		case <-signals:
			program.Quit()
		case <-supervisor.ShutdownRequested():
			remoteDown.Store(true)
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
