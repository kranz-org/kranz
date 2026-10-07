package ui

import (
	"testing"
	"time"
)

func TestFormatExactDuration(t *testing.T) {
	for _, tt := range []struct {
		value time.Duration
		want  string
	}{
		{0, "0s"},
		{10 * time.Minute, "10m"},
		{time.Hour, "1h"},
		{time.Minute + 30*time.Second, "1m 30s"},
		{time.Hour + 5*time.Second, "1h 5s"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1h 2m 3s"},
		{500 * time.Millisecond, "500ms"},
		{500 * time.Microsecond, "500µs"},
		{time.Nanosecond, "1ns"},
		{time.Minute + 500*time.Millisecond, "1m 500ms"},
		{time.Second + 234567890*time.Nanosecond, "1.23456789s"},
		{-(time.Minute + 30*time.Second), "-1m 30s"},
		{time.Duration(1<<63 - 1), "2562047h 47m 16.854775807s"},
		{time.Duration(-1 << 63), "-2562047h 47m 16.854775808s"},
	} {
		if got := formatExactDuration(tt.value); got != tt.want {
			t.Errorf("formatExactDuration(%d) = %q, want %q", tt.value, got, tt.want)
		}
	}
}
