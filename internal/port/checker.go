// Package port detects listeners and safely releases externally owned ports.
package port

import (
	"context"

	"github.com/kranz-org/kranz/internal/config"
)

// Listener is one TCP listening socket observed in an operating-system
// snapshot. Process metadata intentionally stays out of this runtime model.
type Listener struct {
	Protocol string
	Address  string
	Port     int
	PID      int
}

// ListenerScanner captures all current listeners in one platform-specific
// operation. A successful call returns a complete replacement snapshot.
type ListenerScanner interface {
	Snapshot(context.Context) ([]Listener, error)
}

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

// NewListenerScanner creates the snapshot scanner for the current platform.
func NewListenerScanner() ListenerScanner {
	return newPlatformListenerScanner()
}
