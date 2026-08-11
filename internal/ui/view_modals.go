package ui

import (
	"fmt"
	"math"
	"strconv"
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
	for index := range visible {
		visible[index] = "  " + visible[index]
	}

	lines := []string{ModalTitleStyle.Render(" Kranz Help "), ""}
	lines = append(lines, visible...)
	lines = append(lines, "")
	footer := "  [Esc] Close"
	if maxOffset > 0 {
		footer = fmt.Sprintf("  [↑/k] Up  [↓/j] Down  %d–%d/%d    [Esc] Close", offset+1, end, len(body))
	}
	lines = append(lines, renderModalShortcuts(footer, lipgloss.NewStyle().Foreground(ColorDim)))

	content := renderFlushModal(strings.Join(lines, "\n"))
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
		{"Enter", "Expand service actions/action groups, run an action, or expand a tag"},
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
		{"Ctrl+T", "Choose and apply a theme"},
		{"p / m", "In Themes: toggle theme or cycle Auto/Dark/Light mode"},
		{"a / b", "In Themes: cycle the accent or canvas sources, including a custom color once one is set"},
		{"Shift+A/B", "In Themes: edit the six hex digits of the accent or the canvas"},
		{"Enter / r", "In Themes: apply for this session or reload saved appearance from configuration"},
		{"g / c", "In Themes: save appearance globally or to the project config"},
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
		detectedPorts := svc.DetectedPorts()
		serviceActive := svc.Status() != config.StatusStopped
		lines = append(lines, "  Readiness: "+m.readinessSummary(svc))
		if check := healthReadiness(svc); check != nil {
			lines = append(lines, checkDescription(check, detectedPorts, serviceActive))
		}
		lines = append(lines, "  Liveness:  "+m.livenessSummary(svc))
		if check := healthLiveness(svc); check != nil {
			lines = append(lines, checkDescription(check, detectedPorts, serviceActive))
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
	content := renderConfirmationModal(
		"Quit Kranz?",
		[]string{"All child processes will be stopped and", "their listening ports will be released."},
		"[Enter/y] Stop everything and quit",
		"[Esc/n]   Stay here",
	)
	return m.placeOverlay(content)
}

// renderPortConflictView renders verified ownership details for occupied ports.
func (m *Model) renderPortConflictView() string {
	var lines []string
	lines = append(lines, renderConfirmTitle("⚠ Port conflict: "+m.conflictService))
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
		lines = append(lines, renderModalShortcuts("[k] Stop this external process and retry", lipgloss.NewStyle()))
	} else {
		lines = append(lines, "Stop the owning Kranz service before retrying.")
	}
	lines = append(lines, renderModalShortcuts("[r/Enter] Retry  [s/Esc] Close", lipgloss.NewStyle()))

	content := renderModal(strings.Join(lines, "\n"))
	return m.placeOverlay(content)
}

// renderConfirmRestartView lists dependent services affected by a restart.
func (m *Model) renderConfirmRestartView() string {
	content := renderConfirmationModal(
		fmt.Sprintf("Restart %q", m.confirmTarget),
		[]string{fmt.Sprintf("Also restarts: %s", m.confirmAction)},
		"[Enter/y] Continue  [Esc/n] Cancel",
	)
	return m.placeOverlay(content)
}

func (m *Model) renderConfirmClearLogsView() string {
	panel := "logs"
	if m.clearPinned {
		panel = "pinned logs"
	}
	content := renderConfirmationModal(
		fmt.Sprintf("Clear %s for %q?", panel, m.clearTarget),
		[]string{"This cannot be undone."},
		"[Enter] Clear  [Esc] Cancel",
	)
	return m.placeOverlay(content)
}

func (m *Model) renderConfirmThemeSaveView() string {
	title := "Save global appearance?"
	path := m.settingsPath
	if m.themeSaveScope == themeSaveProject {
		title = "Save project appearance?"
		path = m.themeProjectConfigPath()
	}
	body := []string{"This will write the current appearance to:"}
	body = append(body, wrapDetailValue(path, max(20, m.width-20))...)
	body = append(body, "")
	body = append(body, m.renderThemePickerSummaryRows()...)
	content := renderConfirmationModal(title, body, "[Enter/y] Save  [Esc/n] Cancel")
	return m.placeOverlay(content)
}

