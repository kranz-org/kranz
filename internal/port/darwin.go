//go:build darwin

package port

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kranz-org/kranz/internal/config"
)

// DarwinChecker inspects macOS listeners through lsof.
type DarwinChecker struct{}

func newPlatformChecker() Checker {
	return &DarwinChecker{}
}

// CheckPort returns the process listening on a macOS port.
func (d *DarwinChecker) CheckPort(port int) (*config.PortInfo, error) {
	for _, protocol := range []string{"tcp", "udp"} {
		args := []string{"-nP", "-i" + strings.ToUpper(protocol) + fmt.Sprintf(":%d", port), "-Fpcn"}
		if protocol == "tcp" {
			args = append(args, "-sTCP:LISTEN")
		}
		output, err := exec.Command("lsof", args...).Output()
		if err != nil {
			continue
		}
		if info := parseLsofOutput(string(output), port, protocol); info != nil {
			return info, nil
		}
	}
	return nil, nil
}

// CheckPorts inspects multiple macOS ports with one lsof invocation.
func (d *DarwinChecker) CheckPorts(ports []int) (map[int]*config.PortInfo, error) {
	result := make(map[int]*config.PortInfo)
	for _, port := range ports {
		info, err := d.CheckPort(port)
		if err != nil {
			return nil, err
		}
		if info != nil {
			result[port] = info
		}
	}
	return result, nil
}

// parseLsofOutput parses repeated p<PID>, c<command>, and n<address> fields.
func parseLsofOutput(output string, port int, protocol string) *config.PortInfo {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return nil
	}

	info := &config.PortInfo{
		Port:     port,
		Protocol: protocol,
	}

	for _, line := range lines {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, err := strconv.Atoi(line[1:])
			if err == nil {
				info.PID = pid
			}
		case 'c':
			info.Process = line[1:]
		case 'n':
			info.Address = listenerAddress(line[1:])
		}
	}

	// lsof exposes a short process name; ps provides the actionable command line.
	if info.PID > 0 {
		cmdOutput, _ := exec.Command("ps", "-p", strconv.Itoa(info.PID), "-o", "command=").Output()
		info.Command = strings.TrimSpace(string(cmdOutput))
	}

	if info.PID == 0 {
		return nil
	}

	return info
}
