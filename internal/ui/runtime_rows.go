package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

const runtimeRowNameWidth = 22

// runtimeDiscoveryTimeout bounds one registry probe round. The registry
// itself bounds each socket dial; this additionally bounds the whole listing
// call so a pathological number of slow sockets cannot freeze the caller.
const runtimeDiscoveryTimeout = 3 * time.Second

// runtimeRow is one line of the runtime switcher: a session record annotated
// with the display and selection facts the modal needs, computed once so
// rendering and key/mouse handling never re-derive them.
type runtimeRow struct {
	Record     kranzruntime.SessionRecord
	IsCurrent  bool
	Selectable bool
	// Reason explains why Selectable is false; empty when Selectable is true.
	Reason string
}

// runtimeListMsg carries one discovery refresh. Generation lets the caller
// drop a result superseded by a newer refresh or by leaving the switcher.
type runtimeListMsg struct {
	generation uint64
	rows       []runtimeRow
	err        error
}

// discoverRuntimeRows lists every locally registered runtime and classifies
// each one for display. currentSessionID marks (and always sorts first) the
// runtime the caller is already attached to. All user-facing connections,
// including this dashboard's TUI connection, remain visible in the client
// surfaces so the current row describes every way the runtime is being used.
func discoverRuntimeRows(ctx context.Context, registry *kranzruntime.Registry, clientVersion, currentSessionID string) ([]runtimeRow, error) {
	records, err := registry.List(ctx, clientVersion)
	if err != nil {
		return nil, err
	}
	rows := make([]runtimeRow, 0, len(records))
	for _, record := range records {
		row := runtimeRow{Record: record, IsCurrent: record.ID == currentSessionID}
		switch record.State {
		case kranzruntime.SessionRunning:
			row.Selectable = !row.IsCurrent
		case kranzruntime.SessionIncompatible:
			row.Reason = "This runtime speaks a different protocol version. Update Kranz to attach to it."
		case kranzruntime.SessionUnreachable:
			row.Reason = "This runtime is registered but is not answering. The list keeps retrying it."
		default:
			row.Reason = "This runtime reports state " + string(record.State) + " and cannot be attached to."
		}
		rows = append(rows, row)
	}
	sortRuntimeRows(rows)
	return rows, nil
}

// sortRuntimeRows puts the current runtime first, then orders the rest by
// most recently started, breaking ties by name and then ID so rows do not
// reorder between otherwise-identical refreshes.
func sortRuntimeRows(rows []runtimeRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.IsCurrent != b.IsCurrent {
			return a.IsCurrent
		}
		if !a.Record.StartedAt.Equal(b.Record.StartedAt) {
			return a.Record.StartedAt.After(b.Record.StartedAt)
		}
		if a.Record.Name != b.Record.Name {
			return a.Record.Name < b.Record.Name
		}
		return a.Record.ID < b.Record.ID
	})
}

// runtimeRowUptime formats how long a runtime has been running. It follows
// cmd/kranz's shortDuration for every unit above a minute, and deliberately
// differs below one: a table read at a glance says "just now" where a CLI
// column that is scanned for exact values says "42s". It is a copy rather
// than a call because shortDuration lives in package main and cannot be
// imported here; change the two together.
func runtimeRowUptime(record kranzruntime.SessionRecord) string {
	d := time.Since(record.StartedAt)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h" + strconv.Itoa(int(d.Minutes())%60) + "m"
	default:
		return strconv.Itoa(int(d.Hours())/24) + "d" + strconv.Itoa(int(d.Hours())%24) + "h"
	}
}

// runtimeRowSurfaceLabel joins the deduplicated client surfaces a row is
// reporting into the compact form the modal shows, for example "TUI · MCP".
// It never repeats a surface and never shows a count.
func runtimeRowSurfaceLabel(row runtimeRow) string {
	if row.Record.State != kranzruntime.SessionRunning || len(row.Record.ClientSurfaces) == 0 {
		return ""
	}
	labels := make([]string, len(row.Record.ClientSurfaces))
	for i, surface := range row.Record.ClientSurfaces {
		labels[i] = strings.ToUpper(surface)
	}
	return strings.Join(labels, " · ")
}

// runtimeRowStatusLabel is the row's status word on its own, never merged
// with client-surface detail: PRD 3.2 keeps it visible even in a narrow
// terminal, after the path and the surface list have already given way.
func runtimeRowStatusLabel(row runtimeRow) string {
	if row.IsCurrent {
		return "current"
	}
	switch row.Record.State {
	case kranzruntime.SessionRunning:
		return "started"
	case kranzruntime.SessionIncompatible:
		return "incompatible"
	case kranzruntime.SessionUnreachable:
		return "unreachable"
	default:
		return string(row.Record.State)
	}
}

