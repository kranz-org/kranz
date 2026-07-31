package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/health"
	kranzlog "github.com/kranz-org/kranz/internal/log"
	"github.com/kranz-org/kranz/internal/port"
	"github.com/kranz-org/kranz/internal/service"
	usersettings "github.com/kranz-org/kranz/internal/settings"
)

// ViewMode identifies the dashboard or modal currently rendered by the TUI.
type ViewMode int

const (
	ModeNormal ViewMode = iota
	ModeHealthHistory
	ModeNotifications
	ModeSearch
	ModeHelp
	ModeConfirmQuit
	ModePortConflict
	ModeConfirmRestart
	ModeConfirmClearLogs
	ModeThemes
)

type panelFocus int

const (
	panelServices panelFocus = iota + 1
	panelDetails
	panelLogs
	panelPinnedLogs
)

type listMode int

const (
	listServices listMode = iota
	listTags
)

type tagListRow struct {
	Tag     string
	Service *service.Service
}

type logSearchMode int

const (
	searchFilter logSearchMode = iota
	searchHighlight
)

type operationKind string

const (
	operationStart      operationKind = "start"
	operationStartSet   operationKind = "start-selection"
	operationForceStart operationKind = "force-start"
	operationForceStop  operationKind = "force-stop"
	operationStopAll    operationKind = "stop-all"
	operationStopSet    operationKind = "stop-selection"
	operationRestart    operationKind = "restart"
	operationRestartAll operationKind = "restart-all"
)

type operationResultMsg struct {
	id     int
	kind   operationKind
	target string
	err    error
}

type shutdownResultMsg struct{ err error }
type shellFinishedMsg struct{ err error }
type backgroundColorMsg struct {
	dark bool
	err  error
}
type systemAppearanceMsg struct {
	dark      bool
	available bool
}
type releasePortResultMsg struct {
	port        int
	pid         int
	alreadyFree bool
	err         error
}
type tickMsg time.Time

type configStamp struct {
	Modified int64
	Size     int64
}
type configReloadMsg struct {
	cfg     *config.Config
	stamps  map[string]configStamp
	err     error
	changed bool
}

type portDetailsMsg struct {
	id      int
	service string
	details map[int]*config.PortInfo
	err     error
	checked time.Time
}

// Model owns Kranz's Bubble Tea state and runtime service integrations.
type Model struct {
	cfg     *config.Config
	version string

	manager      *service.Manager
	services     []*service.Service
	allServices  []*service.Service
	focused      int
	selected     map[string]bool
	detailOffset int
	logOffset    int
	logAnchor    int
	pinnedOffset int
	pinnedAnchor int
	panelFocus   panelFocus
	listMode     listMode

	healthChecker *health.Checker
	portChecker   port.Checker
	portDetails   map[int]*config.PortInfo
	portError     error
	portService   string
	portChecked   time.Time
	portScanID    int
	portScanBusy  bool

	logSearcher  *kranzlog.Searcher
	searchInput  textinput.Model
	searchNudge  time.Time
	currentMatch int
	searchMode   logSearchMode
	pinnedLog    string

	mode       ViewMode
	width      int
	height     int
	ready      bool
	helpOffset int

	followMode   bool
	pinnedFollow bool
	logPaused    bool
	wrapLogs     bool
	showLogTime  bool
	selectedTags []string
	tagCursor    int
	expandedTags map[string]bool

	notifMu       sync.RWMutex
	notifications []config.Notification
	toastMessage  string
	toastTimer    time.Time

	confirmAction string
	confirmTarget string
	clearTarget   string
	clearPinned   bool

	conflictService  string
	conflictPorts    map[int]*config.PortInfo
	conflictOwner    string
	conflictExternal bool

	operation           string
	operationKind       operationKind
	operationID         int
	operationCancel     context.CancelFunc
	keys                KeyMap
	userSettings        usersettings.Settings
	settingsPath        string
	activeTheme         Theme
	terminalDark        bool
	backgroundProbeBusy bool
	lastBackgroundProbe time.Time
	systemAppearanceSet bool
	systemDark          bool
	themeBefore         Theme
	settingsBefore      usersettings.Settings
	themeCursor         int
	themeUseProject     bool
	themeProjectAccent  bool
	themeBackground     string
	themeColorMode      string
	themeAccentChanged  bool
	themeOriginalAccent string
	configPaths         []string
	configWatchPaths    []string
	configStamps        map[string]configStamp
	lastConfigScan      time.Time
	reloadBusy          bool
	projectExitHandled  bool

	shutdownOnce sync.Once
	shutdownErr  error
}

// ModelOptions supplies user-level preferences and their persistence path.
type ModelOptions struct {
	Settings     usersettings.Settings
	SettingsPath string
	ConfigPaths  []string
	// DarkBackground is detected by the executable. Nil keeps the historical
	// dark default for embedders and deterministic tests.
	DarkBackground *bool
}

// NewModel creates a model with default user settings and terminal detection.
func NewModel(cfg *config.Config, version string) *Model {
	return NewModelWithOptions(cfg, version, ModelOptions{})
}

// NewModelWithOptions creates a model with resolved project/user appearance.
func NewModelWithOptions(cfg *config.Config, version string, options ModelOptions) *Model {
	terminalDark := true
	if options.DarkBackground != nil {
		terminalDark = *options.DarkBackground
	}
	themeName, accent, background, colorMode := effectiveAppearance(cfg.UI, options.Settings)
	activeTheme, themeErr := applyAppearance(themeName, accent, background, colorMode, terminalDark)
	if themeErr != nil {
		activeTheme, _ = applyAppearance(DefaultTheme, "", backgroundTerminal, colorModeAuto, terminalDark)
	}
	manager := service.NewManager(cfg)
	healthChecker := health.NewChecker()
	portChecker := port.NewChecker()
	manager.SetHealthChecker(healthChecker)
	manager.SetPortChecker(portChecker)
	services := manager.Services()

	model := &Model{
		cfg:           cfg,
		version:       version,
		manager:       manager,
		services:      services,
		allServices:   services,
		healthChecker: healthChecker,
		portChecker:   portChecker,
		portDetails:   make(map[int]*config.PortInfo),
		selected:      make(map[string]bool),
		expandedTags:  make(map[string]bool),
		panelFocus:    panelServices,
		listMode:      listServices,
		logSearcher:   kranzlog.NewSearcher(),
		searchInput:   newSearchInput(),
		currentMatch:  -1,
		searchMode:    searchFilter,
		mode:          ModeNormal,
		followMode:    true,
		pinnedFollow:  true,
		keys:          DefaultKeyMap(),
		userSettings:  options.Settings,
		settingsPath:  options.SettingsPath,
		activeTheme:   activeTheme,
		terminalDark:  terminalDark,
		// The executable already performed the initial detection. Suppress the
		// focus event emitted immediately after focus reporting is enabled.
		lastBackgroundProbe: time.Now(),
		notifications:       make([]config.Notification, 0),
		conflictPorts:       make(map[int]*config.PortInfo),
		configPaths:         append([]string(nil), options.ConfigPaths...),
	}
	if len(model.configPaths) == 0 {
		model.configPaths = append([]string(nil), cfg.Paths...)
	}
	model.configWatchPaths = watchedConfigPaths(model.configPaths, cfg.WatchPaths)
	model.configStamps, _ = readConfigStamps(model.configWatchPaths)
	if themeErr != nil {
		model.addNotification("appearance", themeErr.Error()+"; using the Kranz theme", config.LogWarn)
	}
	for _, diagnostic := range cfg.Diagnostics {
		model.addNotification("config", diagnostic, config.LogWarn)
	}
	return model
}

