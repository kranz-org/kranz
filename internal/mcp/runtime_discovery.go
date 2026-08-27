package mcp

import (
	"context"
	"strings"
	"time"

	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

// RuntimeEntry is the read-only registry view exposed to an MCP client. Current
// makes the connection's fixed project binding explicit among all sessions.
type RuntimeEntry struct {
	ID            string                    `json:"id"`
	Name          string                    `json:"name"`
	Project       string                    `json:"project"`
	Directory     string                    `json:"directory"`
	Mode          string                    `json:"mode"`
	State         kranzruntime.SessionState `json:"state"`
	StartedAt     time.Time                 `json:"started_at"`
	UptimeSeconds int64                     `json:"uptime_seconds"`
	Services      *int                      `json:"services"`
	Running       *int                      `json:"running"`
	Current       bool                      `json:"current"`
}

type RuntimeSelectorMatch struct {
	Runtime string `json:"runtime"`
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Service string `json:"service,omitempty"`
	Tag     string `json:"tag,omitempty"`
}

func (s *Server) runtimeEntries(ctx context.Context) ([]RuntimeEntry, error) {
	if s.runtimeListOverride != nil {
		return s.runtimeListOverride(ctx)
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return nil, err
	}
	records, err := registry.List(ctx, s.session.KranzVersion)
	if err != nil {
		return nil, err
	}
	entries := make([]RuntimeEntry, 0, len(records))
	for _, record := range records {
		entries = append(entries, RuntimeEntry{
			ID: record.ID, Name: record.Name, Project: record.Project, Directory: record.Directory,
			Mode: record.Mode, State: record.State, StartedAt: record.StartedAt,
			UptimeSeconds: max(0, int64(time.Since(record.StartedAt).Seconds())),
			Services:      record.Services, Running: record.Running, Current: record.ID == s.session.ID,
		})
	}
	return entries, nil
}

func (s *Server) selectorMatches(ctx context.Context, selector string) ([]RuntimeSelectorMatch, error) {
	if s.selectorMatchOverride != nil {
		return s.selectorMatchOverride(ctx, selector)
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return nil, err
	}
	records, err := registry.List(ctx, s.session.KranzVersion)
	if err != nil {
		return nil, err
	}
	matches := make([]RuntimeSelectorMatch, 0)
	for _, record := range records {
		if record.ID == s.session.ID || record.State != kranzruntime.SessionRunning {
			continue
		}
		client, dialErr := kranzruntime.DialContext(ctx, record.Socket, s.session.KranzVersion)
		if dialErr != nil {
			continue
		}
		cfg := client.Config()
		_ = client.Close()
		if cfg == nil {
			continue
		}
		if _, ok := cfg.Services[selector]; ok {
			matches = append(matches, RuntimeSelectorMatch{Runtime: record.Name, ID: record.ID, Kind: "service", Service: selector})
			continue
		}
		for _, name := range cfg.ServiceOrder {
			for _, tag := range cfg.Services[name].Tags {
				if strings.EqualFold(tag, selector) {
					matches = append(matches, RuntimeSelectorMatch{Runtime: record.Name, ID: record.ID, Kind: "tag", Service: name, Tag: tag})
					break
				}
			}
		}
	}
	return matches, nil
}
