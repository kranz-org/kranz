package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

// RuntimePicker is the standalone bootstrap screen Kranz shows when it is
// launched outside a project directory (no configuration was discovered)
// but local runtimes already exist (PRD Scenario C / 3.1). It is a small,
// separate Bubble Tea program rather than the dashboard Model tolerating a
// nil current session: it reuses the exact discovery, sorting, and row
// rendering the in-dashboard switcher uses, then hands its selection back to
// the executable, which attaches the ordinary way and starts the dashboard.
type RuntimePicker struct {
	registry      *kranzruntime.Registry
	clientVersion string

	rows       []runtimeRow
	cursor     int
	loading    bool
	errText    string
	generation uint64

	width, height int
	ready         bool

	selected *kranzruntime.SessionRecord

	lastClickID string
	lastClickAt time.Time
}

// NewRuntimePicker constructs the picker. registry must not be nil.
func NewRuntimePicker(registry *kranzruntime.Registry, clientVersion string) *RuntimePicker {
	return &RuntimePicker{registry: registry, clientVersion: clientVersion, loading: true}
}

// Selected returns the runtime the user picked, if the program ended that
// way. It returns false if the user quit without choosing one.
func (p *RuntimePicker) Selected() (kranzruntime.SessionRecord, bool) {
	if p.selected == nil {
		return kranzruntime.SessionRecord{}, false
	}
	return *p.selected, true
}

type runtimePickerTickMsg struct{}

func (p *RuntimePicker) Init() tea.Cmd {
	return p.refresh()
}

func (p *RuntimePicker) refresh() tea.Cmd {
	generation := p.generation
	registry := p.registry
	clientVersion := p.clientVersion
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeDiscoveryTimeout)
		defer cancel()
		rows, err := discoverRuntimeRows(ctx, registry, clientVersion, "")
		return runtimeListMsg{generation: generation, rows: rows, err: err}
	}
}

func (p *RuntimePicker) tick() tea.Cmd {
	return tea.Tick(switcherRefreshInterval, func(time.Time) tea.Msg { return runtimePickerTickMsg{} })
}

func (p *RuntimePicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height, p.ready = msg.Width, msg.Height, true
		return p, nil
	case runtimePickerTickMsg:
		return p, p.refresh()
	case runtimeListMsg:
		if msg.generation != p.generation {
			return p, nil
		}
		p.loading = false
		if msg.err != nil {
			p.errText = msg.err.Error()
			return p, p.tick()
		}
		p.errText = ""
		previousID := ""
		if p.cursor >= 0 && p.cursor < len(p.rows) {
			previousID = p.rows[p.cursor].Record.ID
		}
		p.rows = msg.rows
		p.cursor = -1
		for index, row := range p.rows {
			if row.Record.ID == previousID {
				p.cursor = index
				break
			}
		}
		if p.cursor < 0 {
			for index, row := range p.rows {
				if row.Selectable {
					p.cursor = index
					break
				}
			}
		}
		if p.cursor < 0 && len(p.rows) > 0 {
			p.cursor = 0
		}
		return p, p.tick()
	case tea.KeyMsg:
		return p.handleKey(msg)
	case tea.MouseMsg:
		return p.handleMouse(msg)
	}
	return p, nil
}

func (p *RuntimePicker) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	msg = normalizeShortcutKey(msg)
	switch msg.String() {
	case "up", "k":
		if len(p.rows) > 0 {
			p.cursor = max(0, p.cursor-1)
		}
	case "down", "j":
		if len(p.rows) > 0 {
			p.cursor = min(len(p.rows)-1, p.cursor+1)
		}
	case "enter":
		return p.selectCurrent()
	case "esc", "q", "ctrl+c":
		return p, tea.Quit
	}
	return p, nil
}

func (p *RuntimePicker) selectCurrent() (tea.Model, tea.Cmd) {
	if p.cursor < 0 || p.cursor >= len(p.rows) || !p.rows[p.cursor].Selectable {
		return p, nil
	}
	record := p.rows[p.cursor].Record
	p.selected = &record
	return p, tea.Quit
}

func (p *RuntimePicker) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if len(p.rows) > 0 {
			p.cursor = max(0, p.cursor-1)
		}
		return p, nil
	case tea.MouseButtonWheelDown:
		if len(p.rows) > 0 {
			p.cursor = min(len(p.rows)-1, p.cursor+1)
		}
		return p, nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return p, nil
	}
	rendered := p.View()
	for index, row := range p.rows {
		if !renderedTextRegionHit(rendered, msg.X, msg.Y, runtimeRowHitLabel(row), 2, p.width) {
			continue
		}
		p.cursor = index
		now := time.Now()
		if p.lastClickID == row.Record.ID && !p.lastClickAt.IsZero() && now.Sub(p.lastClickAt) >= 0 && now.Sub(p.lastClickAt) <= doubleClickInterval {
			p.lastClickID, p.lastClickAt = "", time.Time{}
			return p.selectCurrent()
		}
		p.lastClickID, p.lastClickAt = row.Record.ID, now
		return p, nil
	}
	if renderedTextHit(rendered, msg.X, msg.Y, "[q] Quit") {
		return p, tea.Quit
	}
	return p, nil
}

func (p *RuntimePicker) View() string {
	if !p.ready {
		return ""
	}
	contentWidth := flushModalContentWidth(p.width, 110)
	shortcutRows := renderModalShortcutRows([]string{"[↑/↓ · j/k] Select", "[Enter] Connect", "[q] Quit"}, contentWidth, lipgloss.NewStyle().Foreground(ColorDim))
	lines := []string{ModalTitleStyle.Render(" Kranz "), ""}
	lines = append(lines, modalTextRows("No Kranz configuration was found in this directory.", contentWidth)...)
	lines = append(lines, modalTextRows("Choose a local runtime to attach to, or press q to quit.", contentWidth)...)
	lines = append(lines, "")
	if p.errText != "" {
		for _, line := range modalTextRows(p.errText, contentWidth) {
			lines = append(lines, ContextBarStyle.Render(line))
		}
		lines = append(lines, "")
	} else if p.loading && len(p.rows) == 0 {
		lines = append(lines, modalTextRows("Discovering local runtimes…", contentWidth)...)
		lines = append(lines, "")
	}
	rowLines := renderRuntimeRowLines(p.rows, p.cursor,
		capacityForHeight(p.height, len(lines)+max(0, len(shortcutRows)-1)), contentWidth)
	if len(rowLines) == 0 && p.errText == "" && !p.loading {
		lines = append(lines, modalTextRows("No local runtimes are registered", contentWidth)...)
	} else {
		lines = append(lines, rowLines...)
	}
	lines = append(lines, "")
	lines = append(lines, shortcutRows...)
	// The picker has no dashboard to overlay, so it centres the same modal on
	// an empty canvas instead of calling placeOverlay. The chrome, the row
	// rendering, and the shortcut strip are the ones the in-dashboard
	// switcher uses, so the two screens are recognisably the same modal.
	modal := renderFlushModal(strings.Join(lines, "\n"))
	centred := lipgloss.Place(p.width, p.height, lipgloss.Center, lipgloss.Center, modal)
	return frameToTerminal(centred, p.width, p.height)
}
