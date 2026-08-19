//go:build linux

package runtime

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func linuxProcessFields(pid int) ([]string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil, err
	}
	end := strings.LastIndex(string(data), ") ")
	if end < 0 {
		return nil, fmt.Errorf("invalid proc stat for %d", pid)
	}
	fields := strings.Fields(string(data)[end+2:])
	if len(fields) <= 19 {
		return nil, fmt.Errorf("short proc stat for %d", pid)
	}
	return fields, nil
}

func processIdentity(pid int) (int, string, error) {
	fields, err := linuxProcessFields(pid)
	if err != nil {
		return 0, "", err
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

func processParent(pid int) (int, error) {
	fields, err := linuxProcessFields(pid)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(fields[1])
}

func processStartedAt(pid int) (time.Time, error) {
	fields, err := linuxProcessFields(pid)
	if err != nil {
		return time.Time{}, err
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	var boot int64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime ") {
			boot, err = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "btime ")), 10, 64)
			break
		}
	}
	if err != nil || boot == 0 {
		return time.Time{}, fmt.Errorf("read Linux boot time: %w", err)
	}
	// Linux exposes process start ticks in USER_HZ, whose userspace ABI value
	// is 100 on the architectures supported by Kranz.
	return time.Unix(boot+int64(ticks/100), int64(ticks%100)*10_000_000).UTC(), nil
}
