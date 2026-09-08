package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

// clientRow is one attached client, named together with the runtime it is
// attached to. Runtimes and their clients are different kinds of thing —
// `ps` lists what is running, this lists who is working in it — so they are
// two commands rather than one table with a mode column doing both jobs.
type clientRow struct {
	Runtime string    `json:"runtime"`
	ID      string    `json:"id"`
	Project string    `json:"project"`
	Surface string    `json:"surface"`
	Label   string    `json:"label"`
	PID     int       `json:"pid"`
	Version string    `json:"version"`
	Since   time.Time `json:"connected_at"`
}

func runClients(options kranzcli.GlobalOptions, args []string, stdout, stderr io.Writer) int {
	formatter, err := parseRowFormat("clients", options.Output, args)
	if err != nil {
		return kranzcli.WriteError(stdout, stderr, options.Output, err)
	}
	registry, err := kranzruntime.DefaultRegistry()
	if err != nil {
		return kranzcli.WriteError(stdout, stderr, options.Output, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	records, err := registry.List(ctx, version)
	if err != nil {
		return kranzcli.WriteError(stdout, stderr, options.Output, err)
	}
	rows := make([]clientRow, 0)
	for _, record := range records {
		if options.Project != "" && !matchesRuntimeReference(record, options.Project) {
			continue
		}
		if record.State != kranzruntime.SessionRunning {
			continue
		}
		client, dialErr := kranzruntime.DialContext(ctx, record.Socket, version)
		if dialErr != nil {
			continue
		}
		connected, clientsErr := client.Clients()
		_ = client.Close()
		if clientsErr != nil {
			continue
		}
		for _, entry := range connected {
			// Discovery and the background owner are runtime infrastructure,
			// not clients using the runtime. Keep this command aligned with
			// the CLIENTS column in `ps` and the TUI runtime switcher.
			if !listableClient(entry, ownPID()) {
				continue
			}
			rows = append(rows, clientRow{
				Runtime: record.Name, ID: record.ID, Project: record.Project,
				Surface: entry.Surface, Label: entry.Label, PID: entry.PID,
				Version: entry.Version, Since: entry.ConnectedAt,
			})
		}
	}
	if options.Output == kranzcli.OutputJSON {
		if err := kranzcli.WriteJSON(stdout, rows); err != nil {
			return kranzcli.WriteError(stdout, stderr, options.Output, err)
		}
		return 0
	}
	if formatter != nil {
		formatted := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			formatted = append(formatted, clientFormatRow(row))
		}
		if err := formatter.write(stdout, clientFormatHeaders(), formatted); err != nil {
			return kranzcli.WriteError(stdout, stderr, options.Output, err)
		}
		return 0
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(stdout, "No client is attached to a Kranz runtime.")
		return 0
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "RUNTIME\tPID\tCLIENT\tCONNECTED")
	for _, row := range rows {
		_, _ = fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", row.Runtime, row.PID, clientDisplayLabel(row.Surface, row.Label), shortDuration(time.Since(row.Since)))
	}
	if err := w.Flush(); err != nil {
		return kranzcli.WriteError(stdout, stderr, options.Output, err)
	}
	return 0
}

func clientFormatHeaders() map[string]any {
	return map[string]any{
		"Runtime": "RUNTIME", "ID": "ID", "FullID": "FULL ID", "Project": "PROJECT", "PID": "PID",
		"Client": "CLIENT", "Surface": "SURFACE", "Label": "LABEL",
		"Version": "VERSION", "Connected": "CONNECTED", "ConnectedAt": "CONNECTED AT",
	}
}

func clientFormatRow(row clientRow) map[string]any {
	return map[string]any{
		"Runtime": row.Runtime, "ID": shortID(row.ID), "FullID": row.ID, "Project": row.Project,
		"PID": row.PID, "Client": clientDisplayLabel(row.Surface, row.Label),
		"Surface": row.Surface, "Label": row.Label, "Version": row.Version,
		"Connected": shortDuration(time.Since(row.Since)), "ConnectedAt": row.Since.Format(time.RFC3339),
	}
}

// ownPID names this process so the listing can exclude its own probe.
func ownPID() int { return os.Getpid() }

func listableClient(client kranzruntime.ClientInfo, listingPID int) bool {
	return client.PID != listingPID && client.Surface != "background"
}

func surfaceLabel(surface string) string {
	if surface == "" {
		return "unknown"
	}
	return surface
}

// clientDisplayLabel keeps the stable surface visible without repeating the
// product name carried by built-in labels. A meaningful label still identifies
// the particular client, for example "MCP: codex" or "TUI: attach".
func clientDisplayLabel(surface, label string) string {
	kind := strings.ToUpper(surfaceLabel(surface))
	switch label {
	case "", "Kranz dashboard", "Kranz CLI", "Kranz MCP":
		return kind
	case "Kranz attach":
		return kind + ": attach"
	case "Kranz foreground":
		return kind + ": foreground"
	}
	prefix := kind + ":"
	if len(label) >= len(prefix) && strings.EqualFold(label[:len(prefix)], prefix) {
		return kind + label[len(kind):]
	}
	return kind + ": " + label
}

func matchesRuntimeReference(record kranzruntime.SessionRecord, reference string) bool {
	return record.Name == reference || record.ID == reference || strings.HasPrefix(record.ID, reference)
}
