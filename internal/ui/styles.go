// Package ui contains the Kranz terminal interface.
package ui

import "github.com/charmbracelet/lipgloss"

var (
	ColorGreen      lipgloss.Color
	ColorYellow     lipgloss.Color
	ColorRed        lipgloss.Color
	ColorGrey       lipgloss.Color
	ColorCyan       lipgloss.Color
	ColorAccentText lipgloss.Color
	ColorInfo       lipgloss.Color
	ColorData       lipgloss.Color
	ColorSelection  lipgloss.Color
	ColorSelectText lipgloss.Color
	ColorBackground lipgloss.Color
	ColorDarkBg     lipgloss.Color
	ColorSurfaceAlt lipgloss.Color
	ColorBorder     lipgloss.Color
	ColorDim        lipgloss.Color
	ColorStopped    lipgloss.Color
	TerminalCanvas  bool

	HeaderStyle           lipgloss.Style
	ModalTitleStyle       lipgloss.Style
	AppStyle              lipgloss.Style
	PanelStyle            lipgloss.Style
	FocusedPanelStyle     lipgloss.Style
	NudgedPanelStyle      lipgloss.Style
	ServiceRunningStyle   lipgloss.Style
	ServiceStartingStyle  lipgloss.Style
	ServiceUnhealthyStyle lipgloss.Style
	ServiceNameStyle      lipgloss.Style
	PortStyle             lipgloss.Style
	TagStyle              lipgloss.Style
	PortWarningStyle      lipgloss.Style
	LogErrorStyle         lipgloss.Style
	LogWarnStyle          lipgloss.Style
	LogDebugStyle         lipgloss.Style
	LogInfoStyle          lipgloss.Style
	LogSystemStyle        lipgloss.Style
	LogTimestampStyle     lipgloss.Style
	LogSourceStyle        lipgloss.Style
	HelpKeyStyle          lipgloss.Style
	ModalStyle            lipgloss.Style
	NewLogIndicatorStyle  lipgloss.Style
	SearchHighlightStyle  lipgloss.Style
	SearchInputStyle      lipgloss.Style
	PanelTitleStyle       lipgloss.Style
	FocusedTitleStyle     lipgloss.Style
	DetailLabelStyle      lipgloss.Style
	SelectionStyle        lipgloss.Style
	ContextBarStyle       lipgloss.Style
	PrimaryButtonStyle    lipgloss.Style
	SecondaryButtonStyle  lipgloss.Style
	WarningButtonStyle    lipgloss.Style
	DangerButtonStyle     lipgloss.Style
	DisabledButtonStyle   lipgloss.Style
	RunningBadgeStyle     lipgloss.Style
	StartingBadgeStyle    lipgloss.Style
	StoppedBadgeStyle     lipgloss.Style
	FailedBadgeStyle      lipgloss.Style
)

