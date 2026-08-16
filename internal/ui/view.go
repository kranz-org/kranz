package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

const (
	compactDashboardHeight = 21
	collapsedPanelHeight   = 1
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
	case ModeConfirmClearLogs:
		content = m.renderConfirmClearLogsView()
	case ModeConfirmAction:
		content = m.renderConfirmActionView()
	case ModeConfirmServiceStart:
		content = m.renderConfirmServiceStartView()
	case ModeConfirmServiceStop:
		content = m.renderConfirmServiceStopView()
	case ModeConfirmThemeSave:
		content = m.renderConfirmThemeSaveView()
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

func (m *Model) panelStyle(focus panelFocus) lipgloss.Style {
	// A rejected click while searching flashes the panel the filter applies to,
	// which is the one holding the user's attention.
	if m.panelFocus == focus && m.searchNudgeActive() {
		return NudgedPanelStyle
	}
	if m.panelFocus == focus {
		return FocusedPanelStyle
	}
	return PanelStyle
}

func (m *Model) panelTitleStyle(focus panelFocus) lipgloss.Style {
	if m.panelFocus == focus {
		return FocusedTitleStyle
	}
	return PanelTitleStyle
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
	left := HeaderStyle.Render(fmt.Sprintf("KRANZ  /  %s", m.cfg.Project))
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
	actionFocused := m.listMode == listServices && m.focusedAction != nil
	actionGroupFocused := m.listMode == listServices && m.focusedActionGroup != ""
	targets := m.selectedTargetNames()
	allActive := len(targets) > 0
	allRunning := len(targets) > 0
	canToggle := len(targets) > 0
	for _, name := range targets {
		svc, ok := m.manager.GetService(name)
		if !ok || !serviceStartPlanned(svc) {
			allActive = false
		}
		if !ok || svc.Status() == config.StatusStopped {
			allRunning = false
		}
		if !ok || (serviceStartPlanned(svc) && !svc.CanStop()) || (!serviceStartPlanned(svc) && !svc.CanStart()) {
			canToggle = false
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
	forceLabel := "Force start: S"
	if allRunning {
		forceLabel = "Force stop: S"
	}
	interruptibleStart := false
	switch m.operationKind {
	case operationStart, operationStartSet:
		interruptibleStart = m.operationCancel != nil && allActive
	}
	if actionFocused {
		toggleStyle = PrimaryButtonStyle
		toggleLabel = "▶ Run action: s"
		compactToggle = "Run: s"
		if state, exists := m.manager.ActionState(*m.focusedAction); exists && state.Status == service.ActionRunning {
			toggleStyle = DangerButtonStyle
			toggleLabel = "■ Stop action: s"
			compactToggle = "Stop: s"
		}
	} else if !canToggle || (m.operation != "" && !interruptibleStart) {
		toggleStyle = DisabledButtonStyle
	}
	if m.width < 100 {
		compact := func(style lipgloss.Style, label string) string {
			return style.Padding(0).Render(label)
		}
		allLabel := "All: a"
		if len(m.allServices) > 0 && len(m.selected) == len(m.allServices) {
			allLabel = "Clear: a"
		}
		if actionFocused {
			return []actionButton{
				{action: "toggle", rendered: compact(toggleStyle, compactToggle)},
				{action: "all", rendered: compact(SecondaryButtonStyle, allLabel)},
				{action: "quit", rendered: compact(DangerButtonStyle, "Quit: q")},
			}
		}
		if actionGroupFocused {
			return []actionButton{
				{action: "all", rendered: compact(SecondaryButtonStyle, allLabel)},
				{action: "quit", rendered: compact(DangerButtonStyle, "Quit: q")},
			}
		}
		return []actionButton{
			{action: "toggle", rendered: compact(toggleStyle, compactToggle)},
			{action: "force", rendered: compact(WarningButtonStyle, "Force: S")},
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
	if actionFocused {
		return []actionButton{
			{action: "toggle", rendered: toggleStyle.Render(toggleLabel)},
			{action: "all", rendered: SecondaryButtonStyle.Render(allLabel)},
			{action: "quit", rendered: DangerButtonStyle.Render("Quit: q")},
		}
	}
	if actionGroupFocused {
		return []actionButton{
			{action: "all", rendered: SecondaryButtonStyle.Render(allLabel)},
			{action: "quit", rendered: DangerButtonStyle.Render("Quit: q")},
		}
	}
	return []actionButton{
		{action: "toggle", rendered: toggleStyle.Render(toggleLabel)},
		{action: "force", rendered: WarningButtonStyle.Render(forceLabel)},
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

// searchEditorMinWidth is the narrowest regex editor worth showing before the
// hint text is sacrificed to make room for it.
const searchEditorMinWidth = 24

// searchBarLayout splits the search bar into its label, its hint text, and the
// width left over for the editor. The editor takes priority: seeing what you
// are typing matters more than the reminders, so the hints shrink and then
// disappear before the input is squeezed below a usable width.
func (m *Model) searchBarLayout() (label, hints string, editorWidth int) {
	mode, alternate := "FILTER", "Highlight"
	if m.searchMode == searchHighlight {
		mode, alternate = "HIGHLIGHT", "Filter"
	}
	label = fmt.Sprintf(" Regex %s /", mode)
	remaining := func(hints string) int {
		return m.width - lipgloss.Width(label) - lipgloss.Width(hints) - 1
	}
	hints = fmt.Sprintf("  [Enter] apply  [Esc] done  [Tab] %s  [Ctrl+U] erase", alternate)
	if remaining(hints) < searchEditorMinWidth {
		hints = "  [Enter] apply  [Esc] done"
	}
	if remaining(hints) < searchEditorMinWidth {
		hints = ""
	}
	return label, hints, max(1, remaining(hints))
}

// renderSearchView keeps the dashboard visible while editing a log expression.
func (m *Model) renderSearchView() string {
	label, hints, _ := m.searchBarLayout()
	contents := label + m.searchInput.View() + hints
	searchBar := SearchInputStyle.Render(preserveStyleAfterReset(contents, SearchInputStyle))
	searchBar = ansi.Truncate(searchBar, m.width, "…")
	if lipgloss.Width(searchBar) < m.width {
		searchBar += strings.Repeat(" ", m.width-lipgloss.Width(searchBar))
	}
	return m.renderDashboard(searchBar)
}

type helpEntry struct{ key, desc string }

// ---- Dashboard panels ---- //

func splitPanelHeights(height int) (topHeight, bottomHeight int) {
	topHeight = max(0, height/2)
	return topHeight, max(0, height-topHeight)
}

func renderCollapsedPanel(title string, width int) string {
	if width <= 1 {
		return PanelTitleStyle.Foreground(ColorDim).Render(ansi.Truncate(title, max(1, width), "…"))
	}
	contentWidth := width - 2
	title = ansi.Truncate(title, contentWidth, "…")
	title += strings.Repeat(" ", max(0, contentWidth-lipgloss.Width(title)))
	return " " + PanelTitleStyle.Foreground(ColorDim).Render(title) + " "
}

type serviceVisualState int

const (
	visualStopped serviceVisualState = iota
	visualQueued
	visualStarting
	visualRunning
	visualUnhealthy
	visualExternal
	visualChecking
	visualUnknown
)

func sortedStringSet(values map[string]bool) []string {
	items := make([]string, 0, len(values))
	for value := range values {
		items = append(items, value)
	}
	sort.Strings(items)
	if len(items) == 0 {
		return []string{"—"}
	}
	return items
}

func renderTitledPanel(style, titleStyle lipgloss.Style, width, height int, title string, lines []string) string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], width, "")
	}
	bodyStyle := style.BorderTop(false).Width(width).Height(height).MaxWidth(width + 2).MaxHeight(height + 1)
	return renderPanelTop(style, titleStyle, title, width) + "\n" + bodyStyle.Render(strings.Join(lines, "\n"))
}

func renderPanelTitle(style lipgloss.Style, title string, width int) string {
	if width <= 0 {
		return ""
	}
	if width == 1 {
		title = ansi.Truncate(title, width, "")
	} else {
		title = " " + ansi.Truncate(title, max(1, width-2), "…") + " "
	}
	title = ansi.Truncate(title, width, "")
	return style.Render(preserveStyleAfterReset(title, style))
}

func renderPanelTop(style, titleStyle lipgloss.Style, title string, width int) string {
	border := style.GetBorderStyle()
	borderStyle := lipgloss.NewStyle().Foreground(style.GetBorderTopForeground())
	leading := ""
	if width > 0 {
		leading = border.Top
	}
	label := renderPanelTitle(titleStyle, title, max(0, width-lipgloss.Width(leading)))
	trailingWidth := max(0, width-lipgloss.Width(leading)-lipgloss.Width(label))
	top := border.TopLeft + leading + label + strings.Repeat(border.Top, trailingWidth) + border.TopRight
	return borderStyle.Render(preserveStyleAfterReset(top, borderStyle))
}

// ---- Rendering helpers ---- //

func renderSelectedLine(line string) string {
	return SelectionStyle.Render(preserveStyleAfterReset(line, SelectionStyle))
}

func padPlainLine(line string, width int) string {
	line = ansi.Truncate(line, width, "")
	if lipgloss.Width(line) < width {
		line += strings.Repeat(" ", width-lipgloss.Width(line))
	}
	return line
}
