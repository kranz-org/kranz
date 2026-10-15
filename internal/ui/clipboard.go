package ui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/config"
)

const clipboardMaxBytes = 64 << 10

// TerminalOutput serializes whole OSC writes with renderer and watchdog writes.
// Both the program and the model must use the same instance.
type TerminalOutput struct {
	mu     sync.Mutex
	writer io.Writer
}

func NewTerminalOutput(w io.Writer) *TerminalOutput { return &TerminalOutput{writer: w} }
func (w *TerminalOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

type clipboardResultMsg struct {
	err          error
	bytes, lines int
	source       string
}

func (m *Model) sendClipboard(payload, source string) tea.Cmd {
	if len(payload) > clipboardMaxBytes {
		m.addNotification(source, fmt.Sprintf("%d bytes exceeds clipboard budget (64 KiB); select a smaller fragment", len(payload)), config.LogWarn)
		return nil
	}
	writer := m.terminalOutput
	return func() tea.Msg {
		var err error
		if writer == nil {
			err = errors.New("terminal output unavailable")
		} else {
			_, err = io.WriteString(writer, "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(payload))+"\a")
		}
		return clipboardResultMsg{err: err, bytes: len(payload), lines: 1 + countNewlines(payload), source: source}
	}
}

// Bubble Tea detects terminal size and lifecycle through term.File, so the
// synchronized writer must preserve those capabilities of the original file.
func (w *TerminalOutput) Fd() uintptr {
	if f, ok := w.writer.(interface{ Fd() uintptr }); ok {
		return f.Fd()
	}
	return ^uintptr(0)
}
func (w *TerminalOutput) Read(p []byte) (int, error) {
	if r, ok := w.writer.(io.Reader); ok {
		return r.Read(p)
	}
	return 0, io.EOF
}
func (w *TerminalOutput) Close() error {
	if c, ok := w.writer.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