const (
	backgroundTerminal = config.UIBackgroundTerminal
	backgroundTheme    = config.UIBackgroundTheme
	colorModeAuto      = config.UIColorModeAuto
	colorModeDark      = config.UIColorModeDark
	colorModeLight     = config.UIColorModeLight
)

func effectiveAppearance(project config.UIConfig, user usersettings.Settings) (theme, accent, background, colorMode string) {
	theme = project.Theme
	accent = project.Accent
	background = normalizeBackgroundSource(project.Background)
	colorMode = normalizeColorMode(project.ColorMode)
	if theme == "" {
		theme = DefaultTheme
	}
	if user.Theme != "" && user.Theme != "auto" {
		theme = user.Theme
		// A user-selected theme uses its own palette. The project's accent only
		// belongs to the project's theme and must not make every preview blue.
		accent = ""
	}
	if user.Accent == "theme" {
		accent = ""
	} else if user.Accent != "" && user.Accent != "auto" {
		accent = user.Accent
	}
	if user.Background != "" {
		background = normalizeBackgroundSource(user.Background)
	}
	if user.ColorMode != "" {
		colorMode = normalizeColorMode(user.ColorMode)
	}
	return theme, accent, background, colorMode
}

func normalizeBackgroundSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case backgroundTheme:
		return backgroundTheme
	default:
		return backgroundTerminal
	}
}

func normalizeColorMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case colorModeDark:
		return colorModeDark
	case colorModeLight:
		return colorModeLight
	default:
		return colorModeAuto
	}
}

func colorModeIsDark(mode string, terminalDark bool) bool {
	switch normalizeColorMode(mode) {
	case colorModeDark:
		return true
	case colorModeLight:
		return false
	default:
		return terminalDark
	}
}

func applyAppearance(name, accent, background, colorMode string, terminalDark bool) (Theme, error) {
	return ApplyThemeVariant(
		name,
		accent,
		colorModeIsDark(colorMode, terminalDark),
		normalizeBackgroundSource(background) == backgroundTerminal,
	)
}

// Init schedules service polling and the initial port inspection.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.pollServices(), m.scanFocusedPorts(true), m.pollSystemAppearance())
}

func (m *Model) pollServices() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) pollSystemAppearance() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		dark, available := detectSystemDarkMode()
		return systemAppearanceMsg{dark: dark, available: available}
	})
}

func (m *Model) probeTerminalBackground(force bool) tea.Cmd {
	if !terminalBackgroundProbeSupported() || m.backgroundProbeBusy || (!force && time.Since(m.lastBackgroundProbe) < time.Second) {
		return nil
	}
	m.backgroundProbeBusy = true
	probe := &terminalBackgroundProbe{}
	return tea.Exec(probe, func(err error) tea.Msg {
		return backgroundColorMsg{dark: probe.dark, err: err}
	})
}

// Shutdown is the idempotent cleanup boundary for every application exit path.
func (m *Model) Shutdown() error {
	m.shutdownOnce.Do(func() {
		if m.operationCancel != nil {
			m.operationCancel()
		}
		m.shutdownErr = m.manager.Shutdown()
		m.healthChecker.StopAll()
	})
	return m.shutdownErr
}

// RequestedExitCode returns the exit code requested by an availability policy.
func (m *Model) RequestedExitCode() int {
	requested, code := m.manager.ProjectExitRequested()
	if !requested {
		return 0
	}
	return code
}

// Update applies one Bubble Tea event and schedules any resulting operation.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height, m.ready = msg.Width, msg.Height, true
		m.syncSearchInputWidth()
		return m, m.scanFocusedPorts(false)
	case tea.FocusMsg:
		// Some terminals (observed with Zed's integrated terminal) drop mouse
		// tracking mode when a tab loses and regains focus, since it is only
		// ever enabled once at startup. Re-assert it defensively on every
		// focus-in so clicks keep working after switching tabs and back.
		var searchCommand tea.Cmd
		if m.mode == ModeSearch {
			m.searchInput, searchCommand = m.searchInput.Update(msg)
		}
		return m, tea.Batch(tea.EnableMouseCellMotion, m.probeTerminalBackground(false), searchCommand)
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.MouseMsg:
		return m.handleMouseMsg(msg)
	case operationResultMsg:
		return m.handleOperationResult(msg)
	case releasePortResultMsg:
		if msg.err != nil {
			m.addNotification("port", msg.err.Error(), config.LogError)
			return m, nil
		}
		message := fmt.Sprintf("Stopped external PID %d on port %d; retrying", msg.pid, msg.port)
		if msg.alreadyFree {
			message = fmt.Sprintf("Port %d is now free; retrying", msg.port)
		}
		m.addNotification("port", message, config.LogInfo)
		m.mode = ModeNormal
		return m.toggleSelectedServices()
	case shutdownResultMsg:
		if msg.err != nil {
			m.addNotification("system", "Shutdown failed: "+msg.err.Error(), config.LogError)
		}
		return m, tea.Quit
	case shellFinishedMsg:
		if msg.err != nil {
			m.addNotification("shell", "Command shell closed: "+msg.err.Error(), config.LogError)
		} else {
			m.addNotification("shell", "Returned to Kranz", config.LogInfo)
		}
		return m, tea.Batch(tea.ClearScreen, m.probeTerminalBackground(true))
	case backgroundColorMsg:
		m.backgroundProbeBusy = false
		m.lastBackgroundProbe = time.Now()
		if msg.err != nil {
			return m, nil
		}
		return m, m.applyDetectedBackground(msg.dark, "Terminal")
	case systemAppearanceMsg:
		poll := m.pollSystemAppearance()
		if !msg.available {
			return m, poll
		}
		if !m.systemAppearanceSet {
			m.systemAppearanceSet = true
			m.systemDark = msg.dark
			return m, poll
		}
		if msg.dark == m.systemDark {
			return m, poll
		}
		m.systemDark = msg.dark
		return m, tea.Batch(poll, m.applyDetectedBackground(msg.dark, "System"))
	case portDetailsMsg:
		if msg.id != m.portScanID {
			return m, nil
		}
		m.portScanBusy = false
		if svc := m.FocusedService(); svc != nil && svc.Name == msg.service {
			m.portService = msg.service
			m.portDetails = msg.details
			m.portError = msg.err
			m.portChecked = msg.checked
		}
		return m, nil
	case tickMsg:
		m.refreshServices()
		m.expireToast()
		if requested, _ := m.manager.ProjectExitRequested(); requested && !m.projectExitHandled {
			m.projectExitHandled = true
			return m.beginShutdown()
		}
		return m, tea.Batch(m.pollServices(), m.scanFocusedPorts(false), m.reloadConfig(false))
	case searchNudgeMsg:
		// Ignore a chain left over from an earlier click.
		if !time.Time(msg).Equal(m.searchNudge) {
			return m, nil
		}
		if time.Since(m.searchNudge) >= searchNudgeDuration {
			m.searchNudge = time.Time{}
			return m, nil
		}
		return m, m.scheduleSearchNudge(m.searchNudge)
	case configReloadMsg:
		return m.handleConfigReload(msg)
	default:
		// textinput emits private follow-up messages for clipboard paste and
		// cursor blinking. Feed them back to the component while the editor is
		// open instead of dropping them at the application boundary.
		if m.mode == ModeSearch {
			var command tea.Cmd
			m.searchInput, command = m.searchInput.Update(msg)
			return m, command
		}
		return m, nil
	}
}

