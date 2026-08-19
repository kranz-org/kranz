//go:build darwin

package runtime

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

func processIdentity(pid int) (int, string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, "", err
	}
	started := info.Proc.P_starttime
	return int(info.Eproc.Pgid), fmt.Sprintf("darwin-start:%d.%06d", started.Sec, started.Usec), nil
}

func processParent(pid int) (int, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	return int(info.Eproc.Ppid), nil
}

func processStartedAt(pid int) (time.Time, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, err
	}
	started := info.Proc.P_starttime
	return time.Unix(started.Sec, int64(started.Usec)*1000).UTC(), nil
}
