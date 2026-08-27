package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func logQueryTestLocal(t *testing.T) *Local {
	t.Helper()
	cfg := &config.Config{
		Project: "Logs",
		Services: map[string]config.Service{
			"api":  {Tags: []string{"backend"}, Actions: map[string]config.Action{"migrate": {Command: "printf 'up\\ndone\\n'", Shell: "/bin/sh"}}, ActionOrder: []string{"migrate"}},
			"docs": {Tags: []string{"frontend"}},
		},
		ServiceOrder:     []string{"api", "docs"},
		ActionGroups:     map[string]config.ActionGroup{"analytics": {Actions: map[string]config.Action{"stats": {Command: "printf report", Shell: "/bin/sh"}}, ActionOrder: []string{"stats"}}},
		ActionGroupOrder: []string{"analytics"},
	}
	local := NewLocal(cfg, nil, Options{})
	t.Cleanup(func() { _ = local.Shutdown() })
	return local
}

func TestQueryLogsResolvesSelectorsAndMultiplexesActions(t *testing.T) {
	local := logQueryTestLocal(t)
	local.AppendLogForTest("api", "service line")
	id, ok := findActionID(local.Config(), "api/migrate")
	if !ok {
		t.Fatal("action missing")
	}
	result, err := local.RunAction(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Run != 1 {
		t.Fatalf("run = %d", result.Run)
	}
	logs, err := local.QueryLogs(LogQuery{Selectors: []string{"backend"}, WithActions: true})
	if err != nil {
		t.Fatal(err)
	}
	foundService, foundAction := false, false
	for _, event := range logs.Events {
		foundService = foundService || event.Stream == "api" && event.Kind == "service"
		foundAction = foundAction || event.Stream == "api/migrate" && event.Kind == "action" && event.Run == result.Run
	}
	if !foundService || !foundAction {
		t.Fatalf("events = %#v", logs.Events)
	}
}

func TestQueryLogsFiltersSourceBeforeTailAndSupportsCursor(t *testing.T) {
	local := logQueryTestLocal(t)
	base := time.Now().Add(-time.Minute)
	service, _ := local.manager.GetService("api")
	service.AppendLogAtSource(base, "stderr", "old error")
	service.AppendLogAtSource(base.Add(time.Second), "stdout", "normal")
	service.AppendLogAtSource(base.Add(2*time.Second), "stderr", "newest error")
	first, err := local.QueryLogs(LogQuery{Selectors: []string{"api"}, Sources: []string{"stderr"}, Tail: 1, Since: base})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 || first.Events[0].Raw != "newest error" || !first.Truncated {
		t.Fatalf("first = %#v", first)
	}
	service.AppendLogAtSource(base.Add(3*time.Second), "stderr", "after cursor")
	next, err := local.QueryLogs(LogQuery{Selectors: []string{"api"}, Sources: []string{"stderr"}, Since: base, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Events) != 1 || next.Events[0].Raw != "after cursor" {
		t.Fatalf("next = %#v", next)
	}
}

func TestQueryLogsRejectsGroupWithoutStreamAndMismatchedCursor(t *testing.T) {
	local := logQueryTestLocal(t)
	_, err := local.QueryLogs(LogQuery{Selectors: []string{"analytics"}})
	var queryErr *LogQueryError
	if !errors.As(err, &queryErr) || queryErr.Code != "group_has_no_stream" {
		t.Fatalf("err = %#v", err)
	}
	initial, err := local.QueryLogs(LogQuery{Selectors: []string{"api"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = local.QueryLogs(LogQuery{Selectors: []string{"docs"}, Cursor: initial.NextCursor})
	if !errors.As(err, &queryErr) || queryErr.Code != "invalid_cursor" {
		t.Fatalf("cursor err = %#v", err)
	}
	local.sessionID = "another-session"
	_, err = local.QueryLogs(LogQuery{Selectors: []string{"api"}, Cursor: initial.NextCursor})
	if !errors.As(err, &queryErr) || queryErr.Code != "invalid_cursor" {
		t.Fatalf("cross-session cursor err = %#v", err)
	}
}

func TestQueryLogsRunRunsSinceAndActionDefault(t *testing.T) {
	local := logQueryTestLocal(t)
	id, _ := findActionID(local.Config(), "api/migrate")
	for run := 1; run <= 3; run++ {
		if _, err := local.RunAction(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		if run == 1 {
			time.Sleep(2 * time.Millisecond)
		}
	}
	second, err := local.QueryLogs(LogQuery{Selectors: []string{"api/migrate"}, Run: -2})
	if err != nil || len(second.Events) == 0 {
		t.Fatalf("run -2 = %#v, %v", second, err)
	}
	for _, event := range second.Events {
		if event.Run != 2 {
			t.Fatalf("run -2 returned #%d", event.Run)
		}
	}
	lastTwo, err := local.QueryLogs(LogQuery{Selectors: []string{"api/migrate"}, Runs: 2})
	if err != nil || len(lastTwo.Events) == 0 {
		t.Fatalf("runs 2 = %#v, %v", lastTwo, err)
	}
	for _, event := range lastTwo.Events {
		if event.Run < 2 {
			t.Fatalf("runs 2 returned #%d", event.Run)
		}
	}
	latest, err := local.QueryLogs(LogQuery{Selectors: []string{"api/migrate"}, DefaultTail: 200})
	if err != nil || len(latest.Events) == 0 {
		t.Fatalf("default = %#v, %v", latest, err)
	}
	for _, event := range latest.Events {
		if event.Run != 3 {
			t.Fatalf("action default returned #%d", event.Run)
		}
	}
	cutoff := lastTwo.Events[0].Timestamp
	recent, err := local.QueryLogs(LogQuery{Selectors: []string{"api/migrate"}, Runs: 3, Since: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range recent.Events {
		if event.Timestamp.Before(cutoff) {
			t.Fatalf("since returned %s before %s", event.Timestamp, cutoff)
		}
	}
}

func TestServiceLogsAreAddressableByRun(t *testing.T) {
	local := logQueryTestLocal(t)
	// Two starts of the same service write into one buffer. Without run
	// addressing, "the logs of the last start" is a time range the caller has
	// to reconstruct; with it, it is --run -1.
	local.SetServiceStatusForTest("api", config.StatusStarting)
	local.AppendLogForTest("api", "[stdout] first boot")
	local.SetServiceStatusForTest("api", config.StatusStopped)
	local.SetServiceStatusForTest("api", config.StatusStarting)
	local.AppendLogForTest("api", "[stdout] second boot")

	latest, err := local.QueryLogs(LogQuery{Selectors: []string{"api"}, Run: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Events) != 1 || latest.Events[0].Text != "[stdout] second boot" || latest.Events[0].Run != 2 {
		t.Fatalf("latest run = %#v", latest.Events)
	}
	first, err := local.QueryLogs(LogQuery{Selectors: []string{"api"}, Run: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 || first.Events[0].Text != "[stdout] first boot" {
		t.Fatalf("run 1 = %#v", first.Events)
	}
	if snapshot, ok := local.Service("api"); !ok || snapshot.State.Run != 2 {
		t.Fatalf("service run = %#v", snapshot)
	}
}

func TestQueryLogsResolvesLatestFromCatalogAndReportsExactRetentionGap(t *testing.T) {
	local := logQueryTestLocal(t)
	local.SetServiceStatusForTest("api", config.StatusStarting)
	for index := 0; index < 10010; index++ {
		local.AppendLogForTest("api", "x")
	}

	summaries := local.manager.RunSummaries(ServiceRunTarget("api"))
	if len(summaries) != 1 || summaries[0].Output.MissingLines == 0 {
		t.Fatalf("summary = %#v", summaries)
	}
	result, err := local.QueryLogs(LogQuery{Selectors: []string{"api"}, Run: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Retention) != 1 || result.Retention[0].Run != 1 || result.Retention[0].Output != summaries[0].Output {
		t.Fatalf("retention = %#v; summary = %#v", result.Retention, summaries[0])
	}
	if !result.Truncated || len(result.Events) == 0 {
		t.Fatalf("result = %#v", result)
	}
	for _, event := range result.Events {
		if event.Run != 1 {
			t.Fatalf("latest catalog run returned #%d", event.Run)
		}
	}
}

func TestQueryLogsFailsOnAnUnaddressableExplicitRun(t *testing.T) {
	local := logQueryTestLocal(t)
	id, _ := findActionID(local.Config(), "api/migrate")
	if _, err := local.RunAction(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	// A run that was never assigned, and an offset past the retained history,
	// must both be reported. Returning an empty success made a typo, a deleted
	// run, and a silent run indistinguishable.
	for _, run := range []int{99, -9} {
		result, err := local.QueryLogs(LogQuery{Selectors: []string{"api/migrate"}, Run: run})
		var queryErr *LogQueryError
		if !errors.As(err, &queryErr) || queryErr.Code != "run_not_retained" {
			t.Fatalf("run %d = %#v, %v; want a run_not_retained error", run, result, err)
		}
		if !strings.Contains(queryErr.Hint, "api/migrate #1") {
			t.Fatalf("run %d hint = %q, want the retained range", run, queryErr.Hint)
		}
	}

	// A target that has simply never run keeps the implicit action default
	// silent: only an explicit address is a claim that has to resolve.
	quiet, err := local.QueryLogs(LogQuery{Selectors: []string{"analytics/stats"}, DefaultTail: 200})
	if err != nil || len(quiet.Events) != 0 {
		t.Fatalf("action default without runs = %#v, %v", quiet, err)
	}

	// One retained target is enough for a multi-target address to succeed.
	both, err := local.QueryLogs(LogQuery{Selectors: []string{"api/migrate", "analytics/stats"}, Run: 1})
	if err != nil || len(both.Events) == 0 {
		t.Fatalf("run 1 across a retained and an unrun target = %#v, %v", both, err)
	}
}
