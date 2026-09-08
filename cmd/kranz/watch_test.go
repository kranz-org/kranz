package main

import (
	"bytes"
	"errors"
	"testing"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

func TestParseWatchQuerySeparatesSelectorsAndFilters(t *testing.T) {
	query, err := parseWatchQuery("status", kranzcli.OutputText, []string{
		"web", "--filter", "state=running,unhealthy", "--filter=health=ready", "--watch", "--interval=2s", "--count", "3", "--format", "{{.Name}}",
	}, "name", "state", "health")
	if err != nil {
		t.Fatal(err)
	}
	if len(query.args) != 1 || query.args[0] != "web" || !query.watch || query.interval != 2*time.Second || query.count != 3 || query.formatter == nil {
		t.Fatalf("unexpected query: %#v", query)
	}
	if !query.filters["state"]["running"] || !query.filters["state"]["unhealthy"] || !query.filters["health"]["ready"] {
		t.Fatalf("unexpected filters: %#v", query.filters)
	}
}

func TestParseWatchQueryRejectsAmbiguousModes(t *testing.T) {
	tests := []struct {
		output kranzcli.OutputFormat
		args   []string
	}{
		{kranzcli.OutputText, []string{"--filter", "unknown=value"}},
		{kranzcli.OutputText, []string{"--interval", "2s"}},
		{kranzcli.OutputJSON, []string{"--watch"}},
	}
	for _, test := range tests {
		if _, err := parseWatchQuery("ps", test.output, test.args, "state"); err == nil {
			t.Errorf("parseWatchQuery(%q) unexpectedly succeeded", test.args)
		}
	}
}

func TestMatchesWatchFiltersUsesOrWithinKeyAndAcrossKeys(t *testing.T) {
	filters := map[string]map[string]bool{
		"state":  {"running": true, "unhealthy": true},
		"client": {"mcp": true},
	}
	if !matchesWatchFilters(filters, map[string][]string{"state": {"Running"}, "client": {"tui", "MCP"}}) {
		t.Fatal("expected row to match")
	}
	if matchesWatchFilters(filters, map[string][]string{"state": {"stopped"}, "client": {"mcp"}}) {
		t.Fatal("unexpected state match")
	}
}

func TestRunWatchHonorsSnapshotCount(t *testing.T) {
	query := watchQuery{watch: true, interval: time.Millisecond, count: 2}
	var output bytes.Buffer
	refreshes := 0
	err := runWatch(query, &output, func() error {
		refreshes++
		return nil
	})
	if err != nil || refreshes != 2 {
		t.Fatalf("runWatch refreshes=%d err=%v", refreshes, err)
	}
}

func TestRunWatchStopsOnRefreshError(t *testing.T) {
	want := errors.New("refresh failed")
	err := runWatch(watchQuery{}, &bytes.Buffer{}, func() error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("runWatch error = %v", err)
	}
}
