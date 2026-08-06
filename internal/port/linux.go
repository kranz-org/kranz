//go:build linux

package port

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/kranz-org/kranz/internal/config"
)

// LinuxChecker inspects Linux listeners through ss and procfs.
type LinuxChecker struct{}

func newPlatformChecker() Checker {
	return &LinuxChecker{}
}

func newPlatformListenerScanner() ListenerScanner {
	return &LinuxChecker{}
}

var ssPIDPattern = regexp.MustCompile(`pid=([0-9]+)`)

// Snapshot returns every attributable TCP listener visible to ss in one invocation.
func (l *LinuxChecker) Snapshot(ctx context.Context) ([]Listener, error) {
	output, err := exec.CommandContext(ctx, "ss", "-H", "-ltnp").Output()
	if err != nil {
		return nil, fmt.Errorf("snapshot TCP listeners with ss: %w", err)
	}
	return parseSSSnapshot(string(output)), nil
}

// CheckPort returns the process listening on a Linux port.
func (l *LinuxChecker) CheckPort(port int) (*config.PortInfo, error) {
	for _, protocol := range []string{"tcp", "udp"} {
		flags := "-Hltnp"
		if protocol == "udp" {
			flags = "-Hlunp"
		}
		output, err := exec.Command("ss", flags, "sport", "=", fmt.Sprintf(":%d", port)).Output()
		if err != nil {
			continue
		}
		if info := parseSSOutput(string(output), port, protocol); info != nil {
			return info, nil
		}
	}
	return nil, nil
}

// CheckPorts inspects multiple Linux ports with one ss invocation.
func (l *LinuxChecker) CheckPorts(ports []int) (map[int]*config.PortInfo, error) {
	result := make(map[int]*config.PortInfo)
	for _, port := range ports {
		info, err := l.CheckPort(port)
		if err != nil {
			return nil, err
		}
		if info != nil {
			result[port] = info
		}
	}
	return result, nil
}

// parseSSOutput extracts listener address, PID, and process name from ss output.
func parseSSOutput(output string, port int, protocol string) *config.PortInfo {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		info := &config.PortInfo{
			Port:     port,
			Address:  listenerAddress(fields[3]),
			Protocol: protocol,
		}

		lastField := fields[len(fields)-1]
		pidStr := extractPID(lastField)
		if pidStr == "" {
			return info
		}

		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			return info
		}

		info.PID = pid
		info.Process = extractProcessName(lastField)

		// procfs provides the complete command line omitted by ss.
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err == nil {
			info.Command = strings.ReplaceAll(string(cmdline), "\x00", " ")
		}

		return info
	}

	return nil
}

// extractPID parses the pid field from an ss users tuple.
func extractPID(s string) string {
	idx := strings.Index(s, "pid=")
	if idx < 0 {
		return ""
	}
	s = s[idx+4:]
	end := strings.IndexAny(s, ",)")
	if end < 0 {
		return s
	}
	return s[:end]
}

// extractProcessName parses the executable name from an ss users tuple.
func extractProcessName(s string) string {
	start := strings.Index(s, "((\"")
	if start < 0 {
		return "unknown"
	}
	s = s[start+3:]
	end := strings.Index(s, "\"")
	if end < 0 {
		return s
	}
	return s[:end]
}

func parseSSSnapshot(output string) []Listener {
	var listeners []Listener
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "LISTEN" {
			continue
		}
		address, portNumber, ok := parseListenerEndpoint(fields[3])
		if !ok {
			continue
		}
		for _, match := range ssPIDPattern.FindAllStringSubmatch(line, -1) {
			pid, err := strconv.Atoi(match[1])
			if err != nil || pid < 1 {
				continue
			}
			listeners = append(listeners, Listener{
				Protocol: "tcp",
				Address:  address,
				Port:     portNumber,
				PID:      pid,
			})
		}
	}
	return listeners
}
