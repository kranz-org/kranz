package log

import (
	"testing"

	"github.com/kranz-org/kranz/internal/config"
)

func TestParseLineError(t *testing.T) {
	tests := []string{
		"ERROR: something went wrong",
		"Fatal error occurred",
		"panic: nil pointer",
		"Exception in thread",
		"Failed to connect",
	}

	for _, line := range tests {
		entry := ParseLine(line)
		if entry.Level != config.LogError {
			t.Errorf("expected LogError for '%s', got %d", line, entry.Level)
		}
	}
}

func TestParseLineWarn(t *testing.T) {
	tests := []string{
		"WARN: low memory",
		"Warning: deprecated API",
	}

	for _, line := range tests {
		entry := ParseLine(line)
		if entry.Level != config.LogWarn {
			t.Errorf("expected LogWarn for '%s', got %d", line, entry.Level)
		}
	}
}

func TestParseLineDebug(t *testing.T) {
	tests := []string{
		"DEBUG: entering function",
		"trace: request params",
	}

	for _, line := range tests {
		entry := ParseLine(line)
		if entry.Level != config.LogDebug {
			t.Errorf("expected LogDebug for '%s', got %d", line, entry.Level)
		}
	}
}

func TestParseLineInfo(t *testing.T) {
	tests := []string{
		"Server started on port 3000",
		"Listening on http://localhost:8080",
		"",
	}

	for _, line := range tests {
		entry := ParseLine(line)
		if entry.Level != config.LogInfo {
			t.Errorf("expected LogInfo for '%s', got %d", line, entry.Level)
		}
	}
}

func TestParseLines(t *testing.T) {
	lines := []string{"ERROR: fail", "INFO: start", "WARN: caution"}
	entries := ParseLines(lines)

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Level != config.LogError {
		t.Errorf("entry 0 should be error")
	}
	if entries[1].Level != config.LogInfo {
		t.Errorf("entry 1 should be info")
	}
	if entries[2].Level != config.LogWarn {
		t.Errorf("entry 2 should be warn")
	}
}

func TestStripANSI(t *testing.T) {
	input := "\x1b[31mRed text\x1b[0m normal"
	result := StripANSI(input)
	if result != "Red text normal" {
		t.Errorf("ANSI not stripped properly: '%s'", result)
	}
}

func TestHasANSI(t *testing.T) {
	if !HasANSI("\x1b[31mcolored") {
		t.Error("should detect ANSI codes")
	}
	if HasANSI("plain text") {
		t.Error("should not detect ANSI in plain text")
	}
}

func TestSearchSetPattern(t *testing.T) {
	s := NewSearcher()
	err := s.SetPattern("error|fail")
	if err != nil {
		t.Fatalf("SetPattern failed: %v", err)
	}
	if !s.HasPattern() {
		t.Error("should have pattern after SetPattern")
	}

	err = s.SetPattern("[invalid")
	if err == nil {
		t.Error("should error on invalid regex")
	}
}

func TestSearch(t *testing.T) {
	s := NewSearcher()
	s.SetPattern("(?i)error")

	lines := []string{
		"INFO: Starting server",
		"ERROR: Connection failed",
		"DEBUG: Processing request",
		"error: Another failure",
	}

	matches := s.Search(lines)
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d: %v", len(matches), matches)
	}
	if matches[0] != 1 || matches[1] != 3 {
		t.Errorf("expected matches at indices 1 and 3, got %v", matches)
	}
}

func TestSearchFindNext(t *testing.T) {
	s := NewSearcher()
	s.SetPattern("(?i)error")

	lines := []string{
		"INFO: Starting server",     // 0
		"ERROR: Connection failed",  // 1
		"DEBUG: Processing request", // 2
		"error: Another failure",    // 3
	}

	next := s.FindNext(lines, 1) // after index 1
	if next != 3 {
		t.Errorf("expected next match at 3, got %d", next)
	}

	next = s.FindNext(lines, 4) // after last, should wrap
	if next != 1 {
		t.Errorf("expected wrap to 1, got %d", next)
	}
}

func TestSearchFindPrev(t *testing.T) {
	s := NewSearcher()
	s.SetPattern("(?i)error")

	lines := []string{
		"INFO: Starting server",     // 0
		"ERROR: Connection failed",  // 1
		"DEBUG: Processing request", // 2
		"error: Another failure",    // 3
	}

	prev := s.FindPrev(lines, 3) // before index 3
	if prev != 1 {
		t.Errorf("expected prev match at 1, got %d", prev)
	}

	prev = s.FindPrev(lines, 0) // before first, should wrap to last
	if prev != 3 {
		t.Errorf("expected wrap to 3, got %d", prev)
	}
}