func (m *Model) applyDetectedBackground(dark bool, source string) tea.Cmd {
	if dark == m.terminalDark {
		return nil
	}
	m.terminalDark = dark
	_, _, _, colorMode := effectiveAppearance(m.cfg.UI, m.userSettings)
	if m.mode == ModeThemes {
		colorMode = m.themeColorMode
	}
	if colorMode != colorModeAuto {
		return nil
	}
	if m.mode == ModeThemes {
		m.previewThemePicker()
	} else if err := m.applyEffectiveAppearance(); err != nil {
		m.addNotification("appearance", "Could not adapt to terminal background: "+err.Error(), config.LogError)
		return nil
	}
	mode := "light"
	if dark {
		mode = "dark"
	}
	m.addNotification("appearance", source+" appearance changed to "+mode, config.LogInfo)
	return tea.ClearScreen
}

func (m *Model) refreshServices() {
	m.allServices = m.manager.Services()
	m.services = m.allServices
	rows := m.tagRows()
	m.tagCursor = min(max(0, len(rows)-1), max(0, m.tagCursor))
	if len(m.services) == 0 {
		m.focused = 0
	} else if m.focused >= len(m.services) {
		m.focused = len(m.services) - 1
	}
	m.markFocusedRead()
}

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
	checker := m.portChecker
	return func() tea.Msg {
		details, err := checker.CheckPorts(ports)
		if details == nil {
			details = make(map[int]*config.PortInfo)
		}
		return portDetailsMsg{
			id: scanID, service: serviceName, details: details, err: err, checked: time.Now(),
		}
	}
}

func (m *Model) expireToast() {
	m.notifMu.RLock()
	expired := m.toastMessage != "" && time.Since(m.toastTimer) > 5*time.Second
	m.notifMu.RUnlock()
	if expired {
		m.notifMu.Lock()
		m.toastMessage = ""
		m.notifMu.Unlock()
	}
}

func (m *Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m.beginShutdown()
	}
	if key.Matches(msg, m.keys.Shell) {
		return m, m.openCommandShell()
	}
	// Search is a text-entry mode and must preserve the user's actual runes.
	// Everywhere else, shortcuts follow their documented physical Latin keys.
	if m.mode != ModeSearch {
		msg = normalizeShortcutKey(msg)
	}

	switch m.mode {
	case ModeNormal:
		return m.handleNormalKeys(msg)
	case ModeSearch:
		return m.handleSearchKeys(msg)
	case ModeHelp:
		return m.handleHelpKeys(msg)
	case ModeConfirmQuit:
		return m.handleConfirmQuitKeys(msg)
	case ModeConfirmRestart:
		return m.handleConfirmRestartKeys(msg)
	case ModeConfirmClearLogs:
		return m.handleConfirmClearLogsKeys(msg)
	case ModePortConflict:
		return m.handlePortConflictKeys(msg)
	case ModeThemes:
		return m.handleThemeKeys(msg)
	default:
		if msg.String() == "esc" || msg.String() == "q" {
			m.mode = ModeNormal
		}
		return m, nil
	}
}

func (m *Model) handleNormalKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Reload) {
		return m, tea.Batch(m.reloadConfig(true), m.probeTerminalBackground(true))
	}
	if m.handleNavigationKey(msg) {
		return m, nil
	}
	if model, command, handled := m.handleLifecycleKey(msg); handled {
		return model, command
	}
	if m.handleSearchNavigationKey(msg) {
		return m, nil
	}
	if m.handleViewKey(msg) {
		return m, nil
	}
	if key.Matches(msg, m.keys.Search) {
		return m, m.openSearchEditor()
	}
	if m.handleLogKey(msg) {
		return m, nil
	}
	if key.Matches(msg, m.keys.Quit) {
		if m.manager.HasRunningServices() || m.operation != "" {
			m.mode = ModeConfirmQuit
			return m, nil
		}
		return m.beginShutdown()
	}
	return m, nil
}

func (m *Model) handleNavigationKey(msg tea.KeyMsg) bool {
	switch {
	case key.Matches(msg, m.keys.FocusList):
		if m.panelFocus == panelServices {
			m.toggleListMode()
		} else {
			m.panelFocus = panelServices
		}
		return true
	case key.Matches(msg, m.keys.FocusDetails):
		m.panelFocus = panelDetails
		return true
	case key.Matches(msg, m.keys.FocusLogs):
		if m.PinnedService() != nil && m.panelFocus == panelLogs {
			m.panelFocus = panelPinnedLogs
		} else {
			m.panelFocus = panelLogs
		}
		return true
	case key.Matches(msg, m.keys.NextPanel):
		m.cyclePanelFocus(1)
		return true
	case key.Matches(msg, m.keys.PreviousPanel):
		m.cyclePanelFocus(-1)
		return true
	case key.Matches(msg, m.keys.Up):
		m.movePanelCursor(-1)
		return true
	case key.Matches(msg, m.keys.Down):
		m.movePanelCursor(1)
		return true
	case key.Matches(msg, m.keys.Left), key.Matches(msg, m.keys.Right):
		if m.panelFocus != panelServices {
			return false
		}
		m.toggleListMode()
		return true
	case key.Matches(msg, m.keys.Open):
		if m.panelFocus == panelServices && m.listMode == listTags {
			return m.toggleFocusedTagExpansion()
		}
		return false
	default:
		return false
	}
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
		next := m.focused + direction
		if next >= 0 && next < len(m.services) {
			m.moveFocus(next)
		}
	}
}

