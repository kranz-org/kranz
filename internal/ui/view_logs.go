package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

// Log panels: layout, scrolling and the follow/browse distinction, filtering and
// match highlighting, and the styling that separates Kranz lifecycle lines from
// ordinary service output.

func (m *Model) renderLogColumn(width, height int) string {
	pinned := m.PinnedService()
	if pinned == nil {
		return m.renderLogPanel(m.FocusedService(), width, height)
	}
	topHeight, bottomHeight := m.logColumnLayout(height)
	top := m.renderPinnedLogPanel(pinned, width, topHeight)
	if topHeight == collapsedPanelHeight {
		top = renderCollapsedPanel("[3] PINNED LOGS │ "+pinned.Name, width)
	}
	focused := m.FocusedService()
	bottom := m.renderLogPanel(focused, width, bottomHeight)
	if bottomHeight == collapsedPanelHeight {
		title := "[3] LOGS"
		if focused != nil {
			title += " │ " + focused.Name
		}
		bottom = renderCollapsedPanel(title, width)
	}
	return lipgloss.JoinVertical(lipgloss.Left, top, bottom)
}

func (m *Model) logColumnLayout(height int) (pinnedHeight, currentHeight int) {
	if height <= compactDashboardHeight-dashboardHeaderRows-dashboardFooterRows {
		if m.panelFocus == panelPinnedLogs {
			return max(collapsedPanelHeight, height-collapsedPanelHeight), collapsedPanelHeight
		}
		return collapsedPanelHeight, max(collapsedPanelHeight, height-collapsedPanelHeight)
	}
	return splitPanelHeights(height)
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
	titleStyle := m.panelTitleStyle(panelLogs)
	titlePrefix := "[3] LOGS"
	followMode, logOffset, logAnchor, logPaused := m.followMode, m.logOffset, m.logAnchor, m.logPaused
	if pinned {
		panelStyle = m.panelStyle(panelPinnedLogs)
		titleStyle = m.panelTitleStyle(panelPinnedLogs)
		titlePrefix = "[3] PINNED LOGS"
		followMode, logOffset, logAnchor, logPaused = m.pinnedFollow, m.pinnedOffset, m.pinnedAnchor, false
	}
	if svc == nil {
		return renderTitledPanel(panelStyle, titleStyle, contentWidth, contentHeight, titlePrefix, []string{"", "Select a service"})
	}

	visualState := m.serviceVisualState(svc)
	title := titlePrefix + ContextBarStyle.Render(" │ ") + serviceStatusIndicator(visualState) + " " + ServiceNameStyle.Render(svc.Name) +
		ContextBarStyle.Render(" · "+strings.ToLower(serviceStatusLabel(svc.Status(), visualState)))
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
		return renderTitledPanel(panelStyle, titleStyle, contentWidth, contentHeight, title, []string{
			"",
			ContextBarStyle.Render("Output will appear after the service starts"),
		})
	}
	if hasPattern && m.searchMode == searchFilter && len(sourceIndices) == 0 {
		return renderTitledPanel(panelStyle, titleStyle, contentWidth, contentHeight, title, []string{
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

	maxLines := contentHeight
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
	return renderTitledPanel(panelStyle, titleStyle, contentWidth, contentHeight, title, rows[startIdx:endIdx])
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
	maxLines := max(1, panelHeight-2)
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
		_, height = m.logColumnLayout(height)
	}
	return height
}

func (m *Model) pinnedLogPanelHeight() int {
	height := max(1, m.height-2)
	if m.PinnedService() == nil {
		return height
	}
	height, _ = m.logColumnLayout(height)
	return height
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
	maxLines := max(1, m.currentLogPanelHeight()-2)
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

// styleLogLine applies a semantic color without trusting child-process ANSI.
func styleLogLine(line string) string {
	timestamp, message := splitLogTimestamp(line)
	level := detectLogLevel(message)
	messageStyle := LogInfoStyle
	switch level {
	case config.LogError:
		messageStyle = LogErrorStyle
	case config.LogWarn:
		messageStyle = LogWarnStyle
	case config.LogDebug:
		messageStyle = LogDebugStyle
	}

	source, remainder := splitLogSource(message)
	styled := messageStyle.Render(message)
	if source != "" {
		sourceStyle := LogSourceStyle
		if strings.EqualFold(source, "[Kranz]") {
			sourceStyle = LogSystemStyle
			if level == config.LogInfo {
				messageStyle = LogSystemStyle
			}
		}
		styled = sourceStyle.Render(source) + messageStyle.Render(remainder)
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
