package ui

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	kranzlog "github.com/kranz-org/kranz/internal/log"
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
	ModeConfirmAction
	ModeConfirmServiceStart
	ModeConfirmServiceStop
	ModeConfirmThemeSave
	ModeThemes
)

type themeSaveScope uint8

const (
	themeSaveNone themeSaveScope = iota
	themeSaveGlobal
	themeSaveProject
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
	Service *app.ServiceSnapshot
}

type actionListRowKind uint8

const (
	actionRowService actionListRowKind = iota
	actionRowGroup
	actionRowAction
)

type actionListRow struct {
	Kind    actionListRowKind
	Service *app.ServiceSnapshot
	Group   string
	Action  config.ActionID
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

type actionResultMsg struct {
	id     config.ActionID
	result app.ActionResult
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

const mouseTrackingRefreshInterval = 250 * time.Millisecond

type configReloadMsg struct {
	result     app.ReloadResult
	err        error
	generation uint64
	changed    bool
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
	cfg              *config.Config
	version          string
	workingDirectory string

	app                 app.API
	services            []*app.ServiceSnapshot
	allServices         []*app.ServiceSnapshot
	focused             int
	selected            map[string]bool
	detailOffset        int
	logOffset           int
	logAnchor           int
	pinnedOffset        int
	pinnedAnchor        int
	panelFocus          panelFocus
	listMode            listMode
	focusedAction       *config.ActionID
	focusedActionGroup  string
	expandedActionOwner map[string]bool

	portDetails  map[int]*config.PortInfo
	portError    error
	portService  string
	portChecked  time.Time
	portScanID   int
	portScanBusy bool

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

	confirmAction      string
	confirmTarget      string
	confirmRestartAll  bool
	pendingAction      *config.ActionID
	pendingActionStop  bool
	pendingStartNames  []string
	pendingStartTarget string
	pendingStartForce  bool
	pendingStopNames   []string
	pendingStopTarget  string
	pendingStopForce   bool
	pendingStopAll     bool
	themeSaveScope     themeSaveScope
	clearTarget        string
	clearPinned        bool

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
	lastMouseRefresh    time.Time
	mousePressSequence  uint64
	lastListClickOwner  string
	lastListClickSeq    uint64
	lastListClickAt     time.Time
	systemAppearanceSet bool
	systemDark          bool
	themeBefore         Theme
	settingsBefore      usersettings.Settings
	themeCursor         int
	themeUseProject     bool
	themeColorMode      string
	// Accent and background are the same shape: one source field answering where
	// the colour comes from, plus a custom value that survives while another
	// source is active so Custom stays available as a position in the a/b cycle.
	// themeAccentChanged is a separate axis — whether the user touched the accent
	// this session — and decides whether to write the user settings.
	themeAccentSource     themeAccentSource
	themeCustomAccent     string
	themeAccentChanged    bool
	themeBackgroundSource themeBackgroundSource
	themeCustomBackground string
	// One hex editor serves both colours; themeColorTarget says which one is
	// being edited.
	themeColorTarget   themeColorTarget
	themeColorInput    textinput.Model
	themeColorEditing  bool
	themeColorReplace  bool
	themeColorError    string
	configPaths        []string
	projectExitHandled bool

	shutdownOnce sync.Once
	shutdownErr  error
	detachOnExit bool
}

// ModelOptions supplies user-level preferences and their persistence path.
type ModelOptions struct {
	Settings     usersettings.Settings
	SettingsPath string
	ConfigPaths  []string
	// DarkBackground is detected by the executable. Nil keeps the historical
	// dark default for embedders and deterministic tests.
	DarkBackground *bool
	// App is the application-layer runtime the model drives. When nil, one
	// is constructed from cfg and ConfigPaths with production defaults —
	// the shape every existing caller and test relies on.
	App app.API
	// DetachOnExit makes q/Ctrl+C close only this delivery surface. The
	// background runtime remains owned by its supervisor process.
	DetachOnExit bool
}

// NewModel creates a model with default user settings and terminal detection.
func NewModel(cfg *config.Config, version string) *Model {
	return NewModelWithOptions(cfg, version, ModelOptions{})
}

// NewModelWithOptions creates a model with resolved project/user appearance.
func NewModelWithOptions(cfg *config.Config, version string, options ModelOptions) *Model {
	workingDirectory, _ := os.Getwd()
	terminalDark := true
	if options.DarkBackground != nil {
		terminalDark = *options.DarkBackground
	}
	themeName, accent, background, colorMode := effectiveAppearance(cfg.UI, options.Settings)
	activeTheme, themeErr := applyAppearance(themeName, accent, background, colorMode, terminalDark)
	if themeErr != nil {
		activeTheme, _ = applyAppearance(DefaultTheme, "", backgroundTerminal, colorModeAuto, terminalDark)
	}
	application := options.App
	if application == nil {
		application = app.NewLocal(cfg, options.ConfigPaths, app.Options{})
	}
	services := application.Services()

	model := &Model{
		cfg:                 application.Config(),
		version:             version,
		workingDirectory:    workingDirectory,
		app:                 application,
		detachOnExit:        options.DetachOnExit,
		services:            services,
		allServices:         services,
		portDetails:         make(map[int]*config.PortInfo),
		selected:            make(map[string]bool),
		expandedTags:        make(map[string]bool),
		expandedActionOwner: make(map[string]bool),
		panelFocus:          panelServices,
		listMode:            listServices,
		logSearcher:         kranzlog.NewSearcher(),
		searchInput:         newSearchInput(),
		themeColorInput:     newThemeColorInput(),
		currentMatch:        -1,
		searchMode:          searchFilter,
		mode:                ModeNormal,
		followMode:          true,
		pinnedFollow:        true,
		keys:                DefaultKeyMap(),
		userSettings:        options.Settings,
		settingsPath:        options.SettingsPath,
		activeTheme:         activeTheme,
		terminalDark:        terminalDark,
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
	if themeErr != nil {
		model.addNotification("appearance", themeErr.Error()+"; using the Kranz theme", config.LogWarn)
	}
	for _, diagnostic := range cfg.Diagnostics {
		model.addNotification("config", diagnostic, config.LogWarn)
	}
	if len(model.services) == 0 && len(model.cfg.ActionGroups) > 0 {
		model.focusServiceListRow(0)
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

func (m *Model) enableMouseTracking(now time.Time) tea.Cmd {
	m.lastMouseRefresh = now
	return tea.EnableMouseCellMotion
}

func (m *Model) refreshMouseTracking(now time.Time) tea.Cmd {
	if !m.lastMouseRefresh.IsZero() && now.Sub(m.lastMouseRefresh) < mouseTrackingRefreshInterval {
		return nil
	}
	return m.enableMouseTracking(now)
}

// RequestedExitCode returns the exit code requested by an availability policy.
func (m *Model) RequestedExitCode() int {
	requested, code := m.app.ProjectExitRequested()
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
		// tracking mode when a tab or macOS workspace loses and regains focus.
		// Re-assert it immediately; the tick watchdog below also recovers when
		// the terminal drops the focus event itself.
		var searchCommand tea.Cmd
		if m.mode == ModeSearch {
			m.searchInput, searchCommand = m.searchInput.Update(msg)
		} else if m.mode == ModeThemes && m.themeColorEditing {
			m.themeColorInput, searchCommand = m.themeColorInput.Update(msg)
		}
		return m, tea.Batch(m.enableMouseTracking(time.Now()), searchCommand)
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.MouseMsg:
		return m.handleMouseMsg(msg)
	case operationResultMsg:
		return m.handleOperationResult(msg)
	case actionResultMsg:
		return m.handleActionResult(msg)
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
		now := time.Time(msg)
		m.refreshServices()
		m.expireToast()
		if requested, _ := m.app.ProjectExitRequested(); requested && !m.projectExitHandled {
			m.projectExitHandled = true
			return m.beginShutdown()
		}
		return m, tea.Batch(
			m.pollServices(),
			m.scanFocusedPorts(false),
			m.reloadConfig(false),
			m.refreshMouseTracking(now),
		)
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
		if m.mode == ModeThemes && m.themeColorEditing {
			var command tea.Cmd
			m.themeColorInput, command = m.themeColorInput.Update(msg)
			m.sanitizeThemeColorInput()
			return m, command
		}
		return m, nil
	}
}

func (m *Model) refreshServices() {
	m.allServices = m.app.Services()
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
func (m *Model) FocusedService() *app.ServiceSnapshot {
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
func (m *Model) PinnedService() *app.ServiceSnapshot {
	if m.pinnedLog == "" {
		return nil
	}
	// Look up within this tick's already-fetched snapshots rather than
	// querying app fresh, so PinnedService and FocusedService return the
	// same pointer for the same service within one render — some callers
	// compare them by identity.
	for _, svc := range m.allServices {
		if svc.Name == m.pinnedLog {
			return svc
		}
	}
	return nil
}
