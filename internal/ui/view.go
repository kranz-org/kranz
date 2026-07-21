package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

// View renders the complete Bubble Tea frame.
func (m *Model) View() string {
	if !m.ready {
		return AppStyle.Render("Loading...")
	}

	var content string
	switch m.mode {
	case ModeHelp:
		content = m.renderHelpView()
	case ModeHealthHistory:
		content = m.renderHealthHistoryView()
	case ModeNotifications:
		content = m.renderNotificationsView()
	case ModeSearch:
		content = m.renderSearchView()
	case ModeConfirmQuit:
		content = m.renderConfirmQuitView()
	case ModePortConflict:
		content = m.renderPortConflictView()
	case ModeConfirmRestart:
		content = m.renderConfirmRestartView()
	case ModeThemes:
		content = m.renderThemeView()
	default:
		content = m.renderMainView()
	}
	// Lipgloss styles embedded in a larger styled block end with SGR 0, which
	// resets both foreground and background. Restore the canvas after those
	// nested resets before applying the outer application style. The outer
	// style's own final reset is added afterwards and remains untouched.
	if !TerminalCanvas {
		content = preserveCanvasBackground(content, ColorBackground)
	}
	style := AppStyle.Width(m.width).Height(m.height).MaxWidth(m.width).MaxHeight(m.height)
	return style.Render(content)
}

func preserveCanvasBackground(content string, background lipgloss.TerminalColor) string {
	return preserveStyleAfterReset(content, lipgloss.NewStyle().Background(background))
}

// preserveStyleAfterReset keeps a deliberately styled region cohesive when a
// nested Lipgloss span emits SGR 0. It is used for selected rows and modals;
// otherwise their background would be punctured by terminal-default patches.
func preserveStyleAfterReset(content string, style lipgloss.Style) string {
	const sgrReset = "\x1b[0m"
	prefix := terminalStylePrefix(style)
	if prefix == "" || !strings.Contains(content, sgrReset) {
		return content
	}
	return strings.ReplaceAll(content, sgrReset, sgrReset+prefix)
}

func terminalStylePrefix(style lipgloss.Style) string {
	const marker = "K"
	rendered := style.Render(marker)
	index := strings.Index(rendered, marker)
	if index <= 0 {
		return ""
	}
	return rendered[:index]
}

// renderMainView renders the dashboard and its contextual action bar.
func (m *Model) renderMainView() string {
	return m.renderDashboard(m.renderStatusBar())
}

func (m *Model) renderDashboard(footer string) string {
	if m.width < 64 || m.height < 14 {
		return renderModal("Kranz needs a terminal of at least 64×14")
	}

	leftWidth := m.dashboardLeftWidth()
	rightWidth := m.width - leftWidth
	panelHeight := m.height - 2

	header := m.renderHeader()
	leftPanel := m.renderServiceColumn(leftWidth, panelHeight)
	rightPanel := m.renderLogColumn(rightWidth, panelHeight)

	mainContent := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	return lipgloss.JoinVertical(lipgloss.Left, header, mainContent, footer)
}

func (m *Model) renderLogColumn(width, height int) string {
	pinned := m.PinnedService()
	if pinned == nil {
		return m.renderLogPanel(m.FocusedService(), width, height)
	}
	topHeight := height / 2
	bottomHeight := height - topHeight
	top := m.renderPinnedLogPanel(pinned, width, topHeight)
	bottom := m.renderLogPanel(m.FocusedService(), width, bottomHeight)
	return lipgloss.JoinVertical(lipgloss.Left, top, bottom)
}

func (m *Model) panelStyle(focus panelFocus) lipgloss.Style {
	if m.panelFocus == focus {
		return FocusedPanelStyle
	}
	return PanelStyle
}

func (m *Model) dashboardLeftWidth() int {
	leftWidth := m.width * 36 / 100
	if leftWidth < 32 {
		leftWidth = 32
	}
	if leftWidth > 52 {
		leftWidth = 52
	}
	return leftWidth
}

// renderHeader renders project identity, service counts, and the help control.
func (m *Model) renderHeader() string {
	running, pending, stopped := m.serviceCounts()
	left := HeaderStyle.Render(fmt.Sprintf(" KRANZ  /  %s", m.cfg.Project))
	summary := RunningBadgeStyle.Render(fmt.Sprintf("%d active", running)) + "  " +
		StartingBadgeStyle.Render(fmt.Sprintf("%d pending", pending)) + "  " +
		StoppedBadgeStyle.Render(fmt.Sprintf("%d stopped", stopped))
	version := displayVersion(m.version)
	rightText := summary + "   " + ContextBarStyle.Render(version) + "   " + HelpKeyStyle.Render("[?] help") + " "
	if m.width < 90 {
		rightText = ContextBarStyle.Render(version) + "  " + HelpKeyStyle.Render("[?] help") + " "
	}
	available := max(0, m.width-lipgloss.Width(left))
	rightText = ansi.Truncate(rightText, available, "…")
	right := rightText

	width := m.width
	spaces := width - lipgloss.Width(left) - lipgloss.Width(right)
	if spaces < 0 {
		spaces = 0
	}

	return left + strings.Repeat(" ", spaces) + right
}

func displayVersion(version string) string {
	if version == "" || version == "dev" {
		return "dev build"
	}
	return "v" + strings.TrimPrefix(version, "v")
}

