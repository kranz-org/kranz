// Package uptime formats elapsed runtime and service uptime for terminal output.
package uptime

import (
	"fmt"
	"time"
)

const (
	day  = 24 * time.Hour
	week = 7 * day
)

// Format truncates uptime to whole units, separating them with separator.
// Calendar months and years use the start date in UTC.
func Format(startedAt, now time.Time, separator string) string {
	elapsed := now.Sub(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	startedAt, now = startedAt.UTC(), now.UTC()
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
	case now.Before(calendarOffset(startedAt, 0, 1)):
		return fmt.Sprintf("%dw%s%dd%s%dh", days/7, separator, days%7, separator, hours%24)
	default:
		years := now.Year() - startedAt.Year()
		if calendarOffset(startedAt, years, 0).After(now) {
			years--
		}
		months := 0
		for months < 11 && !calendarOffset(startedAt, years, months+1).After(now) {
			months++
		}
		remaining := now.Sub(calendarOffset(startedAt, years, months))
		remainingDays := int64(remaining / day)
		if years == 0 {
			return fmt.Sprintf("%dmo%s%dd%s%dh", months, separator, remainingDays, separator, int64(remaining/time.Hour)%24)
		}
		return fmt.Sprintf("%dy%s%dmo%s%dd", years, separator, months, separator, remainingDays)
	}
}

// calendarOffset preserves the start's time of day and clamps dates such as
// February 29 or January 31 to the last day of the target month.
func calendarOffset(start time.Time, years, months int) time.Time {
	last := time.Date(start.Year()+years, start.Month()+time.Month(months)+1, 0, 0, 0, 0, 0, time.UTC)
	return time.Date(last.Year(), last.Month(), min(start.Day(), last.Day()),
		start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), time.UTC)
}
