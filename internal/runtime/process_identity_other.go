//go:build !darwin && !linux

package runtime

import "fmt"

func processIdentity(pid int) (int, string, error) {
	return 0, "", fmt.Errorf("process identity is unsupported on this platform")
}
