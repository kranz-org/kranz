package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
)

// Overlays drawn on top of the dashboard: help, health history, notifications,
// the confirmations, the port-conflict dialog, and the theme picker.

// renderHelpView composites scrollable help over a dimmed dashboard.
func (m *Model) renderHelpView() string {
	body := m.helpBodyLines()
	visibleHeight := m.helpVisibleBodyHeight()
	maxOffset := max(0, len(body)-visibleHeight)
	offset := min(maxOffset, max(0, m.helpOffset))
	end := min(len(body), offset+visibleHeight)
	visible := body[offset:end]

	lines := []string{ModalTitleStyle.Render(" Kranz Help "), ""}
	lines = append(lines, visible...)
	lines = append(lines, "")
	footer := "  [Esc] Close"
	if maxOffset > 0 {
		footer = fmt.Sprintf("  [↑/k] Up  [↓/j] Down  %d–%d/%d    [Esc] Close", offset+1, end, len(body))
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(ColorDim).Render(footer))

	content := renderModal(strings.Join(lines, "\n"))
	return m.placeOverlay(content)
}

func helpEntries() []helpEntry {
	return []helpEntry{
		{"1 / 2 / 3", "Focus panels; 1 switches Services/Tags when the list is focused"},
		{"Shift+3", "Pin focused service logs above the active log panel"},
		{"3 again", "Switch focus between pinned and current logs"},
		{"Tab / Shift+Tab", "Focus the next or previous panel, including pinned logs"},
		{"↑/↓ j/k", "Navigate or scroll focused panel"},
		{"←/→", "Cycle Services/Tags while the list panel is focused"},
		{"t", "Toggle Services/Tags from any panel"},
		{"Enter", "In Tags: expand or collapse services below the focused tag"},
		{"Space", "Select/unselect service or tag"},
		{"s", "Start stopped or stop running targets"},
		{"Shift+S", "Start or stop only targets, ignoring dependency expansion"},
		{"a", "Select/clear all services"},
		{"A", "Stop all services"},
		{"r", "Restart selected service"},
		{"R", "Restart running services"},
		{"T", "Clear selected tags"},
		{"h", "Health check history"},
		{"n", "Notification center"},
		{"/", "Open the regex log search; Tab switches filter/highlight"},
		{"Enter in search", "Apply the query and keep editing it"},
		{"Ctrl+U in search", "Erase the query being edited"},
		{"Ctrl+V in search", "Paste clipboard text at the caret"},
		{"Esc in search", "Close the editor, keeping the last applied filter"},
		{"Esc", "Clear the active log filter"},
		{"n/N", "Next/previous highlighted match"},
		{"w", "Toggle log line wrapping"},
		{"i", "Show/hide captured-at time in logs"},
		{"f", "Pause/resume logs"},
		{"c", "Clear focused or pinned service logs after confirmation"},
		{"q", "Quit"},
		{"?", "Show this help"},
		{"Ctrl+T", "Choose and persist a theme"},
		{"p / a / b / m", "In Themes: toggle theme, accent, background, or Auto/Dark/Light mode"},
		{"Enter / c", "In Themes: save globally or save to the project config"},
		{"Ctrl+L", "Reload configuration and detect terminal appearance"},
		{"Ctrl+O", "Open command shell; Ctrl+O returns to Kranz"},
	}
}

func (m *Model) helpBodyLines() []string {
	helpPairs := helpEntries()
	availableWidth := max(20, min(105, m.width-6))
	if availableWidth < 86 {
		lines := make([]string, 0, len(helpPairs))
		for _, entry := range helpPairs {
			lines = append(lines, renderHelpCell(entry.key, entry.desc, availableWidth)...)
		}
		return lines
	}
	cellWidth := (availableWidth - 3) / 2
	rows := (len(helpPairs) + 1) / 2
	lines := make([]string, 0, rows)
	for row := 0; row < rows; row++ {
		left := renderHelpCell(helpPairs[row].key, helpPairs[row].desc, cellWidth)
		right := []string(nil)
		if rightIndex := row + rows; rightIndex < len(helpPairs) {
			right = renderHelpCell(helpPairs[rightIndex].key, helpPairs[rightIndex].desc, cellWidth)
		}
		rowHeight := max(len(left), len(right))
		for line := 0; line < rowHeight; line++ {
			leftLine, rightLine := strings.Repeat(" ", cellWidth), ""
			if line < len(left) {
				leftLine = left[line]
			}
			if line < len(right) {
				rightLine = right[line]
			}
			lines = append(lines, leftLine+"   "+rightLine)
		}
	}
	return lines
}