// renderStatusBar renders lifecycle actions and contextual log/search state.
func (m *Model) renderStatusBar() string {
	buttons := m.actionButtons()
	parts := make([]string, 0, len(buttons))
	for _, button := range buttons {
		parts = append(parts, button.rendered)
	}
	left := strings.Join(parts, actionSeparator())

	context := m.contextMessage()
	space := m.width - lipgloss.Width(left) - lipgloss.Width(context)
	if space < 1 {
		context = ansi.Truncate(context, max(0, m.width-lipgloss.Width(left)-1), "…")
		space = 1
	}
	return left + strings.Repeat(" ", space) + ContextBarStyle.Render(context)
}

func actionSeparator() string {
	// Buttons already carry horizontal padding, so a single muted cell creates
	// the same visual rhythm as lazygit without wasting narrow-terminal space.
	return ContextBarStyle.Render("│")
}

type actionButton struct {
	action   string
	rendered string
}

func (m *Model) actionButtons() []actionButton {
	targets := m.selectedTargetNames()
	allActive := len(targets) > 0
	for _, name := range targets {
		if svc, ok := m.manager.GetService(name); !ok || !serviceStartPlanned(svc) {
			allActive = false
			break
		}
	}
	toggleStyle := PrimaryButtonStyle
	toggleLabel := "▶ Start: s"
	compactToggle := "Start: s"
	if allActive {
		toggleStyle = DangerButtonStyle
		toggleLabel = "■ Stop: s"
		compactToggle = "Stop: s"
	}
	interruptibleStart := false
	switch m.operationKind {
	case operationStart, operationStartSet, operationStartAll:
		interruptibleStart = m.operationCancel != nil && allActive
	}
	if len(targets) == 0 || (m.operation != "" && !interruptibleStart) {
		toggleStyle = DisabledButtonStyle
	}
	if m.width < 100 {
		compact := func(style lipgloss.Style, label string) string {
			return style.Copy().Padding(0).Render(label)
		}
		allLabel := "All: a"
		if len(m.allServices) > 0 && len(m.selected) == len(m.allServices) {
			allLabel = "Clear: a"
		}
		return []actionButton{
			{action: "toggle", rendered: compact(toggleStyle, compactToggle)},
			{action: "force-start", rendered: compact(WarningButtonStyle, "Force: S")},
			{action: "select", rendered: compact(SecondaryButtonStyle, "Select: Space")},
			{action: "restart", rendered: compact(SecondaryButtonStyle, "Restart: r")},
			{action: "all", rendered: compact(SecondaryButtonStyle, allLabel)},
			{action: "quit", rendered: compact(DangerButtonStyle, "Quit: q")},
		}
	}
	allLabel := "Select all: a"
	if len(m.allServices) > 0 && len(m.selected) == len(m.allServices) {
		allLabel = "Clear all: a"
	}
	return []actionButton{
		{action: "toggle", rendered: toggleStyle.Render(toggleLabel)},
		{action: "force-start", rendered: WarningButtonStyle.Render("Force start: S")},
		{action: "select", rendered: SecondaryButtonStyle.Render("✓ Select: Space")},
		{action: "restart", rendered: SecondaryButtonStyle.Render("↻ Restart: r")},
		{action: "all", rendered: SecondaryButtonStyle.Render(allLabel)},
		{action: "quit", rendered: DangerButtonStyle.Render("Quit: q")},
	}
}

func (m *Model) actionAt(x int) string {
	left := 0
	buttons := m.actionButtons()
	for index, button := range buttons {
		right := left + lipgloss.Width(button.rendered)
		if x >= left && x < right {
			return button.action
		}
		left = right
		if index < len(buttons)-1 {
			left += lipgloss.Width(actionSeparator())
		}
	}
	return ""
}

func (m *Model) contextMessage() string {
	if m.operation != "" {
		return "◐ " + m.operation + " "
	}
	m.notifMu.RLock()
	toast := m.toastMessage
	m.notifMu.RUnlock()
	if toast != "" {
		return toast + " "
	}
	if svc := m.FocusedService(); svc != nil {
		mode := "filter"
		if m.searchMode == searchHighlight {
			mode = "highlight"
		}
		return fmt.Sprintf("[/] regex %s · %s ", mode, svc.Name)
	}
	return "[?] help "
}

func (m *Model) serviceCounts() (running, pending, stopped int) {
	for _, svc := range m.allServices {
		switch svc.Status() {
		case config.StatusRunning, config.StatusUnhealthy:
			running++
		case config.StatusStarting, config.StatusStopping:
			pending++
		default:
			if svc.DesiredRunning() {
				pending++
			} else {
				stopped++
			}
		}
	}
	return
}

// renderSearchView keeps the dashboard visible while editing a log expression.
func (m *Model) renderSearchView() string {
	mode, alternate := "FILTER", "Highlight"
	if m.searchMode == searchHighlight {
		mode, alternate = "HIGHLIGHT", "Filter"
	}
	searchBar := SearchInputStyle.Render(fmt.Sprintf(" Regex %s /%s_  [Tab] %s  [Enter] apply  [Esc] clear", mode, m.searchQuery, alternate))
	searchBar = ansi.Truncate(searchBar, m.width, "…")
	if lipgloss.Width(searchBar) < m.width {
		searchBar += strings.Repeat(" ", m.width-lipgloss.Width(searchBar))
	}
	return m.renderDashboard(searchBar)
}

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

type helpEntry struct{ key, desc string }

