// Package logcommand runs presentation commands independently of service output.
package logcommand

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	MaxLineBytes   = 1 << 20
	MaxOutputBytes = 4 << 20
	MaxOutputLines = 20000
	finishTimeout  = 5 * time.Second
)

type Options struct {
	Command, Shell, Directory string
	Environment               []string
}

type batch struct {
	text  string
	final bool
}

// Snapshot is a bounded append-only output window. First counts evicted lines.
type Snapshot struct {
	Lines    []string
	First    uint64
	Finished bool
	Err      error
}

// Process owns one shell pipeline. Only its input queue is written by the UI;
// command IO and waiting happen on background goroutines.
type Process struct {
	ctx      context.Context
	cancel   context.CancelFunc
	input    chan batch
	Changed  chan struct{}
	Done     chan struct{}
	mu       sync.Mutex
	lines    []string
	pending  []byte
	bytes    int
	first    uint64
	finished bool
	err      error
}

func Start(options Options) *Process {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Process{ctx: ctx, cancel: cancel, input: make(chan batch, 8),
		Changed: make(chan struct{}, 1), Done: make(chan struct{})}
	go p.run(options)
	return p
}

// Send never blocks service capture or the UI. A slow consumer fails locally.
func (p *Process) Send(text string, final bool) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.input <- batch{text, final}:
		return true
	default:
		p.fail(errors.New("log command cannot keep up with incoming output"))
		return false
	}
}

func (p *Process) Cancel() { p.cancel() }

func (p *Process) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Snapshot{Lines: append([]string(nil), p.lines...), First: p.first, Finished: p.finished, Err: p.err}
}

func (p *Process) notify() {
	select {
	case p.Changed <- struct{}{}:
	default:
	}
}

func (p *Process) fail(err error) {
	p.mu.Lock()
	if p.err == nil {
		p.err = err
	}
	p.mu.Unlock()
	p.cancel()
	p.notify()
}

func (p *Process) run(options Options) {
	defer close(p.Done)
	defer p.cancel()
	shell := options.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	command := exec.CommandContext(p.ctx, shell, "-c", options.Command)
	command.Dir, command.Env = options.Directory, options.Environment
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	command.WaitDelay = time.Second
	var stderr cappedBuffer
	command.Stdout, command.Stderr = p, &stderr
	stdin, err := command.StdinPipe()
	if err == nil {
		err = command.Start()
	}
	if err == nil {
		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			defer func() { _ = stdin.Close() }()
			for {
				select {
				case <-p.ctx.Done():
					return
				case next := <-p.input:
					if _, writeErr := stdin.Write([]byte(next.text)); writeErr != nil {
						return
					}
					if next.final {
						// EOF also supports processors that buffer until input ends.
						_ = stdin.Close()
						timer := time.AfterFunc(finishTimeout, func() { p.fail(errors.New("log command did not finish after input closed")) })
						select {
						case <-p.ctx.Done():
						case <-p.Done:
						}
						timer.Stop()
						return
					}
				}
			}
		}()
		err = command.Wait()
		// A shell can exit before children that inherited its output pipes.
		// The presentation owns that whole group, including on a successful exit.
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		p.cancel()
		<-writerDone
	} else if stdin != nil {
		_ = stdin.Close()
	}
	p.mu.Lock()
	if len(p.pending) > 0 {
		p.appendLine(string(p.pending))
		p.pending = nil
	}
	if p.err == nil && err != nil {
		p.err = fmt.Errorf("log command: %w", err)
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			p.err = fmt.Errorf("%w: %s", p.err, detail)
		}
	}
	p.finished = true
	p.mu.Unlock()
	p.notify()
}

// Write reconstructs lines split across writes and bounds both unfinished and
// retained output. Returning an error cancels the entire pipeline.
func (p *Process) Write(data []byte) (int, error) {
	p.mu.Lock()
	for rest := data; len(rest) > 0; {
		end := bytes.IndexByte(rest, '\n')
		part := rest
		if end >= 0 {
			part = rest[:end]
		}
		if len(p.pending)+len(part) > MaxLineBytes {
			p.mu.Unlock()
			err := errors.New("log command produced a line larger than 1 MiB")
			p.fail(err)
			return 0, err
		}
		p.pending = append(p.pending, part...)
		if end < 0 {
			break
		}
		p.appendLine(strings.TrimSuffix(string(p.pending), "\r"))
		p.pending = p.pending[:0]
		rest = rest[end+1:]
	}
	p.mu.Unlock()
	p.notify()
	return len(data), nil
}

func (p *Process) appendLine(line string) {
	p.lines = append(p.lines, line)
	p.bytes += len(line) + 1
	for len(p.lines) > MaxOutputLines || p.bytes > MaxOutputBytes {
		p.bytes -= len(p.lines[0]) + 1
		p.lines[0] = ""
		p.lines = p.lines[1:]
		p.first++
	}
}

type cappedBuffer struct{ buffer bytes.Buffer }

func (b *cappedBuffer) Write(data []byte) (int, error) {
	length := len(data)
	if remaining := 8192 - b.buffer.Len(); remaining > 0 {
		_, _ = b.buffer.Write(data[:min(remaining, length)])
	}
	return length, nil
}

func (b *cappedBuffer) String() string { return b.buffer.String() }
