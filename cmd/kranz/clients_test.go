package main

import (
	"testing"

	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

func TestListableClientExcludesInfrastructureConnections(t *testing.T) {
	const listingPID = 42
	for _, test := range []struct {
		name   string
		client kranzruntime.ClientInfo
		want   bool
	}{
		{name: "TUI", client: kranzruntime.ClientInfo{Surface: "tui", PID: 7}, want: true},
		{name: "listing probe", client: kranzruntime.ClientInfo{Surface: "cli", PID: listingPID}, want: false},
		{name: "background owner", client: kranzruntime.ClientInfo{Surface: "background", PID: 8}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := listableClient(test.client, listingPID); got != test.want {
				t.Fatalf("listableClient() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestClientDisplayLabelCombinesSurfaceAndIdentity(t *testing.T) {
	for _, test := range []struct {
		surface string
		label   string
		want    string
	}{
		{surface: "tui", label: "Kranz dashboard", want: "TUI"},
		{surface: "tui", label: "Kranz attach", want: "TUI: attach"},
		{surface: "cli", label: "Kranz CLI", want: "CLI"},
		{surface: "cli", label: "Kranz foreground", want: "CLI: foreground"},
		{surface: "mcp", label: "Kranz MCP", want: "MCP"},
		{surface: "mcp", label: "MCP: codex", want: "MCP: codex"},
		{surface: "mcp", label: "build agent", want: "MCP: build agent"},
		{surface: "", label: "", want: "UNKNOWN"},
	} {
		t.Run(test.want, func(t *testing.T) {
			if got := clientDisplayLabel(test.surface, test.label); got != test.want {
				t.Fatalf("clientDisplayLabel(%q, %q) = %q, want %q", test.surface, test.label, got, test.want)
			}
		})
	}
}