func helpEntries() []helpEntry {
	return []helpEntry{
		{"1 / 2 / 3", "Focus panels; 1 switches Services/Tags when the list is focused"},
		{"Shift+3", "Pin focused service logs above the active log panel"},
		{"3 again", "Switch focus between pinned and current logs"},
		{"↑/↓ j/k", "Navigate or scroll focused panel"},
		{"t", "Toggle Services/Tags from any panel"},
		{"Space", "Select/unselect service or tag"},
		{"s", "Start stopped or stop running targets"},
		{"Shift+S", "Start targets directly without dependency checks"},
		{"a", "Select/clear all services"},
		{"A", "Stop all services"},
		{"r", "Restart selected service"},
		{"R", "Restart running services"},
		{"T", "Clear selected tags"},
		{"h", "Health check history"},
		{"n", "Notification center"},
		{"/", "Regex filter; Tab switches to highlight"},
		{"n/N", "Next/previous highlighted match"},
		{"w", "Toggle log line wrapping"},
		{"i", "Show/hide captured-at time in logs"},
		{"f", "Pause/resume logs"},
		{"c", "Clear service logs"},
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

// ---- Dashboard panels ---- //

func (m *Model) renderServiceColumn(width, height int) string {
	listHeight, detailHeight := m.serviceColumnLayout(height)
	serviceList := m.renderServicePanel(width, listHeight)
	details := m.renderServiceDetails(m.FocusedService(), width, detailHeight)
	return lipgloss.JoinVertical(lipgloss.Left, serviceList, details)
}

func (m *Model) serviceColumnLayout(height int) (listHeight, detailHeight int) {
	itemCount := len(m.services)
	if m.listMode == listTags {
		itemCount = len(m.currentTags())
	}

	// A panel needs one title row and two border rows in addition to its items.
	// Keep the list compact for small projects and cap it at 20 visible items.
	const (
		maxVisibleItems = 20
		panelChromeRows = 3
		minPanelHeight  = 6
	)
	desiredHeight := min(itemCount, maxVisibleItems) + panelChromeRows
	listHeight = max(minPanelHeight, desiredHeight)
	if listHeight > height-minPanelHeight {
		listHeight = max(minPanelHeight, height-minPanelHeight)
	}
	return listHeight, height - listHeight
}

func (m *Model) mouseInDetails(x, y int) bool {
	if x < 0 || x >= m.dashboardLeftWidth() || y < 1 || y >= m.height-1 {
		return false
	}
	listHeight, _ := m.serviceColumnLayout(m.height - 2)
	return y >= 1+listHeight
}

// renderServicePanel renders the service list in the upper-left panel.
func (m *Model) renderServicePanel(width, height int) string {
	if m.listMode == listTags {
		return m.renderTagPanel(width, height)
	}
	contentWidth := max(1, width-2)
	contentHeight := max(1, height-2)
	if len(m.services) == 0 {
		title := renderPanelTitle("[1] SERVICES", contentWidth)
		return m.panelStyle(panelServices).Width(contentWidth).Height(contentHeight).Render(title + "\n\nNo services")
	}

	title := fmt.Sprintf("[1] SERVICES  %d  · 1 → Tags", len(m.services))
	if len(m.selected) > 0 {
		title += fmt.Sprintf("  SELECTED %d", len(m.selected))
	} else if len(m.selectedTags) > 0 {
		title += fmt.Sprintf("  TAGS %d", len(m.selectedTags))
	}
	lines := []string{renderPanelTitle(title, contentWidth)}
	start, end := m.visibleServiceRange(height)
	for i := start; i < end; i++ {
		svc := m.services[i]
		line := m.renderServiceLine(i, svc, contentWidth)
		lines = append(lines, line)
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], contentWidth, "…")
	}

	content := strings.Join(lines, "\n")
	return m.panelStyle(panelServices).Width(contentWidth).Height(contentHeight).Render(content)
}

func (m *Model) visibleServiceRange(height int) (start, end int) {
	available := max(1, height-3) // border rows plus the title row
	if len(m.services) <= available {
		return 0, len(m.services)
	}
	start = max(0, m.focused-available/2)
	if start+available > len(m.services) {
		start = max(0, len(m.services)-available)
	}
	return start, min(len(m.services), start+available)
}

func (m *Model) renderTagPanel(width, height int) string {
	contentWidth := max(1, width-2)
	contentHeight := max(1, height-2)
	tags := m.currentTags()
	title := fmt.Sprintf("[1] TAGS  %d  · 1 → Services", len(tags))
	if len(m.selectedTags) > 0 {
		title += fmt.Sprintf("  SELECTED %d", len(m.selectedTags))
	}
	lines := []string{renderPanelTitle(title, contentWidth)}
	if len(tags) == 0 {
		lines = append(lines, "", "No tags")
	} else {
		start, end := m.visibleTagRange(height)
		for index := start; index < end; index++ {
			tag := tags[index]
			marker := "  "
			if index == m.tagCursor {
				marker = HelpKeyStyle.Render("› ")
			}
			check := "[ ]"
			if containsTagStr(m.selectedTags, tag) {
				check = RunningBadgeStyle.Render("[✓]")
			}
			count := len(m.cfg.GetServicesByTags([]string{tag}))
			line := fmt.Sprintf("%s%s %s (%d)", marker, check, tag, count)
			line = ansi.Truncate(line, contentWidth, "…")
			if lipgloss.Width(line) < contentWidth {
				line += strings.Repeat(" ", contentWidth-lipgloss.Width(line))
			}
			if index == m.tagCursor {
				line = renderSelectedLine(line)
			}
			lines = append(lines, line)
		}
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], contentWidth, "…")
	}
	return m.panelStyle(panelServices).Width(contentWidth).Height(contentHeight).Render(strings.Join(lines, "\n"))
}

func (m *Model) visibleTagRange(height int) (start, end int) {
	tags := m.currentTags()
	available := max(1, height-3) // border rows plus the title row
	if len(tags) <= available {
		return 0, len(tags)
	}
	start = max(0, m.tagCursor-available/2)
	if start+available > len(tags) {
		start = max(0, len(tags)-available)
	}
	return start, min(len(tags), start+available)
}

