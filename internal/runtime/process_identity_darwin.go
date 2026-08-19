//go:build darwin

package runtime

import (
	"fmt"

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
