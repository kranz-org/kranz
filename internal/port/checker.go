// Package port detects listeners and safely releases externally owned ports.
package port

import "github.com/kranz-org/kranz/internal/config"

// Checker inspects listening ports on the current operating system.
type Checker interface {
	// CheckPort returns listener ownership for one port, or nil when it is free.
	CheckPort(port int) (*config.PortInfo, error)
	// CheckPorts inspects a collection of ports in one platform-specific call.
	CheckPorts(ports []int) (map[int]*config.PortInfo, error)
}

// NewChecker creates the checker implemented for the current platform.
func NewChecker() Checker {
	return newPlatformChecker()
}
