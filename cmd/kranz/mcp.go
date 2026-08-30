package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/kranz-org/kranz/internal/app"
	kranzcli "github.com/kranz-org/kranz/internal/cli"
	kranzmcp "github.com/kranz-org/kranz/internal/mcp"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

var mcpStdin io.Reader = os.Stdin

// runMCP starts the stdio adapter. It needs no project: a Kranz configuration
// in the working directory becomes the default address for calls that name no
// runtime, and its absence is not an error.
func runMCP(options kranzcli.GlobalOptions, attachOnly bool, stdout, stderr io.Writer) error {
	if options.Output != kranzcli.OutputText {
		return errors.New("--output is not valid for MCP stdio; stdout is reserved for JSON-RPC framing")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runMCPContextOptions(ctx, options, attachOnly, mcpStdin, stdout, stderr)
}

func runMCPContext(ctx context.Context, options kranzcli.GlobalOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	return runMCPContextOptions(ctx, options, false, stdin, stdout, stderr)
}

// runMCPContextOptions serves MCP over stdio without owning anything. The
// process writes no registry entry and supervises no project: it is a client
// of runtimes, and which runtime answers is decided per call.
func runMCPContextOptions(ctx context.Context, options kranzcli.GlobalOptions, attachOnly bool, stdin io.Reader, stdout, stderr io.Writer) error {
	// --attach-only described the old owner fallback, which no longer exists.
	// Accepting it silently keeps existing registrations working.
	_ = attachOnly
	resolver, err := newMCPResolver(options)
	if err != nil {
		return err
	}
	defer func() { _ = resolver.Close() }()
	server := kranzmcp.NewServer(resolver, version, stdin, stdout, stderr)
	serveErr := server.Serve(ctx)
	if errors.Is(serveErr, context.Canceled) {
		serveErr = nil
	}
	return serveErr
}

// newMCPResolver builds the addressing rules for this process: the -C/-p pin
// when there is one, the working directory as a lookup when there is not, and
// a launcher that starts detached runtimes rather than hosting them.
func newMCPResolver(options kranzcli.GlobalOptions) (*kranzmcp.Resolver, error) {
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return nil, err
	}
	pin := options.Project
	if pin == "" && optionsPinDirectory(options) {
		// -C names a project explicitly, so it pins even though the reference
		// has to be discovered from that directory's configuration. A -C that
		// holds no configuration is not an error: starting outside a project
		// is the normal case, and the server simply stays unbound.
		if name, nameErr := runtimeNameFromDirectory(options); nameErr == nil {
			pin = name
		}
	}
	directory := options.Directory
	if absolute, absErr := filepath.Abs(directory); absErr == nil {
		directory = absolute
	}
	return kranzmcp.NewResolver(kranzmcp.ResolverOptions{
		Version:          version,
		Registry:         registry,
		Pin:              pin,
		ProjectDirectory: directory,
		Directory: func() (string, error) {
			return runtimeNameFromDirectory(options)
		},
		Dial: func(ctx context.Context, record kranzruntime.SessionRecord) (app.API, func() error, error) {
			client, dialErr := kranzruntime.DialContextWithIdentity(ctx, record.Socket, version,
				kranzruntime.ClientIdentity{Surface: "mcp", Label: mcpClientLabel()})
			if dialErr != nil {
				return nil, nil, dialErr
			}
			return client, client.Close, nil
		},
		Launch: func(ctx context.Context, directory string) (kranzruntime.SessionRecord, bool, error) {
			return launchDetachedRuntime(ctx, options, directory)
		},
	}), nil
}

// optionsPinDirectory reports whether -C or -f was given explicitly. Without
// either, the working directory is a default rather than a statement about
// which project this server speaks for, and it must not pin.
func optionsPinDirectory(options kranzcli.GlobalOptions) bool {
	return options.DirectoryExplicit || len(options.ConfigPaths) > 0
}

// mcpClientLabel names this process in `kranz clients`. The launcher that
// spawned it is the useful identity, and it is the one thing the MCP process
// knows about its caller before any call arrives.
func mcpClientLabel() string {
	if client := os.Getenv("KRANZ_MCP_CLIENT"); client != "" {
		return "MCP: " + client
	}
	return "Kranz MCP"
}

// launchDetachedRuntime starts a runtime the same way `kranz up -d --no-start`
// does, in its own process. The MCP process must not host a supervisor: it
// serves many projects, a hosted runtime would tie it to one directory, and an
// agent disconnecting would take the project down with it. The bool distinguishes
// a process spawned here from an already-running runtime returned by discovery.
func launchDetachedRuntime(ctx context.Context, options kranzcli.GlobalOptions, directory string) (kranzruntime.SessionRecord, bool, error) {
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return kranzruntime.SessionRecord{}, false, err
	}
	target := options
	target.Directory = directory
	target.ConfigPaths = nil
	target.Project = ""
	name, err := runtimeNameFromDirectory(target)
	if err != nil {
		return kranzruntime.SessionRecord{}, false, err
	}
	if record, resolveErr := registry.Resolve(ctx, name, version); resolveErr == nil && record.State == kranzruntime.SessionRunning {
		return record, false, nil
	}
	if err := spawnBackground(target, nil, true, io.Discard); err != nil {
		return kranzruntime.SessionRecord{}, false, err
	}
	record, err := registry.Resolve(ctx, name, version)
	return record, err == nil, err
}