func runtimeRowServicesLabel(row runtimeRow) string {
	if row.Record.Services == nil || row.Record.Running == nil {
		return "-"
	}
	return fmt.Sprintf("%d/%d", *row.Record.Running, *row.Record.Services)
}

// runtimeRowDisplayName prefers the runtime's own name and falls back to the
// project name, matching what `kranz ps` shows for the same session.
func runtimeRowDisplayName(row runtimeRow) string {
	if row.Record.Name != "" {
		return row.Record.Name
	}
	return row.Record.Project
}

// runtimeTableLayout is the column budget one runtime table was laid out
// with: which optional columns survived the available width, and how wide
// each surviving column ended up.
type runtimeTableLayout struct {
	nameWidth, statusWidth, clientsWidth, servicesWidth, uptimeWidth, pathWidth int
	showClients, showUptime, showPath                                           bool
}

func newRuntimeTableLayout(width int) runtimeTableLayout {
	layout := runtimeTableLayout{
		nameWidth: runtimeRowNameWidth, statusWidth: 12, clientsWidth: 16,
		servicesWidth: 8, uptimeWidth: 8, showClients: true, showUptime: true,
	}
	coreWidth := func() int {
		widths := []int{layout.nameWidth, layout.statusWidth, layout.servicesWidth}
		if layout.showClients {
			widths = append(widths, layout.clientsWidth)
		}
		if layout.showUptime {
			widths = append(widths, layout.uptimeWidth)
		}
		total := 2 * (len(widths) - 1)
		for _, columnWidth := range widths {
			total += columnWidth
		}
		return total
	}

	if remaining := width - coreWidth() - 2; remaining >= 8 {
		layout.showPath, layout.pathWidth = true, remaining
	}
	if coreWidth() > width {
		layout.showPath, layout.pathWidth = false, 0
		layout.clientsWidth = max(8, layout.clientsWidth-(coreWidth()-width))
	}
	if coreWidth() > width {
		layout.nameWidth = max(12, layout.nameWidth-(coreWidth()-width))
	}
	if coreWidth() > width {
		layout.showUptime = false
	}
	if coreWidth() > width {
		layout.showClients = false
	}
	if coreWidth() > width {
		layout.nameWidth = max(4, layout.nameWidth-(coreWidth()-width))
	}
	if coreWidth() > width {
		layout.statusWidth = max(8, layout.statusWidth-(coreWidth()-width))
	}
	if coreWidth() > width {
		layout.servicesWidth = max(3, layout.servicesWidth-(coreWidth()-width))
	}
	return layout
}

func (layout runtimeTableLayout) columns(name, status, clients, services, uptime, directory string) string {
	columns := []string{
		padOrTruncate(name, layout.nameWidth),
		padOrTruncate(status, layout.statusWidth),
	}
	if layout.showClients {
		columns = append(columns, padOrTruncate(clients, layout.clientsWidth))
	}
	columns = append(columns, padOrTruncate(services, layout.servicesWidth))
	if layout.showUptime {
		columns = append(columns, fmt.Sprintf("%*s", layout.uptimeWidth, ansi.Truncate(uptime, layout.uptimeWidth, "…")))
	}
	line := strings.Join(columns, "  ")
	if layout.showPath && directory != "" {
		line += "  " + ansi.Truncate(directory, layout.pathWidth, "…")
	}
	return line
}

func runtimeRowHeader(width int) string {
	layout := newRuntimeTableLayout(width)
	return layout.columns("RUNTIME", "STATUS", "CLIENTS", "SERVICES", "UPTIME", "DIRECTORY")
}

// runtimeRowLine lays out one row within width, dropping the path and then
// the client-surface detail before it would ever truncate the name or the
// status word (PRD 3.2: "path and client surfaces shrink before name and
// state"). It takes width explicitly rather than reading it off a *Model so
// the bare-launch runtime picker, which has no Model, can render identical
// rows.
func runtimeRowLine(row runtimeRow, width int) string {
	layout := newRuntimeTableLayout(width)
	clients := runtimeRowSurfaceLabel(row)
	if clients == "" {
		clients = "-"
	}
	return layout.columns(
		runtimeRowDisplayName(row), runtimeRowStatusLabel(row), clients,
		runtimeRowServicesLabel(row), runtimeRowUptime(row.Record), row.Record.Directory,
	)
}

func runtimeRowHitLabel(row runtimeRow) string {
	return strings.TrimSpace(padOrTruncate(runtimeRowDisplayName(row), runtimeRowNameWidth))
}

func padOrTruncate(text string, width int) string {
	current := lipgloss.Width(text)
	if current == width {
		return text
	}
	if current > width {
		return ansi.Truncate(text, width, "…")
	}
	return text + strings.Repeat(" ", width-current)
}
