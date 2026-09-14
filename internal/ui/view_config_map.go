package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kranz-org/kranz/internal/sourceview"
)

// Configuration map: a read-only provenance view of the effective
// configuration. It answers two questions the dashboard cannot: which files
// were resolved into the metaconfig and in what order, and which file defined or
// last overrode each service. Everything comes from config.SourceMap, the same
// projection the CLI renders, so the two surfaces cannot disagree about names,
// ordering, or attribution. Opening the modal never reads a file and never
// mutates one.

// configMapView selects which direction the map is read from.
type configMapView uint8

const (
	// configMapBySource lists the resolved sources in deterministic order with
	// the services each one defined and the writes attributed to that source.
	configMapBySource configMapView = iota
	// configMapByService lists each effective service with the file that
	// defined it and every later override, for "why does this service behave
	// like that?".
	configMapByService
)

const (
	// configMapMaxBodyHeight caps the windowed body so a very tall terminal
	// does not turn the map into a wall of text.
	configMapMaxBodyHeight = 24
	// configMapFooterWrapRows reserves one row for the footer when its counter
	// and view toggle push a group onto a second line.
	configMapFooterWrapRows = 1
)

// openConfigMap opens the provenance map in its default direction and scroll
// position. It is the only entry point, so reopening always starts at the top.
func (m *Model) openConfigMap() {
	m.configMapOffset = 0
	m.configMapView = configMapBySource
	m.mode = ModeConfigMap
}

// handleConfigMapKeys drives the map. It is strictly read-only: the only
// state-changing keys are scrolling (by line, by page, or to either end),
// switching direction, and closing.
func (m *Model) handleConfigMapKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.configMapOffset = max(0, m.configMapOffset-1)
	case key.Matches(msg, m.keys.Down):
		m.configMapOffset = min(m.maxConfigMapOffset(), m.configMapOffset+1)
	case msg.String() == "pgup":
		m.configMapOffset = max(0, m.configMapOffset-m.configMapVisibleBodyHeight())
	case msg.String() == "pgdown" || msg.String() == " ":
		m.configMapOffset = min(m.maxConfigMapOffset(), m.configMapOffset+m.configMapVisibleBodyHeight())
	case msg.String() == "home" || msg.String() == "g":
		m.configMapOffset = 0
	case msg.String() == "end" || msg.String() == "G":
		m.configMapOffset = m.maxConfigMapOffset()
	case msg.String() == "tab":
		m.toggleConfigMapView()
	case msg.String() == "esc":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) toggleConfigMapView() {
	if m.configMapView == configMapBySource {
		m.configMapView = configMapByService
	} else {
		m.configMapView = configMapBySource
	}
	m.configMapOffset = 0
}

// configMapContentWidth mirrors the help modal's sizing: a strip of dimmed
// dashboard stays visible on either side, and a very wide terminal keeps the
// rows readable instead of stretching them across the screen.
func (m *Model) configMapContentWidth() int {
	return max(20, min(84, m.width-16))
}

// configMapInnerWidth is the width every rendered line is padded to. The flush
// modal sizes itself from its widest line, so without this the centered modal
// would change width — and jump sideways — as the body scrolls.
func (m *Model) configMapInnerWidth() int {
	return m.configMapContentWidth() + 2
}

func (m *Model) configMapVisibleBodyHeight() int {
	// The flush modal's vertical padding plus its title, two separators, and
	// the footer; one more row is held for a wrapped footer.
	return min(configMapMaxBodyHeight, max(1, m.height-modalVerticalChrome-4-configMapFooterWrapRows))
}

func (m *Model) maxConfigMapOffset() int {
	return max(0, len(m.configMapBodyLines())-m.configMapVisibleBodyHeight())
}

// configMapBodyLines renders the currently selected direction without any
// windowing, so the offset can be clamped against the true length. The layout
// is internal/sourceview's, shared with `kranz config sources`.
func (m *Model) configMapBodyLines() []string {
	sourceMap := m.cfg.SourceMap()
	options := sourceview.Options{
		Width:    m.configMapContentWidth(),
		Styles:   configMapStyles(),
		Disabled: func(name string) bool { return m.cfg.Services[name].Disabled },
	}
	if m.configMapView == configMapByService {
		return sourceview.ByService(sourceMap, options)
	}
	return sourceview.BySource(sourceMap, options)
}

// renderConfigMapView composites the scrollable provenance map over a dimmed
// dashboard. Scrolling never resizes the centered modal: configMapContent
// always emits the same number of lines, each padded to one fixed inner width.
func (m *Model) renderConfigMapView() string {
	return m.placeOverlay(m.configMapContent())
}

// configMapContent is the modal before it is centered: a fixed-height window
// over the body, every line padded to the same width.
func (m *Model) configMapContent() string {
	body := m.configMapBodyLines()
	visibleHeight := m.configMapVisibleBodyHeight()
	maxOffset := max(0, len(body)-visibleHeight)
	offset := min(maxOffset, max(0, m.configMapOffset))
	end := min(len(body), offset+visibleHeight)
	width := m.configMapInnerWidth()

	lines := []string{ModalTitleStyle.Render(" Config Map "), ""}
	for row := 0; row < visibleHeight; row++ {
		line := ""
		if index := offset + row; index < len(body) {
			line = "  " + body[index]
		}
		lines = append(lines, configMapPadLine(line, width))
	}
	lines = append(lines, "")

	groups := make([]string, 0, 5)
	if maxOffset > 0 {
		groups = append(groups,
			"[↑/↓ · j/k] Scroll",
			fmt.Sprintf("%d–%d/%d", offset+1, end, len(body)),
		)
	}
	groups = append(groups, configMapToggleLabel(m.configMapView), "[Esc] Close")
	lines = append(lines, renderModalShortcutRows(groups, width, lipgloss.NewStyle().Foreground(ColorDim))...)

	for index, line := range lines {
		lines[index] = configMapPadLine(line, width)
	}
	return renderFlushModal(strings.Join(lines, "\n"))
}

func configMapToggleLabel(view configMapView) string {
	if view == configMapByService {
		return "[Tab] By source"
	}
	return "[Tab] By service"
}

// configMapPadLine pads one ANSI-styled line to the modal's fixed inner width.
func configMapPadLine(line string, width int) string {
	if pad := width - lipgloss.Width(line); pad > 0 {
		return line + strings.Repeat(" ", pad)
	}
	return line
}

// configMapStyles hands the theme to the shared layout. The closures read the
// style variables at render time, so a theme switch applies without reopening.
func configMapStyles() sourceview.Styles {
	return sourceview.Styles{
		Muted:    func(text string) string { return ContextBarStyle.Render(text) },
		Strong:   func(text string) string { return ServiceNameStyle.Render(text) },
		Label:    func(text string) string { return DetailLabelStyle.Render(text) },
		Accent:   func(text string) string { return lipgloss.NewStyle().Foreground(ColorData).Render(text) },
		Warning:  func(text string) string { return StartingBadgeStyle.Render(text) },
		Disabled: func(text string) string { return StoppedBadgeStyle.Render(text) },
	}
}
