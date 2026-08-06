package service

import (
	"reflect"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func TestDetectedPortsAreSortedDeduplicatedAndClearedWithRuntime(t *testing.T) {
	svc := NewService("api", config.Service{}, 10)
	pm := NewProcessManager(10)
	generation := svc.setRuntime(pm, make(chan struct{}))

	if updated := svc.updateDetectedPorts(generation, []int{8080, 3000, 8080, 0, 70000}); !updated {
		t.Fatal("expected current runtime generation to accept detected ports")
	}
	if got, want := svc.DetectedPorts(), []int{3000, 8080}; !reflect.DeepEqual(got, want) {
		t.Fatalf("detected ports = %v, want %v", got, want)
	}

	copyOfPorts := svc.DetectedPorts()
	copyOfPorts[0] = 9999
	if got := svc.DetectedPorts(); got[0] != 3000 {
		t.Fatalf("DetectedPorts exposed mutable state: %v", got)
	}

	svc.clearRuntime(pm)
	if got := svc.DetectedPorts(); len(got) != 0 {
		t.Fatalf("detected ports after clear = %v", got)
	}
	if svc.updateDetectedPorts(generation, []int{9090}) {
		t.Fatal("stale runtime generation updated detected ports")
	}
}

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
