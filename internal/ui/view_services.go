package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

// The first column: the service list, the tag list with its inline expansion,
// and the state indicators shared by both.

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

	// The title sits in the top border, so only the two border rows are chrome.
	// Keep the list compact for small projects and cap it at 20 visible items.
	const (
		maxVisibleItems = 20
		panelChromeRows = 2
		minPanelHeight  = 6
	)
	desiredHeight := min(itemCount, maxVisibleItems) + panelChromeRows
	listHeight = max(minPanelHeight, desiredHeight)
	if height < listHeight*2 {
		return splitPanelHeights(height)
	}
	return listHeight, height - listHeight
}

// renderServicePanel renders the service list in the upper-left panel.
func (m *Model) renderServicePanel(width, height int) string {
	if m.listMode == listTags {
		return m.renderTagPanel(width, height)
	}
	contentWidth := max(1, width-2)
	contentHeight := max(1, height-2)
	if len(m.services) == 0 {
		return renderTitledPanel(m.panelStyle(panelServices), m.panelTitleStyle(panelServices), contentWidth, contentHeight, "[1] SERVICES", []string{"", "No services"})
	}

	meta := fmt.Sprintf("%d · 1 → Tags", len(m.services))
	if len(m.selected) > 0 {
		meta += fmt.Sprintf(" · SELECTED %d", len(m.selected))
	} else if len(m.selectedTags) > 0 {
		meta += fmt.Sprintf(" · TAGS %d", len(m.selectedTags))
	}
	title := "[1] SERVICES" + ContextBarStyle.Render(" │ "+meta)
	lines := make([]string, 0, contentHeight)
	start, end := m.visibleServiceRange(height)
	for i := start; i < end; i++ {
		svc := m.services[i]
		line := m.renderServiceLine(i, svc, contentWidth)
		lines = append(lines, line)
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], contentWidth, "…")
	}

	return renderTitledPanel(m.panelStyle(panelServices), m.panelTitleStyle(panelServices), contentWidth, contentHeight, title, lines)
}

func (m *Model) visibleServiceRange(height int) (start, end int) {
	available := max(1, height-2) // the title shares the top border row
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
	lines := make([]string, 0, contentHeight)
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
	return renderTitledPanel(m.panelStyle(panelServices), m.panelTitleStyle(panelServices), contentWidth, contentHeight, title, lines)
}

func (m *Model) visibleTagRange(height int) (start, end int) {
	rows := m.tagRows()
	available := max(1, height-2) // the title shares the top border row
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

// containsTagStr reports whether tags contains target, ignoring case.
func containsTagStr(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}
