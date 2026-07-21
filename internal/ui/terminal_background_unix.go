//go:build darwin || linux

package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

const terminalBackgroundTimeout = 350 * time.Millisecond

func readTerminalBackground(input *os.File, output io.Writer) (string, error) {
	termName := strings.ToLower(os.Getenv("TERM"))
	if strings.HasPrefix(termName, "screen") || strings.HasPrefix(termName, "tmux") || strings.HasPrefix(termName, "dumb") {
		return "", fmt.Errorf("terminal %q does not expose a direct OSC background response", termName)
	}
	fd := input.Fd()
	if !term.IsTerminal(fd) {
		return "", errors.New("input is not a terminal")
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", fmt.Errorf("enter raw mode for background probe: %w", err)
	}
	defer term.Restore(fd, state) //nolint:errcheck -- preserve the original probe error.

	if _, err := io.WriteString(output, terminalBackgroundQuery); err != nil {
		return "", fmt.Errorf("query terminal background: %w", err)
	}
	deadline := time.Now().Add(terminalBackgroundTimeout)
	response := make([]byte, 0, 64)
	for len(response) < 256 {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "", errors.New("terminal background query timed out")
		}
		var readable unix.FdSet
		readable.Set(int(fd))
		timeout := unix.NsecToTimeval(remaining.Nanoseconds())
		count, err := unix.Select(int(fd)+1, &readable, nil, nil, &timeout)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("wait for terminal background: %w", err)
		}
		if count == 0 {
			return "", errors.New("terminal background query timed out")
		}
		var buffer [1]byte
		if _, err := input.Read(buffer[:]); err != nil {
			return "", fmt.Errorf("read terminal background: %w", err)
		}
		response = append(response, buffer[0])
		if buffer[0] == '\a' || (len(response) >= 2 && response[len(response)-2] == '\x1b' && buffer[0] == '\\') {
			return string(response), nil
		}
	}
	return "", errors.New("terminal background response is too long")
}
