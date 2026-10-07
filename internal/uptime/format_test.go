package uptime

import (
	"strings"
	"testing"
	"time"
)

func TestFormatRangesAndBoundaries(t *testing.T) {
	start := time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC)
	year := 365 * day
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
		{"before month", 31*day - time.Nanosecond, "4w 2d 23h"},
		{"month", 31 * day, "1mo 0d 0h"},
		{"months days hours", 31*day + 2*day + 3*time.Hour + 59*time.Minute, "1mo 2d 3h"},
		{"before year", year - time.Nanosecond, "11mo 30d 23h"},
		{"year", year, "1y 0mo 0d"},
		{"years weeks days", 2*year + 3*week + 4*day + 23*time.Hour, "2y 0mo 25d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Format(start, start.Add(tt.elapsed), " "); got != tt.want {
				t.Errorf("TUI format = %q, want %q", got, tt.want)
			}
			if got, want := Format(start, start.Add(tt.elapsed), ""), strings.ReplaceAll(tt.want, " ", ""); got != want {
				t.Errorf("CLI format = %q, want %q", got, want)
			}
		})
	}
}

func TestFormatCalendarMonthsAndYears(t *testing.T) {
	for _, tt := range []struct {
		name, start, now, want string
	}{
		{"short February before anniversary", "2023-01-31T12:00:00Z", "2023-02-28T11:59:59Z", "3w 6d 23h"},
		{"short February anniversary", "2023-01-31T12:00:00Z", "2023-02-28T12:00:00Z", "1mo 0d 0h"},
		{"leap February before anniversary", "2024-01-31T12:00:00Z", "2024-02-29T11:59:59Z", "4w 0d 23h"},
		{"leap February anniversary", "2024-01-31T12:00:00Z", "2024-02-29T12:00:00Z", "1mo 0d 0h"},
		{"preserve original month day", "2023-01-31T12:00:00Z", "2023-03-30T12:00:00Z", "1mo 30d 0h"},
		{"next original month day", "2023-01-31T12:00:00Z", "2023-03-31T12:00:00Z", "2mo 0d 0h"},
		{"leap year before anniversary", "2023-03-01T12:00:00Z", "2024-03-01T11:59:59Z", "11mo 28d 23h"},
		{"leap year anniversary", "2023-03-01T12:00:00Z", "2024-03-01T12:00:00Z", "1y 0mo 0d"},
		{"leap day anniversary", "2024-02-29T12:00:00Z", "2025-02-28T12:00:00Z", "1y 0mo 0d"},
		{"year month day remainder", "2024-01-31T12:00:00Z", "2025-04-02T23:59:59Z", "1y 2mo 2d"},
		{"calendar year rollover", "2024-12-31T12:00:00Z", "2025-01-31T12:00:00Z", "1mo 0d 0h"},
		{"UTC ignores display offset", "2023-02-01T01:00:00+02:00", "2023-02-28T23:00:00Z", "1mo 0d 0h"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			start, err := time.Parse(time.RFC3339, tt.start)
			if err != nil {
				t.Fatal(err)
			}
			now, err := time.Parse(time.RFC3339, tt.now)
			if err != nil {
				t.Fatal(err)
			}
			if got := Format(start, now, " "); got != tt.want {
				t.Fatalf("calendar format = %q, want %q", got, tt.want)
			}
		})
	}
}
