package uptime

import (
	"strings"
	"testing"
	"time"
)

func TestFormatRangesAndBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		elapsed time.Duration
		want    string
	}{
		{"negative", -time.Second, "0s"},
		{"zero", 0, "0s"},
		{"fractional second", 999 * time.Millisecond, "0s"},
		{"whole seconds", 42*time.Second + 900*time.Millisecond, "42s"},
		{"before minute", time.Minute - time.Nanosecond, "59s"},
		{"minute", time.Minute, "1m 0s"},
		{"minutes seconds", 3*time.Minute + 12*time.Second + 900*time.Millisecond, "3m 12s"},
		{"before hour", time.Hour - time.Nanosecond, "59m 59s"},
		{"hour", time.Hour, "1h 0m"},
		{"hours minutes", 10*time.Hour + 20*time.Minute + 59*time.Second, "10h 20m"},
		{"before day", day - time.Nanosecond, "23h 59m"},
		{"day", day, "1d 0h 0m"},
		{"days hours minutes", 2*day + 4*time.Hour + 15*time.Minute + 59*time.Second, "2d 4h 15m"},
		{"before week", week - time.Nanosecond, "6d 23h 59m"},
		{"week", week, "1w 0d 0h"},
		{"weeks days hours", 2*week + 4*day + 5*time.Hour + 59*time.Minute, "2w 4d 5h"},
		{"before year", year - time.Nanosecond, "52w 0d 23h"},
		{"year", year, "1y 0w 0d"},
		{"years weeks days", 2*year + 3*week + 4*day + 23*time.Hour, "2y 3w 4d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Format(tt.elapsed, " "); got != tt.want {
				t.Errorf("TUI format = %q, want %q", got, tt.want)
			}
			if got, want := Format(tt.elapsed, ""), strings.ReplaceAll(tt.want, " ", ""); got != want {
				t.Errorf("CLI format = %q, want %q", got, want)
			}
		})
	}
}
