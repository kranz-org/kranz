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
	allRunning := len(targets) > 0
	for _, name := range targets {
		svc, ok := m.manager.GetService(name)
		if !ok || !serviceStartPlanned(svc) {
			allActive = false
		}
		if !ok || svc.Status() == config.StatusStopped {
			allRunning = false
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
	if len(targets) == 0 || (m.operation != "" && !interruptibleStart) {
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
	searchBar := SearchInputStyle.Render(label + m.searchInput.View() + hints)
	searchBar = ansi.Truncate(searchBar, m.width, "…")
	if lipgloss.Width(searchBar) < m.width {
		searchBar += strings.Repeat(" ", m.width-lipgloss.Width(searchBar))
	}
	return m.renderDashboard(searchBar)
}

type helpEntry struct{ key, desc string }

// ---- Dashboard panels ---- //

func (m *Model) renderServiceColumn(width, height int) string {
	listHeight, detailHeight := m.serviceColumnLayout(height)
	serviceList := m.renderServicePanel(width, listHeight)
	if listHeight == collapsedPanelHeight {
		title := "[1] SERVICES"
		if m.listMode == listTags {
			title = "[1] TAGS"
		}
		serviceList = renderCollapsedPanel(title, width)
	}
	details := m.renderServiceDetails(m.FocusedService(), width, detailHeight)
	if m.listMode == listTags {
		if svc := m.focusedTagService(); svc != nil {
			details = m.renderServiceDetails(svc, width, detailHeight)
		} else {
			details = m.renderTagDetails(m.focusedTag(), width, detailHeight)
		}
	}
	if detailHeight == collapsedPanelHeight {
		details = renderCollapsedPanel("[2] DETAILS", width)
	}
	return lipgloss.JoinVertical(lipgloss.Left, serviceList, details)
}

func (m *Model) serviceColumnLayout(height int) (listHeight, detailHeight int) {
	if height <= compactDashboardHeight-dashboardHeaderRows-dashboardFooterRows {
		if m.panelFocus == panelDetails {
			return collapsedPanelHeight, max(collapsedPanelHeight, height-collapsedPanelHeight)
		}
		return max(collapsedPanelHeight, height-collapsedPanelHeight), collapsedPanelHeight
	}

	itemCount := len(m.services)
	if m.listMode == listTags {
		itemCount = len(m.tagRows())
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
	if height < listHeight*2 {
		return splitPanelHeights(height)
	}
	return listHeight, height - listHeight
}

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

	meta := fmt.Sprintf("%d · 1 → Tags", len(m.services))
	if len(m.selected) > 0 {
		meta += fmt.Sprintf(" · SELECTED %d", len(m.selected))
	} else if len(m.selectedTags) > 0 {
		meta += fmt.Sprintf(" · TAGS %d", len(m.selectedTags))
	}
	title := "[1] SERVICES" + ContextBarStyle.Render(" │ "+meta)
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
	rows := m.tagRows()
	meta := fmt.Sprintf("%d · Enter expand", len(tags))
	if len(m.selectedTags) > 0 {
		meta += fmt.Sprintf(" · SELECTED %d", len(m.selectedTags))
	} else if len(m.selected) > 0 {
		meta += fmt.Sprintf(" · SERVICES %d", len(m.selected))
	}
	title := "[1] TAGS" + ContextBarStyle.Render(" │ "+meta)
	lines := []string{renderPanelTitle(title, contentWidth)}
	if len(rows) == 0 {
		lines = append(lines, "", "No tags")
	} else {
		start, end := m.visibleTagRange(height)
		for index := start; index < end; index++ {
			row := rows[index]
			marker := "  "
			if index == m.tagCursor {
				marker = HelpKeyStyle.Render("› ")
			}
			check := "[ ]"
			var line string
			if row.Service == nil {
				if containsTagStr(m.selectedTags, row.Tag) {
					check = RunningBadgeStyle.Render("[✓]")
				}
				disclosure := "▸"
				if m.expandedTags[row.Tag] {
					disclosure = "▾"
				}
				count := len(m.servicesForTag(row.Tag))
				line = fmt.Sprintf("%s%s %s %s (%d)", marker, check, disclosure, row.Tag, count)
			} else {
				if m.selected[row.Service.Name] {
					check = RunningBadgeStyle.Render("[✓]")
				}
				visualState := m.serviceVisualState(row.Service)
				line = fmt.Sprintf("%s  %s %s %s", marker, check, serviceStatusIndicator(visualState), row.Service.Name)
			}
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
	rows := m.tagRows()
	available := max(1, height-3) // border rows plus the title row
	if len(rows) <= available {
		return 0, len(rows)
	}
	start = max(0, m.tagCursor-available/2)
	if start+available > len(rows) {
		start = max(0, len(rows)-available)
	}
	return start, min(len(rows), start+available)
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

// ---- Rendering helpers ---- //

func renderSelectedLine(line string) string {
	return SelectionStyle.Render(preserveStyleAfterReset(line, SelectionStyle))
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

func padPlainLine(line string, width int) string {
	line = ansi.Truncate(line, width, "")
	if lipgloss.Width(line) < width {
		line += strings.Repeat(" ", width-lipgloss.Width(line))
	}
	return line
}
