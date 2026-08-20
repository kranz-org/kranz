package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

type runtimeHost struct {
	session       *kranzruntime.SessionHandle
	supervisor    *kranzruntime.Supervisor
	client        *kranzruntime.Client
	serveErr      chan error
	stopOwnership chan struct{}
	ownershipDone chan struct{}
	restoreDir    func() error
	closed        bool
}

func startRuntime(options kranzcli.GlobalOptions, mode string) (*runtimeHost, *config.Config, error) {
	originalDirectory, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	if err := os.Chdir(options.Directory); err != nil {
		return nil, nil, fmt.Errorf("change directory to %s: %w", options.Directory, err)
	}
	restore := func() error { return os.Chdir(originalDirectory) }
	fail := func(err error) (*runtimeHost, *config.Config, error) { return nil, nil, errors.Join(err, restore()) }

	cfgPaths := options.ConfigPaths
	if len(cfgPaths) == 0 {
		cfgPaths, err = config.DiscoverFiles(".")
		if err != nil {
			return fail(fmt.Errorf("discover configuration: %w", err))
		}
	}
	cfg, err := config.LoadFiles(cfgPaths)
	if err != nil {
		return fail(fmt.Errorf("load configuration: %w", err))
	}
	name := cfg.RuntimeName()
	if options.Project != "" {
		if err := config.ValidateRuntimeName(options.Project); err != nil {
			return fail(err)
		}
		name = options.Project
	}
	directory, err := os.Getwd()
	if err != nil {
		return fail(err)
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return fail(err)
	}
	session, err := registry.Acquire(name)
	if err != nil {
		return fail(err)
	}
	metadata, err := session.Prepare(cfg.Project, version, mode, directory)
	if err != nil {
		_ = session.Close()
		return fail(err)
	}
	local := app.NewLocal(cfg, cfgPaths, app.Options{})
	supervisor := kranzruntime.NewSupervisor(local)
	if err := supervisor.Listen(metadata.Socket); err != nil {
		_ = session.Close()
		return fail(err)
	}
	if err := session.Publish(); err != nil {
		_ = supervisor.Close()
		_ = session.Close()
		return fail(err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- supervisor.Serve() }()
	client, err := kranzruntime.Dial(metadata.Socket, version)
	if err != nil {
		_ = supervisor.Close()
		<-serveErr
		_ = session.Close()
		return fail(err)
	}
	host := &runtimeHost{session: session, supervisor: supervisor, client: client, serveErr: serveErr, stopOwnership: make(chan struct{}), ownershipDone: make(chan struct{}), restoreDir: restore}
	go host.watchOwnership()
	return host, cfg, nil
}

func (h *runtimeHost) watchOwnership() {
	defer close(h.ownershipDone)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		_ = h.session.UpdateOwnership(h.client.Services())
		select {
		case <-ticker.C:
		case <-h.stopOwnership:
			return
		}
	}
}

func (h *runtimeHost) Close() error {
	if h == nil || h.closed {
		return nil
	}
	h.closed = true
	close(h.stopOwnership)
	<-h.ownershipDone
	clientErr := h.client.Close()
	supervisorErr := h.supervisor.Close()
	serveErr := <-h.serveErr
	sessionErr := h.session.Close()
	return errors.Join(clientErr, supervisorErr, serveErr, sessionErr, h.restoreDir())
}

