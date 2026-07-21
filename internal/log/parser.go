// Package log classifies, sanitizes, and searches captured service output.
package log

import (
	"regexp"
	"strings"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// errorPatterns identify common error-level prefixes and words.
var errorPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\berror\b`),
	regexp.MustCompile(`(?i)\bfatal\b`),
	regexp.MustCompile(`(?i)\bpanic\b`),
	regexp.MustCompile(`(?i)\bexception\b`),
	regexp.MustCompile(`(?i)\bfailed\b`),
}

// warnPatterns identify common warning-level prefixes and words.
var warnPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bwarn(?:ing)?\b`),
}

// debugPatterns identify common debug-level prefixes and words.
var debugPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bdebug\b`),
	regexp.MustCompile(`(?i)\btrace\b`),
}

// ParseLine sanitizes one line and infers its semantic severity.
func ParseLine(line string) config.LogEntry {
	entry := config.LogEntry{
		Timestamp: time.Now(),
		Raw:       line,
	}

	lower := strings.ToLower(line)

	// Error takes precedence when a line contains multiple severity tokens.
	for _, p := range errorPatterns {
		if p.MatchString(lower) {
			entry.Level = config.LogError
			entry.Text = line
			return entry
		}
	}

	// Warnings take precedence over debug markers.
	for _, p := range warnPatterns {
		if p.MatchString(lower) {
			entry.Level = config.LogWarn
			entry.Text = line
			return entry
		}
	}

	// Debug is the lowest explicit severity.
	for _, p := range debugPatterns {
		if p.MatchString(lower) {
			entry.Level = config.LogDebug
			entry.Text = line
			return entry
		}
	}

	// Untagged output is treated as informational.
	entry.Level = config.LogInfo
	entry.Text = line
	return entry
}

// ParseLines parses a batch while preserving input order.
func ParseLines(lines []string) []config.LogEntry {
	entries := make([]config.LogEntry, 0, len(lines))
	for _, line := range lines {
		entries = append(entries, ParseLine(line))
	}
	return entries
}

// StripANSI removes terminal control sequences while preserving readable text.
func StripANSI(s string) string {
	// This package only accepts CSI styling; the UI performs stricter stripping
	// before rendering untrusted child-process output.
	ansiRe := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	return ansiRe.ReplaceAllString(s, "")
}

// HasANSI reports whether a string contains a CSI escape sequence.
func HasANSI(s string) bool {
	return strings.Contains(s, "\x1b[")
}