func (m *Model) renderThemeView() string {
	names := ThemeNames()
	pathWidth := max(20, m.width-12)
	pathLines := make([]string, 0, 2)
	if m.settingsPath != "" {
		pathLines = append(pathLines, renderThemePath("Global", m.settingsPath, pathWidth)...)
	}
	if path := m.themeProjectConfigPath(); path != "" {
		pathLines = append(pathLines, renderThemePath("Project", path, pathWidth)...)
	}

	projectTheme := m.cfg.UI.Theme
	if projectTheme == "" {
		projectTheme = DefaultTheme
	}
	previewThemeName := names[m.themeCursor]
	if m.themeUseProject {
		previewThemeName = projectTheme
	}
	// The card must show what Apply would produce, so it is built from the
	// picker's own resolved accent and canvas rather than from the theme's
	// defaults. A colour still being typed overrides only the channel its editor
	// targets — otherwise a background in progress would repaint the accent.
	previewAccent := m.themePickerAccent()
	previewBackground, _ := customBackgroundColor(m.themePickerBackground())
	if candidate, ok := m.themeColorCandidate(); ok {
		if m.themeColorTarget == themeColorTargetBackground {
			previewBackground = candidate
		} else {
			previewAccent = candidate
		}
	}
	previewTheme, err := BuildTheme(previewThemeName, previewAccent, previewBackground,
		colorModeIsDark(m.themeColorMode, m.terminalDark))
	if err != nil {
		previewTheme, _ = LookupTheme(previewThemeName)
		previewTheme = adaptThemeBackground(previewTheme, colorModeIsDark(m.themeColorMode, m.terminalDark))
	}
	// Keep the controls visible even in a 24-row terminal. The fixed rows are
	// the six-row settings summary, the five-row footer, the modal's vertical
	// padding, the path separator, and the paths themselves. The remaining rows
	// are shared by the theme table and its side preview.
	pathSeparatorRows := 0
	if len(pathLines) > 0 {
		pathSeparatorRows = 1
	}
	fixedRows := 6 + 5 + 4 + pathSeparatorRows + len(pathLines)
	sectionRows := max(2, m.height-fixedRows)
	// The side preview is a fixed-height panel, and JoinHorizontal stretches the
	// whole section to the taller side. Showing it in a section that cannot hold
	// it would push the footer and the config paths off the bottom of the modal,
	// so a short terminal keeps the theme table alone.
	showPreview := m.width >= themePreviewMinWidth && sectionRows >= themePreviewCardRows
	themeCapacity := max(1, sectionRows-1) // ATMB header
	showPosition := len(names) > themeCapacity && sectionRows >= 3
	visibleRows := min(len(names), themeCapacity)
	if showPosition {
		// The scroll position indicator consumes one additional row.
		visibleRows--
	}
	start := max(0, m.themeCursor-visibleRows/2)
	if start+visibleRows > len(names) {
		start = max(0, len(names)-visibleRows)
	}

	lines := []string{
		ModalTitleStyle.Render(" Themes "),
		"",
	}
	lines = append(lines, m.renderThemePickerSummaryRows()...)
	// The header aligns with the palette column: the row indent plus the marker
	// plus the padded name width.
	themeLines := []string{"  " + strings.Repeat(" ", themeRowMarkerWidth+themeRowNameWidth) + ContextBarStyle.Render("A T M B")}
	for index := start; index < start+visibleRows; index++ {
		theme, _ := LookupTheme(names[index])
		theme = adaptThemeBackground(theme, colorModeIsDark(m.themeColorMode, m.terminalDark))
		marker := "  "
		if index == m.themeCursor {
			marker = "› "
		}
		name := themeNameStyle(theme).Render(theme.DisplayName)
		line := marker + name + strings.Repeat(" ", themeRowNamePadding(theme)) + themePalettePreview(theme, m.activeTheme.SurfaceAlt)
		if index == m.themeCursor {
			line = renderSelectedLine(line)
		}
		themeLines = append(themeLines, "  "+line)
	}
	if showPosition {
		themeLines = append(themeLines, "  "+ContextBarStyle.Render(fmt.Sprintf("%d/%d", m.themeCursor+1, len(names))))
	}
	themeSection := strings.Join(themeLines, "\n")
	if showPreview {
		themeSection = lipgloss.JoinHorizontal(lipgloss.Top, themeSection, "   ", renderThemePreviewCard(previewTheme))
	}
	lines = append(lines, themeSection, "")
	lines = append(lines, renderThemeControlRows([][2]string{
		{"[p] Theme: Project / Selected", "[m] Mode: Auto / Dark / Light"},
		{m.themeAccentControlLabel(), "[Shift+A] Edit color"},
		{m.themeBackgroundControlLabel(), "[Shift+B] Edit color"},
	}, m.themeControlLabelReserve())...)
	lines = append(lines,
		"  "+DetailLabelStyle.Render("SESSION")+"  "+renderModalShortcuts("[Enter] Apply  [r] Reload saved  [Esc] Cancel", lipgloss.NewStyle()),
		"  "+DetailLabelStyle.Render("SAVE")+"     "+renderModalShortcuts("[g] Global  [c] Project", lipgloss.NewStyle()),
	)
	if len(pathLines) > 0 {
		lines = append(lines, "")
		lines = append(lines, pathLines...)
	}
	return m.placeOverlay(renderFlushModal(strings.Join(lines, "\n")))
}