func (m *Model) handleLifecycleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch {
	case key.Matches(msg, m.keys.Select):
		m.toggleCurrentSelection()
		return m, nil, true
	case key.Matches(msg, m.keys.ForceStart):
		model, command := m.forceToggleSelectedServices()
		return model, command, true
	case key.Matches(msg, m.keys.Toggle):
		model, command := m.toggleSelectedServices()
		return model, command, true
	case key.Matches(msg, m.keys.StartAll):
		m.toggleAllSelection()
		return m, nil, true
	case key.Matches(msg, m.keys.StopAll):
		m.cancelStartOperation()
		model, command := m.beginOperation(operationStopAll, "all services", "Stopping all services", m.manager.StopAll)
		return model, command, true
	case key.Matches(msg, m.keys.Restart):
		model, command := m.restartSelectedService()
		return model, command, true
	case key.Matches(msg, m.keys.RestartAll):
		model, command := m.beginOperation(operationRestartAll, "running services", "Restarting services", m.manager.RestartAll)
		return model, command, true
	default:
		return m, nil, false
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

func (m *Model) handleViewKey(msg tea.KeyMsg) bool {
	switch {
	case key.Matches(msg, m.keys.Tags):
		m.toggleListMode()
		m.panelFocus = panelServices
		return true
	case key.Matches(msg, m.keys.ResetTags):
		m.selectedTags = nil
		return true
	case key.Matches(msg, m.keys.Health):
		m.mode = ModeHealthHistory
		return true
	case key.Matches(msg, m.keys.Notifs):
		m.mode = ModeNotifications
		return true
	case key.Matches(msg, m.keys.Help):
		m.helpOffset = 0
		m.mode = ModeHelp
		return true
	case key.Matches(msg, m.keys.PinLogs):
		m.togglePinnedLog()
		return true
	case msg.String() == "ctrl+t":
		m.openThemePicker()
		return true
	default:
		return false
	}
}

func (m *Model) reloadConfig(force bool) tea.Cmd {
	if len(m.configPaths) == 0 || m.reloadBusy || m.operation != "" {
		return nil
	}
	if !force && time.Since(m.lastConfigScan) < time.Second {
		return nil
	}
	m.lastConfigScan = time.Now()
	m.reloadBusy = true
	paths := append([]string(nil), m.configPaths...)
	watchPaths := append([]string(nil), m.configWatchPaths...)
	previous := cloneConfigStamps(m.configStamps)
	return func() tea.Msg {
		stamps, err := readConfigStamps(watchPaths)
		if err != nil {
			return configReloadMsg{stamps: stamps, err: err}
		}
		changed := force || !equalConfigStamps(previous, stamps)
		if !changed {
			return configReloadMsg{stamps: stamps}
		}
		cfg, err := config.LoadFiles(paths)
		return configReloadMsg{cfg: cfg, stamps: stamps, err: err, changed: true}
	}
}

func (m *Model) handleConfigReload(msg configReloadMsg) (tea.Model, tea.Cmd) {
	m.reloadBusy = false
	if msg.stamps != nil {
		m.configStamps = msg.stamps
	}
	if msg.err != nil {
		m.addNotification("config", "Reload failed: "+msg.err.Error(), config.LogError)
		return m, nil
	}
	if !msg.changed || msg.cfg == nil {
		return m, nil
	}
	focusedName := ""
	if svc := m.FocusedService(); svc != nil {
		focusedName = svc.Name
	}
	result, err := m.manager.ApplyConfig(msg.cfg)
	if err != nil {
		m.addNotification("config", "Reload failed: "+err.Error(), config.LogError)
		return m, nil
	}
	m.cfg = msg.cfg
	m.configWatchPaths = watchedConfigPaths(m.configPaths, msg.cfg.WatchPaths)
	if stamps, stampErr := readConfigStamps(m.configWatchPaths); stampErr == nil {
		m.configStamps = stamps
	}
	m.refreshServices()
	for index, svc := range m.services {
		if svc.Name == focusedName {
			m.focused = index
			break
		}
	}
	if m.PinnedService() == nil {
		m.pinnedLog = ""
	}
	if err := m.applyEffectiveAppearance(); err != nil {
		m.addNotification("appearance", err.Error(), config.LogWarn)
	}
	message := fmt.Sprintf("Configuration reloaded: %d added, %d removed, %d updated, %d restarted",
		len(result.Added), len(result.Removed), len(result.Updated), len(result.Restarted))
	m.addNotification("config", message, config.LogInfo)
	return m, m.scanFocusedPorts(true)
}

func readConfigStamps(paths []string) (map[string]configStamp, error) {
	result := make(map[string]configStamp, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			result[path] = configStamp{}
			continue
		}
		if err != nil {
			return result, fmt.Errorf("stat %s: %w", path, err)
		}
		result[path] = configStamp{Modified: info.ModTime().UnixNano(), Size: info.Size()}
	}
	return result, nil
}

func watchedConfigPaths(configPaths, auxiliaryPaths []string) []string {
	result := append([]string(nil), configPaths...)
	seen := make(map[string]bool, len(result)+len(auxiliaryPaths))
	for _, path := range result {
		seen[path] = true
	}
	for _, path := range auxiliaryPaths {
		if path != "" && !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}

func cloneConfigStamps(source map[string]configStamp) map[string]configStamp {
	result := make(map[string]configStamp, len(source))
	for path, stamp := range source {
		result[path] = stamp
	}
	return result
}

func equalConfigStamps(left, right map[string]configStamp) bool {
	if len(left) != len(right) {
		return false
	}
	for path, stamp := range left {
		if right[path] != stamp {
			return false
		}
	}
	return true
}

func (m *Model) handleHelpKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.helpOffset = max(0, m.helpOffset-1)
	case key.Matches(msg, m.keys.Down):
		m.helpOffset = min(m.maxHelpOffset(), m.helpOffset+1)
	case msg.String() == "esc", msg.String() == "q", msg.String() == "?":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) togglePinnedLog() {
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

func (m *Model) openThemePicker() {
	m.themeBefore = m.activeTheme
	m.settingsBefore = m.userSettings
	m.themeCursor = 0
	for index, name := range ThemeNames() {
		if name == m.activeTheme.Name {
			m.themeCursor = index
			break
		}
	}
	m.themeUseProject = m.userSettings.Theme == "" || m.userSettings.Theme == "auto"
	projectAccent := strings.TrimSpace(m.cfg.UI.Accent)
	m.themeOriginalAccent = m.userSettings.Accent
	m.themeAccentChanged = false
	m.themeProjectAccent = projectAccent != "" && m.userSettings.Accent != "theme" &&
		(m.themeUseProject || strings.EqualFold(m.userSettings.Accent, projectAccent))
	_, _, m.themeBackground, m.themeColorMode = effectiveAppearance(m.cfg.UI, m.userSettings)
	m.mode = ModeThemes
}

func (m *Model) handleThemeKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	names := ThemeNames()
	switch msg.String() {
	case "up", "k":
		m.themeCursor = (m.themeCursor - 1 + len(names)) % len(names)
		m.themeUseProject = false
		m.previewThemePicker()
	case "down", "j":
		m.themeCursor = (m.themeCursor + 1) % len(names)
		m.themeUseProject = false
		m.previewThemePicker()
	case "enter":
		m.saveThemePicker(names)
	case "c", "C":
		m.saveThemePickerToProject()
	case "p", "P":
		m.themeUseProject = !m.themeUseProject
		m.previewThemePicker()
	case "a", "A":
		m.toggleThemeAccentSource()
	case "b", "B":
		m.toggleThemeBackgroundSource()
	case "m", "M":
		m.cycleThemeColorMode()
	case "esc", "q":
		m.cancelThemePicker()
	}
	return m, nil
}

