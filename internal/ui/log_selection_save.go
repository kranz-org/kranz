package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/config"
)

type selectionSaveResultMsg struct {
	selection *logSelection
	err       error
}

func writeSelectionFile(path, text string, overwrite bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_TRUNC
	} // confirmed existing path only
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(text)
	return errors.Join(err, f.Close())
}
func (m *Model) handleSelectionSaveKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.logSelection
	if s.writing {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		s.saving = false
		s.overwrite = false
		m.exportInput.Blur()
		return m, nil
	case "enter":
		if !s.overwrite {
			path := strings.TrimSpace(m.exportInput.Value())
			if path == "" {
				m.addNotification("selection", "Save path cannot be empty", config.LogError)
				return m, nil
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(m.workingDirectory, path)
			}
			s.savePath = filepath.Clean(path)
		}
		path, text, overwrite := s.savePath, s.text(), s.overwrite
		s.writing = true
		return m, func() tea.Msg { return selectionSaveResultMsg{s, writeSelectionFile(path, text, overwrite)} }
	}
	if s.overwrite {
		return m, nil
	}
	var cmd tea.Cmd
	m.exportInput, cmd = m.exportInput.Update(msg)
	return m, cmd
}
func (m *Model) renderSelectionSave() string {
	s := m.logSelection
	prompt := "Save selection on this TUI client's host"
	body := m.exportInput.View()
	footer := "[Enter] Write file  [Esc] Cancel"
	if s.overwrite {
		prompt = "Replace existing file?"
		body = "The selected file already exists."
		footer = "[Enter] Overwrite  [Esc] Cancel"
	}
	if s.writing {
		footer = "Writing…"
	}
	return m.placeOverlay(renderConfirmationModal(prompt, []string{body, fmt.Sprintf("%d bytes · clean text", len(s.text()))}, footer))
}
