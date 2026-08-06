package health

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"

	"github.com/kranz-org/kranz/internal/config"
)

func (hc *Checker) executeCheck(name string, cfg *config.CheckConfig) error {
	resolved, err := hc.resolveCheck(name, cfg)
	if err != nil {
		return err
	}
	return executeCheck(name, resolved)
}

func (hc *Checker) resolveCheck(name string, cfg *config.CheckConfig) (*config.CheckConfig, error) {
	if !cfg.UsesDetectedPort() {
		return cfg, nil
	}

	hc.mu.RLock()
	provider := hc.detectedPortsProvider
	hc.mu.RUnlock()
	if provider == nil {
		return nil, fmt.Errorf("detected port is not available: runtime port provider is not configured")
	}

	return ResolveCheckTarget(cfg, provider(name))
}

// ResolveCheckTarget returns a copy of a dynamic probe with its current
// detected port applied. Callers use the same normalization and selection
// rules as the health-check execution path.
func ResolveCheckTarget(cfg *config.CheckConfig, detectedPorts []int) (*config.CheckConfig, error) {
	if !cfg.UsesDetectedPort() {
		return cfg, nil
	}

	ports := normalizedPorts(detectedPorts)
	port, err := selectDetectedPort(ports, cfg.DetectedPortIndex)
	if err != nil {
		return nil, err
	}

	resolved := *cfg
	resolved.Port = port
	if resolved.Type == config.CheckHTTP {
		parsed, parseErr := url.Parse(resolved.URL)
		if parseErr != nil {
			return nil, fmt.Errorf("parse dynamic HTTP probe URL: %w", parseErr)
		}
		parsed.Host = net.JoinHostPort(parsed.Hostname(), strconv.Itoa(port))
		resolved.URL = parsed.String()
	}
	return &resolved, nil
}

func selectDetectedPort(ports []int, index *int) (int, error) {
	if len(ports) == 0 {
		return 0, fmt.Errorf("waiting for a detected port")
	}
	if index == nil {
		if len(ports) != 1 {
			return 0, fmt.Errorf("detected port is ambiguous: %v; set detected_port_index", ports)
		}
		return ports[0], nil
	}
	if *index < 0 || *index >= len(ports) {
		return 0, fmt.Errorf("detected_port_index %d is unavailable; detected ports: %v", *index, ports)
	}
	return ports[*index], nil
}

func normalizedPorts(ports []int) []int {
	result := append([]int(nil), ports...)
	sort.Ints(result)
	write := 0
	for _, port := range result {
		if port < 1 || port > 65535 || (write > 0 && result[write-1] == port) {
			continue
		}
		result[write] = port
		write++
	}
	return result[:write]
}