func (m *Model) saveThemePicker(names []string) {
	if m.themeUseProject {
		m.userSettings.Theme = ""
	} else {
		m.userSettings.Theme = names[m.themeCursor]
	}
	if m.themeAccentChanged {
		switch {
		case !m.themeProjectAccent:
			m.userSettings.Accent = "theme"
		case m.themeUseProject:
			m.userSettings.Accent = ""
		default:
			m.userSettings.Accent = strings.TrimSpace(m.cfg.UI.Accent)
		}
	}
	projectBackground := normalizeBackgroundSource(m.cfg.UI.Background)
	if m.themeBackground == projectBackground {
		m.userSettings.Background = ""
	} else {
		m.userSettings.Background = m.themeBackground
	}
	projectColorMode := normalizeColorMode(m.cfg.UI.ColorMode)
	if m.themeColorMode == projectColorMode {
		m.userSettings.ColorMode = ""
	} else {
		m.userSettings.ColorMode = m.themeColorMode
	}
	if err := m.persistSettings(); err != nil {
		m.addNotification("settings", err.Error(), config.LogError)
	} else {
		m.addNotification("appearance", "Appearance saved: "+m.themePickerSummary(), config.LogInfo)
	}
	m.mode = ModeNormal
}

func (m *Model) saveThemePickerToProject() {
	path := m.themeProjectConfigPath()
	if path == "" {
		m.addNotification("settings", "No project configuration path is available", config.LogError)
		return
	}
	appearance := config.UIConfig{
		Theme:      m.activeTheme.Name,
		Background: m.themeBackground,
		ColorMode:  m.themeColorMode,
	}
	if m.themeProjectAccent || (!m.themeAccentChanged && isCustomAccent(m.themeOriginalAccent, m.cfg.UI.Accent)) {
		appearance.Accent = m.activeTheme.Accent
	}
	if err := config.SaveUIAppearance(path, appearance); err != nil {
		m.addNotification("settings", err.Error(), config.LogError)
		return
	}
	// The project file is already authoritative at this point, even if clearing
	// the personal overrides below fails. Keep the in-memory config aligned with
	// disk and then reapply any overrides that could not be removed.
	m.cfg.UI = appearance

	previousSettings := m.userSettings
	m.userSettings.Theme = ""
	m.userSettings.Accent = ""
	m.userSettings.Background = ""
	m.userSettings.ColorMode = ""
	if err := m.persistSettings(); err != nil {
		m.userSettings = previousSettings
		_ = m.applyEffectiveAppearance()
		m.addNotification("settings", "Project appearance was saved, but user overrides could not be cleared: "+err.Error(), config.LogWarn)
	} else {
		m.themeUseProject = true
		m.themeProjectAccent = appearance.Accent != ""
		m.themeOriginalAccent = ""
		m.addNotification("appearance", "Project appearance saved to "+path, config.LogInfo)
	}
	m.configStamps, _ = readConfigStamps(m.configWatchPaths)
	m.mode = ModeNormal
}

func (m *Model) themeProjectConfigPath() string {
	if len(m.configPaths) == 0 {
		return ""
	}
	return m.configPaths[len(m.configPaths)-1]
}

func (m *Model) toggleThemeAccentSource() {
	if strings.TrimSpace(m.cfg.UI.Accent) == "" {
		m.addNotification("appearance", "This project does not define an accent", config.LogWarn)
		return
	}
	m.themeAccentChanged = true
	m.themeProjectAccent = !m.themeProjectAccent
	m.previewThemePicker()
}

func (m *Model) toggleThemeBackgroundSource() {
	if m.themeBackground == backgroundTerminal {
		m.themeBackground = backgroundTheme
	} else {
		m.themeBackground = backgroundTerminal
	}
	m.previewThemePicker()
}

func (m *Model) cycleThemeColorMode() {
	switch m.themeColorMode {
	case colorModeAuto:
		m.themeColorMode = colorModeDark
	case colorModeDark:
		m.themeColorMode = colorModeLight
	default:
		m.themeColorMode = colorModeAuto
	}
	m.previewThemePicker()
}

func (m *Model) cancelThemePicker() {
	m.userSettings = m.settingsBefore
	m.activeTheme = m.themeBefore
	applyPalette(m.themeBefore)
	m.mode = ModeNormal
}

func (m *Model) previewThemePicker() {
	name := ThemeNames()[m.themeCursor]
	if m.themeUseProject {
		name = m.cfg.UI.Theme
		if name == "" {
			name = DefaultTheme
		}
	}
	accent := ""
	if !m.themeAccentChanged && isCustomAccent(m.themeOriginalAccent, m.cfg.UI.Accent) {
		accent = m.themeOriginalAccent
	} else if m.themeProjectAccent {
		accent = strings.TrimSpace(m.cfg.UI.Accent)
	}
	theme, err := applyAppearance(name, accent, m.themeBackground, m.themeColorMode, m.terminalDark)
	if err == nil {
		m.activeTheme = theme
	}
}

func isCustomAccent(accent, projectAccent string) bool {
	accent = strings.TrimSpace(accent)
	return accent != "" && accent != "auto" && accent != "theme" && !strings.EqualFold(accent, strings.TrimSpace(projectAccent))
}

func (m *Model) themePickerSummary() string {
	theme := "Selected · " + ThemeNames()[m.themeCursor]
	if m.themeUseProject {
		theme = "Project · " + m.activeTheme.Name
	}
	accent := "Theme"
	if m.themeProjectAccent {
		accent = "Project"
	}
	background := "Terminal"
	if m.themeBackground == backgroundTheme {
		background = "Theme"
	}
	return theme + " / " + accent + " accent / " + background + " background / " + strings.ToUpper(m.themeColorMode)
}

func (m *Model) applyEffectiveAppearance() error {
	name, accent, background, colorMode := effectiveAppearance(m.cfg.UI, m.userSettings)
	theme, err := applyAppearance(name, accent, background, colorMode, m.terminalDark)
	if err != nil {
		return err
	}
	m.activeTheme = theme
	return nil
}

func (m *Model) persistSettings() error {
	if m.settingsPath == "" {
		return nil
	}
	return usersettings.Save(m.settingsPath, m.userSettings)
}

