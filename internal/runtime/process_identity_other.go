//go:build !darwin && !linux

package runtime

import (
	"fmt"
	"time"
)

func processIdentity(pid int) (int, string, error) {
	return 0, "", fmt.Errorf("process identity is unsupported on this platform")
}

func processParent(pid int) (int, error) {
	return 0, fmt.Errorf("process ancestry is unsupported on this platform")
}

func processStartedAt(pid int) (time.Time, error) {
	return time.Time{}, fmt.Errorf("process start time is unsupported on this platform")
}
