package ui

import (
	"strconv"
	"strings"
	"time"
)

// formatExactDuration omits zero units and spaces the remaining units without
// rounding configured values. Seconds retain any fractional precision.
func formatExactDuration(duration time.Duration) string {
	if duration == 0 {
		return "0s"
	}
	negative := duration < 0
	magnitude := uint64(duration)
	if negative {
		// Avoid overflowing the smallest representable duration.
		magnitude = uint64(-(duration + 1)) + 1
	}
	parts := make([]string, 0, 3)
	if hours := magnitude / uint64(time.Hour); hours > 0 {
		parts = append(parts, strconv.FormatUint(hours, 10)+"h")
	}
	magnitude %= uint64(time.Hour)
	if minutes := magnitude / uint64(time.Minute); minutes > 0 {
		parts = append(parts, strconv.FormatUint(minutes, 10)+"m")
	}
	magnitude %= uint64(time.Minute)
	if magnitude > 0 {
		parts = append(parts, time.Duration(magnitude).String())
	}
	value := strings.Join(parts, " ")
	if negative {
		value = "-" + value
	}
	return value
}
