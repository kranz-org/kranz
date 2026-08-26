package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
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
func runMCP(options kranzcli.GlobalOptions, stdout, stderr io.Writer) error {
	if options.Output != kranzcli.OutputText {
		return errors.New("--output is not valid for MCP stdio; stdout is reserved for JSON-RPC framing")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runMCPContext(ctx, options, mcpStdin, stdout, stderr)
}

func runMCPContext(ctx context.Context, options kranzcli.GlobalOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	client, metadata, mode, owner, err := connectMCP(options)
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
	server := kranzmcp.NewServer(client, kranzmcp.SessionFromMetadata(metadata, mode), stdin, stdout, stderr)
	err = server.Serve(serveCtx)
	cancelServe()
	<-watchDone
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	return errors.Join(err, cleanup())
}

func connectMCP(options kranzcli.GlobalOptions) (*kranzruntime.Client, kranzruntime.SessionMetadata, string, *runtimeHost, error) {
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
