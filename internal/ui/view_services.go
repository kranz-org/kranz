package ui

import (
	"fmt"
	"strings"
	"time"

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
		title := "[1] SERVICES/ACTIONS"
		if m.listMode == listTags {
			title = "[1] TAGS"
		}
		serviceList = renderCollapsedPanel(title, width)
	}
	details := m.renderServiceDetails(m.FocusedService(), width, detailHeight)
	if m.listMode == listServices && m.focusedAction != nil {
		details = m.renderActionDetails(width, detailHeight)
	} else if m.listMode == listServices && m.focusedActionGroup != "" {
		details = m.renderActionGroupDetails(m.focusedActionGroup, width, detailHeight)
	} else if m.listMode == listTags {
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

	itemCount := len(m.serviceListRows())
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
	rows := m.serviceListRows()
	if len(rows) == 0 {
		return renderTitledPanel(m.panelStyle(panelServices), m.panelTitleStyle(panelServices), contentWidth, contentHeight, "[1] SERVICES/ACTIONS", []string{"", "No services or actions"})
	}

	meta := fmt.Sprintf("%d · 1→Tags · %d actions · Enter open · s run", len(m.services), len(m.cfg.ActionIDs()))
	if len(m.selected) > 0 {
		meta += fmt.Sprintf(" · SELECTED %d", len(m.selected))
	} else if len(m.selectedTags) > 0 {
		meta += fmt.Sprintf(" · TAGS %d", len(m.selectedTags))
	}
	title := "[1] SERVICES/ACTIONS" + ContextBarStyle.Render(" │ "+meta)
	lines := make([]string, 0, contentHeight)
	start, end := m.visibleServiceRange(height)
	for index := start; index < end; index++ {
		line := m.renderServiceListRow(index, rows[index], contentWidth)
		lines = append(lines, line)
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], contentWidth, "…")
	}

	return renderTitledPanel(m.panelStyle(panelServices), m.panelTitleStyle(panelServices), contentWidth, contentHeight, title, lines)
}

func (m *Model) visibleServiceRange(height int) (start, end int) {
	rows := m.serviceListRows()
	available := max(1, height-2) // the title shares the top border row
	if len(rows) <= available {
		return 0, len(rows)
	}
	cursor := max(0, m.focusedServiceListRow())
	start = max(0, cursor-available/2)
	if start+available > len(rows) {
		start = max(0, len(rows)-available)
	}
	return start, min(len(rows), start+available)
}

func (m *Model) renderServiceListRow(index int, row actionListRow, width int) string {
	focused := index == m.focusedServiceListRow()
	switch row.Kind {
	case actionRowService:
		return m.renderServiceOwnerLine(row.Service, width, focused)
	case actionRowGroup:
		disclosure := "▸"
		if m.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, row.Group)] {
			disclosure = "▾"
		}
		line := "  ⚡ " + ServiceNameStyle.Render(row.Group) + ContextBarStyle.Render(" "+disclosure)
		return renderListLine(line, width, focused)
	case actionRowAction:
		state, _ := m.manager.ActionState(row.Action)
		status := actionStatusIndicator(state.Status) + " " + row.Action.Name
		if state.Status != service.ActionReady {
			status += ContextBarStyle.Render("  " + state.Status.String())
		}
		if state.Duration > 0 && state.Status != service.ActionRunning {
			status += ContextBarStyle.Render(" · " + state.Duration.Round(time.Millisecond).String())
		}
		return renderListLine("      "+status, width, focused)
	default:
		return ""
	}
}

func (m *Model) renderServiceOwnerLine(svc *service.Service, width int, focused bool) string {
	disclosure := " "
	if len(svc.Config.Actions) > 0 {
		disclosure = "▸"
		if m.expandedActionOwner[actionOwnerKey(config.ActionOwnerService, svc.Name)] {
			disclosure = "▾"
		}
	}
	visualState := m.serviceVisualState(svc)
	selection := "[ ]"
	if m.selected[svc.Name] {
		selection = RunningBadgeStyle.Render("[✓]")
	}
	line := "  " + selection + " " + serviceStatusIndicator(visualState) + " " + ServiceNameStyle.Render(svc.Name)
	if visualState == visualQueued {
		line += StartingBadgeStyle.Render("  queued")
	}
	if !focused && svc.NewLogCount() > 0 {
		line += NewLogIndicatorStyle.Render(fmt.Sprintf(" +%d", svc.NewLogCount()))
	}
	if disclosure != " " {
		line += ContextBarStyle.Render(" " + disclosure)
	}
	return renderListLine(line, width, focused)
}

func renderListLine(line string, width int, focused bool) string {
	marker := "  "
	if focused {
		marker = HelpKeyStyle.Render("› ")
	}
	line = marker + strings.TrimPrefix(line, "  ")
	line = ansi.Truncate(line, width, "…")
	if lipgloss.Width(line) < width {
		line += strings.Repeat(" ", width-lipgloss.Width(line))
	}
	if focused {
		return renderSelectedLine(line)
	}
	return line
}

func actionStatusIndicator(status service.ActionStatus) string {
	switch status {
	case service.ActionRunning:
		return StartingBadgeStyle.Render("◐")
	case service.ActionSucceeded:
		return RunningBadgeStyle.Render("✓")
	case service.ActionFailed, service.ActionTimedOut:
		return FailedBadgeStyle.Render("×")
	case service.ActionCancelled:
		return StartingBadgeStyle.Render("■")
	default:
		return ContextBarStyle.Render("○")
	}
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
	return m.renderServiceOwnerLine(svc, width, index == m.focused && m.focusedAction == nil && m.focusedActionGroup == "")
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