// renderServiceLine renders selection, health state, name, and unread log count.
func (m *Model) renderServiceLine(index int, svc *service.Service, width int) string {
	focusMarker := "  "
	if index == m.focused {
		focusMarker = HelpKeyStyle.Render("› ")
	}

	visualState := m.serviceVisualState(svc)
	selection := "[ ]"
	if m.selected[svc.Name] {
		selection = RunningBadgeStyle.Render("[✓]")
	}
	line := focusMarker + selection + " " + serviceStatusIndicator(visualState) + " " + ServiceNameStyle.Render(svc.Name)
	if visualState == visualQueued {
		line += StartingBadgeStyle.Render("  queued")
	}

	// Unread counts disappear as soon as the service receives focus.
	newIndicator := ""
	if index != m.focused && svc.NewLogCount() > 0 {
		count := svc.NewLogCount()
		newIndicator = NewLogIndicatorStyle.Render(fmt.Sprintf(" +%d", count))
	}

	line += newIndicator
	line = ansi.Truncate(line, width, "…")
	if lipgloss.Width(line) < width {
		line += strings.Repeat(" ", width-lipgloss.Width(line))
	}

	if index == m.focused {
		return renderSelectedLine(line)
	}
	return line
}

type serviceVisualState int

const (
	visualStopped serviceVisualState = iota
	visualQueued
	visualStarting
	visualRunning
	visualUnhealthy
)

func serviceStatusIndicator(state serviceVisualState) string {
	switch state {
	case visualRunning:
		return RunningBadgeStyle.Render("●")
	case visualStarting:
		return StartingBadgeStyle.Render("●")
	case visualQueued:
		return StartingBadgeStyle.Render("●")
	case visualUnhealthy:
		return FailedBadgeStyle.Render("●")
	default:
		return StoppedBadgeStyle.Render("●")
	}
}

func serviceStatusLabel(status config.ServiceStatus, state serviceVisualState) string {
	if status == config.StatusStopping {
		return "Stopping"
	}
	switch state {
	case visualRunning:
		return "Running"
	case visualStarting:
		return "Starting"
	case visualQueued:
		return "Queued"
	case visualUnhealthy:
		return "Unhealthy"
	default:
		return "Stopped"
	}
}

func (m *Model) serviceVisualState(svc *service.Service) serviceVisualState {
	switch svc.Status() {
	case config.StatusStopped:
		if svc.DesiredRunning() {
			return visualQueued
		}
		return visualStopped
	case config.StatusStarting, config.StatusStopping:
		return visualStarting
	case config.StatusUnhealthy:
		return visualUnhealthy
	}

	checkConfig := svc.Config.HealthCheck
	if checkConfig == nil {
		return visualRunning
	}
	healthData := m.healthChecker.GetHealth(svc.Name)
	if healthData == nil {
		return visualStarting
	}
	if checkConfig.Readiness != nil && !healthData.IsReady() {
		return visualStarting
	}
	if checkConfig.Liveness != nil {
		if healthData.GetLastCheck().IsZero() {
			return visualStarting
		}
		if !healthData.IsAlive() {
			return visualUnhealthy
		}
	}
	return visualRunning
}

func (m *Model) renderServiceDetails(svc *service.Service, width, height int) string {
	contentWidth := max(1, width-2)
	contentHeight := max(1, height-2)
	if svc == nil {
		title := renderPanelTitle("[2] DETAILS", contentWidth)
		return m.panelStyle(panelDetails).Width(contentWidth).Height(contentHeight).Render(title + "\n\nNo service selected")
	}

	lines := m.serviceDetailLines(svc)
	viewportHeight := max(1, contentHeight-1)
	maxOffset := max(0, len(lines)-viewportHeight)
	offset := min(m.detailOffset, maxOffset)
	end := min(len(lines), offset+viewportHeight)
	title := "[2] DETAILS"
	if maxOffset > 0 {
		title = fmt.Sprintf("[2] DETAILS  %d–%d/%d  ↑/↓", offset+1, end, len(lines))
	}
	visible := append([]string{renderPanelTitle(title, contentWidth)}, lines[offset:end]...)
	for i := range visible {
		visible[i] = ansi.Truncate(visible[i], contentWidth, "…")
	}
	return m.panelStyle(panelDetails).Width(contentWidth).Height(contentHeight).Render(strings.Join(visible, "\n"))
}

