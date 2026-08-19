//go:build linux

package runtime

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func processIdentity(pid int) (int, string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, "", err
	}
	end := strings.LastIndex(string(data), ") ")
	if end < 0 {
		return 0, "", fmt.Errorf("invalid proc stat for %d", pid)
	}
	fields := strings.Fields(string(data)[end+2:])
	if len(fields) <= 19 {
		return 0, "", fmt.Errorf("short proc stat for %d", pid)
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, "", err
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return 0, "", err
	}
	return pgid, fmt.Sprintf("linux-proc-start:%d", start), nil
}
