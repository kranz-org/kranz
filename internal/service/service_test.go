package service

import (
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func TestLogEntriesKeepTimestampsAlignedAcrossOverflowAndClear(t *testing.T) {
	svc := NewService("api", config.Service{Command: "true"}, 2)
	first := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	svc.AppendLogAt(first, "one")
	svc.AppendLogAt(first.Add(time.Second), "two")
	svc.AppendLogAt(first.Add(2*time.Second), "three")

	entries := svc.LogEntries()
	if len(entries) != 2 || entries[0].Raw != "two" || entries[1].Raw != "three" {
		t.Fatalf("overflow entries = %#v", entries)
	}
	if !entries[0].Timestamp.Equal(first.Add(time.Second)) || !entries[1].Timestamp.Equal(first.Add(2*time.Second)) {
		t.Fatalf("overflow timestamps = %#v", entries)
	}

	svc.ClearLogs()
	if len(svc.LogEntries()) != 0 || svc.Logs.Len() != 0 {
		t.Fatal("ClearLogs left text or timestamp metadata")
	}
}