func (m *Model) serviceDetailLines(svc *service.Service) []string {
	visualState := m.serviceVisualState(svc)
	lines := []string{
		serviceStatusIndicator(visualState) + " " + ServiceNameStyle.Render(svc.Name) + "  " +
			ContextBarStyle.Render(serviceStatusLabel(svc.Status(), visualState)),
		DetailLabelStyle.Render("PID") + " " + detailValue(fmt.Sprintf("%d", svc.PID())) +
			"   " + DetailLabelStyle.Render("DIR") + " " + detailValue(svc.Config.Dir),
	}
	if visualState == visualQueued {
		reason := "Scheduled by the current start operation"
		if len(svc.Config.DependsOn) > 0 {
			reason = "Waiting for dependencies: " + strings.Join(svc.Config.DependsOn, ", ")
		}
		lines = append(lines, DetailLabelStyle.Render("START")+" "+StartingBadgeStyle.Render(reason))
	}
	if svc.Config.Description != "" {
		lines = append(lines, DetailLabelStyle.Render("ABOUT")+" "+detailValue(svc.Config.Description))
	}
	lines = append(lines, m.renderPortDetailLines(svc)...)
	lines = append(lines,
		DetailLabelStyle.Render("TAGS")+" "+detailValue(joinOrDash(svc.Config.Tags)),
	)
	lines = append(lines, dependencyDetailLines(svc)...)
	lines = append(lines, m.healthDetailLines("READINESS", healthReadiness(svc), m.readinessSummary(svc))...)
	lines = append(lines, m.healthDetailLines("LIVENESS", healthLiveness(svc), m.livenessSummary(svc))...)
	if svc.Config.ReadyLogLine != "" {
		lines = append(lines, DetailLabelStyle.Render("READY LOG")+" "+detailValue(svc.Config.ReadyLogLine))
	}
	lines = append(lines, availabilityDetailLines(svc)...)
	lines = append(lines, shutdownDetailLines(svc)...)
	if len(svc.Config.EnvFiles) > 0 {
		lines = append(lines, DetailLabelStyle.Render("ENV FILES")+" "+detailValue(strings.Join(svc.Config.EnvFiles, ", ")))
	}
	if len(svc.Config.SuccessExitCodes) > 0 {
		codes := make([]string, 0, len(svc.Config.SuccessExitCodes))
		for _, code := range svc.Config.SuccessExitCodes {
			codes = append(codes, strconv.Itoa(code))
		}
		lines = append(lines, DetailLabelStyle.Render("SUCCESS")+" "+detailValue("0, "+strings.Join(codes, ", ")))
	}
	if svc.Config.Disabled {
		lines = append(lines, StartingBadgeStyle.Render("DISABLED")+" "+detailValue("manual start only"))
	}
	lines = append(lines, DetailLabelStyle.Render("COMMAND")+" "+detailValue(svc.Config.Command))
	return lines
}

func dependencyDetailLines(svc *service.Service) []string {
	if len(svc.Config.DependsOn) == 0 {
		return []string{DetailLabelStyle.Render("DEPENDS") + " " + detailValue("—")}
	}
	lines := make([]string, 0, len(svc.Config.DependsOn))
	for index, dependency := range svc.Config.DependsOn {
		label := "       "
		if index == 0 {
			label = "DEPENDS"
		}
		condition := config.DependencyHealthy
		if configured, ok := svc.Config.DependencyConditions[dependency]; ok && configured.Condition != "" {
			condition = configured.Condition
		}
		lines = append(lines, DetailLabelStyle.Render(label)+" "+detailValue(dependency+" · "+string(condition)))
	}
	return lines
}

func availabilityDetailLines(svc *service.Service) []string {
	availability := svc.Config.Availability
	policy := availability.Restart
	if policy == "" {
		policy = "no"
	}
	parts := []string{"restart " + policy}
	if policy == "always" || policy == "on_failure" {
		backoff := availability.Backoff
		if backoff <= 0 {
			backoff = time.Second
		}
		limit := "unlimited"
		if availability.MaxRestarts > 0 {
			limit = strconv.Itoa(availability.MaxRestarts)
		}
		parts = append(parts, "backoff "+backoff.String(), "limit "+limit, fmt.Sprintf("restarts %d", svc.GetState().RestartCount))
	}
	if availability.ExitOnEnd {
		parts = append(parts, "exit on end")
	}
	if availability.ExitOnSkipped {
		parts = append(parts, "exit on skipped")
	}
	return detailSectionLines("RECOVERY", parts)
}

func shutdownDetailLines(svc *service.Service) []string {
	shutdown := svc.Config.Shutdown
	timeout := shutdown.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	target := "process group"
	if shutdown.ParentOnly {
		target = "parent only"
	}
	if shutdown.Command != "" {
		return detailSectionLines("SHUTDOWN", []string{"command " + shutdown.Command, "timeout " + timeout.String()})
	}
	signal := shutdown.Signal
	if signal == 0 {
		signal = 15
	}
	return detailSectionLines("SHUTDOWN", []string{fmt.Sprintf("signal %d", signal), "timeout " + timeout.String(), "target " + target})
}

func detailSectionLines(label string, parts []string) []string {
	lines := []string{DetailLabelStyle.Render(label)}
	for _, part := range parts {
		lines = append(lines, ContextBarStyle.Render("  ↳ ")+detailValue(part))
	}
	return lines
}

func (m *Model) healthDetailLines(label string, check *config.CheckConfig, status string) []string {
	lines := []string{DetailLabelStyle.Render(label) + " " + status}
	if check != nil {
		lines = append(lines, ContextBarStyle.Render(checkDescription(check)))
	}
	return lines
}

func healthReadiness(svc *service.Service) *config.CheckConfig {
	if svc.Config.HealthCheck == nil {
		return nil
	}
	return svc.Config.HealthCheck.Readiness
}

func healthLiveness(svc *service.Service) *config.CheckConfig {
	if svc.Config.HealthCheck == nil {
		return nil
	}
	return svc.Config.HealthCheck.Liveness
}

func (m *Model) scrollDetails(direction int) {
	svc := m.FocusedService()
	if svc == nil {
		m.detailOffset = 0
		return
	}
	_, detailHeight := m.serviceColumnLayout(m.height - 2)
	viewportHeight := max(1, detailHeight-3)
	maxOffset := max(0, len(m.serviceDetailLines(svc))-viewportHeight)
	m.detailOffset = min(maxOffset, max(0, m.detailOffset+direction))
}

func (m *Model) renderPortDetailLines(svc *service.Service) []string {
	if len(svc.Config.Ports) == 0 {
		return []string{DetailLabelStyle.Render("PORTS") + " " + detailValue("—")}
	}

	lines := make([]string, 0, len(svc.Config.Ports))
	for index, portNumber := range svc.Config.Ports {
		label := "     "
		if index == 0 {
			label = "PORTS"
		}
		lines = append(lines, m.renderPortDetail(svc, portNumber, label)...)
	}
	return lines
}