func runUp(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	noStart := false
	detached := false
	selectors := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--no-start":
			noStart = true
		case "-d", "--detach":
			detached = true
		default:
			if strings.HasPrefix(arg, "-") {
				return &kranzcli.Error{Code: "unknown_option", Message: "unknown up option " + arg, ExitCode: kranzcli.ExitUsage}
			}
			selectors = append(selectors, arg)
		}
	}
	if noStart && len(selectors) > 0 {
		return &kranzcli.Error{Code: "invalid_arguments", Message: "--no-start cannot be combined with selectors", ExitCode: kranzcli.ExitUsage}
	}
	if detached {
		if os.Getenv("KRANZ_INTERNAL_BACKGROUND") == "1" {
			_ = os.Unsetenv("KRANZ_INTERNAL_BACKGROUND")
			return runBackgroundChild(options, selectors, noStart)
		}
		return spawnBackground(options, selectors, noStart, stdout)
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	host, cfg, err := startRuntime(options, "foreground")
	if err != nil {
		return classifyRuntimeError(err)
	}
	closed := false
	closeHost := func() error {
		if closed {
			return nil
		}
		closed = true
		return host.Close()
	}
	defer func() { _ = closeHost() }()
	if !noStart {
		if len(selectors) == 0 {
			for _, name := range cfg.ServiceOrder {
				if !cfg.Services[name].Disabled {
					selectors = append(selectors, name)
				}
			}
		}
		for _, name := range selectors {
			if _, ok := cfg.Services[name]; !ok {
				_ = host.client.Shutdown()
				return &kranzcli.Error{Code: "service_not_found", Message: fmt.Sprintf("service %q was not found", name), ExitCode: kranzcli.ExitNotFound}
			}
		}
		startCtx, cancelStart := context.WithCancel(context.Background())
		startDone := make(chan error, 1)
		go func() { startDone <- host.client.StartServicesContext(startCtx, selectors) }()
		select {
		case err := <-startDone:
			cancelStart()
			if err != nil {
				_ = host.client.Shutdown()
				return err
			}
			if err := host.session.UpdateOwnership(host.client.Services()); err != nil {
				_ = host.client.Shutdown()
				return fmt.Errorf("record runtime ownership: %w", err)
			}
		case sig := <-signals:
			cancelStart()
			<-startDone
			return terminateForegroundWithSignal(host, closeHost, signals, sig)
		}
	}
	effectiveName := cfg.RuntimeName()
	if options.Project != "" {
		effectiveName = options.Project
	}
	if _, err := fmt.Fprintf(stdout, "Kranz runtime %s is ready (%s). Press Ctrl+C to stop.\n", cfg.Project, effectiveName); err != nil {
		_ = host.client.Shutdown()
		return err
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	logOffsets := make(map[string]int)
	for {
		select {
		case sig := <-signals:
			return terminateForegroundWithSignal(host, closeHost, signals, sig)
		case <-host.supervisor.ShutdownRequested():
			return closeHost()
		case <-ticker.C:
			for _, service := range host.client.Services() {
				entries := host.client.Logs(service.Name)
				start := logOffsets[service.Name]
				if start > len(entries) {
					start = 0
				}
				for _, entry := range entries[start:] {
					line := strings.TrimRight(entry.Raw, "\r\n")
					if line == "" {
						line = strings.TrimRight(entry.Text, "\r\n")
					}
					_, _ = fmt.Fprintf(stdout, "[%s] %s\n", service.Name, line)
				}
				logOffsets[service.Name] = len(entries)
			}
			if requested, code := host.client.ProjectExitRequested(); requested {
				_ = host.client.Shutdown()
				if err := closeHost(); err != nil {
					return err
				}
				if code != 0 {
					return requestedExitError{code: code}
				}
				return nil
			}
		}
	}
}

type backgroundReady struct {
	OK       bool   `json:"ok"`
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Error    string `json:"error,omitempty"`
	Code     string `json:"code,omitempty"`
	Hint     string `json:"hint,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
}

var newBackgroundCommand = func(executable string, args ...string) *exec.Cmd { return exec.Command(executable, args...) }

func spawnBackground(options kranzcli.GlobalOptions, selectors []string, noStart bool, stdout io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	args := []string{"-C", options.Directory}
	for _, path := range options.ConfigPaths {
		args = append(args, "-f", path)
	}
	if options.Project != "" {
		args = append(args, "-p", options.Project)
	}
	args = append(args, "up", "-d")
	if noStart {
		args = append(args, "--no-start")
	} else {
		args = append(args, selectors...)
	}
	command := newBackgroundCommand(executable, args...)
	if command.Env == nil {
		command.Env = os.Environ()
	}
	command.Env = append(command.Env, "KRANZ_INTERNAL_BACKGROUND=1")
	command.ExtraFiles = []*os.File{writer}
	command.SysProcAttr = backgroundProcessAttributes()
	command.Stdout, command.Stderr = io.Discard, io.Discard
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		_ = writer.Close()
		return err
	}
	defer func() { _ = devNull.Close() }()
	command.Stdin = devNull
	if err := command.Start(); err != nil {
		_ = writer.Close()
		return err
	}
	_ = writer.Close()
	readyResult := make(chan struct {
		ready backgroundReady
		err   error
	}, 1)
	go func() {
		var ready backgroundReady
		decodeErr := json.NewDecoder(reader).Decode(&ready)
		readyResult <- struct {
			ready backgroundReady
			err   error
		}{ready, decodeErr}
	}()
	select {
	case result := <-readyResult:
		if result.err != nil {
			waitErr := command.Wait()
			return fmt.Errorf("background runtime exited before readiness: %w", errors.Join(result.err, waitErr))
		}
		if !result.ready.OK {
			_ = command.Wait()
			exitCode := result.ready.ExitCode
			if exitCode == 0 {
				exitCode = kranzcli.ExitInternal
			}
			code := result.ready.Code
			if code == "" {
				code = "background_start"
			}
			return &kranzcli.Error{Code: code, Message: result.ready.Error, Hint: result.ready.Hint, ExitCode: exitCode}
		}
		if err := command.Process.Release(); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Started %s (%s), PID %d.\n", result.ready.Name, shortID(result.ready.ID), result.ready.PID)
		return err
	case <-time.After(60 * time.Second):
		_ = command.Process.Signal(syscall.SIGTERM)
		waitDone := make(chan error, 1)
		go func() { waitDone <- command.Wait() }()
		select {
		case <-waitDone:
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-waitDone
		}
		return &kranzcli.Error{Code: "background_timeout", Message: "background runtime did not become ready within 1m", ExitCode: kranzcli.ExitUnavailable}
	}
}

func runBackgroundChild(options kranzcli.GlobalOptions, selectors []string, noStart bool) error {
	readyFile := os.NewFile(3, "kranz-readiness")
	if readyFile == nil {
		return errors.New("background readiness descriptor is missing")
	}
	syscall.CloseOnExec(3)
	defer func() { _ = readyFile.Close() }()
	sendReady := func(ready backgroundReady) error { return json.NewEncoder(readyFile).Encode(ready) }
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	host, cfg, err := startRuntime(options, "background")
	if err != nil {
		classified := kranzcli.AsError(classifyRuntimeError(err))
		_ = sendReady(backgroundReady{Error: classified.Error(), Code: classified.Code, Hint: classified.Hint, ExitCode: classified.ExitCode})
		return err
	}
	closed := false
	closeHost := func() error {
		if closed {
			return nil
		}
		closed = true
		return host.Close()
	}
	defer func() { _ = closeHost() }()
	if !noStart {
		if len(selectors) == 0 {
			for _, name := range cfg.ServiceOrder {
				if !cfg.Services[name].Disabled {
					selectors = append(selectors, name)
				}
			}
		}
		for _, name := range selectors {
			if _, ok := cfg.Services[name]; !ok {
				_ = host.client.Shutdown()
				commandErr := &kranzcli.Error{Code: "service_not_found", Message: fmt.Sprintf("service %q was not found", name), ExitCode: kranzcli.ExitNotFound}
				_ = sendReady(backgroundReady{Error: commandErr.Error(), Code: commandErr.Code, ExitCode: commandErr.ExitCode})
				return commandErr
			}
		}
		startCtx, cancelStart := context.WithCancel(context.Background())
		startDone := make(chan error, 1)
		go func() { startDone <- host.client.StartServicesContext(startCtx, selectors) }()
		select {
		case err = <-startDone:
			cancelStart()
		case <-signals:
			cancelStart()
			<-startDone
			_ = host.client.Shutdown()
			return closeHost()
		}
		if err != nil {
			_ = host.client.Shutdown()
			commandErr := kranzcli.AsError(err)
			_ = sendReady(backgroundReady{Error: commandErr.Error(), Code: commandErr.Code, ExitCode: commandErr.ExitCode})
			return err
		}
		if err := host.session.UpdateOwnership(host.client.Services()); err != nil {
			_ = host.client.Shutdown()
			commandErr := kranzcli.AsError(fmt.Errorf("record runtime ownership: %w", err))
			_ = sendReady(backgroundReady{Error: commandErr.Error(), Code: commandErr.Code, ExitCode: commandErr.ExitCode})
			return err
		}
	}
	metadata := host.session.Metadata()
	select {
	case <-signals:
		_ = host.client.Shutdown()
		return closeHost()
	default:
	}
	if err := sendReady(backgroundReady{OK: true, ID: metadata.ID, Name: metadata.Name, PID: metadata.PID}); err != nil {
		_ = host.client.Shutdown()
		return err
	}
	_ = readyFile.Close()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-host.supervisor.ShutdownRequested():
			return closeHost()
		case <-signals:
			_ = host.client.Shutdown()
			return closeHost()
		case <-ticker.C:
			if requested, code := host.client.ProjectExitRequested(); requested {
				_ = host.client.Shutdown()
				if err := closeHost(); err != nil {
					return err
				}
				if code != 0 {
					return requestedExitError{code: code}
				}
				return nil
			}
		}
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func terminateForegroundWithSignal(host *runtimeHost, closeHost func() error, signals chan os.Signal, sig os.Signal) error {
	shutdownErr := host.client.Shutdown()
	closeErr := closeHost()
	if err := errors.Join(shutdownErr, closeErr); err != nil {
		return err
	}
	signal.Stop(signals)
	signal.Reset(sig)
	unixSignal, ok := sig.(syscall.Signal)
	if !ok {
		return fmt.Errorf("unsupported signal %v", sig)
	}
	if err := reraiseDefaultSignal(unixSignal); err != nil {
		return fmt.Errorf("re-raise %s with default disposition: %w", sig, err)
	}
	return fmt.Errorf("re-raising %s with default disposition did not terminate the process", sig)
}

// resolveSession finds the runtime a command applies to. An explicit -p always
// wins, including from a directory that has a Kranz project of its own, so any
// command can be aimed at another project without leaving the current one.
// Without -p the runtime is the one named by the configuration in the working
// directory, so every command agrees about "the project I am standing in"
// instead of some commands knowing it and others demanding it be spelled out.
func resolveSession(options kranzcli.GlobalOptions) (kranzruntime.SessionRecord, error) {
	reference := options.Project
	if reference == "" {
		name, err := runtimeNameFromDirectory(options)
		if err != nil {
			return kranzruntime.SessionRecord{}, err
		}
		reference = name
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return kranzruntime.SessionRecord{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	record, err := registry.Resolve(ctx, reference, version)
	if err != nil {
		return kranzruntime.SessionRecord{}, classifyMissingRuntime(err, options, reference)
	}
	return record, nil
}

// classifyMissingRuntime turns "not found" into advice. A project that has not
// been started is the most common reason any of these commands fails, and
// naming the runtime the user never mentioned explains nothing: what they need
// is the command that would make it exist.
func classifyMissingRuntime(err error, options kranzcli.GlobalOptions, reference string) error {
	classified := classifyRuntimeError(err)
	var commandError *kranzcli.Error
	if !errors.As(classified, &commandError) || commandError.Code != "runtime_not_found" {
		return classified
	}
	if options.Project == "" {
		return &kranzcli.Error{
			Code:     "runtime_not_found",
			Message:  fmt.Sprintf("this project has no runtime running (it would be called %q)", reference),
			Hint:     "Start it with `kranz up -d`, or run `kranz ps` to see what is running.",
			ExitCode: kranzcli.ExitNotFound,
		}
	}
	return &kranzcli.Error{
		Code:     "runtime_not_found",
		Message:  commandError.Message,
		Hint:     "Run `kranz ps` to see the runtimes that are active.",
		ExitCode: kranzcli.ExitNotFound,
	}
}

// runtimeNameFromDirectory reads the runtime name the working directory
// declares. A directory with no configuration is a usage problem rather than a
// missing runtime, so it says how to aim the command instead of reporting that
// some unnamed runtime could not be found.
func runtimeNameFromDirectory(options kranzcli.GlobalOptions) (string, error) {
	original, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if err := os.Chdir(options.Directory); err != nil {
		return "", err
	}
	defer func() { _ = os.Chdir(original) }() // best effort; command performs no work after resolution on failure
	paths := options.ConfigPaths
	if len(paths) == 0 {
		paths, err = config.DiscoverFiles(".")
		if err != nil {
			return "", &kranzcli.Error{
				Code:     "no_project",
				Message:  "no Kranz configuration was found in this directory",
				Hint:     "Run from a project directory, pass -f PATH, or name a runtime with -p NAME_OR_ID.",
				ExitCode: kranzcli.ExitUsage,
				Cause:    err,
			}
		}
	}
	cfg, err := config.LoadFiles(paths)
	if err != nil {
		return "", err
	}
	return cfg.RuntimeName(), nil
}

func runStatus(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	record, err := resolveSession(options)
	if err != nil {
		return err
	}
	client, err := kranzruntime.DialContext(context.Background(), record.Socket, version)
	if err != nil {
		return classifyRuntimeError(err)
	}
	defer func() { _ = client.Close() }()
	services := client.Services()
	if len(args) > 0 {
		selected := make([]*app.ServiceSnapshot, 0, len(args))
		for _, name := range args {
			service, ok := client.Service(name)
			if !ok {
				return &kranzcli.Error{Code: "service_not_found", Message: fmt.Sprintf("service %q was not found", name), ExitCode: kranzcli.ExitNotFound}
			}
			selected = append(selected, service)
		}
		services = selected
	}
	if options.Output == kranzcli.OutputJSON {
		type statusService struct {
			Name          string `json:"name"`
			State         string `json:"state"`
			PID           int    `json:"pid"`
			Ready         *bool  `json:"ready"`
			Alive         *bool  `json:"alive"`
			DetectedPorts []int  `json:"detected_ports"`
		}
		safe := make([]statusService, 0, len(services))
		for _, service := range services {
			var ready, alive *bool
			if service.Health.Observed {
				readyValue, aliveValue := service.Health.Ready, service.Health.Alive
				ready, alive = &readyValue, &aliveValue
			}
			safe = append(safe, statusService{Name: service.Name, State: service.State.Status.String(), PID: service.State.PID, Ready: ready, Alive: alive, DetectedPorts: service.DetectedPorts})
		}
		return kranzcli.WriteJSON(stdout, struct {
			Session  kranzruntime.SessionMetadata `json:"session"`
			Services []statusService              `json:"services"`
		}{record.SessionMetadata, safe})
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSTATE\tHEALTH\tUPTIME\tPID\tPORTS")
	for _, service := range services {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			service.Name,
			service.State.Status.String(),
			healthLabel(service),
			serviceUptime(service),
			pidLabel(service.State.PID),
			joinPortsOrDash(service.DetectedPorts),
		)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	// The exit code of a stopped service is the first thing anyone asks about,
	// and it does not fit a column that is empty for every healthy service.
	for _, service := range services {
		if service.State.ExitError != "" {
			_, _ = fmt.Fprintf(stdout, "\n%s: %s\n", service.Name, service.State.ExitError)
		}
	}
	return nil
}

// healthLabel reports readiness in words. A bare true/false column made a
// service with no health check look like a failing one.
func healthLabel(service *app.ServiceSnapshot) string {
	if !service.Health.Observed {
		return "-"
	}
	if service.Health.Ready {
		return "ready"
	}
	return "not ready"
}

// pidLabel hides the zero a detached or stopped service reports. Printing 0 as
// a process id says something untrue about a service that is running fine.
func pidLabel(pid int) string {
	if pid <= 0 {
		return "-"
	}
	return strconv.Itoa(pid)
}

func serviceUptime(service *app.ServiceSnapshot) string {
	if service.State.Status != config.StatusRunning || service.State.StartedAt.IsZero() {
		return "-"
	}
	return shortDuration(time.Since(service.State.StartedAt))
}

func runLifecycle(options kranzcli.GlobalOptions, command string, args []string, stdout io.Writer) error {
	if command == "reload" {
		if len(args) != 0 {
			return &kranzcli.Error{Code: "invalid_arguments", Message: "reload does not accept selectors", ExitCode: kranzcli.ExitUsage}
		}
	} else if len(args) == 0 {
		return &kranzcli.Error{Code: "missing_selector", Message: command + " requires at least one service or tag selector", ExitCode: kranzcli.ExitUsage}
	}
	record, err := resolveSession(options)
	if err != nil {
		return err
	}
	client, err := kranzruntime.DialContext(context.Background(), record.Socket, version)
	if err != nil {
		return classifyRuntimeError(err)
	}
	defer func() { _ = client.Close() }()
	if command == "reload" {
		result, reloadErr := client.Reload(true)
		if reloadErr != nil {
			return reloadErr
		}
		reportReload(stdout, options, record.Name, result)
		return nil
	}
	names, err := resolveServiceSelectors(client.Config(), args)
	if err != nil {
		return err
	}
	// stop and restart expand to dependents, so what the user named is not what
	// the command touched. The expansion is read before acting, while the
	// services are still in the state that produced it.
	affected := names
	if command == "stop" || command == "restart" {
		affected = expandToDependents(client, names)
	}
	switch command {
	case "start":
		err = client.StartServicesContext(context.Background(), names)
	case "stop":
		err = client.StopServices(names)
	case "restart":
		err = client.RestartServices(names)
	}
	if err != nil {
		return err
	}
	names = affected
	// A command that changes something says what it changed. Silence is
	// indistinguishable from a no-op, and a selector that expands to dependents
	// changes more than the user named.
	reportLifecycle(stdout, options, command, names)
	return nil
}

// expandToDependents returns the services a stop or restart will actually
// touch, in the runtime's own order, without repeating one twice.
func expandToDependents(client *kranzruntime.Client, names []string) []string {
	seen := make(map[string]bool, len(names))
	expanded := make([]string, 0, len(names))
	for _, name := range names {
		for _, affected := range client.AffectedServices(name) {
			if seen[affected] {
				continue
			}
			seen[affected] = true
			expanded = append(expanded, affected)
		}
		if !seen[name] {
			seen[name] = true
			expanded = append(expanded, name)
		}
	}
	return expanded
}

var lifecyclePastTense = map[string]string{"start": "Started", "stop": "Stopped", "restart": "Restarted"}

func reportLifecycle(stdout io.Writer, options kranzcli.GlobalOptions, command string, names []string) {
	if options.Output == kranzcli.OutputJSON {
		return
	}
	_, _ = fmt.Fprintf(stdout, "%s %s.\n", lifecyclePastTense[command], strings.Join(names, ", "))
}

func reportReload(stdout io.Writer, options kranzcli.GlobalOptions, name string, result app.ReloadResult) {
	if options.Output == kranzcli.OutputJSON {
		return
	}
	changed := len(result.Added) + len(result.Removed) + len(result.Restarted) + len(result.Updated)
	if changed == 0 {
		_, _ = fmt.Fprintf(stdout, "Reloaded %s. Nothing changed.\n", name)
		return
	}
	_, _ = fmt.Fprintf(stdout, "Reloaded %s.\n", name)
	// Ordered explicitly: a map would print the same reload differently on
	// consecutive runs.
	for _, group := range []struct {
		label    string
		services []string
	}{
		{"added", result.Added},
		{"removed", result.Removed},
		{"restarted", result.Restarted},
		{"updated", result.Updated},
	} {
		if len(group.services) > 0 {
			_, _ = fmt.Fprintf(stdout, "  %s: %s\n", group.label, strings.Join(group.services, ", "))
		}
	}
}

func resolveServiceSelectors(cfg *config.Config, selectors []string) ([]string, error) {
	selected := make(map[string]bool)
	for _, selector := range selectors {
		if _, ok := cfg.Services[selector]; ok {
			selected[selector] = true
			continue
		}
		matched := false
		for name, service := range cfg.Services {
			for _, tag := range service.Tags {
				if strings.EqualFold(tag, selector) {
					selected[name] = true
					matched = true
					break
				}
			}
		}
		if !matched {
			return nil, &kranzcli.Error{Code: "selector_not_found", Message: fmt.Sprintf("service or tag %q was not found", selector), ExitCode: kranzcli.ExitNotFound}
		}
	}
	names := make([]string, 0, len(selected))
	for _, name := range cfg.ServiceOrder {
		if selected[name] {
			names = append(names, name)
		}
	}
	return names, nil
}

func runDown(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	force := false
	for _, arg := range args {
		if arg == "--force" {
			force = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return &kranzcli.Error{Code: "unknown_option", Message: fmt.Sprintf("unknown down option %q", arg), ExitCode: kranzcli.ExitUsage}
		}
		// down is deliberately project-wide. A service name here is a
		// misdirected stop, not a malformed flag, so it is answered as one.
		return &kranzcli.Error{
			Code:     "invalid_arguments",
			Message:  "down stops the whole runtime and does not take service selectors",
			Hint:     fmt.Sprintf("Stop one service with `kranz stop %s`, or stop everything with `kranz down`.", arg),
			ExitCode: kranzcli.ExitUsage,
		}
	}
	record, err := resolveSession(options)
	if err != nil {
		return err
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	client, dialErr := kranzruntime.DialContext(dialCtx, record.Socket, version)
	cancel()
	if dialErr == nil {
		if err := client.Shutdown(); err != nil {
			return err
		}
		reportDown(stdout, options, record.Name, shortID(record.ID), false)
		return nil
	}
	if !force {
		return classifyRuntimeError(dialErr)
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return err
	}
	forceCtx, forceCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer forceCancel()
	if err := registry.ForceDown(forceCtx, record); err != nil {
		return classifyRuntimeError(err)
	}
	reportDown(stdout, options, record.Name, shortID(record.ID), true)
	return nil
}

// reportDown names the runtime that stopped. A bare prompt after `down` leaves
// the user checking `ps` to find out whether anything happened, and a forced
// stop is worth distinguishing from an orderly one.
func reportDown(stdout io.Writer, options kranzcli.GlobalOptions, name, id string, forced bool) {
	if options.Output == kranzcli.OutputJSON {
		return
	}
	if forced {
		_, _ = fmt.Fprintf(stdout, "Force-stopped %s (%s). Services it owned may have survived; check with `kranz ps`.\n", name, id)
		return
	}
	_, _ = fmt.Fprintf(stdout, "Stopped %s (%s).\n", name, id)
}

func classifyRuntimeError(err error) error {
	var conflict *kranzruntime.SessionConflictError
	if errors.As(err, &conflict) {
		return &kranzcli.Error{Code: "runtime_conflict", Message: conflict.Error(), Hint: "Inspect it with `kranz status`, stop it with `kranz down`, or start a second one with `kranz -p " + conflict.Name + "-2 up -d`.", ExitCode: kranzcli.ExitConflict}
	}
	var missing *kranzruntime.SessionNotFoundError
	if errors.As(err, &missing) {
		return &kranzcli.Error{Code: "runtime_not_found", Message: missing.Error(), ExitCode: kranzcli.ExitNotFound}
	}
	var ambiguous *kranzruntime.AmbiguousSessionError
	if errors.As(err, &ambiguous) {
		return &kranzcli.Error{Code: "ambiguous_runtime", Message: ambiguous.Error(), ExitCode: kranzcli.ExitConflict}
	}
	var mismatch *kranzruntime.VersionMismatchError
	if errors.As(err, &mismatch) {
		return &kranzcli.Error{Code: "protocol_mismatch", Message: mismatch.Error(), ExitCode: kranzcli.ExitUnavailable}
	}
	var refused *kranzruntime.ForceDownError
	if errors.As(err, &refused) {
		return &kranzcli.Error{Code: "force_down_refused", Message: refused.Error(), ExitCode: kranzcli.ExitUnavailable}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return &kranzcli.Error{Code: "runtime_unavailable", Message: "runtime is unavailable", ExitCode: kranzcli.ExitUnavailable, Cause: err}
	}
	return err
}
