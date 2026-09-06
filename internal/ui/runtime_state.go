package ui

import (
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// runtimeUIState is the volatile dashboard state Kranz keeps for one visited
// runtime session, so returning to it looks the same as never having left.
// It is indexed by session ID in Model.uiStateCache, lives only in process
// memory, and is never written to disk (PRD 3.4).
type runtimeUIState struct {
	// populated distinguishes "no state captured yet" (a runtime visited for
	// the first time gets the model's zero-value defaults) from a state that
	// was genuinely captured and should be applied verbatim.
	populated bool

	focusedServiceName  string
	focusedAction       *config.ActionID
	focusedActionGroup  string
	panelFocus          panelFocus
	listMode            listMode
	detailOffset        int
	selectedTags        []string
	selected            map[string]bool
	expandedTags        map[string]bool
	expandedActionOwner map[string]bool
	tagCursor           int

	pinnedLog     string
	pinnedTarget  app.RunTarget
	pinnedRunMode runViewMode
	pinnedRun     uint32

	logOffset, logAnchor         int
	pinnedOffset, pinnedAnchor   int
	followMode, pinnedFollow     bool
	logPaused                    bool
	wrapLogs, showLogTime        bool
	searchMode, pinnedSearchMode logSearchMode
	currentMatch, pinnedMatch    int
	logPattern, pinnedPattern    string

	runMode          runViewMode
	selectedRun      uint32
	runFollowsLatest bool
	runListCursor    int
	runStatusFilter  string
	runViewports     map[runViewportKey]runViewportState

	notifications []config.Notification
	toastMessage  string
	toastTimer    time.Time
}

// captureUIState snapshots every field runtimeUIState tracks from the live
// model. Called synchronously on the Update goroutine, immediately before a
// switch tears down the current session, so nothing here can race a
// concurrent field write.
func (m *Model) captureUIState() runtimeUIState {
	m.notifMu.RLock()
	notifications := append([]config.Notification(nil), m.notifications...)
	toastMessage, toastTimer := m.toastMessage, m.toastTimer
	m.notifMu.RUnlock()

	state := runtimeUIState{
		populated:           true,
		focusedActionGroup:  m.focusedActionGroup,
		panelFocus:          m.panelFocus,
		listMode:            m.listMode,
		detailOffset:        m.detailOffset,
		selectedTags:        append([]string(nil), m.selectedTags...),
		selected:            copyBoolMap(m.selected),
		expandedTags:        copyBoolMap(m.expandedTags),
		expandedActionOwner: copyBoolMap(m.expandedActionOwner),
		tagCursor:           m.tagCursor,

		pinnedLog:     m.pinnedLog,
		pinnedTarget:  m.pinnedTarget,
		pinnedRunMode: m.pinnedRunMode,
		pinnedRun:     m.pinnedRun,

		logOffset: m.logOffset, logAnchor: m.logAnchor,
		pinnedOffset: m.pinnedOffset, pinnedAnchor: m.pinnedAnchor,
		followMode: m.followMode, pinnedFollow: m.pinnedFollow,
		logPaused:   m.logPaused,
		wrapLogs:    m.wrapLogs,
		showLogTime: m.showLogTime,

		searchMode: m.searchMode, pinnedSearchMode: m.pinnedSearchMode,
		currentMatch: m.currentMatch, pinnedMatch: m.pinnedMatch,

		runMode:          m.runMode,
		selectedRun:      m.selectedRun,
		runFollowsLatest: m.runFollowsLatest,
		runListCursor:    m.runListCursor,
		runStatusFilter:  m.runStatusFilter,
		runViewports:     copyViewportMap(m.runViewports),

		notifications: notifications,
		toastMessage:  toastMessage,
		toastTimer:    toastTimer,
	}
	if svc := m.FocusedService(); svc != nil {
		state.focusedServiceName = svc.Name
	}
	if m.focusedAction != nil {
		id := *m.focusedAction
		state.focusedAction = &id
	}
	if m.logSearcher != nil {
		state.logPattern = m.logSearcher.Pattern()
	}
	if m.pinnedSearcher != nil {
		state.pinnedPattern = m.pinnedSearcher.Pattern()
	}
	return state
}

// applyUIState installs state onto the model, reconciling every reference
// against the just-fetched configuration and services of the session being
// entered: a service, action, group, or run that no longer exists is
// dropped rather than kept as a dangling reference (PRD 3.4's "reload
// removes missing services/actions/runs from the restored selection").
// Passing the zero value applies Kranz's ordinary defaults, which is what a
// runtime visited for the first time in this process gets.
func (m *Model) applyUIState(state runtimeUIState) {
	if !state.populated {
		m.resetUIStateToDefaults()
		return
	}

	m.panelFocus = state.panelFocus
	if m.panelFocus == 0 {
		m.panelFocus = panelServices
	}
	m.listMode = state.listMode
	m.detailOffset = max(0, state.detailOffset)
	m.selectedTags = filterExistingTags(state.selectedTags, m.cfg)
	m.selected = filterExistingServices(state.selected, m.cfg)
	m.expandedTags = copyBoolMap(state.expandedTags)
	m.expandedActionOwner = copyBoolMap(state.expandedActionOwner)
	m.tagCursor = state.tagCursor

	m.focusedAction = nil
	m.focusedActionGroup = ""
	if state.focusedAction != nil {
		if _, exists := m.cfg.ResolveAction(*state.focusedAction); exists {
			id := *state.focusedAction
			m.focusedAction = &id
		}
	}
	if m.focusedAction == nil && state.focusedActionGroup != "" {
		if _, exists := m.cfg.ActionGroups[state.focusedActionGroup]; exists {
			m.focusedActionGroup = state.focusedActionGroup
		}
	}
	m.focused = 0
	if m.focusedAction == nil && m.focusedActionGroup == "" && state.focusedServiceName != "" {
		for index, svc := range m.services {
			if svc.Name == state.focusedServiceName {
				m.focused = index
				break
			}
		}
	}
	if m.focusedAction != nil || m.focusedActionGroup != "" {
		if row := m.focusedServiceListRow(); row >= 0 {
			m.focusServiceListRow(row)
		}
	}

	m.pinnedLog, m.pinnedTarget, m.pinnedRunMode, m.pinnedRun = "", app.RunTarget{}, runViewCombined, 0
	if state.pinnedTarget.Kind != "" || state.pinnedLog != "" {
		valid := false
		if state.pinnedTarget.Kind == app.RunKindAction {
			_, valid = m.cfg.ResolveAction(state.pinnedTarget.Action)
		} else {
			name := state.pinnedLog
			if state.pinnedTarget.Kind == app.RunKindService {
				name = state.pinnedTarget.Name
			}
			_, valid = m.cfg.Services[name]
		}
		if valid {
			m.pinnedLog, m.pinnedTarget, m.pinnedRunMode, m.pinnedRun = state.pinnedLog, state.pinnedTarget, state.pinnedRunMode, state.pinnedRun
		} else if m.panelFocus == panelPinnedLogs {
			m.panelFocus = panelLogs
		}
	}

	m.logOffset, m.logAnchor = state.logOffset, state.logAnchor
	m.pinnedOffset, m.pinnedAnchor = state.pinnedOffset, state.pinnedAnchor
	m.followMode, m.pinnedFollow = state.followMode, state.pinnedFollow
	m.logPaused = state.logPaused
	m.wrapLogs = state.wrapLogs
	m.showLogTime = state.showLogTime
	m.searchMode, m.pinnedSearchMode = state.searchMode, state.pinnedSearchMode
	m.currentMatch, m.pinnedMatch = state.currentMatch, state.pinnedMatch
	if m.logSearcher != nil {
		_ = m.logSearcher.SetPattern(state.logPattern)
	}
	if m.pinnedSearcher != nil {
		_ = m.pinnedSearcher.SetPattern(state.pinnedPattern)
	}

	// runTarget is derived from the focus restored above rather than cached,
	// so the two can never disagree about which target's runs the fields
	// below describe. It has to be derived *before* them: syncRunTarget
	// treats an unset runTarget as entering a new target and resets the run
	// selection to that target's defaults, which would discard exactly what
	// this function is restoring.
	m.runTarget = app.RunTarget{}
	m.syncRunTarget()

	m.runMode = state.runMode
	m.selectedRun = state.selectedRun
	m.runFollowsLatest = state.runFollowsLatest
	m.runListCursor = state.runListCursor
	m.runStatusFilter = state.runStatusFilter
	m.runViewports = copyViewportMap(state.runViewports)
	m.reconcileRestoredRunSelection()
	// syncRunTarget skipped its own restore while runTarget was still being
	// established; the log panel has to be pointed at the cached viewport now
	// that both the target and the selected run are back.
	m.restoreRunViewport()
	m.notifMu.Lock()
	m.notifications = append([]config.Notification(nil), state.notifications...)
	m.toastMessage, m.toastTimer = state.toastMessage, state.toastTimer
	m.notifMu.Unlock()
}

// reconcileRestoredRunSelection keeps an exact saved run when it still
// exists. If retention removed it while this runtime was detached, the
// closest retained run becomes selected instead; follow-latest state always
// advances to the current latest run.
func (m *Model) reconcileRestoredRunSelection() {
	if m.runMode != runViewSingle {
		return
	}
	runs := m.runsForTarget(m.runTarget)
	if m.runFollowsLatest || m.selectedRun == 0 {
		m.selectedRun = latestRun(runs)
		return
	}
	closest := uint32(0)
	closestDistance := ^uint32(0)
	for _, run := range runs {
		if run.Run == m.selectedRun {
			return
		}
		distance := run.Run - m.selectedRun
		if run.Run < m.selectedRun {
			distance = m.selectedRun - run.Run
		}
		if distance < closestDistance {
			closest, closestDistance = run.Run, distance
		}
	}
	m.selectedRun = closest
}

// resetUIStateToDefaults restores the values NewModelWithOptions gives a
// freshly constructed dashboard, for a runtime session with no cached state.
func (m *Model) resetUIStateToDefaults() {
	m.focused = 0
	m.selected = make(map[string]bool)
	m.detailOffset = 0
	m.logOffset, m.logAnchor = 0, 0
	m.pinnedOffset, m.pinnedAnchor = 0, 0
	m.panelFocus = panelServices
	m.listMode = listServices
	m.focusedAction = nil
	m.focusedActionGroup = ""
	m.expandedActionOwner = make(map[string]bool)
	m.pinnedLog, m.pinnedTarget, m.pinnedRunMode, m.pinnedRun = "", app.RunTarget{}, runViewCombined, 0
	m.runTarget = app.RunTarget{}
	m.runMode = runViewCombined
	m.selectedRun = 0
	m.runFollowsLatest = false
	m.runListCursor = 0
	m.runStatusFilter = ""
	m.runViewports = make(map[runViewportKey]runViewportState)
	m.followMode, m.pinnedFollow = true, true
	m.logPaused = false
	m.wrapLogs, m.showLogTime = false, false
	m.selectedTags = nil
	m.tagCursor = 0
	m.expandedTags = make(map[string]bool)
	m.currentMatch, m.pinnedMatch = -1, -1
	m.searchMode, m.pinnedSearchMode = searchFilter, searchFilter
	m.notifMu.Lock()
	m.notifications = nil
	m.toastMessage, m.toastTimer = "", time.Time{}
	m.notifMu.Unlock()
	if m.logSearcher != nil {
		_ = m.logSearcher.SetPattern("")
	}
	if m.pinnedSearcher != nil {
		_ = m.pinnedSearcher.SetPattern("")
	}
}

// resetRuntimeDataCaches drops data fetched from the previously attached
// runtime. The supervisor remains the source of truth and the new session is
// fetched immediately after this reset; retaining these maps would let two
// projects with the same service or action names share log cursors and append
// each other's output.
func (m *Model) resetRuntimeDataCaches() {
	m.services = nil
	m.allServices = nil
	m.runs = nil
	m.actionStates = make(map[config.ActionID]app.ActionResult)
	m.logEntries = make(map[app.RunTarget][]config.LogEntry)
	m.actionLogLines = make(map[app.RunTarget][]cachedActionLogLine)
	m.actionRunLogLines = make(map[app.RunTarget]map[uint32][]cachedActionLogLine)
	m.logCursors = make(map[app.RunTarget]string)
	m.portDetails = make(map[int]*config.PortInfo)
	m.portError = nil
}

func copyBoolMap(source map[string]bool) map[string]bool {
	if source == nil {
		return make(map[string]bool)
	}
	dest := make(map[string]bool, len(source))
	for key, value := range source {
		dest[key] = value
	}
	return dest
}

func copyViewportMap(source map[runViewportKey]runViewportState) map[runViewportKey]runViewportState {
	if source == nil {
		return make(map[runViewportKey]runViewportState)
	}
	dest := make(map[runViewportKey]runViewportState, len(source))
	for key, value := range source {
		dest[key] = value
	}
	return dest
}

func filterExistingTags(tags []string, cfg *config.Config) []string {
	if len(tags) == 0 {
		return nil
	}
	valid := make(map[string]bool)
	for _, svc := range cfg.Services {
		for _, tag := range svc.Tags {
			valid[tag] = true
		}
	}
	kept := make([]string, 0, len(tags))
	for _, tag := range tags {
		if valid[tag] {
			kept = append(kept, tag)
		}
	}
	return kept
}

func filterExistingServices(selected map[string]bool, cfg *config.Config) map[string]bool {
	kept := make(map[string]bool, len(selected))
	for name, on := range selected {
		if !on {
			continue
		}
		if _, exists := cfg.Services[name]; exists {
			kept[name] = true
		}
	}
	return kept
}
