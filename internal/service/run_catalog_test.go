package service

import (
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func TestRunCatalogRetentionIsFairPerTarget(t *testing.T) {
	catalog := NewRunCatalog(2)
	api := ServiceRunTarget("api")
	worker := ServiceRunTarget("worker")
	for run := uint32(1); run <= 3; run++ {
		catalog.Begin(RunSummary{Target: api, Run: run, Status: "running", StartedAt: time.Unix(int64(run), 0)})
	}
	catalog.Begin(RunSummary{Target: worker, Run: 1, Status: "running", StartedAt: time.Unix(1, 0)})

	apiRuns := catalog.List(api)
	if len(apiRuns) != 2 || apiRuns[0].Run != 2 || apiRuns[1].Run != 3 {
		t.Fatalf("api runs = %#v, want #2 and #3", apiRuns)
	}
	workerRuns := catalog.List(worker)
	if len(workerRuns) != 1 || workerRuns[0].Run != 1 {
		t.Fatalf("worker history was displaced by api: %#v", workerRuns)
	}
}

func TestRunCatalogReportsPartialAndUnavailableOutput(t *testing.T) {
	catalog := NewRunCatalog(10)
	target := ServiceRunTarget("api")
	catalog.Begin(RunSummary{Target: target, Run: 1, Status: "running", StartedAt: time.Now()})
	catalog.RecordOutput(target, 1, 5)
	catalog.RecordOutput(target, 1, 7)
	catalog.EvictOutput(target, 1, 5)

	output := catalog.List(target)[0].Output
	if output.State != RunOutputPartial || output.MissingLines != 1 || output.MissingBytes != 5 || output.RetainedLines != 1 || output.RetainedBytes != 7 {
		t.Fatalf("partial output = %#v", output)
	}
	catalog.ClearOutput(target)
	output = catalog.List(target)[0].Output
	if output.State != RunOutputUnavailable || output.MissingLines != 2 || output.MissingBytes != 12 || output.RetainedLines != 0 {
		t.Fatalf("unavailable output = %#v", output)
	}
}

func TestRunCatalogAllIsDeterministicWhenRunsStartTogether(t *testing.T) {
	catalog := NewRunCatalog(10)
	started := time.Now()
	catalog.Begin(RunSummary{Target: ServiceRunTarget("worker"), Run: 1, StartedAt: started})
	catalog.Begin(RunSummary{Target: ActionRunTarget(config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "api", Name: "check"}), Run: 1, StartedAt: started})
	catalog.Begin(RunSummary{Target: ServiceRunTarget("api"), Run: 1, StartedAt: started})

	all := catalog.All()
	if len(all) != 3 || all[0].Target.Kind != RunKindAction || all[1].Target.Name != "api" || all[2].Target.Name != "worker" {
		t.Fatalf("All() order = %#v", all)
	}
}

func TestManagerCatalogTracksServiceAndActionRuns(t *testing.T) {
	cfg := &config.Config{Services: map[string]config.Service{
		"api": {Command: "true", Actions: map[string]config.Action{"check": {Command: "true"}}},
	}}
	manager := NewManager(cfg)
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopService("api"); err != nil {
		t.Fatal(err)
	}
	serviceRuns := manager.RunSummaries(ServiceRunTarget("api"))
	if len(serviceRuns) != 1 || serviceRuns[0].Run != 1 || serviceRuns[0].Live || serviceRuns[0].ExitCode == nil {
		t.Fatalf("service summaries = %#v", serviceRuns)
	}

	id := config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "api", Name: "check"}
	if _, err := manager.RunAction(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	actionRuns := manager.RunSummaries(ActionRunTarget(id))
	if len(actionRuns) != 1 || actionRuns[0].Run != 1 || actionRuns[0].Live || actionRuns[0].Status != ActionSucceeded.String() {
		t.Fatalf("action summaries = %#v", actionRuns)
	}
}

func TestManagerCatalogTracksInteractiveActionRuns(t *testing.T) {
	interactive := true
	id := config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "api", Name: "shell"}
	manager := NewManager(&config.Config{Services: map[string]config.Service{
		"api": {Command: "true", Actions: map[string]config.Action{"shell": {Command: "true", Interactive: &interactive}}},
	}})
	_, lease, err := manager.AcquireInteractiveAction(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CompleteInteractiveAction(id, lease, 0, 123, nil); err != nil {
		t.Fatal(err)
	}
	runs := manager.RunSummaries(ActionRunTarget(id))
	if len(runs) != 1 || runs[0].Live || runs[0].PID != 123 || runs[0].ExitCode == nil || *runs[0].ExitCode != 0 {
		t.Fatalf("interactive summaries = %#v", runs)
	}
}

func TestLogStreamEvictionUpdatesCatalogBoundary(t *testing.T) {
	catalog := NewRunCatalog(10)
	target := ServiceRunTarget("api")
	catalog.Begin(RunSummary{Target: target, Run: 1, Status: "running", StartedAt: time.Now()})
	stream := newLogStream(2)
	stream.SetCatalog(catalog, target)
	stream.BeginRunNumber(1)
	stream.Append(time.Now(), "stdout", "one")
	stream.Append(time.Now(), "stdout", "two")
	stream.Append(time.Now(), "stdout", "three")

	output := catalog.List(target)[0].Output
	if output.State != RunOutputPartial || output.CapturedLines != 3 || output.RetainedLines != 2 || output.MissingLines != 1 || output.MissingBytes != 3 {
		t.Fatalf("output boundary = %#v", output)
	}
}