func (m *Model) toggleListMode() {
	if m.listMode == listServices {
		m.listMode = listTags
	} else {
		m.listMode = listServices
	}
	m.detailOffset = 0
}

func (m *Model) handleLogKey(msg tea.KeyMsg) bool {
	switch {
	case key.Matches(msg, m.keys.ClearSearch):
		// Esc is the second step out of search: the editor exit keeps the
		// filter, and this drops it. Without a pattern the key stays inert.
		if m.logSearcher == nil || !m.logSearcher.HasPattern() {
			return false
		}
		m.clearSearch()
		return true
	case key.Matches(msg, m.keys.WrapLogs):
		m.wrapLogs = !m.wrapLogs
		m.logOffset = 0
		m.logAnchor = 0
		m.followMode = true
		m.pinnedOffset = 0
		m.pinnedAnchor = 0
		m.pinnedFollow = true
		m.logPaused = false
		state := "disabled"
		if m.wrapLogs {
			state = "enabled"
		}
		m.addNotification("logs", "Line wrapping "+state, config.LogInfo)
		return true
	case key.Matches(msg, m.keys.LogTime):
		m.showLogTime = !m.showLogTime
		m.logOffset = 0
		m.logAnchor = 0
		m.followMode = true
		m.pinnedOffset = 0
		m.pinnedAnchor = 0
		m.pinnedFollow = true
		m.logPaused = false
		state := "hidden"
		if m.showLogTime {
			state = "shown"
		}
		m.addNotification("logs", "Log timestamps "+state, config.LogInfo)
		return true
	case key.Matches(msg, m.keys.Freeze):
		if !m.followMode {
			m.followMode = true
			m.logPaused = false
			m.logOffset = 0
			m.logAnchor = 0
		} else {
			m.followMode = false
			m.logPaused = true
			m.logAnchor = m.displayedLogLineCount()
		}
		return true
	case key.Matches(msg, m.keys.Clear):
		return m.beginClearLogs()
	default:
		return false
	}
}

func (m *Model) beginClearLogs() bool {
	var svc *service.Service
	switch m.panelFocus {
	case panelLogs:
		svc = m.FocusedService()
	case panelPinnedLogs:
		svc = m.PinnedService()
	default:
		return false
	}
	if svc == nil {
		return false
	}
	m.mode = ModeConfirmClearLogs
	m.clearTarget = svc.Name
	m.clearPinned = m.panelFocus == panelPinnedLogs
	return true
}

func (m *Model) clearConfirmedLogs() {
	svc, ok := m.manager.GetService(m.clearTarget)
	if ok {
		svc.ClearLogs()
		svc.ResetNewLogCount()
		if focused := m.FocusedService(); focused != nil && focused.Name == svc.Name {
			m.logOffset, m.logAnchor, m.followMode, m.logPaused = 0, 0, true, false
			m.currentMatch = -1
		}
		if pinned := m.PinnedService(); pinned != nil && pinned.Name == svc.Name {
			m.pinnedOffset, m.pinnedAnchor, m.pinnedFollow = 0, 0, true
		}
		m.addNotification(svc.Name, "Logs cleared", config.LogInfo)
	}
	m.clearTarget = ""
	m.clearPinned = false
	m.mode = ModeNormal
}

func (m *Model) handleSearchNavigationKey(msg tea.KeyMsg) bool {
	if m.panelFocus != panelLogs || m.searchMode != searchHighlight || m.logSearcher == nil || !m.logSearcher.HasPattern() {
		return false
	}
	svc := m.FocusedService()
	if svc == nil {
		return false
	}
	switch msg.String() {
	case "n":
		m.currentMatch = m.logSearcher.FindNext(serviceLogLines(svc), m.currentMatch)
		m.focusLogMatch(m.currentMatch)
		return true
	case "N":
		m.currentMatch = m.logSearcher.FindPrev(serviceLogLines(svc), m.currentMatch)
		m.focusLogMatch(m.currentMatch)
		return true
	default:
		return false
	}
}

func (m *Model) triggerAction(action string) (tea.Model, tea.Cmd) {
	switch action {
	case "toggle":
		return m.toggleSelectedServices()
	case "force":
		return m.forceToggleSelectedServices()
	case "select":
		m.toggleFocusedSelection()
		return m, nil
	case "restart":
		return m.restartSelectedService()
	case "all":
		m.toggleAllSelection()
		return m, nil
	case "quit":
		if m.manager.HasRunningServices() || m.operation != "" {
			m.mode = ModeConfirmQuit
			return m, nil
		}
		return m.beginShutdown()
	default:
		return m, nil
	}
}