// renderThemePickerSummaryRows is shared by the picker and its save
// confirmation so labels, punctuation, source names, and colour styling cannot
// drift between the two views.
func (m *Model) renderThemePickerSummaryRows() []string {
	projectTheme := m.cfg.UI.Theme
	if projectTheme == "" {
		projectTheme = DefaultTheme
	}
	return []string{
		"  " + m.renderThemePickerThemeSetting(projectTheme),
		"  " + m.renderThemePickerAccentSetting(),
		"  " + m.renderThemePickerBackgroundSetting(),
		"  " + renderThemeSetting("Mode", m.themePickerColorModeLabel()),
	}
}

const (
	// Layout of one theme row: "› " marker, the display name padded to a fixed
	// column, then the A/T/M/B palette. renderThemeView draws it and
	// handleThemeMouseClick sizes its hit target from the same numbers.
	themeRowMarkerWidth = 2
	themeRowNameWidth   = 20
	// The side preview is renderTitledPanel(height: 4): a titled top border,
	// four content rows, and a bottom border. Both dimensions gate it — the
	// narrowest terminal that fits the table and the card side by side, and the
	// shortest section that can hold the card without stealing rows from the
	// footer below it.
	themePreviewCardRows = 6
	themePreviewMinWidth = 78
	// Space between the two columns of key hints below the theme table.
	themeControlColumnGap = 2
	// Gutter a flush modal keeps on the right, matching the indent its content
	// carries on the left.
	modalSideMargin = 2
)

// renderThemeControlRows lays the picker's key hints out as two columns. The
// left column's width is measured from its own longest entry, so the right
// column stays aligned when a label grows a Custom position.
func renderThemeControlRows(rows [][2]string, leftWidth int) []string {
	for _, row := range rows {
		leftWidth = max(leftWidth, lipgloss.Width(row[0]))
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		left := renderModalShortcuts(row[0], lipgloss.NewStyle())
		if row[1] == "" {
			lines = append(lines, "  "+left)
			continue
		}
		gap := strings.Repeat(" ", leftWidth-lipgloss.Width(row[0])+themeControlColumnGap)
		lines = append(lines, "  "+left+gap+renderModalShortcuts(row[1], lipgloss.NewStyle()))
	}
	return lines
}

// themeRowNamePadding reports the spaces between a theme's display name and the
// palette column, keeping at least one space when the name overflows.
func themeRowNamePadding(theme Theme) int {
	return max(1, themeRowNameWidth-lipgloss.Width(theme.DisplayName))
}

// themePalettePreview renders a theme's accent, text, muted, and background
// colours as dots. They are drawn over the active theme's modal surface rather
// than their own, so each dot needs a contrast floor of its own: without it a
// dark theme's background dot is invisible on a dark modal.
func themePalettePreview(theme Theme, surface string) string {
	dot := func(color string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(ensureContrast(color, surface, 3.0))).Bold(true).Render("●")
	}
	return strings.Join([]string{
		dot(theme.Accent),
		dot(theme.Text),
		dot(theme.Muted),
		dot(theme.Background),
	}, " ")
}

