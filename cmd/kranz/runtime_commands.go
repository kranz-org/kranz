package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
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
	selectors := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--no-start":
			noStart = true
		case "-d", "--detach":
			return &kranzcli.Error{Code: "not_implemented", Message: "background mode is not implemented in this build", ExitCode: kranzcli.ExitUsage}
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
	if err := forceDefaultSignal(unixSignal); err != nil {
		return fmt.Errorf("restore default %s disposition: %w", sig, err)
	}
	if err := syscall.Kill(os.Getpid(), unixSignal); err != nil {
		return err
	}
	return nil
}

func resolveSession(options kranzcli.GlobalOptions, requireProject bool) (kranzruntime.SessionRecord, error) {
	reference := options.Project
	if reference == "" {
		if requireProject {
			return kranzruntime.SessionRecord{}, &kranzcli.Error{Code: "missing_project", Message: "-p NAME_OR_ID is required", ExitCode: kranzcli.ExitUsage}
		}
		original, err := os.Getwd()
		if err != nil {
			return kranzruntime.SessionRecord{}, err
		}
		if err := os.Chdir(options.Directory); err != nil {
			return kranzruntime.SessionRecord{}, err
		}
		defer func() { _ = os.Chdir(original) }() // best effort; command performs no work after resolution on failure
		paths := options.ConfigPaths
		if len(paths) == 0 {
			paths, err = config.DiscoverFiles(".")
			if err != nil {
				return kranzruntime.SessionRecord{}, err
			}
		}
		cfg, err := config.LoadFiles(paths)
		if err != nil {
			return kranzruntime.SessionRecord{}, err
		}
		reference = cfg.RuntimeName()
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return kranzruntime.SessionRecord{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	record, err := registry.Resolve(ctx, reference, version)
	if err != nil {
		return kranzruntime.SessionRecord{}, classifyRuntimeError(err)
	}
	return record, nil
}

func runStatus(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	record, err := resolveSession(options, false)
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
	_, _ = fmt.Fprintln(w, "NAME\tSTATE\tPID\tREADY")
	for _, service := range services {
		ready := "-"
		if service.Health.Observed {
			ready = fmt.Sprint(service.Health.Ready)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", service.Name, service.State.Status.String(), service.State.PID, ready)
	}
	return w.Flush()
}

func runDown(options kranzcli.GlobalOptions, args []string) error {
	if len(args) > 0 {
		return &kranzcli.Error{Code: "invalid_arguments", Message: "down does not accept arguments in this build", ExitCode: kranzcli.ExitUsage}
	}
	record, err := resolveSession(options, true)
	if err != nil {
		return err
	}
	client, err := kranzruntime.DialContext(context.Background(), record.Socket, version)
	if err != nil {
		return classifyRuntimeError(err)
	}
	return client.Shutdown()
}

func classifyRuntimeError(err error) error {
	var conflict *kranzruntime.SessionConflictError
	if errors.As(err, &conflict) {
		return &kranzcli.Error{Code: "runtime_conflict", Message: conflict.Error(), Hint: "Use `kranz -p " + conflict.Name + " status` or `kranz -p " + conflict.Name + " down`.", ExitCode: kranzcli.ExitConflict}
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
	var networkError net.Error
	if errors.As(err, &networkError) {
		return &kranzcli.Error{Code: "runtime_unavailable", Message: "runtime is unavailable", ExitCode: kranzcli.ExitUnavailable, Cause: err}
	}
	return err
}
