package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

func TestLogOptionsAcceptTailAndSinceTogether(t *testing.T) {
	options, err := parseLogOptions([]string{"--since", "5m", "--tail", "50", "api"})
	if err != nil {
		t.Fatalf("parse = %v", err)
	}
	if !options.sinceSet || options.since != 5*time.Minute || !options.tailSet || options.tail != 50 {
		t.Errorf("options = %+v", options)
	}
	if len(options.selectors) != 1 || options.selectors[0] != "api" {
		t.Errorf("selectors = %v", options.selectors)
	}
}

func TestLogOptionsRejectMalformedAndContradictoryValues(t *testing.T) {
	for _, args := range [][]string{{"--tail"}, {"--tail", "-3"}, {"--since", "yesterday"}, {"--run", "0"}, {"--runs", "0"}, {"--run", "2", "--runs", "3"}, {"--all", "--tail", "3"}, {"--source", "stdin"}, {"--nope"}} {
		if _, err := parseLogOptions(args); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

func TestLogOptionsParseDisplayAndSourceFlags(t *testing.T) {
	options, err := parseLogOptions([]string{"--plain", "--source", "stdout,stderr", "--source=kranz", "--run=-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.noTimes || !options.noLabels || options.run != -1 || len(options.sources) != 3 {
		t.Errorf("options = %+v", options)
	}
}

func TestLogTimestampLayoutIsFixedWidth(t *testing.T) {
	widths := map[int]bool{}
	for _, moment := range []time.Time{time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC), time.Date(2026, 8, 20, 9, 0, 0, 123456789, time.UTC)} {
		widths[len(moment.Format(logTimestampLayout))] = true
	}
	if len(widths) != 1 {
		t.Errorf("timestamp width varies: %v", widths)
	}
}

func TestLogEventFormattingKeepsActionRunReadable(t *testing.T) {
	event := app.LogEvent{Stream: "analytics/stats", Kind: "action", Run: 3, Source: "stderr", Timestamp: time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC), Raw: "failed"}
	if got := logEventLabel(event, false); got != "analytics/stats#3 stderr" {
		t.Errorf("label = %q", got)
	}
	if got := logEventPrefix(event, logOptions{noTimes: true}, false); got != "[analytics/stats#3 stderr] " {
		t.Errorf("prefix = %q", got)
	}
	if got := logEventPrefix(event, logOptions{noTimes: true, noLabels: true}, false); got != "" {
		t.Errorf("plain prefix = %q", got)
	}
}

func TestServiceRunAppearsOnlyWhenTheWindowSpansRuns(t *testing.T) {
	single := []app.LogEvent{{Stream: "api", Kind: "service", Run: 2, Source: "stdout", Raw: "one"}}
	spanning := append(append([]app.LogEvent(nil), single...), app.LogEvent{Stream: "api", Kind: "service", Run: 3, Source: "stdout", Raw: "two"})
	if got := streamsSpanningRuns(single); got["api"] {
		t.Error("one run must not be labelled as spanning")
	}
	if got := logEventLabel(single[0], false); got != "api#2 stdout" {
		t.Errorf("stable single-run label = %q", got)
	}
	if got := streamsSpanningRuns(spanning); !got["api"] {
		t.Error("two runs of one service must be labelled")
	}
	if got := logEventLabel(spanning[1], true); got != "api#3 stdout" {
		t.Errorf("spanning label = %q", got)
	}
}

func TestTextAndJSONAgreeOnEventCount(t *testing.T) {
	events := []app.LogEvent{{Stream: "api", Source: "stdout", Raw: "one"}, {Stream: "api", Source: "stdout", Raw: "two"}}
	var text, encoded bytes.Buffer
	options := logOptions{noTimes: true, noLabels: true}
	if err := writeLogEvents(&text, kranzcli.OutputText, events, options); err != nil {
		t.Fatal(err)
	}
	if err := writeLogEvents(&encoded, kranzcli.OutputJSON, events, options); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data []app.LogEvent `json:"data"`
	}
	if err := json.Unmarshal(encoded.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if strings.Count(text.String(), "\n") != len(envelope.Data) {
		t.Errorf("text=%q json=%d", text.String(), len(envelope.Data))
	}
}
