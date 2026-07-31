package ui

import (
	"context"
	"fmt"
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

// Init schedules service polling and the initial port inspection.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.pollServices(), m.scanFocusedPorts(true), m.pollSystemAppearance())
}

func (m *Model) pollServices() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
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

func (m *Model) themeProjectConfigPath() string {
	if len(m.configPaths) == 0 {
		return ""
	}
	return m.configPaths[len(m.configPaths)-1]
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
