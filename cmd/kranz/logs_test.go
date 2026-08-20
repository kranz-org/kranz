package main

import (
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// --since narrows the window and --tail caps what that window returns, so the
// two have to compose rather than exclude each other.
func TestLogOptionsAcceptTailAndSinceTogether(t *testing.T) {
	options, err := parseLogOptions([]string{"--since", "5m", "--tail", "50", "api"})
	if err != nil {
		t.Fatalf("parse = %v", err)
	}
	if !options.sinceSet || options.since != 5*time.Minute {
		t.Errorf("since = %v", options.since)
	}
	if !options.tailSet || options.tail != 50 {
		t.Errorf("tail = %d", options.tail)
	}
	if len(options.selectors) != 1 || options.selectors[0] != "api" {
		t.Errorf("selectors = %v", options.selectors)
	}
}

func TestLogOptionsRejectMalformedValues(t *testing.T) {
	for _, args := range [][]string{
		{"--tail"},
		{"--tail", "-3"},
		{"--tail", "many"},
		{"--since"},
		{"--since", "yesterday"},
		{"--nope"},
	} {
		if _, err := parseLogOptions(args); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

// A log line's terminator is how it arrived, not part of what it says, so it
// must not reach a JSON field a consumer will compare or print.
func TestTrimLineEndingRemovesOnlyTheTerminator(t *testing.T) {
	for input, want := range map[string]string{
		"hello\n":      "hello",
		"hello\r\n":    "hello",
		"hello":        "hello",
		"  spaced  \n": "  spaced  ",
		"two\nlines\n": "two\nlines",
	} {
		if got := trimLineEnding(input); got != want {
			t.Errorf("trimLineEnding(%q) = %q, want %q", input, got, want)
		}
	}
}

// A ragged timestamp column makes multi-service output hard to scan, and
// RFC3339Nano drops trailing zeros, so the layout is fixed width by design.
func TestLogTimestampLayoutIsFixedWidth(t *testing.T) {
	widths := make(map[int]bool)
	for _, moment := range []time.Time{
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 20, 9, 0, 0, 100000000, time.UTC),
		time.Date(2026, 8, 20, 9, 0, 0, 123456789, time.UTC),
	} {
		widths[len(moment.Format(logTimestampLayout))] = true
	}
	if len(widths) != 1 {
		t.Errorf("timestamp width varies: %v", widths)
	}
}

func TestLogLevelNamesCoverEveryLevel(t *testing.T) {
	for level, want := range map[config.LogLevel]string{
		config.LogError: "error",
		config.LogWarn:  "warn",
		config.LogInfo:  "info",
		config.LogDebug: "debug",
	} {
		if got := logLevelName(level); got != want {
			t.Errorf("logLevelName(%v) = %q, want %q", level, got, want)
		}
	}
}