func (m *Model) startSelectedService() (tea.Model, tea.Cmd) {
	svc := m.FocusedService()
	if svc == nil {
		return m, nil
	}
	if svc.Status() != config.StatusStopped {
		m.addNotification(svc.Name, "Service is already running. Press s to stop it.", config.LogInfo)
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return m.beginCancelableOperation(operationStart, svc.Name, "Starting "+svc.Name, cancel, func() error {
		return m.manager.StartServicesContext(ctx, []string{svc.Name})
	})
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

func (m *Model) selectedTargetNames() []string {
	selectedTags := m.selectedTags
	if len(selectedTags) == 0 && len(m.selected) == 0 && m.listMode == listTags {
		if row, ok := m.focusedTagRow(); ok {
			if row.Service != nil {
				return []string{row.Service.Name}
			}
			selectedTags = []string{row.Tag}
		}
	}
	if len(selectedTags) > 0 {
		matches := make(map[string]bool)
		for _, name := range m.cfg.GetServicesByTags(selectedTags) {
			matches[name] = true
		}
		names := make([]string, 0, len(matches))
		for _, svc := range m.allServices {
			if matches[svc.Name] {
				names = append(names, svc.Name)
			}
		}
		return names
	}
	if len(m.selected) == 0 {
		if svc := m.FocusedService(); svc != nil {
			return []string{svc.Name}
		}
		return nil
	}
	names := make([]string, 0, len(m.selected))
	for _, svc := range m.allServices {
		if m.selected[svc.Name] {
			names = append(names, svc.Name)
		}
	}
	return names
}

func (m *Model) selectedTargetLabel(names []string) string {
	if len(m.selectedTags) == 1 {
		return "tag " + m.selectedTags[0]
	}
	if len(m.selectedTags) > 1 {
		return fmt.Sprintf("%d selected tags", len(m.selectedTags))
	}
	if m.listMode == listTags && len(m.selectedTags) == 0 && len(m.selected) == 0 {
		if row, ok := m.focusedTagRow(); ok {
			if row.Service != nil {
				return row.Service.Name
			}
			return "tag " + row.Tag
		}
	}
	if len(names) > 1 {
		return fmt.Sprintf("%d selected services", len(names))
	}
	return names[0]
}

func (m *Model) toggleSelectedServices() (tea.Model, tea.Cmd) {
	names := m.selectedTargetNames()
	if len(names) == 0 {
		return m, nil
	}

	allActive := true
	for _, name := range names {
		svc, ok := m.manager.GetService(name)
		if !ok || !serviceStartPlanned(svc) {
			allActive = false
			break
		}
	}
	target := m.selectedTargetLabel(names)
	if allActive {
		m.cancelStartOperation()
		return m.beginOperation(operationStopSet, target, "Stopping "+target, func() error {
			return m.manager.StopServices(names)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	return m.beginCancelableOperation(operationStartSet, target, "Starting "+target, cancel, func() error {
		return m.manager.StartServicesContext(ctx, names)
	})
}

func serviceStartPlanned(svc *service.Service) bool {
	return svc.Status() != config.StatusStopped || svc.DesiredRunning()
}

func (m *Model) forceToggleSelectedServices() (tea.Model, tea.Cmd) {
	names := m.selectedTargetNames()
	if len(names) == 0 {
		return m, nil
	}
	// Shift+S is also an escape hatch from an in-flight dependency gate. The
	// stale dependency-aware result is ignored because beginOperation advances
	// the operation ID before starting the direct targets.
	m.cancelStartOperation()
	target := m.selectedTargetLabel(names)
	allRunning := true
	for _, name := range names {
		svc, ok := m.manager.GetService(name)
		if !ok || svc.Status() == config.StatusStopped {
			allRunning = false
			break
		}
	}
	if allRunning {
		return m.beginOperation(operationForceStop, target, "Force stopping "+target, func() error {
			return m.manager.ForceStopServices(names)
		})
	}
	return m.beginOperation(operationForceStart, target, "Force starting "+target, func() error {
		return m.manager.ForceStartServices(names)
	})
}

func (m *Model) restartSelectedService() (tea.Model, tea.Cmd) {
	svc := m.FocusedService()
	if svc == nil {
		return m, nil
	}
	if svc.Status() == config.StatusStopped {
		return m.startSelectedService()
	}
	affected := m.manager.GetAffectedServices(svc.Name)
	if len(affected) > 1 {
		m.mode = ModeConfirmRestart
		m.confirmTarget = svc.Name
		m.confirmAction = strings.Join(affected[1:], ", ")
		return m, nil
	}
	return m.beginRestart(svc.Name)
}

func (m *Model) beginRestart(name string) (tea.Model, tea.Cmd) {
	m.mode = ModeNormal
	return m.beginOperation(operationRestart, name, "Restarting "+name, func() error {
		return m.manager.RestartService(name)
	})
}

func (m *Model) beginOperation(kind operationKind, target, label string, operation func() error) (tea.Model, tea.Cmd) {
	return m.beginCancelableOperation(kind, target, label, nil, operation)
}

func (m *Model) beginCancelableOperation(kind operationKind, target, label string, cancel context.CancelFunc, operation func() error) (tea.Model, tea.Cmd) {
	if m.operation != "" {
		if cancel != nil {
			cancel()
		}
		m.addNotification("system", "Wait for the current operation: "+m.operation, config.LogWarn)
		return m, nil
	}
	m.operation = label
	m.operationKind = kind
	m.operationCancel = cancel
	m.operationID++
	operationID := m.operationID
	return m, func() tea.Msg {
		return operationResultMsg{id: operationID, kind: kind, target: target, err: operation()}
	}
}

func (m *Model) cancelStartOperation() {
	switch m.operationKind {
	case operationStart, operationStartSet:
		if m.operationCancel != nil {
			m.operationCancel()
		}
		m.operation = ""
		m.operationKind = ""
		m.operationCancel = nil
	}
}

func (m *Model) handleOperationResult(msg operationResultMsg) (tea.Model, tea.Cmd) {
	if msg.id != m.operationID {
		return m, nil
	}
	m.operation = ""
	m.operationKind = ""
	m.operationCancel = nil
	if msg.err != nil {
		var conflict *service.PortConflictError
		if errors.As(msg.err, &conflict) {
			m.conflictService = conflict.Service
			if m.conflictService == "" {
				m.conflictService = msg.target
			}
			m.conflictPorts = map[int]*config.PortInfo{
				conflict.Port: {
					Port:    conflict.Port,
					PID:     conflict.PID,
					Process: conflict.Process,
					Command: conflict.Command,
				},
			}
			m.conflictOwner = conflict.OwnerService
			m.conflictExternal = conflict.External
			m.mode = ModePortConflict
			return m, nil
		}
		m.addNotification(msg.target, msg.err.Error(), config.LogError)
		return m, nil
	}

	message := map[operationKind]string{
		operationStart:      "Service started",
		operationStartSet:   "Selection started (required dependencies included)",
		operationForceStart: "Selected services started without dependencies",
		operationForceStop:  "Selected services stopped without stopping dependents",
		operationStopAll:    "All services have been stopped",
		operationStopSet:    "Selection and dependent services stopped; ports released",
		operationRestart:    "Service restarted",
		operationRestartAll: "Running services have been restarted",
	}[msg.kind]
	m.addNotification(msg.target, message, config.LogInfo)
	m.portService = ""
	m.portChecked = time.Time{}
	m.portScanBusy = false
	return m, m.scanFocusedPorts(true)
}

func (m *Model) beginShutdown() (tea.Model, tea.Cmd) {
	if m.operationCancel != nil {
		m.operationCancel()
		m.operationCancel = nil
	}
	m.operationID++
	m.operationKind = ""
	if m.operation != "Shutting down" {
		m.operation = "Shutting down"
	}
	m.mode = ModeNormal
	return m, func() tea.Msg { return shutdownResultMsg{err: m.Shutdown()} }
}

func (m *Model) handleConfirmQuitKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.beginShutdown()
	case "n", "N", "esc":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) handleConfirmRestartKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.beginRestart(m.confirmTarget)
	case "n", "N", "esc":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) handleConfirmClearLogsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.clearConfirmedLogs()
	case "esc":
		m.clearTarget = ""
		m.clearPinned = false
		m.mode = ModeNormal
	}
	return m, nil
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
	checker := m.portChecker
	manager := m.manager
	return func() tea.Msg {
		info, err := checker.CheckPort(portNumber)
		if err != nil {
			return releasePortResultMsg{port: portNumber, pid: expectedPID, err: err}
		}
		if info == nil {
			return releasePortResultMsg{port: portNumber, pid: expectedPID, alreadyFree: true}
		}
		if info.PID != expectedPID {
			return releasePortResultMsg{port: portNumber, pid: expectedPID, err: fmt.Errorf(
				"port %d owner changed from PID %d to PID %d; refusing to stop it", portNumber, expectedPID, info.PID,
			)}
		}
		if owner := manager.ManagedServiceForPID(info.PID); owner != "" {
			return releasePortResultMsg{port: portNumber, pid: expectedPID, err: fmt.Errorf(
				"port %d is now owned by Kranz service %s; refusing to stop it as external", portNumber, owner,
			)}
		}
		return releasePortResultMsg{
			port: portNumber,
			pid:  expectedPID,
			err:  port.TerminateExternalPID(expectedPID, 3*time.Second),
		}
	}
}

const (
	// The filtered log panel blinks after a click the search editor could not
	// act on. A short phase and several pulses read as "you are still in here",
	// where a single long highlight would read as a state change.
	searchNudgeBlink  = 90 * time.Millisecond
	searchNudgePulses = 5
	// Phases alternate lit and dark starting lit, so the window closes on the
	// trailing edge of the last pulse.
	searchNudgeDuration = time.Duration(searchNudgePulses*2-1) * searchNudgeBlink
)

// searchNudgeMsg drives one blink phase. It carries the click it belongs to so
// a later click's chain supersedes an earlier one instead of stacking with it.
type searchNudgeMsg time.Time

// nudgeSearchFocus answers a click that landed outside the editor while the
// search was open. The editor is modal because leaving it has to mean either
// apply or discard, and a click says neither, so the panel blinks instead of
// swallowing the click in silence.
func (m *Model) nudgeSearchFocus() tea.Cmd {
	m.searchNudge = time.Now()
	return m.scheduleSearchNudge(m.searchNudge)
}

// scheduleSearchNudge repaints on the blink interval. The dashboard's own tick
// is both too slow and unsynchronized with the click, so the blink needs its
// own beat for its phases to be visible at all.
func (m *Model) scheduleSearchNudge(start time.Time) tea.Cmd {
	return tea.Tick(searchNudgeBlink, func(time.Time) tea.Msg { return searchNudgeMsg(start) })
}

// searchNudgeActive reports whether the border is lit in the current phase.
func (m *Model) searchNudgeActive() bool {
	if m.searchNudge.IsZero() {
		return false
	}
	elapsed := time.Since(m.searchNudge)
	if elapsed >= searchNudgeDuration {
		return false
	}
	return (elapsed/searchNudgeBlink)%2 == 0
}

// applySearchQuery compiles the edited query and makes it the active pattern.
// Enter is the only way to apply, and the editor stays open afterwards so a
// pattern can be refined without reopening it. It reports whether the query
// compiled.
// newSearchInput builds the regex editor. Editing, cursor motion, and the
// horizontal window that keeps the caret visible on a long pattern all come
// from the component; Kranz only owns apply, cancel, and the mode toggle.
func newSearchInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	// A regex can legitimately be long, and the editor scrolls horizontally
	// rather than truncating, so no character limit applies.
	input.CharLimit = 0
	return input
}

