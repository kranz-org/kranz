package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
)

const terminalBackgroundQuery = "\x1b]11;?\x1b\\"

// terminalBackgroundProbe runs while Bubble Tea has temporarily released the
// terminal. This avoids racing its input reader for the OSC 11 response.
type terminalBackgroundProbe struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	dark   bool
}

func (p *terminalBackgroundProbe) SetStdin(reader io.Reader)  { p.stdin = reader }
func (p *terminalBackgroundProbe) SetStdout(writer io.Writer) { p.stdout = writer }
func (p *terminalBackgroundProbe) SetStderr(writer io.Writer) { p.stderr = writer }

func (p *terminalBackgroundProbe) Run() error {
	input, ok := p.stdin.(*os.File)
	if !ok {
		return errors.New("terminal background probe requires a file input")
	}
	if p.stdout == nil {
		return errors.New("terminal background probe has no output")
	}
	response, err := readTerminalBackground(input, p.stdout)
	if err != nil {
		return err
	}
	p.dark, err = parseTerminalBackground(response)
	return err
}

func terminalBackgroundProbeSupported() bool {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return false
	}
	termName := strings.ToLower(os.Getenv("TERM"))
	return !strings.HasPrefix(termName, "screen") && !strings.HasPrefix(termName, "tmux") && !strings.HasPrefix(termName, "dumb")
}

func parseTerminalBackground(response string) (bool, error) {
	marker := strings.LastIndex(response, "]11;")
	if marker < 0 {
		return false, fmt.Errorf("OSC 11 response is missing from %q", response)
	}
	payload := response[marker+len("]11;"):]
	payload = strings.TrimSuffix(payload, "\a")
	payload = strings.TrimSuffix(payload, "\x1b\\")
	payload = strings.TrimSpace(payload)
	if !strings.HasPrefix(payload, "rgb:") {
		return false, fmt.Errorf("unsupported OSC 11 color %q", payload)
	}
	components := strings.Split(strings.TrimPrefix(payload, "rgb:"), "/")
	if len(components) != 3 {
		return false, fmt.Errorf("invalid OSC 11 color %q", payload)
	}
	rgb := make([]int, len(components))
	for index, component := range components {
		value, err := parseTerminalColorComponent(component)
		if err != nil {
			return false, fmt.Errorf("invalid OSC 11 color %q: %w", payload, err)
		}
		rgb[index] = value
	}
	minimum, maximum := rgb[0], rgb[0]
	for _, value := range rgb[1:] {
		minimum = min(minimum, value)
		maximum = max(maximum, value)
	}
	// Match termenv's HSL-lightness boundary while avoiding another cached
	// terminal detector: lightness is (max+min)/2 on normalized RGB channels.
	return maximum+minimum < 255, nil
}

func parseTerminalColorComponent(component string) (int, error) {
	if component == "" || len(component) > 4 {
		return 0, errors.New("color component must contain one to four hex digits")
	}
	value, err := strconv.ParseUint(component, 16, 16)
	if err != nil {
		return 0, err
	}
	maximum := uint64(1<<(4*len(component))) - 1
	return int((value*255 + maximum/2) / maximum), nil
}