func TestFailedStartFinishesItsServiceRun(t *testing.T) {
	manager := NewManager(&config.Config{Services: map[string]config.Service{
		"api": {Command: "true", Shell: "/definitely/missing/kranz-shell"},
	}})
	if err := manager.StartService("api"); err == nil {
		t.Fatal("start unexpectedly succeeded")
	}
	runs := manager.RunSummaries(ServiceRunTarget("api"))
	if len(runs) != 1 || runs[0].Live || runs[0].ExitCode == nil || *runs[0].ExitCode != -1 || runs[0].Cause == nil || runs[0].Cause.Type != "start_failed" {
		t.Fatalf("failed start summary = %#v", runs)
	}
}

func TestDetachedServiceRunStaysLiveAfterStartAction(t *testing.T) {
	manager := NewManager(&config.Config{Services: map[string]config.Service{
		"stack": {
			Supervision: config.SupervisionDetached,
			Lifecycle: config.LifecycleConfig{
				Start: &config.Action{Command: "true", Shell: "/bin/sh"},
				Stop:  &config.Action{Command: "true", Shell: "/bin/sh"},
			},
		},
	}})
	if err := manager.StartService("stack"); err != nil {
		t.Fatal(err)
	}
	runs := manager.RunSummaries(ServiceRunTarget("stack"))
	if len(runs) != 1 || !runs[0].Live || runs[0].ExitCode != nil || runs[0].Status != config.StatusRunning.String() {
		t.Fatalf("running detached summary = %#v", runs)
	}
	if err := manager.StopService("stack"); err != nil {
		t.Fatal(err)
	}
	runs = manager.RunSummaries(ServiceRunTarget("stack"))
	if len(runs) != 1 || runs[0].Live || runs[0].ExitCode == nil || *runs[0].ExitCode != 0 || runs[0].Status != config.StatusStopped.String() {
		t.Fatalf("stopped detached summary = %#v", runs)
	}
}

func TestConfigReloadMarkerStaysInsideContinuingRun(t *testing.T) {
	manager := NewManager(&config.Config{Services: map[string]config.Service{
		"api": {Command: "sleep 30", Shell: "/bin/sh"},
	}})
	t.Cleanup(func() { _ = manager.Shutdown() })
	ctx := WithRunProvenance(t.Context(), RunProvenance{Surface: "tui", ClientLabel: "dashboard"})
	if err := manager.StartServicesContext(ctx, []string{"api"}); err != nil {
		t.Fatal(err)
	}
	manager.RecordConfigReload(2, nil)
	svc, _ := manager.GetService("api")
	entries := svc.LogEntries()
	if len(entries) == 0 || entries[len(entries)-1].Run != 1 || entries[len(entries)-1].Raw != "[Kranz] Config reloaded · generation 2 · api#1" {
		t.Fatalf("reload marker entries = %#v", entries)
	}
	runs := manager.RunSummaries(ServiceRunTarget("api"))
	if len(runs) != 1 || runs[0].Run != 1 || runs[0].Surface != "tui" || runs[0].ClientLabel != "dashboard" || runs[0].StartReason != "first_start" {
		t.Fatalf("continuing run = %#v", runs)
	}
}

func TestLogStreamEnforcesByteAndEntryBudgetsIndependently(t *testing.T) {
	catalog := NewRunCatalog(10)
	target := ServiceRunTarget("api")
	stream := newLogStreamWithLimits(10, 5)
	stream.SetCatalog(catalog, target)
	run := stream.BeginRun()
	catalog.Begin(RunSummary{Target: target, Run: run, Status: "running", StartedAt: time.Now()})
	stream.Append(time.Now(), "stdout", "aaa")
	stream.Append(time.Now(), "stderr", "bbbb")

	entries := stream.Entries()
	if len(entries) != 1 || entries[0].Raw != "bbbb" || entries[0].Source != "stderr" {
		t.Fatalf("retained entries = %#v", entries)
	}
	summary := catalog.List(target)[0]
	if summary.Output.CapturedLines != 2 || summary.Output.CapturedBytes != 7 || summary.Output.RetainedLines != 1 ||
		summary.Output.RetainedBytes != 4 || summary.Output.MissingLines != 1 || summary.Output.MissingBytes != 3 || summary.Output.State != RunOutputPartial {
		t.Fatalf("output retention = %+v", summary.Output)
	}
	boundary := catalog.Boundaries()[0]
	if boundary.MaxEntries != 10 || boundary.MaxBytes != 5 || boundary.OldestRetainedRun != 1 {
		t.Fatalf("boundary = %+v", boundary)
	}
}

func TestRunCatalogPublishesOldestBoundaryAfterSummaryEviction(t *testing.T) {
	catalog := NewRunCatalog(2)
	target := ServiceRunTarget("api")
	catalog.SetOutputLimits(target, 100, 1024)
	for run := uint32(1); run <= 3; run++ {
		catalog.Begin(RunSummary{Target: target, Run: run, Status: "running", StartedAt: time.Now()})
	}
	boundary := catalog.Boundaries()[0]
	if boundary.OldestRetainedRun != 2 || boundary.EvictedRuns != 1 || boundary.MaxRuns != 2 {
		t.Fatalf("boundary = %+v", boundary)
	}
}