func (m *Model) helpVisibleBodyHeight() int {
	return max(1, m.height-10)
}

func (m *Model) maxHelpOffset() int {
	return max(0, len(m.helpBodyLines())-m.helpVisibleBodyHeight())
}

func renderHelpCell(keyText, description string, width int) []string {
	keyWidth := 14
	descriptionLines := wrapHelpText(description, max(1, width-keyWidth-1))
	result := make([]string, 0, len(descriptionLines))
	for index, line := range descriptionLines {
		keyPart := strings.Repeat(" ", keyWidth)
		if index == 0 {
			keyPart = HelpKeyStyle.Render(fmt.Sprintf("%-*s", keyWidth, keyText))
		}
		cell := keyPart + " " + line
		result = append(result, cell+strings.Repeat(" ", max(0, width-lipgloss.Width(cell))))
	}
	return result
}

func wrapHelpText(value string, width int) []string {
	words := strings.Fields(value)
	if len(words) == 0 {
		return []string{""}
	}
	lines := []string{words[0]}
	for _, word := range words[1:] {
		last := len(lines) - 1
		if len(lines[last])+1+len(word) <= width {
			lines[last] += " " + word
		} else {
			lines = append(lines, word)
		}
	}
	return lines
}

// renderHealthHistoryView renders readiness and liveness history.
func (m *Model) renderHealthHistoryView() string {
	svc := m.FocusedService()
	if svc == nil {
		return m.placeOverlay(renderModal("No service selected"))
	}

	healthData := m.healthChecker.GetHealth(svc.Name)

	var lines []string
	lines = append(lines, ModalTitleStyle.Render(fmt.Sprintf(" Health: %s ", svc.Name)))
	lines = append(lines, "")

	if svc.Config.HealthCheck != nil {
		lines = append(lines, "  Readiness: "+m.readinessSummary(svc))
		if check := healthReadiness(svc); check != nil {
			lines = append(lines, checkDescription(check))
		}
		lines = append(lines, "  Liveness:  "+m.livenessSummary(svc))
		if check := healthLiveness(svc); check != nil {
			lines = append(lines, checkDescription(check))
		}
		if healthData != nil {
			lines = append(lines, "")
			lines = append(lines, "History:")
			for _, h := range healthData.History.Lines() {
				lines = append(lines, "  "+h)
			}
		}
	} else {
		lines = append(lines, "No health checks configured for this service")
	}

	lines = append(lines, "")
	lines = append(lines, "[Esc] Close")

	content := renderModal(strings.Join(lines, "\n"))
	return m.placeOverlay(content)
}

// renderNotificationsView renders the in-memory notification center.
func (m *Model) renderNotificationsView() string {
	var lines []string
	lines = append(lines, ModalTitleStyle.Render(" Notifications "))
	lines = append(lines, "")

	m.notifMu.RLock()
	notifs := m.notifications
	m.notifMu.RUnlock()

	if len(notifs) == 0 {
		lines = append(lines, "No notifications")
	} else {
		for _, notif := range notifs {
			prefix := "  ●"
			switch notif.Level {
			case config.LogError:
				prefix = LogErrorStyle.Render("  ✗")
			case config.LogWarn:
				prefix = LogWarnStyle.Render("  ⚠")
			case config.LogDebug:
				prefix = LogDebugStyle.Render("  ·")
			}
			t := notif.Time.Format("15:04:05")
			line := fmt.Sprintf("%s %s [%s] %s", prefix, t, notif.Service, notif.Message)
			lines = append(lines, line)
		}
	}

	lines = append(lines, "")
	lines = append(lines, "[Esc] Close")

	content := renderModal(strings.Join(lines, "\n"))
	return m.placeOverlay(content)
}

// renderConfirmQuitView explains the process cleanup performed on exit.
func (m *Model) renderConfirmQuitView() string {
	content := renderModal(
		" Quit Kranz? \n\n" +
			"All child processes will be stopped and\ntheir listening ports will be released.\n\n" +
			" [Enter/y] Stop everything and quit\n" +
			" [Esc/n]   Stay here",
	)
	return m.placeOverlay(content)
}

