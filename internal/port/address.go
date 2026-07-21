package port

import (
	"net"
	"strings"
)

func listenerAddress(endpoint string) string {
	endpoint = strings.TrimSpace(strings.TrimSuffix(endpoint, " (LISTEN)"))
	if endpoint == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(endpoint); err == nil {
		host = strings.Trim(host, "[]")
		if host == "" {
			return "*"
		}
		return host
	}
	if separator := strings.LastIndex(endpoint, ":"); separator >= 0 {
		host := strings.Trim(endpoint[:separator], "[]")
		if host == "" {
			return "*"
		}
		return host
	}
	return endpoint
}