func renderThemePreviewCard(theme Theme) string {
	const contentWidth = 22
	text := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text)).Render(" Text")
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Render(" Muted text")
	accentText := ensureContrast(theme.Text, theme.Accent, 4.5)
	accentBackground := lipgloss.NewStyle().
		Width(contentWidth).
		Foreground(lipgloss.Color(accentText)).
		Background(lipgloss.Color(theme.Accent)).
		Bold(true).
		Render(" Accent background")
	neutralText := ensureContrast(theme.Text, theme.SurfaceAlt, 4.5)
	neutralBackground := lipgloss.NewStyle().
		Width(contentWidth).
		Foreground(lipgloss.Color(neutralText)).
		Background(lipgloss.Color(theme.SurfaceAlt)).
		Render(" Neutral background")
	panelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Accent))
	titleBackground := focusedPanelTitleBackground(theme)
	titleText := ensureContrast(theme.Text, titleBackground, 4.5)
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(titleText)).
		Background(lipgloss.Color(titleBackground)).
		Bold(true)
	return renderTitledPanel(panelStyle, titleStyle, contentWidth, 4, "Preview", []string{
		text,
		muted,
		accentBackground,
		neutralBackground,
	})
}

func themeNameStyle(theme Theme) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.AccentText)).Bold(true)
}

func renderThemePath(label, path string, width int) []string {
	prefix := label + ": "
	prefixWidth := lipgloss.Width(prefix)
	parts := wrapDetailValue(path, max(1, width-prefixWidth))
	lines := make([]string, 0, len(parts))
	for index, part := range parts {
		linePrefix := strings.Repeat(" ", prefixWidth)
		if index == 0 {
			linePrefix = ContextBarStyle.Render(prefix)
		}
		lines = append(lines, "  "+linePrefix+ContextBarStyle.Bold(true).Render(part))
	}
	return lines
}

func renderModal(content string) string {
	return renderModalWithStyle(content, ModalStyle)
}

func renderFlushModal(content string) string {
	return renderModalWithStyle(padModalSideMargin(content), ModalStyle.PaddingLeft(2).PaddingRight(2))
}

// padModalSideMargin gives a flush modal the same surface gutter on the right
// that its content already indents on the left. Flush modals drop the style's
// horizontal padding and every line carries its own two-space indent.
func padModalSideMargin(content string) string {
	lines := strings.Split(content, "\n")
	widest := 0
	for _, line := range lines {
		widest = max(widest, lipgloss.Width(line))
	}
	for index, line := range lines {
		lines[index] = line + strings.Repeat(" ", widest-lipgloss.Width(line)+modalSideMargin)
	}
	return strings.Join(lines, "\n")
}

func renderModalWithStyle(content string, style lipgloss.Style) string {
	modalContentStyle := lipgloss.NewStyle().Foreground(ColorGrey).Background(ColorSurfaceAlt)
	return style.Render(preserveStyleAfterReset(content, modalContentStyle))
}

func renderModalShortcuts(value string, textStyle lipgloss.Style) string {
	var result strings.Builder
	for len(value) > 0 {
		start := strings.IndexByte(value, '[')
		if start < 0 {
			result.WriteString(textStyle.Render(value))
			break
		}
		end := strings.IndexByte(value[start:], ']')
		if end < 0 {
			result.WriteString(textStyle.Render(value))
			break
		}
		end += start
		result.WriteString(textStyle.Render(value[:start]))
		result.WriteString(HelpKeyStyle.Render(value[start : end+1]))
		value = value[end+1:]
	}
	return result.String()
}

func renderConfirmationModal(title string, bodyLines []string, actionLines ...string) string {
	lines := []string{renderConfirmTitle(title)}
	if len(bodyLines) > 0 {
		lines = append(lines, "")
		lines = append(lines, bodyLines...)
	}
	if len(actionLines) > 0 {
		lines = append(lines, "")
		for _, action := range actionLines {
			lines = append(lines, renderModalShortcuts(action, lipgloss.NewStyle()))
		}
	}
	return renderModal(strings.Join(lines, "\n"))
}

func renderConfirmTitle(title string) string {
	return ModalTitleStyle.Padding(0).Render(title)
}

func renderThemeSetting(label, value string) string {
	return label + ": " + HelpKeyStyle.Render(value)
}

func (m *Model) renderThemePickerAccentSetting() string {
	return m.renderThemePickerColorSetting(themeColorTargetAccent, "Accent", m.themePickerAccentLabel())
}

