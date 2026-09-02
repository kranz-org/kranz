package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// Row-count bookkeeping for the log panels. Scrolling needs to know how tall
// the whole history is, but styling it to find out would tie the cost of a
// frame to the retention limit rather than to the size of the terminal.

// logPanelSlot identifies which panel a cache belongs to. Both panels can be on
// screen at once showing different targets at different widths.
type logPanelSlot int

const (
	logSlotMain logPanelSlot = iota
	logSlotPinned
	logSlotCount
)

// logRowMetrics remembers how many terminal rows each log sequence occupies. A
// log entry never changes after it is emitted, so its sequence is a sound cache
// key; everything that would change the answer instead invalidates the cache.
type logRowMetrics struct {
	target   app.RunTarget
	width    int
	wrap     bool
	showTime bool
	counts   map[uint64]int
}

func (m *Model) logRowMetricsFor(slot logPanelSlot, target app.RunTarget, width int) *logRowMetrics {
	metrics := m.logRowCache[slot]
	if metrics == nil || metrics.target != target || metrics.width != width ||
		metrics.wrap != m.wrapLogs || metrics.showTime != m.showLogTime {
		metrics = &logRowMetrics{
			target:   target,
			width:    width,
			wrap:     m.wrapLogs,
			showTime: m.showLogTime,
			counts:   make(map[uint64]int),
		}
		m.logRowCache[slot] = metrics
	}
	return metrics
}

// rowCount answers from the cache when it can. Sequence zero means the entry
// carries no identity worth caching, so it is measured every time.
func (c *logRowMetrics) rowCount(m *Model, entry config.LogEntry, width int) int {
	if entry.Sequence == 0 {
		return m.countLogEntryRows(entry, width)
	}
	if count, ok := c.counts[entry.Sequence]; ok {
		return count
	}
	count := m.countLogEntryRows(entry, width)
	c.counts[entry.Sequence] = count
	return count
}

// forget drops measurements for entries retention has already evicted. It runs
// only once the cache has outgrown the history it describes, so the sweep costs
// nothing on an ordinary frame.
func (c *logRowMetrics) forget(entries []config.LogEntry) {
	if len(c.counts) <= 2*len(entries)+64 {
		return
	}
	oldest := uint64(0)
	if len(entries) > 0 {
		oldest = entries[0].Sequence
	}
	for sequence := range c.counts {
		if sequence < oldest {
			delete(c.counts, sequence)
		}
	}
}

// countLogEntryRows must agree exactly with logEntryVisualRows, which is what
// keeps the scrollbar honest about output nobody has styled yet.
func (m *Model) countLogEntryRows(entry config.LogEntry, contentWidth int) int {
	count := 0
	for _, segment := range strings.Split(m.displayLogEntry(entry), "\n") {
		if !m.wrapLogs {
			count++
			continue
		}
		count += strings.Count(ansi.Hardwrap(styleLogLine(segment), contentWidth, true), "\n") + 1
	}
	return count
}
