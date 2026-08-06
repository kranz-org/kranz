package port

import (
	"net"
	"strconv"
	"strings"
)

func parseListenerEndpoint(endpoint string) (string, int, bool) {
	endpoint = strings.TrimSpace(strings.TrimSuffix(endpoint, " (LISTEN)"))
	separator := strings.LastIndex(endpoint, ":")
	if separator < 0 || separator == len(endpoint)-1 {
		return "", 0, false
	}
	portNumber, err := strconv.Atoi(endpoint[separator+1:])
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", 0, false
	}
	address := listenerAddress(endpoint)
	if address == "" {
		return "", 0, false
	}
	return address, portNumber, true
}

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
