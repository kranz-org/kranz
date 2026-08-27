package app

import (
	"testing"

	"github.com/kranz-org/kranz/internal/config"
)

func TestExportRunCarriesCanonicalEntriesProvenanceAndRetention(t *testing.T) {
	local := NewLocal(&config.Config{Project: "export", Services: map[string]config.Service{
		"api": {Command: "exit 0", Dir: ".", Shell: "sh"},
	}}, nil, Options{})
	defer func() { _ = local.Shutdown() }()
	local.SetServiceStatusForTest("api", config.StatusStarting)
	local.AppendLogForTest("api", "exported line")
	snapshot, _ := local.Service("api")
	state := snapshot.State
	state.Completed = true
	local.SetServiceStateForTest("api", state)
	local.SetServiceStatusForTest("api", config.StatusStopped)

	export, err := local.ExportRun(ServiceRunTarget("api"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if export.Summary.Run != 1 || export.Summary.Surface != "runtime" || len(export.Entries) != 1 || export.Entries[0].Raw != "exported line" {
		t.Fatalf("export = %+v entries=%#v", export.Summary, export.Entries)
	}
	if export.Retention.OldestRetainedRun != 1 || export.Retention.MaxEntries == 0 || export.Retention.MaxBytes == 0 {
		t.Fatalf("retention = %+v", export.Retention)
	}
}
