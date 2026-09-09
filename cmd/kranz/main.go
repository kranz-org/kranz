// Package main provides the Kranz command-line entry point.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
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
		if invocation.Globals.Output == kranzcli.OutputJSON {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, textOnlyOutputError("help"))
		}
		output, helpErr := kranzcli.Help(tree, invocation.CommandPath)
		if helpErr != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, helpErr)
		}
		if _, err := fmt.Fprint(stdout, output); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	}
	// A command whose grammar is reserved but whose execution a future change
	// still has to attach is refused from the tree itself, so help and
	// dispatch can never disagree about what this build supports.
	if len(invocation.CommandPath) > 0 {
		if command, resolveErr := tree.Resolve(invocation.CommandPath); resolveErr == nil && command.IsPlanned() {
			err := &kranzcli.Error{
				Code:     "not_implemented",
				Message:  fmt.Sprintf("command %q is not implemented yet", invocation.Command()),
				Hint:     "It is planned for a future release. Run `kranz --help` for the commands this build supports.",
				ExitCode: kranzcli.ExitUsage,
			}
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
	}
	if invocation.Command() == "version" {
		return writeVersion(stdout, stderr, invocation.Globals.Output)
	}
	if invocation.Command() == "mcp" {
		for _, arg := range invocation.Args {
			_, _ = fmt.Fprintf(stderr, "Kranz MCP: unknown mcp option or argument %q\n", arg)
			return kranzcli.ExitUsage
		}
		if err := runMCP(invocation.Globals, stdout, stderr); err != nil {
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
		if err := runDoctor(invocation.Globals, invocation.Args, stdout); err != nil {
			var requested requestedExitError
			if errors.As(err, &requested) {
				return requested.code
			}
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "services list":
		if err := runServices(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "tags":
		if err := runTags(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "services info":
		if err := runServiceInfo(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "project":
		if err := runProject(invocation.Globals, invocation.Args, stdout); err != nil {
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
	case "runs retention":
		if err := runRunsRetention(invocation.Globals, invocation.Args, stdout); err != nil {
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
	case "ports list":
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
		if invocation.Globals.Output == kranzcli.OutputJSON {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, textOnlyOutputError("completion"))
		}
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
	case "actions list":
		if err := runActionList(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "actions info":
		if err := runActionInfo(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "actions run":
		if err := runActionRun(invocation.Globals, invocation.Args, stdout); err != nil {
			var requested requestedExitError
			if errors.As(err, &requested) {
				return requested.code
			}
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	case "ports inspect":
		if err := runPortInspect(invocation.Globals, invocation.Args, stdout); err != nil {
			return kranzcli.WriteError(stdout, stderr, invocation.Globals.Output, err)
		}
		return 0
	}

	if invocation.Command() == "ps" {
		return runPS(invocation.Globals, invocation.Args, stdout, stderr)
	}
	if invocation.Command() == "clients" {
		return runClients(invocation.Globals, invocation.Args, stdout, stderr)
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

func textOnlyOutputError(command string) error {
	return &kranzcli.Error{
		Code:     "unsupported_output",
		Message:  fmt.Sprintf("%s produces text and does not support --output=json", command),
		Hint:     fmt.Sprintf("Run `kranz %s` without --output=json.", command),
		ExitCode: kranzcli.ExitUsage,
	}
}

func containsMCPCommand(args []string) bool {
	for _, arg := range args {
		if arg == "mcp" {
			return true
		}
	}
	return false
}

func runPS(options kranzcli.GlobalOptions, args []string, stdout, stderr io.Writer) int {
	query, err := parseWatchQuery("ps", options.Output, args, "name", "project", "state", "client")
	if err != nil {
		return kranzcli.WriteError(stdout, stderr, options.Output, err)
	}
	if len(query.args) > 0 {
		return kranzcli.WriteError(stdout, stderr, options.Output, watchUsageError("ps", fmt.Sprintf("unexpected argument %q", query.args[0])))
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return kranzcli.WriteError(stdout, stderr, options.Output, err)
	}
	err = runWatch(query, stdout, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		records, listErr := registry.List(ctx, version)
		if listErr != nil {
			return listErr
		}
		filtered := records[:0]
		for _, record := range records {
			if options.Project != "" && !matchesRuntimeReference(record, options.Project) {
				continue
			}
			if !matchesWatchFilters(query.filters, map[string][]string{
				"name": {record.Name}, "project": {record.Project}, "state": {string(record.State)}, "client": record.ClientSurfaces,
			}) {
				continue
			}
			filtered = append(filtered, record)
		}
		return writePS(options.Output, query.formatter, filtered, stdout)
	})
	if err != nil {
		return kranzcli.WriteError(stdout, stderr, options.Output, err)
	}
	return 0
}

func writePS(output kranzcli.OutputFormat, formatter *rowTemplate, records []kranzruntime.SessionRecord, stdout io.Writer) error {
	if output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, records)
	}
	if formatter != nil {
		rows := make([]map[string]any, 0, len(records))
		for _, record := range records {
			rows = append(rows, psFormatRow(record))
		}
		return formatter.write(stdout, psFormatHeaders(), rows)
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	// MODE is gone: with the MCP adapter no longer a registry entry, the only
	// values left describe how a runtime was launched, not what it is. CLIENTS
	// answers the question the row could not: who is working in this project.
	_, _ = fmt.Fprintln(w, "ID\tPID\tNAME\tPROJECT\tSERVICES\tCLIENTS\tSTATE\tUPTIME")
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
		_, _ = fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n", id, record.PID, record.Name, record.Project, services, clients, record.State, shortDuration(time.Since(record.StartedAt)))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return nil
}

func psFormatHeaders() map[string]any {
	return map[string]any{
		"ID": "ID", "FullID": "FULL ID", "PID": "PID", "Name": "NAME", "Project": "PROJECT",
		"Services": "SERVICES", "Clients": "CLIENTS", "State": "STATE",
		"Uptime": "UPTIME", "Directory": "DIRECTORY", "Mode": "MODE",
		"Version": "VERSION", "StartedAt": "STARTED AT",
	}
}

func psFormatRow(record kranzruntime.SessionRecord) map[string]any {
	services := "-"
	if record.Services != nil && record.Running != nil {
		services = fmt.Sprintf("%d/%d", *record.Running, *record.Services)
	}
	clients := "-"
	if record.Clients != nil {
		clients = strconv.Itoa(*record.Clients)
	}
	return map[string]any{
		"ID": shortID(record.ID), "FullID": record.ID, "PID": record.PID, "Name": record.Name,
		"Project": record.Project, "Services": services, "Clients": clients,
		"State": string(record.State), "Uptime": shortDuration(time.Since(record.StartedAt)),
		"Directory": record.Directory, "Mode": record.Mode, "Version": record.KranzVersion,
		"StartedAt": record.StartedAt.Format(time.RFC3339),
	}
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
			// Automatic discovery, not an explicitly named path, found
			// nothing here: this is the one case where an already-running
			// local runtime is worth offering instead of failing outright
			// (PRD 3.1, Scenario C). An invalid configuration that WAS
			// found is a different error, returned below unchanged.
			return runBareWithoutConfig(options)
		}
	}
	cfg, err := config.LoadFiles(cfgPaths)
	if err != nil {
		return &kranzcli.Error{Code: "invalid_config", Message: "load configuration", ExitCode: kranzcli.ExitConfig, Cause: err}
	}
	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve runtime directory: %w", err)
	}
	record, err := resolveOrStartDashboardRuntime(options, cfgPaths, cfg.RuntimeName(), directory)
	if err != nil {
		return err
	}

	client, err := kranzruntime.DialWithIdentity(record.Socket, version,
		kranzruntime.ClientIdentity{Surface: "tui", Label: "Kranz dashboard"})
	if err != nil {
		return classifyRuntimeError(err)
	}
	defer func() { runErr = errors.Join(runErr, client.Close()) }()
	activeConfig := client.Config()
	if activeConfig == nil {
		return errors.New("runtime returned no effective configuration")
	}
	return runAttachedTUI(client, activeConfig, record, options)
}

// makeRestartRuntime builds the "Restart runtime" callback for the recovery
// screen: the exact background-launch mechanism the ordinary bare-launch
// path already uses, aimed at a specific project directory and config paths
// instead of the current working directory. It never builds a shell string;
// spawnBackground always execs the Kranz binary with an explicit argv.
func makeRestartRuntime(base kranzcli.GlobalOptions) func(directory string, configPaths []string) error {
	return func(directory string, configPaths []string) error {
		options := base
		options.Directory = directory
		options.ConfigPaths = configPaths
		options.Project = ""
		options.Output = kranzcli.OutputText
		return spawnBackground(options, nil, false, io.Discard)
	}
}

func resolveOrStartDashboardRuntime(options kranzcli.GlobalOptions, cfgPaths []string, runtimeName, directory string) (kranzruntime.SessionRecord, error) {
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return kranzruntime.SessionRecord{}, fmt.Errorf("open runtime registry: %w", err)
	}
	resolve := func() (kranzruntime.SessionRecord, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return registry.Resolve(ctx, runtimeName, version)
	}
	record, lookupErr := resolve()
	if lookupErr == nil {
		return record, nil
	}
	var missingRuntime *kranzruntime.SessionNotFoundError
	if !errors.As(lookupErr, &missingRuntime) {
		return kranzruntime.SessionRecord{}, classifyRuntimeError(lookupErr)
	}

	backgroundOptions := options
	backgroundOptions.Directory = directory
	backgroundOptions.ConfigPaths = cfgPaths
	backgroundOptions.Output = kranzcli.OutputText
	if err := spawnBackground(backgroundOptions, nil, false, io.Discard); err != nil {
		return kranzruntime.SessionRecord{}, classifyRuntimeError(err)
	}
	record, err = resolve()
	if err != nil {
		return kranzruntime.SessionRecord{}, classifyRuntimeError(err)
	}
	return record, nil
}
