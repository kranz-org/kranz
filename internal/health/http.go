package health

import (
	"fmt"
	"net/http"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// executeCheck dispatches one probe to its configured transport.
func executeCheck(name string, cfg *config.CheckConfig) error {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 2 * time.Second
	}

	switch cfg.Type {
	case config.CheckHTTP:
		return checkHTTP(cfg.URL, timeout, cfg.Headers, cfg.StatusCode)
	case config.CheckTCP:
		return checkTCP(cfg.Port, timeout)
	case config.CheckCommand:
		return checkCommand(cfg.Command, timeout)
	default:
		return fmt.Errorf("unknown health check type: %s", cfg.Type)
	}
}

// checkHTTP performs a GET request and accepts only a 2xx response.
func checkHTTP(url string, timeout time.Duration, headers map[string]string, expectedStatus int) error {
	client := &http.Client{
		Timeout: timeout,
		// A redirect can hide an unhealthy or incorrectly configured endpoint.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create HTTP GET %s: %w", url, err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if expectedStatus > 0 && resp.StatusCode != expectedStatus {
		return fmt.Errorf("HTTP %s returned status %d, expected %d", url, resp.StatusCode, expectedStatus)
	}
	if expectedStatus == 0 && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return fmt.Errorf("HTTP %s returned status %d", url, resp.StatusCode)
	}

	return nil
}