func (m *Model) renderThemePickerBackgroundSetting() string {
	return m.renderThemePickerColorSetting(themeColorTargetBackground, "Background", m.themePickerBackgroundLabel())
}

// renderThemePickerColorSetting draws one settings row for a colour that can be
// edited. While its editor is open the row becomes the input; otherwise it shows
// the label, tinting a trailing #RRGGBB with the colour it names.
func (m *Model) renderThemePickerColorSetting(target themeColorTarget, label, value string) string {
	if m.themeColorEditing && m.themeColorTarget == target {
		// The field itself becomes the colour: the value being typed is its
		// background, so the colour shows as an area and stays visible whatever
		// it is. Its foreground is lifted off that background, otherwise a pale
		// canvas colour — the common case — would swallow its own digits.
		inputStyle := HelpKeyStyle
		swatch := ContextBarStyle.Render("○")
		if candidate, ok := m.themeColorCandidate(); ok {
			inputStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(readableTextOn(candidate, m.activeTheme.Text))).
				Background(lipgloss.Color(candidate)).
				Bold(true)
			swatch = lipgloss.NewStyle().Foreground(lipgloss.Color(candidate)).Bold(true).Render("●")
		}
		m.themeColorInput.TextStyle = inputStyle
		m.themeColorInput.PlaceholderStyle = ContextBarStyle
		m.themeColorInput.Cursor.Style = inputStyle.Reverse(true)
		m.themeColorInput.Cursor.TextStyle = inputStyle
		suffix := renderModalShortcuts("[Enter] Apply  [Esc] Cancel", ContextBarStyle)
		if m.themeColorError != "" {
			suffix = PortWarningStyle.Render(m.themeColorError)
		}
		input := lipgloss.NewStyle().Width(7).Render(m.themeColorInput.View())
		return label + ": " + inputStyle.Render("#") + input + " " + swatch + "  " + suffix
	}
	source, color, ok := strings.Cut(value, " · ")
	if !ok || !hexColorPattern.MatchString(color) {
		return renderThemeSetting(label, value)
	}
	colorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true)
	return label + ": " + HelpKeyStyle.Render(source+" · ") + colorStyle.Render(color)
}

// themeAccentControlLabel and themeBackgroundControlLabel name what their key
// will cycle through. Custom appears only once a colour exists, so the label
// never promises a position the cycle cannot reach.
func (m *Model) themeAccentControlLabel() string {
	return m.accentControlLabel(m.themeCustomAccent != "")
}

func (m *Model) accentControlLabel(withCustom bool) string {
	names := make([]string, 0, 3)
	if strings.TrimSpace(m.cfg.UI.Accent) != "" {
		names = append(names, "Project")
	}
	names = append(names, "Theme")
	if withCustom {
		names = append(names, "Custom")
	}
	if len(names) == 1 {
		return "[a/Shift+A] Accent: Edit color"
	}
	return "[a] Accent: " + strings.Join(names, " / ")
}

func (m *Model) themeBackgroundControlLabel() string {
	return m.backgroundControlLabel(m.themeCustomBackground != "")
}

func (m *Model) backgroundControlLabel(withCustom bool) string {
	names := []string{"Terminal", "Theme"}
	if withCustom {
		names = append(names, "Custom")
	}
	return "[b] Background: " + strings.Join(names, " / ")
}

// themeControlLabelReserve is the width the key-hint column holds open: the
// widest label these controls can reach in this project. Measuring only the
// current labels would let the modal grow sideways the moment a Custom position
// appears, so the picker would change width while the user is typing in it.
func (m *Model) themeControlLabelReserve() int {
	reserve := 0
	for _, label := range []string{
		m.accentControlLabel(true),
		m.backgroundControlLabel(true),
	} {
		reserve = max(reserve, lipgloss.Width(label))
	}
	return reserve
}

func (m *Model) themeColorCandidate() (string, bool) {
	candidate := "#" + strings.ToUpper(m.themeColorInput.Value())
	return candidate, m.themeColorEditing && hexColorPattern.MatchString(candidate)
}