// syncSearchInputWidth refreshes the editor's visible window. The component
// recomputes its horizontal scroll while handling a key, not while rendering,
// so the width has to be current before input reaches it.
func (m *Model) syncSearchInputWidth() {
	_, _, editorWidth := m.searchBarLayout()
	m.searchInput.Width = editorWidth
}

// openSearchEditor shows the editor seeded with the active pattern and focuses
// the logs it filters, so match navigation works as soon as the editor closes.
func (m *Model) openSearchEditor() tea.Cmd {
	m.mode = ModeSearch
	m.searchNudge = time.Time{}
	m.syncSearchInputWidth()
	m.searchInput.SetValue(m.logSearcher.Pattern())
	m.searchInput.CursorEnd()
	command := m.searchInput.Focus()
	if m.panelFocus != panelPinnedLogs {
		m.panelFocus = panelLogs
	}
	return command
}

func (m *Model) applySearchQuery() bool {
	if err := m.logSearcher.SetPattern(m.searchInput.Value()); err != nil {
		m.addNotification("search", err.Error(), config.LogError)
		return false
	}
	m.currentMatch = -1
	m.logOffset = 0
	m.logAnchor = 0
	m.followMode = true
	m.logPaused = false
	if m.searchMode == searchHighlight && m.logSearcher.HasPattern() {
		if svc := m.FocusedService(); svc != nil {
			m.currentMatch = m.logSearcher.FindNext(serviceLogLines(svc), -1)
			m.focusLogMatch(m.currentMatch)
		}
	}
	return true
}

// clearSearch drops the active pattern and restores unfiltered following.
func (m *Model) clearSearch() {
	m.currentMatch = -1
	m.searchInput.SetValue("")
	_ = m.logSearcher.SetPattern("")
	m.followMode, m.logPaused, m.logOffset, m.logAnchor = true, false, 0, 0
}

func (m *Model) handleSearchKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// These three own the editor's lifecycle and must be claimed before the
	// text input sees them; Tab in particular is a suggestion key upstream.
	switch msg.String() {
	case "esc":
		// Esc cancels the edit rather than applying it, keeping Enter the only
		// apply. Restoring the query to the active pattern means reopening the
		// editor always shows the filter that is actually in effect.
		m.searchInput.SetValue(m.logSearcher.Pattern())
		m.searchInput.Blur()
		m.searchNudge = time.Time{}
		m.mode = ModeNormal
		m.panelFocus = panelLogs
		return m, nil
	case "tab", "shift+tab":
		if m.searchMode == searchFilter {
			m.searchMode = searchHighlight
		} else {
			m.searchMode = searchFilter
		}
		m.syncSearchInputWidth()
		// Switching to highlight over an already applied pattern should land on
		// a match instead of waiting for the next apply.
		if m.searchMode == searchHighlight && m.logSearcher.HasPattern() {
			if svc := m.FocusedService(); svc != nil {
				m.currentMatch = m.logSearcher.FindNext(serviceLogLines(svc), -1)
				m.focusLogMatch(m.currentMatch)
			}
		}
		return m, nil
	case "enter":
		m.applySearchQuery()
		return m, nil
	}

	var command tea.Cmd
	m.searchInput, command = m.searchInput.Update(msg)
	return m, command
}

// FocusedService returns the service selected by the list cursor.
func (m *Model) FocusedService() *service.Service {
	if m.focused < 0 || m.focused >= len(m.services) {
		return nil
	}
	return m.services[m.focused]
}

func (m *Model) addNotification(serviceName, message string, level config.LogLevel) {
	notification := config.Notification{
		Time: time.Now(), Level: level, Service: serviceName, Message: message,
	}
	m.notifMu.Lock()
	m.notifications = append([]config.Notification{notification}, m.notifications...)
	if len(m.notifications) > 100 {
		m.notifications = m.notifications[:100]
	}
	m.toastMessage, m.toastTimer = message, time.Now()
	m.notifMu.Unlock()
}

// PinnedService returns the service shown in the fixed upper log panel.
func (m *Model) PinnedService() *service.Service {
	if m.pinnedLog == "" {
		return nil
	}
	svc, _ := m.manager.GetService(m.pinnedLog)
	return svc
}

func toggleTag(tags []string, tag string) []string {
	for i, current := range tags {
		if strings.EqualFold(current, tag) {
			return append(tags[:i], tags[i+1:]...)
		}
	}
	return append(tags, tag)
}
