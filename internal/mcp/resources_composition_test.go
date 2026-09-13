package mcp

import (
	"testing"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

func TestServiceEntryExposesStableCompositionIdentity(t *testing.T) {
	entry, err := serviceEntry(&app.ServiceSnapshot{
		ID:           "svc_stable",
		Name:         "team/api",
		SourceName:   "api",
		SourceID:     "src_team",
		SourcePath:   "repositories/team/kranz.yaml",
		ReloadState:  "pending_restart",
		ReloadReason: "explicit restart required",
		Config:       config.Service{Command: "run"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID != "svc_stable" || entry.Name != "team/api" || entry.SourceName != "api" || entry.SourceID != "src_team" || entry.SourcePath == "" || entry.ReloadState != "pending_restart" || entry.ReloadReason == "" {
		t.Fatalf("composition identity was not preserved: %#v", entry)
	}
}