// renderPortConflictView renders verified ownership details for occupied ports.
func (m *Model) renderPortConflictView() string {
	var lines []string
	lines = append(lines, "⚠ Port conflict: "+m.conflictService)
	lines = append(lines, "")

	for port, info := range m.conflictPorts {
		lines = append(lines, fmt.Sprintf("Port %d is occupied:", port))
		if m.conflictExternal {
			lines = append(lines, "  Owner: external process (not started by Kranz)")
		} else if m.conflictOwner != "" {
			lines = append(lines, "  Owner: Kranz service "+m.conflictOwner)
		}
		lines = append(lines, fmt.Sprintf("  PID: %d", info.PID))
		lines = append(lines, fmt.Sprintf("  Process: %s", info.Process))
		if info.Command != "" {
			lines = append(lines, fmt.Sprintf("  Command: %s", info.Command))
		}
	}

	lines = append(lines, "")
	if m.conflictExternal {
		lines = append(lines, "[k] Stop this external process and retry")
	} else {
		lines = append(lines, "Stop the owning Kranz service before retrying.")
	}
	lines = append(lines, "[r/Enter] Retry  [s/Esc] Close")

	content := renderModal(strings.Join(lines, "\n"))
	return m.placeOverlay(content)
}

// renderConfirmRestartView lists dependent services affected by a restart.
func (m *Model) renderConfirmRestartView() string {
	content := renderModal(
		fmt.Sprintf(" Restart %q \n\nAlso restarts: %s\n\n[Enter/y] Continue  [Esc/n] Cancel",
			m.confirmTarget, m.confirmAction),
	)
	return m.placeOverlay(content)
}

func (m *Model) renderConfirmClearLogsView() string {
	panel := "logs"
	if m.clearPinned {
		panel = "pinned logs"
	}
	content := renderModal(
		fmt.Sprintf(" Clear %s for %q? \n\nThis cannot be undone.\n\n[Enter] Clear  [Esc] Cancel",
			panel, m.clearTarget),
	)
	return m.placeOverlay(content)
}

func (m *Model) renderThemeView() string {
	names := ThemeNames()
	// Keep the controls visible even in a 24-row terminal. The fixed rows are
	// the summary, footer, modal border/padding, and optional settings path.
	fixedRows := 6 + 7 + 4
	if m.settingsPath != "" {
		fixedRows++
	}
	if m.themeProjectConfigPath() != "" {
		fixedRows++
	}
	visibleRows := min(len(names), max(1, m.height-fixedRows))
	if visibleRows < len(names) {
		// The scroll position indicator consumes one additional row.
		visibleRows = max(1, visibleRows-1)
	}
	start := max(0, m.themeCursor-visibleRows/2)
	if start+visibleRows > len(names) {
		start = max(0, len(names)-visibleRows)
	}

	projectTheme := m.cfg.UI.Theme
	if projectTheme == "" {
		projectTheme = DefaultTheme
	}
	lines := []string{
		ModalTitleStyle.Render(" Themes "),
		fmt.Sprintf("Theme: %s", m.themePickerThemeLabel(projectTheme)),
		fmt.Sprintf("Accent: %s", m.themePickerAccentLabel()),
		fmt.Sprintf("Background: %s", m.themePickerBackgroundLabel()),
		fmt.Sprintf("Mode: %s", m.themePickerColorModeLabel()),
		"",
	}
	for index := start; index < start+visibleRows; index++ {
		theme, _ := LookupTheme(names[index])
		theme = adaptThemeBackground(theme, colorModeIsDark(m.themeColorMode, m.terminalDark))
		swatchAccent := ensureContrast(theme.Accent, m.activeTheme.SurfaceAlt, 3.0)
		marker := "  "
		if index == m.themeCursor {
			marker = "› "
		}
		swatches := lipgloss.NewStyle().Foreground(lipgloss.Color(swatchAccent)).Bold(true).Render("●") + " " +
			lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Green)).Bold(true).Render("●") + " " +
			lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Yellow)).Bold(true).Render("●") + " " +
			lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Red)).Bold(true).Render("●")
		line := fmt.Sprintf("%s%-20s %s", marker, theme.DisplayName, swatches)
		if index == m.themeCursor {
			line = renderSelectedLine(line)
		}
		lines = append(lines, line)
	}
	if len(names) > visibleRows {
		lines = append(lines, ContextBarStyle.Render(fmt.Sprintf("%d/%d", m.themeCursor+1, len(names))))
	}
	lines = append(lines,
		"",
		"[p] Theme: Project / Selected",
		"[a] Accent: Project / Theme default",
		"[b] Background: Terminal / Theme",
		"[m] Mode: Auto / Dark / Light",
		"[Enter] Save globally   [c] Save to project",
		"[Esc] Cancel",
	)
	pathWidth := max(20, m.width-12)
	if m.settingsPath != "" {
		lines = append(lines, ContextBarStyle.Render(ansi.Truncate("Global: "+m.settingsPath, pathWidth, "…")))
	}
	if path := m.themeProjectConfigPath(); path != "" {
		lines = append(lines, ContextBarStyle.Render(ansi.Truncate("Project: "+path, pathWidth, "…")))
	}
	return m.placeOverlay(renderModal(strings.Join(lines, "\n")))
}

