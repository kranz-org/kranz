package ui

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

func newRunExportInput() textinput.Model {
	input := textinput.New()
	input.Prompt = "Path: "
	input.CharLimit = 0
	return input
}

func (m *Model) selectedRunIdentity() (app.RunTarget, uint32, bool) {
	if m.panelFocus == panelPinnedLogs {
		if target, ok := m.pinnedRunTarget(); ok && m.pinnedRun > 0 {
			return target, m.pinnedRun, true
		}
	}
	if !m.syncRunTarget() {
		return app.RunTarget{}, 0, false
	}
	run := m.selectedRun
	if m.runMode == runViewCombined || run == 0 {
		run = latestRun(m.runsForTarget(m.runTarget))
	}
	return m.runTarget, run, run > 0
}

func (m *Model) copySelectedRun() tea.Cmd {
	target, run, ok := m.selectedRunIdentity()
	if !ok {
		m.addNotification("export", "No run is selected", config.LogWarn)
		return nil
	}
	export, err := m.app.ExportRun(target, run)
	if err != nil {
		m.addNotification("export", err.Error(), config.LogError)
		return nil
	}
	payload := formatRunExport(export)
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	m.addNotification("export", fmt.Sprintf("Copied %s#%d to clipboard", runTargetLabel(target), run), config.LogInfo)
	// OSC 52 is the terminal-native clipboard protocol and works identically
	// for `up` and `attach`; it writes only after the explicit keypress.
	return tea.Printf("%s", "\x1b]52;c;"+encoded+"\a")
}

func (m *Model) openRunExport() tea.Cmd {
	target, run, ok := m.selectedRunIdentity()
	if !ok {
		m.addNotification("export", "No run is selected", config.LogWarn)
		return nil
	}
	m.exportTarget, m.exportRun = target, run
	name := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(runTargetLabel(target))
	m.exportInput.SetValue(fmt.Sprintf("kranz-%s-%d.log", name, run))
	m.exportInput.CursorEnd()
	m.mode = ModeRunExport
	return m.exportInput.Focus()
}

func (m *Model) handleRunExportKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.exportInput.Blur()
		m.mode = ModeNormal
		return m, nil
	case "enter":
		path := strings.TrimSpace(m.exportInput.Value())
		if path == "" {
			m.addNotification("export", "Export path cannot be empty", config.LogError)
			return m, nil
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(m.workingDirectory, path)
		}
		export, err := m.app.ExportRun(m.exportTarget, m.exportRun)
		if err != nil {
			m.addNotification("export", err.Error(), config.LogError)
			return m, nil
		}
		payload := []byte(formatRunExport(export))
		m.exportInput.Blur()
		return m, func() tea.Msg {
			err := os.WriteFile(filepath.Clean(path), payload, 0o644)
			return runExportResultMsg{path: filepath.Clean(path), err: err}
		}
	}
	var command tea.Cmd
	m.exportInput, command = m.exportInput.Update(msg)
	return m, command
}

func (m *Model) renderRunExportView() string {
	identity := fmt.Sprintf("%s#%d", runTargetLabel(m.exportTarget), m.exportRun)
	lines := []string{ModalTitleStyle.Render(" Export run "), "", "  " + identity, "", "  " + m.exportInput.View(), "",
		renderModalShortcuts("  [Enter] Write file  [Esc] Cancel", lipgloss.NewStyle().Foreground(ColorDim))}
	return m.placeOverlay(renderFlushModal(strings.Join(lines, "\n")))
}

func formatRunExport(export app.RunExport) string {
	summary := export.Summary
	finished := "running"
	if !summary.FinishedAt.IsZero() {
		finished = summary.FinishedAt.Format(time.RFC3339Nano)
	}
	exit := "-"
	if summary.ExitCode != nil {
		exit = fmt.Sprint(*summary.ExitCode)
	}
	var output strings.Builder
	_, _ = fmt.Fprintf(&output, "Kranz run: %s#%d\nStatus: %s\nStarted: %s\nFinished: %s\nExit code: %s\nSurface: %s\nClient: %s\nStart reason: %s\n",
		runTargetLabel(summary.Target), summary.Run, summary.Status, summary.StartedAt.Format(time.RFC3339Nano), finished, exit,
		summary.Surface, summary.ClientLabel, summary.StartReason)
	_, _ = fmt.Fprintf(&output, "Output: %s; captured %d lines/%d bytes; retained %d lines/%d bytes; missing %d lines/%d bytes\n",
		summary.Output.State, summary.Output.CapturedLines, summary.Output.CapturedBytes, summary.Output.RetainedLines, summary.Output.RetainedBytes,
		summary.Output.MissingLines, summary.Output.MissingBytes)
	_, _ = fmt.Fprintf(&output, "Retention: oldest #%d; budgets %d runs/%d entries/%d bytes; evicted %d summaries\n---\n",
		export.Retention.OldestRetainedRun, export.Retention.MaxRuns, export.Retention.MaxEntries, export.Retention.MaxBytes, export.Retention.EvictedRuns)
	if summary.Output.MissingLines > 0 || summary.Output.MissingBytes > 0 {
		_, _ = fmt.Fprintf(&output, "[Kranz] Output truncated · missing %d lines / %d bytes\n", summary.Output.MissingLines, summary.Output.MissingBytes)
	}
	for _, entry := range export.Entries {
		line := strings.TrimRight(strings.ReplaceAll(ansi.Strip(entry.Raw), "\r", ""), "\n")
		_, _ = fmt.Fprintf(&output, "[%s] [%s] [seq=%d] %s\n", entry.Timestamp.Format(time.RFC3339Nano), entry.Source, entry.Sequence, line)
	}
	return output.String()
}
