package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

func TestParseRunIdentityRequiresAbsolutePositiveNumber(t *testing.T) {
	target, run, err := parseRunIdentity("analytics/stats#42")
	if err != nil || target != "analytics/stats" || run != 42 {
		t.Fatalf("parseRunIdentity = %q, %d, %v", target, run, err)
	}
	for _, invalid := range []string{"api", "#1", "api#0", "api#latest"} {
		if _, _, err := parseRunIdentity(invalid); err == nil {
			t.Errorf("parseRunIdentity(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestRunsDeleteRequiresExplicitConfirmationBeforeRuntimeAccess(t *testing.T) {
	err := runRunsDelete(kranzcli.GlobalOptions{}, []string{"api#1"}, nil)
	var commandErr *kranzcli.Error
	if !errors.As(err, &commandErr) || commandErr.Code != "confirmation_required" {
		t.Fatalf("runRunsDelete error = %#v", err)
	}
}

func TestParseRunsListArgsCombinesFiltersAndFormat(t *testing.T) {
	query, err := parseRunsListArgs(kranzcli.OutputText, []string{"api", "--status", "running, stopped", "--status=failed", "--since=90m", "--limit", "2", "--format", "{{.Run}}"})
	if err != nil {
		t.Fatal(err)
	}
	if len(query.targets) != 1 || query.targets[0] != "api" || query.limit != 2 || query.since != 90*time.Minute || query.formatter == nil {
		t.Fatalf("unexpected query: %#v", query)
	}
	for _, status := range []string{"running", "stopped", "failed"} {
		if !query.statuses[status] {
			t.Errorf("status %q was not selected", status)
		}
	}
}

func TestParseRunsListArgsRejectsInvalidBounds(t *testing.T) {
	for _, args := range [][]string{{"--limit", "0"}, {"--since", "later"}, {"--status="}} {
		if _, err := parseRunsListArgs(kranzcli.OutputText, args); err == nil {
			t.Errorf("parseRunsListArgs(%q) unexpectedly succeeded", args)
		}
	}
}

func TestFilterRunsAppliesTargetStatusAndTime(t *testing.T) {
	now := time.Date(2026, time.January, 2, 15, 0, 0, 0, time.UTC)
	runs := []app.RunSummary{
		{Target: app.ServiceRunTarget("api"), Run: 1, Status: "stopped", StartedAt: now.Add(-2 * time.Hour)},
		{Target: app.ServiceRunTarget("api"), Run: 2, Status: "Running", StartedAt: now.Add(-10 * time.Minute)},
		{Target: app.ServiceRunTarget("worker"), Run: 3, Status: "running", StartedAt: now.Add(-5 * time.Minute)},
	}
	filtered := filterRuns(runs, []string{"API"}, map[string]bool{"running": true}, time.Hour, now)
	if len(filtered) != 1 || filtered[0].Run != 2 {
		t.Fatalf("filterRuns = %#v", filtered)
	}
}

func TestRunFormatterExposesStableRunFields(t *testing.T) {
	formatter, err := parseRowFormat("runs", kranzcli.OutputText, []string{"--format", "table {{.Run}}\\t{{.PID}}\\t{{.Status}}"})
	if err != nil {
		t.Fatal(err)
	}
	run := app.RunSummary{Target: app.ServiceRunTarget("api"), Run: 7, PID: 321, Status: "running", StartedAt: time.Now()}
	var output bytes.Buffer
	if err := formatter.write(&output, runFormatHeaders(), []map[string]any{runFormatRow(run)}); err != nil {
		t.Fatal(err)
	}
	if rendered := output.String(); !strings.Contains(rendered, "RUN") || !strings.Contains(rendered, "api#7") || !strings.Contains(rendered, "321") {
		t.Fatalf("unexpected formatted output: %q", rendered)
	}
}
