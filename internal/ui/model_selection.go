package ui

import (
	"sort"
	"strings"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

// Cursor, panel focus, and what is selected. Tags expand into their services
// here, so a tag can be acted on as a single target.

func (m *Model) markFocusedRead() {
	if svc := m.FocusedService(); svc != nil {
		svc.ResetNewLogCount()
	}
	if svc := m.PinnedService(); svc != nil {
		svc.ResetNewLogCount()
	}
}

func (m *Model) moveFocus(next int) {
	if current := m.FocusedService(); current != nil {
		current.ResetNewLogCount()
	}
	m.focused = next
	m.focusedAction = nil
	m.focusedActionGroup = ""
	m.detailOffset = 0
	m.logOffset = 0
	m.logAnchor = 0
	m.followMode = true
	m.logPaused = false
	m.markFocusedRead()
	m.portService = ""
	m.portDetails = make(map[int]*config.PortInfo)
	m.portError = nil
	m.portChecked = time.Time{}
	m.portScanBusy = false
}

func (m *Model) cyclePanelFocus(direction int) {
	panels := []panelFocus{panelServices, panelDetails, panelLogs}
	if m.PinnedService() != nil {
		panels = append(panels, panelPinnedLogs)
	}
	current := 0
	for index, panel := range panels {
		if panel == m.panelFocus {
			current = index
			break
		}
	}
	next := (current + direction + len(panels)) % len(panels)
	m.panelFocus = panels[next]
}

func (m *Model) movePanelCursor(direction int) {
	switch m.panelFocus {
	case panelDetails:
		m.scrollDetails(direction)
	case panelLogs:
		m.scrollLogs(direction)
	case panelPinnedLogs:
		m.scrollLogs(direction)
	default:
		if m.listMode == listTags {
			rows := m.tagRows()
			next := min(max(0, len(rows)-1), max(0, m.tagCursor+direction))
			if next != m.tagCursor {
				m.focusTagRow(next)
			}
			return
		}
		m.moveServiceListCursor(direction)
	}
}

func (m *Model) toggleAllSelection() {
	allSelected := len(m.allServices) > 0 && len(m.selected) == len(m.allServices)
	if allSelected {
		for _, svc := range m.allServices {
			if !m.selected[svc.Name] {
				allSelected = false
				break
			}
		}
	}
	m.selectedTags = nil
	m.selected = make(map[string]bool, len(m.allServices))
	if !allSelected {
		for _, svc := range m.allServices {
			m.selected[svc.Name] = true
		}
	}
}

func (m *Model) togglePinnedLog() {
	if m.focusedAction != nil || m.focusedActionGroup != "" {
		return
	}
	if m.listMode == listTags && m.focusedTagService() == nil {
		return
	}
	svc := m.FocusedService()
	if svc == nil {
		return
	}
	if m.pinnedLog == svc.Name {
		m.pinnedLog = ""
		m.pinnedOffset, m.pinnedAnchor, m.pinnedFollow = 0, 0, true
		if m.panelFocus == panelPinnedLogs {
			m.panelFocus = panelLogs
		}
		m.addNotification("logs", "Pinned log closed", config.LogInfo)
		return
	}
	m.pinnedLog = svc.Name
	m.pinnedOffset, m.pinnedAnchor, m.pinnedFollow = 0, 0, true
	svc.ResetNewLogCount()
	m.addNotification("logs", "Pinned logs: "+svc.Name, config.LogInfo)
}

func (m *Model) toggleListMode() {
	if m.listMode == listServices {
		m.listMode = listTags
		m.focusedAction = nil
		m.focusedActionGroup = ""
	} else {
		m.listMode = listServices
	}
	m.detailOffset = 0
}

func (m *Model) toggleFocusedSelection() {
	svc := m.FocusedService()
	if svc == nil {
		return
	}
	m.selectedTags = nil
	if m.selected[svc.Name] {
		delete(m.selected, svc.Name)
	} else {
		m.selected[svc.Name] = true
	}
}

func (m *Model) toggleCurrentSelection() {
	if m.listMode == listTags {
		row, ok := m.focusedTagRow()
		if !ok {
			return
		}
		if row.Service != nil {
			m.selectedTags = nil
			if m.selected[row.Service.Name] {
				delete(m.selected, row.Service.Name)
			} else {
				m.selected[row.Service.Name] = true
			}
		} else {
			m.selectedTags = toggleTag(m.selectedTags, row.Tag)
			m.syncSelectedServicesFromTags()
		}
		return
	}
	if m.focusedAction != nil || m.focusedActionGroup != "" {
		return
	}
	m.toggleFocusedSelection()
}

func (m *Model) syncSelectedServicesFromTags() {
	m.selected = make(map[string]bool)
	if len(m.selectedTags) == 0 {
		return
	}
	for _, name := range m.cfg.GetServicesByTags(m.selectedTags) {
		m.selected[name] = true
	}
}

func (m *Model) currentTags() []string {
	tags := m.cfg.GetAllTags()
	sort.Strings(tags)
	return tags
}

func (m *Model) focusedTag() string {
	row, ok := m.focusedTagRow()
	if !ok {
		return ""
	}
	return row.Tag
}

func (m *Model) servicesForTag(tag string) []*service.Service {
	if tag == "" {
		return nil
	}
	names := make(map[string]bool)
	for _, name := range m.cfg.GetServicesByTags([]string{tag}) {
		names[name] = true
	}
	services := make([]*service.Service, 0, len(names))
	for _, svc := range m.allServices {
		if names[svc.Name] {
			services = append(services, svc)
		}
	}
	return services
}

func (m *Model) tagRows() []tagListRow {
	tags := m.currentTags()
	rows := make([]tagListRow, 0, len(tags))
	for _, tag := range tags {
		rows = append(rows, tagListRow{Tag: tag})
		if m.expandedTags[tag] {
			for _, svc := range m.servicesForTag(tag) {
				rows = append(rows, tagListRow{Tag: tag, Service: svc})
			}
		}
	}
	return rows
}

func (m *Model) focusedTagRow() (tagListRow, bool) {
	rows := m.tagRows()
	if m.tagCursor < 0 || m.tagCursor >= len(rows) {
		return tagListRow{}, false
	}
	return rows[m.tagCursor], true
}

func (m *Model) focusedTagService() *service.Service {
	row, ok := m.focusedTagRow()
	if !ok {
		return nil
	}
	return row.Service
}

func (m *Model) focusTagRow(index int) {
	rows := m.tagRows()
	if index < 0 || index >= len(rows) {
		return
	}
	m.tagCursor = index
	m.detailOffset = 0
	if rows[index].Service == nil {
		return
	}
	for serviceIndex, svc := range m.services {
		if svc.Name == rows[index].Service.Name && serviceIndex != m.focused {
			m.moveFocus(serviceIndex)
			return
		}
	}
}

func (m *Model) toggleFocusedTagExpansion() bool {
	row, ok := m.focusedTagRow()
	if !ok || row.Service != nil {
		return false
	}
	if m.expandedTags == nil {
		m.expandedTags = make(map[string]bool)
	}
	m.expandedTags[row.Tag] = !m.expandedTags[row.Tag]
	m.detailOffset = 0
	return true
}

func toggleTag(tags []string, tag string) []string {
	for i, current := range tags {
		if strings.EqualFold(current, tag) {
			return append(tags[:i], tags[i+1:]...)
		}
	}
	return append(tags, tag)
}