func (m *Model) renderPortDetail(svc *service.Service, portNumber int, label string) []string {
	prefix := DetailLabelStyle.Render(label) + " " + PortStyle.Render(fmt.Sprintf("%d", portNumber)) + " "
	if m.portService != svc.Name || (m.portScanBusy && m.portChecked.IsZero()) {
		return []string{prefix + StartingBadgeStyle.Render("checking…")}
	}
	if m.portError != nil {
		return []string{prefix + FailedBadgeStyle.Render("unavailable")}
	}
	if info := m.portDetails[portNumber]; info != nil {
		return renderListeningPort(prefix, info, m.manager.ManagedServiceForPID(info.PID))
	}
	return []string{prefix + StoppedBadgeStyle.Render("free")}
}

func renderListeningPort(prefix string, info *config.PortInfo, managedService string) []string {
	line := prefix + RunningBadgeStyle.Render("listening")
	if endpoint := listenerEndpoint(info); endpoint != "" {
		line += ContextBarStyle.Render(" · " + endpoint)
	}
	lines := []string{line}
	owner := make([]string, 0, 2)
	if info.Process != "" {
		owner = append(owner, info.Process)
	}
	if info.PID > 0 {
		owner = append(owner, fmt.Sprintf("PID %d", info.PID))
	}
	if len(owner) > 0 {
		ownership := "owner: unknown"
		if managedService != "" {
			ownership = "owner: kranz"
		} else if info.PID > 0 {
			ownership = "owner: external"
		}
		lines = append(lines, DetailLabelStyle.Render("     ")+" "+ContextBarStyle.Render(
			"↳ "+strings.Join(owner, " · ")+" · "+ownership,
		))
	}
	return lines
}

func listenerEndpoint(info *config.PortInfo) string {
	if info == nil || info.Protocol == "" || info.Address == "" {
		return ""
	}
	address := info.Address
	if strings.Contains(address, ":") && !strings.HasPrefix(address, "[") {
		address = "[" + address + "]"
	}
	return strings.ToLower(info.Protocol) + "://" + address + fmt.Sprintf(":%d", info.Port)
}

func (m *Model) readinessSummary(svc *service.Service) string {
	if svc.Config.HealthCheck == nil || svc.Config.HealthCheck.Readiness == nil {
		return detailValue("not configured")
	}
	healthData := m.healthChecker.GetHealth(svc.Name)
	if healthData == nil {
		return StoppedBadgeStyle.Render("inactive")
	}
	if !healthData.IsReady() {
		return StartingBadgeStyle.Render("waiting")
	}
	return RunningBadgeStyle.Render("ready")
}

func (m *Model) livenessSummary(svc *service.Service) string {
	if svc.Config.HealthCheck == nil || svc.Config.HealthCheck.Liveness == nil {
		return detailValue("not configured")
	}
	healthData := m.healthChecker.GetHealth(svc.Name)
	if healthData == nil {
		return StoppedBadgeStyle.Render("inactive")
	}
	if healthData.GetLastCheck().IsZero() {
		return StartingBadgeStyle.Render("checking")
	}
	if healthData.IsAlive() {
		return RunningBadgeStyle.Render("alive")
	}
	return FailedBadgeStyle.Render("failed")
}

func checkDescription(check *config.CheckConfig) string {
	switch check.Type {
	case config.CheckHTTP:
		return check.URL
	case config.CheckTCP:
		return fmt.Sprintf("tcp://localhost:%d", check.Port)
	case config.CheckCommand:
		return "$ " + check.Command
	default:
		return string(check.Type)
	}
}

func joinOrDash(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	return strings.Join(values, ", ")
}

func detailValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

// renderLogPanel renders the focused service's bounded log viewport.
func (m *Model) renderLogPanel(svc *service.Service, width, height int) string {
	return m.renderLogPanelMode(svc, width, height, false)
}

func (m *Model) renderPinnedLogPanel(svc *service.Service, width, height int) string {
	return m.renderLogPanelMode(svc, width, height, true)
}