func (m *Model) renderThemePickerThemeSetting(projectTheme string) string {
	value := m.themePickerThemeLabel(projectTheme)
	source, name, ok := strings.Cut(value, " · ")
	if !ok {
		return renderThemeSetting("Theme", value)
	}
	themeName := projectTheme
	if !m.themeUseProject {
		themeName = ThemeNames()[m.themeCursor]
	}
	theme, _ := LookupTheme(themeName)
	theme = adaptThemeBackground(theme, colorModeIsDark(m.themeColorMode, m.terminalDark))
	return "Theme: " + HelpKeyStyle.Render(source+" · ") + themeNameStyle(theme).Render(name)
}

func (m *Model) themePickerThemeLabel(projectTheme string) string {
	if m.themeUseProject {
		return "PROJECT · " + projectTheme
	}
	theme, _ := LookupTheme(ThemeNames()[m.themeCursor])
	return "SELECTED · " + theme.DisplayName
}

func (m *Model) themePickerAccentLabel() string {
	switch m.themeAccentSource {
	case themeAccentSourceCustom:
		return "CUSTOM · " + m.themePickerAccent()
	case themeAccentSourceProject:
		return "PROJECT · " + m.themePickerAccent()
	default:
		return "THEME DEFAULT"
	}
}

func (m *Model) themePickerBackgroundLabel() string {
	switch m.themeBackgroundSource {
	case themeBackgroundSourceCustom:
		return "CUSTOM · " + m.themeCustomBackground
	case themeBackgroundSourceTheme:
		return "THEME · painted " + m.activeTheme.Background
	default:
		return "TERMINAL · inherited"
	}
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
	background := strings.Split(m.renderMainView(), "\n")
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
	// Emulate a translucent black layer by mixing every ANSI colour with black.
	// The explicit background applies the same blend to otherwise empty cells.
	dim := lipgloss.NewStyle().Background(ColorOverlay)
	dimBackground := func(fragment string) string {
		fragment = darkenANSIColors(fragment, modalOverlayOpacity)
		return dim.Render(preserveStyleAfterReset(fragment, dim))
	}

	result := make([]string, m.height)
	for row := 0; row < m.height; row++ {
		base := padPlainLine(background[row], m.width)
		modalRow := row - top
		if modalRow < 0 || modalRow >= len(contentLines) {
			result[row] = dimBackground(base)
			continue
		}
		foreground := ansi.Truncate(contentLines[modalRow], contentWidth, "")
		foreground += strings.Repeat(" ", max(0, contentWidth-lipgloss.Width(foreground)))
		end := min(m.width, left+contentWidth)
		result[row] = dimBackground(ansi.Cut(base, 0, left)) + foreground + dimBackground(ansi.Cut(base, end, m.width))
	}
	return strings.Join(result, "\n")
}

// darkenANSIColors emulates a translucent black overlay for true-colour SGR
// foregrounds, backgrounds, and underline colours without changing their hue.
func darkenANSIColors(value string, opacity float64) string {
	var result strings.Builder
	for len(value) > 0 {
		start := strings.Index(value, "\x1b[")
		if start < 0 {
			result.WriteString(value)
			break
		}
		result.WriteString(value[:start])
		end := strings.IndexByte(value[start+2:], 'm')
		if end < 0 {
			result.WriteString(value[start:])
			break
		}
		end += start + 2
		params := strings.Split(value[start+2:end], ";")
		kept := make([]string, 0, len(params))
		for index := 0; index < len(params); index++ {
			parameter := params[index]
			if (parameter == "38" || parameter == "48" || parameter == "58") &&
				index+4 < len(params) && params[index+1] == "2" {
				kept = append(kept, parameter, "2")
				for component := index + 2; component <= index+4; component++ {
					componentValue, err := strconv.Atoi(params[component])
					if err != nil {
						kept = append(kept, params[component])
						continue
					}
					darkened := int(math.Round(float64(componentValue) * (1 - opacity)))
					kept = append(kept, strconv.Itoa(max(0, min(255, darkened))))
				}
				index += 4
				continue
			}
			// Default background would punch a terminal-coloured hole through the
			// overlay; leaving it out lets the blended canvas remain in force.
			if parameter == "49" {
				continue
			}
			kept = append(kept, parameter)
		}
		if len(kept) > 0 {
			result.WriteString("\x1b[")
			result.WriteString(strings.Join(kept, ";"))
			result.WriteByte('m')
		}
		value = value[end+1:]
	}
	return result.String()
}
