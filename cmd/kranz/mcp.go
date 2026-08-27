package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	kranzmcp "github.com/kranz-org/kranz/internal/mcp"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

var mcpStdin io.Reader = os.Stdin

// runMCP attaches first. Only a proven absence for the current configured
// runtime permits owner fallback; ambiguous, unreachable, stale-but-locked,
// and incompatible records are returned without attempting Acquire.
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

func runMCPContextOptions(ctx context.Context, options kranzcli.GlobalOptions, attachOnly bool, stdin io.Reader, stdout, stderr io.Writer) error {
	client, metadata, mode, owner, err := connectMCP(options, attachOnly)
	if err != nil {
		return err
	}
	closed := false
	cleanup := func() error {
		if closed {
			return nil
		}
		closed = true
		if owner == nil {
			return client.Close()
		}
		shutdownErr := client.Shutdown()
		return errors.Join(shutdownErr, owner.Close())
	}
	defer func() { _ = cleanup() }()

	serveCtx, cancelServe := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		if owner == nil {
			select {
			case <-serveCtx.Done():
			case <-client.Done():
				cancelServe()
			}
			return
		}
		select {
		case <-serveCtx.Done():
		case <-client.Done():
			cancelServe()
		case <-owner.supervisor.ShutdownRequested():
			cancelServe()
		}
	}()
	identity := kranzmcp.SessionFromMetadata(metadata, mode)
	if owner != nil {
		identity.OwnerReason = "created_missing_runtime"
	}
	server := kranzmcp.NewServer(client, identity, stdin, stdout, stderr)
	err = server.Serve(serveCtx)
	cancelServe()
	<-watchDone
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	return errors.Join(err, cleanup())
}

func connectMCP(options kranzcli.GlobalOptions, attachOnly ...bool) (*kranzruntime.Client, kranzruntime.SessionMetadata, string, *runtimeHost, error) {
	reference := options.Project
	if reference == "" {
		name, err := runtimeNameFromDirectory(options)
		if err != nil {
			return nil, kranzruntime.SessionMetadata{}, "", nil, err
		}
		reference = name
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return nil, kranzruntime.SessionMetadata{}, "", nil, err
	}
	resolveCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	record, resolveErr := registry.ResolveForAttach(resolveCtx, reference, version)
	cancel()
	if resolveErr == nil {
		return attachMCPRecord(reference, record)
	}
	var notFound *kranzruntime.SessionNotFoundError
	if errors.As(resolveErr, &notFound) && len(attachOnly) > 0 && attachOnly[0] {
		return nil, kranzruntime.SessionMetadata{}, "", nil, mcpAttachOnlyError(registry, reference)
	}
	if !errors.As(resolveErr, &notFound) || options.Project != "" {
		return nil, kranzruntime.SessionMetadata{}, "", nil, fmt.Errorf("resolve runtime %s: %w", reference, resolveErr)
	}
	host, _, err := startRuntime(options, "mcp")
	if err != nil {
		// Another TUI/MCP process may have won Acquire after our absence check.
		// Re-resolve once and attach only to a now-proven compatible runtime.
		retryCtx, retryCancel := context.WithTimeout(context.Background(), 2*time.Second)
		record, retryErr := registry.ResolveForAttach(retryCtx, reference, version)
		retryCancel()
		if retryErr == nil {
			return attachMCPRecord(reference, record)
		}
		return nil, kranzruntime.SessionMetadata{}, "", nil, fmt.Errorf("create MCP-owned runtime: %w", err)
	}
	return host.client, host.session.Metadata(), "owner", host, nil
}

func mcpAttachOnlyError(registry *kranzruntime.Registry, reference string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	records, err := registry.List(ctx, version)
	if err != nil {
		return fmt.Errorf("runtime %q is not running; refusing to create one (--attach-only); list runtimes: %w", reference, err)
	}
	running := make([]string, 0)
	for _, record := range records {
		if record.State != kranzruntime.SessionRunning {
			continue
		}
		count := "services unknown"
		if record.Services != nil {
			count = fmt.Sprintf("%d services", *record.Services)
		}
		id := record.ID
		if len(id) > 8 {
			id = id[:8]
		}
		running = append(running, fmt.Sprintf("%s (%s, %s)", record.Name, id, count))
	}
	if len(running) == 0 {
		return fmt.Errorf("runtime %q is not running; refusing to create one (--attach-only); no other runtimes are running", reference)
	}
	return fmt.Errorf("runtime %q is not running; refusing to create one (--attach-only); running runtimes: %s", reference, strings.Join(running, ", "))
}

func attachMCPRecord(reference string, record kranzruntime.SessionRecord) (*kranzruntime.Client, kranzruntime.SessionMetadata, string, *runtimeHost, error) {
	if record.State != kranzruntime.SessionRunning {
		return nil, record.SessionMetadata, "", nil, fmt.Errorf("runtime %s is %s; refusing to create a second supervisor", reference, record.State)
	}
	client, err := kranzruntime.DialWithIdentity(record.Socket, version,
		kranzruntime.ClientIdentity{Surface: "mcp", Label: "Kranz MCP"})
	if err != nil {
		return nil, record.SessionMetadata, "", nil, fmt.Errorf("attach runtime %s: %w", reference, err)
	}
	return client, record.SessionMetadata, "attached", nil, nil
}