func applyPalette(theme Theme) {
	ColorGreen = lipgloss.Color(theme.Green)
	ColorYellow = lipgloss.Color(theme.Yellow)
	ColorRed = lipgloss.Color(theme.Red)
	ColorGrey = lipgloss.Color(theme.Text)
	// Keep the configured accent intact for persistence and use AccentText in
	// foreground-only roles such as borders. normalizeTheme decides what that
	// is: an entered accent verbatim, a theme's own accent with a contrast
	// floor.
	ColorCyan = lipgloss.Color(theme.AccentText)
	ColorAccentText = lipgloss.Color(theme.AccentText)
	ColorInfo = lipgloss.Color(theme.Info)
	ColorData = lipgloss.Color(theme.Data)
	ColorSelection = lipgloss.Color(theme.Selection)
	ColorSelectText = lipgloss.Color(theme.SelectText)
	ColorBackground = lipgloss.Color(theme.Background)
	ColorDarkBg = lipgloss.Color(theme.Surface)
	ColorSurfaceAlt = lipgloss.Color(theme.SurfaceAlt)
	ColorBorder = lipgloss.Color(theme.Border)
	ColorDim = lipgloss.Color(theme.Muted)
	stopped := "#94A3B8"
	if background, ok := parseHex(theme.Background); ok && relativeLuminance(background) > 0.45 {
		stopped = "#64748B"
	}
	ColorStopped = lipgloss.Color(stopped)
	TerminalCanvas = theme.TerminalCanvas

	AppStyle = lipgloss.NewStyle().Foreground(ColorGrey)
	HeaderStyle = lipgloss.NewStyle().Foreground(ColorAccentText).Bold(true).Padding(0, 1)
	ModalTitleStyle = lipgloss.NewStyle().Foreground(ColorAccentText).Bold(true).Padding(0, 1)
	PanelStyle = lipgloss.NewStyle().Foreground(ColorGrey).Border(lipgloss.RoundedBorder()).BorderForeground(ColorBorder)
	if !TerminalCanvas {
		AppStyle = AppStyle.Background(ColorBackground)
		HeaderStyle = HeaderStyle.Background(ColorBackground)
		// Border cells are painted only by BorderBackground; Background alone
		// leaves them transparent and the canvas shows through. For panels that
		// is invisible today because normalizeTheme keeps Surface equal to
		// Background, but stating it keeps the panel one solid card if those
		// ever diverge.
		PanelStyle = PanelStyle.Background(ColorDarkBg).BorderBackground(ColorDarkBg)
	}
	FocusedPanelStyle = PanelStyle.BorderForeground(ColorCyan)
	// Border colour only: a text attribute here would propagate to the whole
	// panel body and make the logs jump on every blink phase.
	NudgedPanelStyle = PanelStyle.BorderForeground(ColorYellow)
	ServiceRunningStyle = lipgloss.NewStyle().Foreground(ColorGreen)
	ServiceStartingStyle = lipgloss.NewStyle().Foreground(ColorYellow)
	ServiceUnhealthyStyle = lipgloss.NewStyle().Foreground(ColorRed)
	ServiceNameStyle = lipgloss.NewStyle().Foreground(ColorGrey).Bold(true)
	PortStyle = lipgloss.NewStyle().Foreground(ColorInfo)
	TagStyle = lipgloss.NewStyle().Foreground(ColorDim)
	PortWarningStyle = lipgloss.NewStyle().Foreground(ColorRed).Bold(true)
	LogErrorStyle = lipgloss.NewStyle().Foreground(ColorRed)
	LogWarnStyle = lipgloss.NewStyle().Foreground(ColorYellow)
	LogDebugStyle = lipgloss.NewStyle().Foreground(ColorDim)
	LogInfoStyle = lipgloss.NewStyle().Foreground(ColorGrey)
	LogSystemStyle = lipgloss.NewStyle().Foreground(ColorInfo).Bold(true)
	LogTimestampStyle = lipgloss.NewStyle().Foreground(ColorDim)
	LogSourceStyle = lipgloss.NewStyle().Foreground(ColorDim)
	HelpKeyStyle = lipgloss.NewStyle().Foreground(ColorInfo).Bold(true)
	ModalStyle = lipgloss.NewStyle().Foreground(ColorGrey).Border(lipgloss.RoundedBorder()).BorderForeground(ColorCyan).Padding(1, 2)
	NewLogIndicatorStyle = lipgloss.NewStyle().Foreground(ColorYellow).Bold(true)
	SearchHighlightStyle = lipgloss.NewStyle().Background(ColorInfo).Foreground(lipgloss.Color(ensureContrast(theme.Surface, theme.Info, 4.5))).Bold(true)
	SearchInputStyle = lipgloss.NewStyle().Foreground(ColorInfo)
	PanelTitleStyle = lipgloss.NewStyle().Foreground(ColorGrey).Background(ColorSurfaceAlt).Bold(true)
	focusedTitleBackground := focusedPanelTitleBackground(theme)
	FocusedTitleStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ensureContrast(theme.Text, focusedTitleBackground, 4.5))).
		Background(lipgloss.Color(focusedTitleBackground)).
		Bold(true)
	DetailLabelStyle = lipgloss.NewStyle().Foreground(ColorDim).Bold(true)
	SelectionStyle = lipgloss.NewStyle().Foreground(ColorSelectText).Background(ColorSelection).Bold(true)
	ContextBarStyle = lipgloss.NewStyle().Foreground(ColorDim)
	PrimaryButtonStyle = lipgloss.NewStyle().Foreground(ColorAccentText).Bold(true).Padding(0, 1)
	SecondaryButtonStyle = lipgloss.NewStyle().Foreground(ColorInfo).Padding(0, 1)
	WarningButtonStyle = lipgloss.NewStyle().Foreground(ColorData).Padding(0, 1)
	DangerButtonStyle = lipgloss.NewStyle().Foreground(ColorRed).Padding(0, 1)
	DisabledButtonStyle = lipgloss.NewStyle().Foreground(ColorDim).Padding(0, 1)
	RunningBadgeStyle = lipgloss.NewStyle().Foreground(ColorGreen).Bold(true)
	StartingBadgeStyle = lipgloss.NewStyle().Foreground(ColorYellow).Bold(true)
	StoppedBadgeStyle = lipgloss.NewStyle().Foreground(ColorStopped)
	FailedBadgeStyle = lipgloss.NewStyle().Foreground(ColorRed).Bold(true)
	if !TerminalCanvas {
		// Modals and confirmations sit on SurfaceAlt, a colour the canvas does
		// not share, so an unpainted border leaves a one-cell seam of canvas
		// around every dialog.
		ModalStyle = ModalStyle.Background(ColorSurfaceAlt).BorderBackground(ColorSurfaceAlt)
		ModalTitleStyle = ModalTitleStyle.Background(ColorSurfaceAlt)
	}
}

func focusedPanelTitleBackground(theme Theme) string {
	blend := 0.16
	if background, ok := parseHex(theme.Background); ok && relativeLuminance(background) > 0.45 {
		blend = 0.10
	}
	return mixHex(theme.SurfaceAlt, theme.Accent, blend)
}