func (m *Model) renderLogPanelMode(svc *service.Service, width, height int, pinned bool) string {
	contentWidth := max(1, width-2)
	contentHeight := max(1, height-2)
	panelStyle := m.panelStyle(panelLogs)
	titlePrefix := "[3] LOGS  "
	followMode, logOffset, logAnchor, logPaused := m.followMode, m.logOffset, m.logAnchor, m.logPaused
	if pinned {
		panelStyle = m.panelStyle(panelPinnedLogs)
		titlePrefix = "[3] PINNED LOGS  "
		followMode, logOffset, logAnchor, logPaused = m.pinnedFollow, m.pinnedOffset, m.pinnedAnchor, false
	}
	if svc == nil {
		title := renderPanelTitle(titlePrefix, contentWidth)
		return boundedPanel(panelStyle, contentWidth, contentHeight, []string{title, "", "Select a service"})
	}

	title := titlePrefix + ServiceNameStyle.Render(svc.Name)
	if !followMode {
		state := "BROWSING"
		if logPaused {
			state = "PAUSED"
		}
		title += " " + StartingBadgeStyle.Render(state)
	}
	if m.wrapLogs {
		title += " " + RunningBadgeStyle.Render("WRAP")
	}
	if m.showLogTime {
		title += " " + RunningBadgeStyle.Render("TIME")
	}

	sourceEntries := svc.LogEntries()
	sourceLines := logEntryLines(sourceEntries)

	var searchMatches []int
	hasPattern := !pinned && m.logSearcher != nil && m.logSearcher.HasPattern()
	if hasPattern {
		searchMatches = m.logSearcher.Search(sourceLines)
		mode := "FILTER"
		if m.searchMode == searchHighlight {
			mode = "HIGHLIGHT"
		}
		title += SearchInputStyle.Render(fmt.Sprintf("  %s /%s/ · %d", mode, m.logSearcher.Pattern(), len(searchMatches)))
	}
	matchSet := make(map[int]bool, len(searchMatches))
	for _, idx := range searchMatches {
		matchSet[idx] = true
	}
	sourceIndices := make([]int, len(sourceLines))
	for index := range sourceIndices {
		sourceIndices[index] = index
	}
	if hasPattern && m.searchMode == searchFilter {
		sourceIndices = append([]int(nil), searchMatches...)
	}

	if len(sourceLines) == 0 {
		return boundedPanel(panelStyle, contentWidth, contentHeight, []string{
			renderPanelTitle(title, contentWidth),
			"",
			ContextBarStyle.Render("Output will appear after the service starts"),
		})
	}
	if hasPattern && m.searchMode == searchFilter && len(sourceIndices) == 0 {
		return boundedPanel(panelStyle, contentWidth, contentHeight, []string{
			renderPanelTitle(title, contentWidth),
			"",
			ContextBarStyle.Render("No log lines match this regex"),
		})
	}

	rows := make([]string, 0, len(sourceIndices))
	for _, actualIndex := range sourceIndices {
		displayLine := m.displayLogEntry(sourceEntries[actualIndex])
		for _, segment := range strings.Split(displayLine, "\n") {
			styled := styleLogLine(segment)
			visualLines := []string{ansi.Truncate(styled, contentWidth, "…")}
			if m.wrapLogs {
				visualLines = strings.Split(ansi.Hardwrap(styled, contentWidth, true), "\n")
			}
			for _, visualLine := range visualLines {
				visualLine = ansi.Truncate(visualLine, contentWidth, "")
				if !pinned && m.searchMode == searchHighlight && matchSet[actualIndex] {
					visualLine = SearchHighlightStyle.Render(preserveStyleAfterReset(visualLine, SearchHighlightStyle))
				}
				rows = append(rows, visualLine)
			}
		}
	}

	maxLines := max(1, contentHeight-1)
	maxStart := max(0, len(rows)-maxLines)
	startIdx := maxStart
	visibleLimit := len(rows)
	if !followMode {
		anchor := min(len(rows), max(0, logAnchor))
		anchorStart := max(0, anchor-maxLines)
		startIdx = max(0, anchorStart-logOffset)
		visibleLimit = anchor
	}
	endIdx := min(visibleLimit, startIdx+maxLines)
	if maxStart > 0 {
		title += ContextBarStyle.Render(fmt.Sprintf("  %d–%d/%d  ↑/↓", startIdx+1, endIdx, len(rows)))
	}
	renderedLines := []string{renderPanelTitle(title, contentWidth)}
	for _, row := range rows[startIdx:endIdx] {
		renderedLines = append(renderedLines, row)
	}
	return boundedPanel(panelStyle, contentWidth, contentHeight, renderedLines)
}

func boundedPanel(style lipgloss.Style, width, height int, lines []string) string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], width, "")
	}
	style = style.Width(width).Height(height).MaxWidth(width + 2).MaxHeight(height + 2)
	return style.Render(strings.Join(lines, "\n"))
}

