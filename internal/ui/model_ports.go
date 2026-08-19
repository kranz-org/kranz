package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/config"
)

// Port inspection and the external-process conflict dialog. Terminating a
// listener Kranz does not own is always an explicit user action.

func (m *Model) scanFocusedPorts(force bool) tea.Cmd {
	svc := m.FocusedService()
	if svc == nil {
		return nil
	}
	if len(svc.Config.Ports) == 0 {
		m.portService = svc.Name
		m.portDetails = make(map[int]*config.PortInfo)
		m.portError = nil
		m.portChecked = time.Now()
		return nil
	}
	if m.portScanBusy && m.portService == svc.Name {
		return nil
	}
	if !force && m.portService == svc.Name && time.Since(m.portChecked) < 2*time.Second {
		return nil
	}

	m.portScanID++
	scanID := m.portScanID
	serviceName := svc.Name
	ports := append([]int(nil), svc.Config.Ports...)
	m.portService = serviceName
	m.portScanBusy = true
	application := m.app
	return func() tea.Msg {
		details, err := application.InspectPorts(ports)
		if details == nil {
			details = make(map[int]*config.PortInfo)
		}
		return portDetailsMsg{
			id: scanID, service: serviceName, details: details, err: err, checked: time.Now(),
		}
	}
}

func (m *Model) handlePortConflictKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r", "R", "enter":
		m.mode = ModeNormal
		return m.toggleSelectedServices()
	case "k", "K":
		if !m.conflictExternal {
			m.addNotification("port", "This port belongs to another Kranz service; stop that service instead", config.LogWarn)
			return m, nil
		}
		for portNumber, info := range m.conflictPorts {
			if info == nil || info.PID <= 0 {
				m.addNotification("port", "The external process PID is unavailable", config.LogError)
				return m, nil
			}
			return m, m.releaseExternalPort(portNumber, info.PID)
		}
	case "s", "S", "c", "C", "esc":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) releaseExternalPort(portNumber, expectedPID int) tea.Cmd {
	application := m.app
	return func() tea.Msg {
		alreadyFree, err := application.ReleaseExternalPort(portNumber, expectedPID)
		if err != nil {
			return releasePortResultMsg{port: portNumber, pid: expectedPID, err: err}
		}
		if alreadyFree {
			return releasePortResultMsg{port: portNumber, pid: expectedPID, alreadyFree: true}
		}
		return releasePortResultMsg{port: portNumber, pid: expectedPID}
	}
}
