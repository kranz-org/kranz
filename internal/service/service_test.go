package service

import (
	"reflect"
	"strings"
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
	if len(svc.LogEntries()) != 0 || len(svc.LogLines()) != 0 {
		t.Fatal("ClearLogs left text or timestamp metadata")
	}
}

func TestLogEntriesPreserveSourceSequenceAndHotReloadHistory(t *testing.T) {
	first := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	previous := NewService("worker", config.Service{}, 3)
	previous.AppendLogAtSource(first, "stdout", "one")
	previous.AppendLogAtSource(first.Add(time.Second), "stderr", "two")
	replacement := NewService("worker", config.Service{}, 3)
	replacement.CopyLogHistoryFrom(previous)
	replacement.AppendLogAtSource(first.Add(2*time.Second), "stdout", "three")
	entries := replacement.LogEntries()
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	for index, want := range []struct {
		source   string
		sequence uint64
		raw      string
	}{{"stdout", 1, "one"}, {"stderr", 2, "two"}, {"stdout", 3, "three"}} {
		if entries[index].Source != want.source || entries[index].Sequence != want.sequence || entries[index].Raw != want.raw {
			t.Fatalf("entry %d = %+v, want %+v", index, entries[index], want)
		}
	}
}

// A pipe read is not line-aligned, so one captured chunk can carry many lines.
// Storing it as a single entry is what made every consumer that counts entries
// report a number the user could not reproduce by counting what they saw.
func TestAppendLogStoresOneEntryPerLine(t *testing.T) {
	svc := NewService("api", config.Service{}, 100)
	svc.AppendLogAtSource(time.Now(), "stdout", "first\nsecond\n\nfourth\n")

	entries := svc.LogEntries()
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(entries))
	}
	for index, want := range []string{"first", "second", "", "fourth"} {
		if entries[index].Raw != want {
			t.Errorf("entry %d = %q, want %q", index, entries[index].Raw, want)
		}
		if strings.Contains(entries[index].Raw, "\n") {
			t.Errorf("entry %d still holds more than one line", index)
		}
		// Every line is separately addressable, which is what a follow cursor
		// and a search hit index both rely on.
		if index > 0 && entries[index].Sequence <= entries[index-1].Sequence {
			t.Errorf("entry %d did not advance the cursor", index)
		}
	}
}

func TestSplitCapturedLinesKeepsBlanksAndDropsTheTerminator(t *testing.T) {
	for input, want := range map[string][]string{
		"solo":               {"solo"},
		"one\ntwo\n":         {"one", "two"},
		"one\ntwo":           {"one", "two"},
		"trailing blank\n\n": {"trailing blank", ""},
		"crlf\r\n":           {"crlf"},
		"":                   {""},
	} {
		got := splitCapturedLines(input)
		if len(got) != len(want) {
			t.Errorf("splitCapturedLines(%q) = %q, want %q", input, got, want)
			continue
		}
		for index := range got {
			if got[index] != want[index] {
				t.Errorf("splitCapturedLines(%q) = %q, want %q", input, got, want)
				break
			}
		}
	}
}