func renderModal(content string) string {
	modalContentStyle := lipgloss.NewStyle().Foreground(ColorGrey)
	if !TerminalCanvas {
		modalContentStyle = modalContentStyle.Background(ColorSurfaceAlt)
	}
	return ModalStyle.Render(preserveStyleAfterReset(content, modalContentStyle))
}

func (m *Model) themePickerThemeLabel(projectTheme string) string {
	if m.themeUseProject {
		return "PROJECT · " + projectTheme
	}
	theme, _ := LookupTheme(ThemeNames()[m.themeCursor])
	return "SELECTED · " + theme.DisplayName
}

func (m *Model) themePickerAccentLabel() string {
	if !m.themeAccentChanged && isCustomAccent(m.themeOriginalAccent, m.cfg.UI.Accent) {
		return "CUSTOM · " + m.themeOriginalAccent
	}
	if m.themeProjectAccent {
		return "PROJECT · " + strings.TrimSpace(m.cfg.UI.Accent)
	}
	return "THEME DEFAULT"
}

func (m *Model) themePickerBackgroundLabel() string {
	if m.themeBackground == backgroundTheme {
		return "THEME · painted " + m.activeTheme.Background
	}
	return "TERMINAL · inherited"
}

func (m *Model) themePickerColorModeLabel() string {
	switch m.themeColorMode {
	case colorModeDark:
		return "DARK"
	case colorModeLight:
		return "LIGHT"
	default:
		detected := "Light"
		if m.terminalDark {
			detected = "Dark"
		}
		return "AUTO · " + detected + " detected"
	}
}

// placeOverlay composites a modal over a dimmed snapshot of the dashboard.
func (m *Model) placeOverlay(content string) string {
	background := strings.Split(ansi.Strip(m.renderMainView()), "\n")
	for len(background) < m.height {
		background = append(background, "")
	}
	contentLines := strings.Split(content, "\n")
	contentWidth := 0
	for _, line := range contentLines {
		contentWidth = max(contentWidth, lipgloss.Width(line))
	}
	contentWidth = min(m.width, contentWidth)
	if len(contentLines) > m.height {
		contentLines = contentLines[:m.height]
	}
	top := max(0, (m.height-len(contentLines))/2)
	left := max(0, (m.width-contentWidth)/2)
	dim := lipgloss.NewStyle().Foreground(ColorDim).Faint(true)

	result := make([]string, m.height)
	for row := 0; row < m.height; row++ {
		base := padPlainLine(background[row], m.width)
		modalRow := row - top
		if modalRow < 0 || modalRow >= len(contentLines) {
			result[row] = dim.Render(base)
			continue
		}
		foreground := ansi.Truncate(contentLines[modalRow], contentWidth, "")
		foreground += strings.Repeat(" ", max(0, contentWidth-lipgloss.Width(foreground)))
		baseRunes := []rune(base)
		end := min(len(baseRunes), left+contentWidth)
		result[row] = dim.Render(string(baseRunes[:min(left, len(baseRunes))])) + foreground + dim.Render(string(baseRunes[end:]))
	}
	return strings.Join(result, "\n")
}