func renderPanelTitle(title string, width int) string {
	title = ansi.Truncate(title, width, "…")
	if titleWidth := lipgloss.Width(title); titleWidth < width {
		title += strings.Repeat(" ", width-titleWidth)
	}
	return PanelTitleStyle.Render(preserveStyleAfterReset(title, PanelTitleStyle))
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

func (m *Model) scrollLogs(direction int) {
	pinned := m.panelFocus == panelPinnedLogs && m.PinnedService() != nil
	svc := m.FocusedService()
	panelHeight := m.currentLogPanelHeight()
	displayLineCount := m.displayedLogLineCount()
	offset, anchor, follow := m.logOffset, m.logAnchor, m.followMode
	if pinned {
		svc = m.PinnedService()
		panelHeight = m.pinnedLogPanelHeight()
		displayLineCount = m.displayedPinnedLogLineCount()
		offset, anchor, follow = m.pinnedOffset, m.pinnedAnchor, m.pinnedFollow
	}
	if svc == nil {
		if pinned {
			m.pinnedOffset = 0
		} else {
			m.logOffset = 0
		}
		return
	}
	maxLines := max(1, panelHeight-3)
	maxOffset := max(0, displayLineCount-maxLines)
	if direction < 0 {
		if follow {
			anchor = displayLineCount
		}
		follow = false
		offset = min(maxOffset, offset+1)
	} else {
		offset = max(0, offset-1)
		if offset == 0 {
			follow, anchor = true, 0
		}
	}
	if pinned {
		m.pinnedOffset, m.pinnedAnchor, m.pinnedFollow = offset, anchor, follow
	} else {
		m.logOffset, m.logAnchor, m.followMode = offset, anchor, follow
		m.logPaused = false
	}
}

func (m *Model) currentLogPanelHeight() int {
	height := max(1, m.height-2)
	if m.PinnedService() != nil {
		height -= height / 2
	}
	return height
}

func (m *Model) pinnedLogPanelHeight() int {
	height := max(1, m.height-2)
	if m.PinnedService() == nil {
		return height
	}
	return height / 2
}

func (m *Model) displayedPinnedLogLineCount() int {
	svc := m.PinnedService()
	if svc == nil {
		return 0
	}
	lines := make([]string, 0, len(svc.LogEntries()))
	for _, entry := range svc.LogEntries() {
		lines = append(lines, m.displayLogEntry(entry))
	}
	return visualLogRowCount(lines, m.currentLogContentWidth(), m.wrapLogs)
}

func (m *Model) displayedLogLineCount() int {
	svc := m.FocusedService()
	if svc == nil {
		return 0
	}
	entries := svc.LogEntries()
	lines := logEntryLines(entries)
	indices := make([]int, len(lines))
	for index := range indices {
		indices[index] = index
	}
	if m.searchMode == searchFilter && m.logSearcher != nil && m.logSearcher.HasPattern() {
		indices = m.logSearcher.Search(lines)
	}
	selectedLines := make([]string, 0, len(indices))
	for _, index := range indices {
		selectedLines = append(selectedLines, m.displayLogEntry(entries[index]))
	}
	return visualLogRowCount(selectedLines, m.currentLogContentWidth(), m.wrapLogs)
}

func visualLogRowCount(lines []string, width int, wrap bool) int {
	count := 0
	for _, line := range lines {
		for _, segment := range strings.Split(strings.ReplaceAll(line, "\r", ""), "\n") {
			count++
			if wrap {
				count += strings.Count(ansi.Hardwrap(styleLogLine(segment), width, true), "\n")
			}
		}
	}
	return count
}

func (m *Model) currentLogContentWidth() int {
	return max(1, m.width-m.dashboardLeftWidth()-2)
}

func (m *Model) focusLogMatch(match int) {
	svc := m.FocusedService()
	if svc == nil || match < 0 {
		return
	}
	entries := svc.LogEntries()
	maxLines := max(1, m.currentLogPanelHeight()-3)
	displayLines := make([]string, 0, min(match, len(entries)))
	for _, entry := range entries[:min(match, len(entries))] {
		displayLines = append(displayLines, m.displayLogEntry(entry))
	}
	row := visualLogRowCount(displayLines, m.currentLogContentWidth(), m.wrapLogs)
	totalRows := m.displayedLogLineCount()
	maxStart := max(0, totalRows-maxLines)
	desiredStart := min(maxStart, max(0, row-maxLines/2))
	m.logOffset = maxStart - desiredStart
	if desiredStart == maxStart {
		m.logAnchor = 0
		m.followMode = true
	} else {
		m.logAnchor = totalRows
		m.followMode = false
	}
	m.logPaused = false
	m.panelFocus = panelLogs
}

func serviceLogLines(svc *service.Service) []string {
	if svc == nil {
		return nil
	}
	return logEntryLines(svc.LogEntries())
}

func logEntryLines(entries []config.LogEntry) []string {
	lines := make([]string, len(entries))
	for index, entry := range entries {
		lines[index] = strings.TrimRight(ansi.Strip(entry.Raw), "\r\n")
	}
	return lines
}

func (m *Model) displayLogEntry(entry config.LogEntry) string {
	line := strings.TrimRight(strings.ReplaceAll(ansi.Strip(entry.Raw), "\r", ""), "\n")
	if !m.showLogTime || entry.Timestamp.IsZero() {
		return line
	}
	prefix := "[" + entry.Timestamp.Local().Format("15:04:05.000") + "] "
	segments := strings.Split(line, "\n")
	for index := range segments {
		segments[index] = prefix + segments[index]
	}
	return strings.Join(segments, "\n")
}

// ---- Rendering helpers ---- //

// styleLogLine applies a semantic color without trusting child-process ANSI.
func styleLogLine(line string) string {
	timestamp, message := splitLogTimestamp(line)
	level := detectLogLevel(message)
	var styled string
	switch level {
	case config.LogError:
		styled = LogErrorStyle.Render(message)
	case config.LogWarn:
		styled = LogWarnStyle.Render(message)
	case config.LogDebug:
		styled = LogDebugStyle.Render(message)
	default:
		source, remainder := splitLogSource(message)
		styled = LogInfoStyle.Render(message)
		if source != "" {
			styled = LogSourceStyle.Render(source) + LogInfoStyle.Render(remainder)
		}
	}
	if timestamp != "" {
		return LogTimestampStyle.Render(timestamp) + styled
	}
	return styled
}

func splitLogTimestamp(line string) (timestamp, message string) {
	if len(line) < 15 || line[0] != '[' || line[3] != ':' || line[6] != ':' ||
		line[9] != '.' || line[13] != ']' || line[14] != ' ' {
		return "", line
	}
	return line[:15], line[15:]
}

func splitLogSource(line string) (source, remainder string) {
	if !strings.HasPrefix(line, "[") {
		return "", line
	}
	end := strings.IndexByte(line, ']')
	if end < 1 || end > 24 {
		return "", line
	}
	return line[:end+1], line[end+1:]
}

func renderSelectedLine(line string) string {
	return SelectionStyle.Render(preserveStyleAfterReset(line, SelectionStyle))
}

// detectLogLevel infers a display level from common log prefixes.
func detectLogLevel(line string) config.LogLevel {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") ||
		strings.Contains(lower, "panic") || strings.Contains(lower, "exception") ||
		strings.Contains(lower, "failed") {
		return config.LogError
	}
	if strings.Contains(lower, "warn") || strings.Contains(lower, "warning") {
		return config.LogWarn
	}
	if strings.Contains(lower, "debug") || strings.Contains(lower, "trace") {
		return config.LogDebug
	}
	return config.LogInfo
}

// containsTagStr reports whether tags contains target, ignoring case.
func containsTagStr(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
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

func padPlainLine(line string, width int) string {
	line = ansi.Truncate(line, width, "")
	if lipgloss.Width(line) < width {
		line += strings.Repeat(" ", width-lipgloss.Width(line))
	}
	return line
}
