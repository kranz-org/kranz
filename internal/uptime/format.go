// Package uptime formats elapsed runtime and service uptime for terminal output.
package uptime

import (
	"fmt"
	"time"
)

const (
	day  = 24 * time.Hour
	week = 7 * day
	year = 365 * day
)

// Format truncates elapsed time to whole units, separating them with separator.
// Short uptimes use seconds; longer uptimes retain two or three useful units.
// Years are fixed 365-day durations, rather than calendar anniversaries.
func Format(elapsed time.Duration, separator string) string {
	if elapsed < 0 {
		elapsed = 0
	}
	seconds := int64(elapsed / time.Second)
	minutes := seconds / 60
	hours := minutes / 60
	days := hours / 24
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", seconds)
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm%s%ds", minutes, separator, seconds%60)
	case elapsed < day:
		return fmt.Sprintf("%dh%s%dm", hours, separator, minutes%60)
	case elapsed < week:
		return fmt.Sprintf("%dd%s%dh%s%dm", days, separator, hours%24, separator, minutes%60)
	case elapsed < year:
		return fmt.Sprintf("%dw%s%dd%s%dh", days/7, separator, days%7, separator, hours%24)
	default:
		remainingDays := days % 365
		return fmt.Sprintf("%dy%s%dw%s%dd", days/365, separator, remainingDays/7, separator, remainingDays%7)
	}
}
