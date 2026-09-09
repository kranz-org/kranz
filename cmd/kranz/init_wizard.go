package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/ui"
)

const projectActionOwner = "project"

type initServiceDraft struct {
	Name    string
	Dir     string
	Command string
	Port    int
}

type initActionDraft struct {
	Name    string
	Owner   string
	Dir     string
	Command string
}

type initDraft struct {
	Project              string
	Directory            string
	AppearanceConfigured bool
	Theme                string
	ThemeSet             bool
	AccentSource         string
	CustomAccent         string
	AccentSet            bool
	BackgroundSource     string
	CustomBackground     string
	BackgroundSet        bool
	ColorMode            string
	ColorModeSet         bool
	Services             []initServiceDraft
	Actions              []initActionDraft
}

func newInitDraft(directory string, options initOptions) initDraft {
	project := options.project
	if project == "" {
		project = filepath.Base(mustAbs(directory))
	}
	draft := initDraft{
		Project:          project,
		Directory:        mustAbs(directory),
		Theme:            ui.DefaultTheme,
		AccentSource:     "theme",
		BackgroundSource: config.UIBackgroundTerminal,
		ColorMode:        config.UIColorModeAuto,
	}
	if options.service != "" || options.command != "" {
		draft.Services = append(draft.Services, initServiceDraft{
			Name:    defaultString(options.service, "app"),
			Dir:     ".",
			Command: options.command,
		})
	}
	return draft
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (d initDraft) target(outputPath string) string {
	if filepath.IsAbs(outputPath) {
		return filepath.Clean(outputPath)
	}
	return filepath.Join(d.Directory, outputPath)
}

func (d initDraft) config() (*config.Config, error) {
	cfg := &config.Config{
		Project:      strings.TrimSpace(d.Project),
		Services:     make(map[string]config.Service, len(d.Services)),
		ServiceOrder: make([]string, 0, len(d.Services)),
	}
	if d.AppearanceConfigured {
		if d.ThemeSet {
			cfg.UI.Theme = d.Theme
		}
		if d.AccentSet && d.AccentSource == "custom" {
			cfg.UI.Accent = strings.ToUpper(d.CustomAccent)
		}
		if d.BackgroundSet {
			switch d.BackgroundSource {
			case "custom":
				cfg.UI.Background = strings.ToUpper(d.CustomBackground)
			default:
				cfg.UI.Background = d.BackgroundSource
			}
		}
		if d.ColorModeSet {
			cfg.UI.ColorMode = d.ColorMode
		}
	}
	for _, item := range d.Services {
		service := config.Service{Command: strings.TrimSpace(item.Command)}
		if dir := cleanDraftDir(item.Dir); dir != "." {
			service.Dir = dir
		}
		if item.Port != 0 {
			service.Ports = []int{item.Port}
		}
		name := strings.TrimSpace(item.Name)
		cfg.Services[name] = service
		cfg.ServiceOrder = append(cfg.ServiceOrder, name)
	}
	for _, item := range d.Actions {
		action := config.Action{Command: strings.TrimSpace(item.Command)}
		if dir := cleanDraftDir(item.Dir); dir != "." {
			action.Dir = dir
		}
		name, owner := strings.TrimSpace(item.Name), strings.TrimSpace(item.Owner)
		if owner == "" || owner == projectActionOwner {
			if cfg.ActionGroups == nil {
				cfg.ActionGroups = make(map[string]config.ActionGroup)
			}
			group, exists := cfg.ActionGroups[projectActionOwner]
			if !exists {
				group.Actions = make(map[string]config.Action)
				cfg.ActionGroupOrder = append(cfg.ActionGroupOrder, projectActionOwner)
			}
			group.Actions[name] = action
			group.ActionOrder = append(group.ActionOrder, name)
			cfg.ActionGroups[projectActionOwner] = group
			continue
		}
		service, exists := cfg.Services[owner]
		if !exists {
			return nil, fmt.Errorf("action %q names unknown service owner %q", name, owner)
		}
		if service.Actions == nil {
			service.Actions = make(map[string]config.Action)
		}
		service.Actions[name] = action
		service.ActionOrder = append(service.ActionOrder, name)
		cfg.Services[owner] = service
	}
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func cleanDraftDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "."
	}
	return filepath.Clean(dir)
}

func (d initDraft) document() (*yaml.Node, error) {
	cfg, err := d.config()
	if err != nil {
		return nil, err
	}
	return effectiveDocument(cfg)
}

type initWizardScreen int

const (
	initWizardReplace initWizardScreen = iota
	initWizardProject
	initWizardBuilder
	initWizardService
	initWizardAction
	initWizardAppearance
	initWizardColor
	initWizardReview
)

type initWizardRowKind int

const (
	initRowAppearance initWizardRowKind = iota
	initRowService
	initRowAction
	initRowAddService
	initRowAddAction
	initRowReview
)

type initWizardRow struct {
	Kind  initWizardRowKind
	Index int
}

type initWizardModel struct {
	draft         initDraft
	outputPath    string
	screen        initWizardScreen
	cursor        int
	fields        []textinput.Model
	fieldCursor   int
	formButton    int
	editingIndex  int
	editingColor  string
	themeCursor   int
	errorMessage  string
	statusMessage string
	baseDirectory string
	initialTarget string
	initialExists bool
	replaceCursor int
	hoverRow      int
	hoverAction   string
	width         int
	height        int
	reviewOffset  int
	finished      bool
	cancelled     bool
}

func newInitWizardModel(directory, outputPath string, options initOptions) initWizardModel {
	draft := newInitDraft(directory, options)
	target := draft.target(outputPath)
	_, err := os.Stat(target)
	exists := err == nil
	model := initWizardModel{
		draft:         draft,
		outputPath:    outputPath,
		screen:        initWizardProject,
		baseDirectory: mustAbs(directory),
		initialTarget: target,
		initialExists: exists,
		editingIndex:  -1,
		formButton:    -1,
		hoverRow:      -1,
	}
	if exists {
		model.screen = initWizardReplace
	}
	model.openProjectForm()
	return model
}

func (m initWizardModel) Init() tea.Cmd { return textinput.Blink }

func (m initWizardModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := message.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		for index := range m.fields {
			m.fields[index].Width = max(20, min(60, size.Width-8))
		}
		return m, nil
	}
	if msg, ok := message.(tea.MouseMsg); ok {
		return m.updateMouse(msg)
	}
	msg, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if msg.String() == "ctrl+c" {
		m.cancelled = true
		return m, tea.Quit
	}
	if m.screen == initWizardBuilder {
		m.hoverRow, m.hoverAction = -1, ""
	}
	switch m.screen {
	case initWizardReplace:
		return m.updateReplace(msg)
	case initWizardProject, initWizardService, initWizardAction, initWizardColor:
		return m.updateForm(msg)
	case initWizardBuilder:
		return m.updateBuilder(msg)
	case initWizardAppearance:
		return m.updateAppearance(msg)
	case initWizardReview:
		return m.updateReview(msg)
	default:
		return m, nil
	}
}

func (m initWizardModel) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.screen != initWizardBuilder {
		return m, nil
	}
	rendered := ansi.Strip(m.View())
	lines := strings.Split(rendered, "\n")
	if msg.Y < 0 || msg.Y >= len(lines) {
		return m, nil
	}
	line := lines[msg.Y]
	rows := m.builderRows()
	for index, row := range rows {
		if !strings.Contains(line, m.builderRowIdentity(row)) {
			continue
		}
		m.cursor = index
		m.hoverRow = index
		m.hoverAction = "open"
		if (row.Kind == initRowService || row.Kind == initRowAction) && wizardTextHit(line, msg.X, "Delete") {
			m.hoverAction = "delete"
		}
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		if m.hoverAction == "delete" {
			return m.updateBuilder(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
		}
		return m.updateBuilder(tea.KeyMsg{Type: tea.KeyEnter})
	}
	m.hoverRow, m.hoverAction = -1, ""
	return m, nil
}

func wizardTextHit(line string, x int, label string) bool {
	start := strings.Index(line, label)
	if start < 0 {
		return false
	}
	left := lipgloss.Width(line[:start])
	return x >= left && x < left+lipgloss.Width(label)
}

func (m initWizardModel) builderRowIdentity(row initWizardRow) string {
	switch row.Kind {
	case initRowAppearance:
		return "Appearance"
	case initRowService:
		return m.draft.Services[row.Index].Name
	case initRowAction:
		item := m.draft.Actions[row.Index]
		return item.Owner + "/" + item.Name
	case initRowAddService:
		return "Add service"
	case initRowAddAction:
		return "Add action"
	case initRowReview:
		return "Review and save"
	default:
		return ""
	}
}

func (m initWizardModel) updateReplace(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h", "right", "l", "tab":
		m.replaceCursor = 1 - m.replaceCursor
	case "enter":
		if m.replaceCursor == 0 {
			m.cancelled = true
			return m, tea.Quit
		}
		m.screen = initWizardProject
	case "esc", "q":
		m.cancelled = true
		return m, tea.Quit
	}
	return m, nil
}

func (m initWizardModel) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.formButton >= 0 {
		switch msg.String() {
		case "esc":
			return m.cancelForm()
		case "enter":
			if m.formButton == 0 {
				return m.submitForm()
			}
			return m.cancelForm()
		case "tab", "right", "down":
			if m.formButton == 0 {
				m.formButton = 1
				return m, nil
			}
			return m.focusFormField(0)
		case "shift+tab", "left", "up":
			if m.formButton == 1 {
				m.formButton = 0
				return m, nil
			}
			return m.focusFormField(len(m.fields) - 1)
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		return m.cancelForm()
	case "tab", "down":
		return m.moveField(1)
	case "shift+tab", "up":
		return m.moveField(-1)
	case "enter":
		if m.fieldCursor < len(m.fields)-1 {
			return m.moveField(1)
		}
		return m.focusFormButton(0)
	}
	var cmd tea.Cmd
	m.fields[m.fieldCursor], cmd = m.fields[m.fieldCursor].Update(msg)
	return m, cmd
}

func (m initWizardModel) moveField(delta int) (tea.Model, tea.Cmd) {
	if delta > 0 && m.fieldCursor == len(m.fields)-1 {
		return m.focusFormButton(0)
	}
	if delta < 0 && m.fieldCursor == 0 {
		return m.focusFormButton(1)
	}
	m.fields[m.fieldCursor].Blur()
	m.fieldCursor += delta
	return m, m.fields[m.fieldCursor].Focus()
}

func (m initWizardModel) focusFormButton(index int) (tea.Model, tea.Cmd) {
	m.fields[m.fieldCursor].Blur()
	m.formButton = index
	return m, nil
}

func (m initWizardModel) focusFormField(index int) (tea.Model, tea.Cmd) {
	m.formButton, m.fieldCursor = -1, index
	return m, m.fields[index].Focus()
}

func (m initWizardModel) cancelForm() (tea.Model, tea.Cmd) {
	m.errorMessage = ""
	if m.screen == initWizardProject {
		m.cancelled = true
		return m, tea.Quit
	}
	if m.screen == initWizardColor {
		m.screen = initWizardAppearance
	} else {
		m.screen = initWizardBuilder
	}
	return m, nil
}

func (m initWizardModel) submitForm() (tea.Model, tea.Cmd) {
	m.errorMessage = ""
	switch m.screen {
	case initWizardProject:
		directory, project := strings.TrimSpace(m.fields[0].Value()), strings.TrimSpace(m.fields[1].Value())
		if project == "" || directory == "" {
			m.errorMessage = "Project name and directory are required."
			return m, nil
		}
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(m.baseDirectory, directory)
		}
		m.draft.Project, m.draft.Directory = project, filepath.Clean(directory)
		m.screen = initWizardBuilder
		m.cursor = 0
		return m, nil
	case initWizardService:
		return m.submitService()
	case initWizardAction:
		return m.submitAction()
	case initWizardColor:
		value := strings.ToUpper(strings.TrimSpace(m.fields[0].Value()))
		if !validHexColor(value) {
			m.errorMessage = "Use a six-digit color such as #7AA2F7."
			return m, nil
		}
		if m.editingColor == "accent" {
			m.draft.CustomAccent = value
			m.draft.AccentSet = true
		} else {
			m.draft.CustomBackground = value
			m.draft.BackgroundSet = true
		}
		m.draft.AppearanceConfigured = true
		m.screen = initWizardAppearance
		return m, nil
	}
	return m, nil
}

func (m initWizardModel) submitService() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.fields[0].Value())
	dir := cleanDraftDir(m.fields[1].Value())
	command := strings.TrimSpace(m.fields[2].Value())
	if name == "" || command == "" {
		m.errorMessage = "Service name and command are required."
		return m, nil
	}
	port := 0
	if value := strings.TrimSpace(m.fields[3].Value()); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			m.errorMessage = "Port must be between 1 and 65535."
			return m, nil
		}
		port = parsed
	}
	for index, item := range m.draft.Services {
		if item.Name == name && index != m.editingIndex {
			m.errorMessage = "A service with this name already exists."
			return m, nil
		}
	}
	item := initServiceDraft{Name: name, Dir: dir, Command: command, Port: port}
	if m.editingIndex >= 0 {
		oldName := m.draft.Services[m.editingIndex].Name
		m.draft.Services[m.editingIndex] = item
		if oldName != name {
			for index := range m.draft.Actions {
				if m.draft.Actions[index].Owner == oldName {
					m.draft.Actions[index].Owner = name
				}
			}
		}
		m.statusMessage = "Service updated in the draft."
	} else {
		m.draft.Services = append(m.draft.Services, item)
		m.statusMessage = "Service added to the draft."
	}
	m.screen, m.editingIndex = initWizardBuilder, -1
	return m, nil
}

func (m initWizardModel) submitAction() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.fields[0].Value())
	owner := strings.TrimSpace(m.fields[1].Value())
	dir := cleanDraftDir(m.fields[2].Value())
	command := strings.TrimSpace(m.fields[3].Value())
	if owner == "" {
		owner = projectActionOwner
	}
	if name == "" || command == "" {
		m.errorMessage = "Action name and command are required."
		return m, nil
	}
	if owner != projectActionOwner && !m.hasService(owner) {
		m.errorMessage = fmt.Sprintf("Owner must be %q or an existing service.", projectActionOwner)
		return m, nil
	}
	for index, item := range m.draft.Actions {
		if item.Name == name && item.Owner == owner && index != m.editingIndex {
			m.errorMessage = "An action with this owner and name already exists."
			return m, nil
		}
	}
	item := initActionDraft{Name: name, Owner: owner, Dir: dir, Command: command}
	if m.editingIndex >= 0 {
		m.draft.Actions[m.editingIndex] = item
		m.statusMessage = "Action updated in the draft."
	} else {
		m.draft.Actions = append(m.draft.Actions, item)
		m.statusMessage = "Action added to the draft."
	}
	m.screen, m.editingIndex = initWizardBuilder, -1
	return m, nil
}

func (m initWizardModel) hasService(name string) bool {
	for _, item := range m.draft.Services {
		if item.Name == name {
			return true
		}
	}
	return false
}

func (m initWizardModel) updateBuilder(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.builderRows()
	if len(rows) == 0 {
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		m.cursor = (m.cursor - 1 + len(rows)) % len(rows)
	case "down", "j":
		m.cursor = (m.cursor + 1) % len(rows)
	case "esc", "backspace":
		m.openProjectForm()
		m.screen = initWizardProject
	case "d", "delete":
		row := rows[m.cursor]
		switch row.Kind {
		case initRowService:
			name := m.draft.Services[row.Index].Name
			m.draft.Services = append(m.draft.Services[:row.Index], m.draft.Services[row.Index+1:]...)
			filtered := m.draft.Actions[:0]
			for _, action := range m.draft.Actions {
				if action.Owner != name {
					filtered = append(filtered, action)
				}
			}
			m.draft.Actions = filtered
			m.statusMessage = "Service and its actions removed from the draft."
		case initRowAction:
			m.draft.Actions = append(m.draft.Actions[:row.Index], m.draft.Actions[row.Index+1:]...)
			m.statusMessage = "Action removed from the draft."
		}
		if m.cursor >= len(m.builderRows()) {
			m.cursor = max(0, len(m.builderRows())-1)
		}
	case "enter":
		row := rows[m.cursor]
		switch row.Kind {
		case initRowAppearance:
			m.openAppearance()
		case initRowService:
			m.openServiceForm(row.Index)
		case initRowAction:
			m.openActionForm(row.Index)
		case initRowAddService:
			m.openServiceForm(-1)
		case initRowAddAction:
			m.openActionForm(-1)
		case initRowReview:
			if _, err := m.draft.document(); err != nil {
				m.errorMessage = err.Error()
				return m, nil
			}
			m.screen, m.reviewOffset = initWizardReview, 0
		}
	}
	return m, nil
}

func (m initWizardModel) builderRows() []initWizardRow {
	rows := []initWizardRow{{Kind: initRowAppearance}}
	for index := range m.draft.Services {
		rows = append(rows, initWizardRow{Kind: initRowService, Index: index})
	}
	rows = append(rows, initWizardRow{Kind: initRowAddService})
	for index := range m.draft.Actions {
		rows = append(rows, initWizardRow{Kind: initRowAction, Index: index})
	}
	return append(rows,
		initWizardRow{Kind: initRowAddAction},
		initWizardRow{Kind: initRowReview},
	)
}

func (m initWizardModel) updateAppearance(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.cursor = (m.cursor + 5) % 6
	case "down", "j", "tab":
		m.cursor = (m.cursor + 1) % 6
	case "left", "h":
		m.changeAppearance(-1)
	case "right", "l":
		m.changeAppearance(1)
	case "enter":
		switch m.cursor {
		case 2:
			if m.draft.AccentSource == "custom" {
				m.openColorForm("accent")
			}
		case 3:
			if m.draft.BackgroundSource == "custom" {
				m.openColorForm("background")
			}
		case 5:
			m.screen, m.cursor = initWizardBuilder, 0
		}
	case "esc":
		m.screen, m.cursor = initWizardBuilder, 0
	}
	return m, nil
}

func (m *initWizardModel) changeAppearance(delta int) {
	switch m.cursor {
	case 0:
		m.draft.AppearanceConfigured = !m.draft.AppearanceConfigured
	case 1:
		names := ui.ThemeNames()
		m.themeCursor = (m.themeCursor + delta + len(names)) % len(names)
		m.draft.Theme = names[m.themeCursor]
		m.draft.ThemeSet = true
		m.draft.AppearanceConfigured = true
	case 2:
		if m.draft.AccentSource == "theme" {
			m.draft.AccentSource = "custom"
			if m.draft.CustomAccent == "" {
				if theme, ok := ui.LookupTheme(m.draft.Theme); ok {
					m.draft.CustomAccent = theme.Accent
				}
			}
		} else {
			m.draft.AccentSource = "theme"
		}
		m.draft.AccentSet = true
		m.draft.AppearanceConfigured = true
	case 3:
		values := []string{config.UIBackgroundTerminal, config.UIBackgroundTheme, "custom"}
		m.draft.BackgroundSource = cycleString(values, m.draft.BackgroundSource, delta)
		if m.draft.BackgroundSource == "custom" && m.draft.CustomBackground == "" {
			if theme, ok := ui.LookupTheme(m.draft.Theme); ok {
				m.draft.CustomBackground = theme.Background
			}
		}
		m.draft.BackgroundSet = true
		m.draft.AppearanceConfigured = true
	case 4:
		values := []string{config.UIColorModeAuto, config.UIColorModeDark, config.UIColorModeLight}
		m.draft.ColorMode = cycleString(values, m.draft.ColorMode, delta)
		m.draft.ColorModeSet = true
		m.draft.AppearanceConfigured = true
	}
}

func cycleString(values []string, current string, delta int) string {
	index := 0
	for candidate, value := range values {
		if value == current {
			index = candidate
			break
		}
	}
	return values[(index+delta+len(values))%len(values)]
}

func (m initWizardModel) updateReview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.reviewOffset = max(0, m.reviewOffset-1)
	case "down", "j":
		m.reviewOffset = min(m.maxReviewOffset(), m.reviewOffset+1)
	case "pgup", "ctrl+u":
		m.reviewOffset = max(0, m.reviewOffset-max(1, m.reviewPageHeight()/2))
	case "pgdown", "ctrl+d":
		m.reviewOffset = min(m.maxReviewOffset(), m.reviewOffset+max(1, m.reviewPageHeight()/2))
	case "esc", "backspace":
		m.screen = initWizardBuilder
	case "enter", "y":
		m.finished = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *initWizardModel) openProjectForm() {
	m.errorMessage, m.statusMessage = "", ""
	m.formButton = -1
	m.fields = []textinput.Model{
		newInitInput(m.draft.Directory, "Directory"),
		newInitInput(m.draft.Project, "Project name"),
	}
	m.fieldCursor = 0
	_ = m.fields[0].Focus()
}

func (m *initWizardModel) openServiceForm(index int) {
	m.errorMessage, m.statusMessage = "", ""
	item := initServiceDraft{}
	if index >= 0 {
		item = m.draft.Services[index]
	}
	port := ""
	if item.Port != 0 {
		port = strconv.Itoa(item.Port)
	}
	m.fields = []textinput.Model{
		newInitInput(item.Name, "Service name"),
		newInitInput(item.Dir, "."),
		newInitInput(item.Command, "Command"),
		newInitInput(port, "Port (optional)"),
	}
	m.fieldCursor, m.formButton, m.editingIndex, m.screen = 0, -1, index, initWizardService
	_ = m.fields[0].Focus()
}

func (m *initWizardModel) openActionForm(index int) {
	m.errorMessage, m.statusMessage = "", ""
	item := initActionDraft{Owner: projectActionOwner}
	if index >= 0 {
		item = m.draft.Actions[index]
	}
	m.fields = []textinput.Model{
		newInitInput(item.Name, "Action name"),
		newInitInput(item.Owner, "Owner"),
		newInitInput(item.Dir, "."),
		newInitInput(item.Command, "Command"),
	}
	m.fieldCursor, m.formButton, m.editingIndex, m.screen = 0, -1, index, initWizardAction
	_ = m.fields[0].Focus()
}

func (m *initWizardModel) openAppearance() {
	m.errorMessage, m.statusMessage = "", ""
	m.screen, m.cursor = initWizardAppearance, 0
	for index, name := range ui.ThemeNames() {
		if name == m.draft.Theme {
			m.themeCursor = index
			break
		}
	}
}

func (m *initWizardModel) openColorForm(kind string) {
	m.errorMessage, m.statusMessage = "", ""
	value := m.draft.CustomBackground
	if kind == "accent" {
		value = m.draft.CustomAccent
	}
	m.editingColor = kind
	m.fields = []textinput.Model{newInitInput(value, "#RRGGBB")}
	m.fieldCursor, m.formButton, m.screen = 0, -1, initWizardColor
	_ = m.fields[0].Focus()
}

func newInitInput(value, placeholder string) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = placeholder
	input.CharLimit = 512
	input.Width = 60
	input.SetValue(value)
	input.CursorEnd()
	return input
}

func validHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(value[1:], 16, 24)
	return err == nil
}

func (m initWizardModel) View() string {
	var body string
	switch m.screen {
	case initWizardReplace:
		body = m.viewReplace()
	case initWizardProject:
		body = m.viewForm("Create a Kranz project", []string{"Directory", "Project name"})
	case initWizardBuilder:
		body = m.viewBuilder()
	case initWizardService:
		body = m.viewForm(formTitle("service", m.editingIndex), []string{"Name", "Directory", "Command", "Port (optional)"})
	case initWizardAction:
		body = m.viewForm(formTitle("action", m.editingIndex), []string{"Name", "Owner (project or service)", "Directory", "Command"})
	case initWizardAppearance:
		body = m.viewAppearance()
	case initWizardColor:
		body = m.viewForm("Custom "+m.editingColor, []string{"Color"})
	case initWizardReview:
		body = m.viewReview()
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(body)
}

func formTitle(kind string, index int) string {
	if index >= 0 {
		return "Edit " + kind
	}
	return "Add " + kind
}

func (m initWizardModel) viewReplace() string {
	buttons := []string{"Cancel", "Create replacement draft"}
	for index := range buttons {
		buttons[index] = button(buttons[index], index == m.replaceCursor)
	}
	warning := panelStyle().BorderForeground(lipgloss.Color("#F59E0B")).Render(
		sectionTitle("EXISTING CONFIGURATION") + "\n" +
			filepath.Base(m.initialTarget) + " will remain untouched until the final review.")
	return titleStyle().Render("Configuration already exists") + "\n\n" + warning + "\n\n" +
		strings.Join(buttons, "  ") + "\n\n" + controls(
		control("←/→", "choose"), control("Enter", "continue"), control("Esc", "cancel"),
	)
}

func (m initWizardModel) viewForm(title string, labels []string) string {
	lines := []string{titleStyle().Render(title), mutedStyle().Render("Enter or Tab moves forward. From the last field, choose Save or Cancel.")}
	fieldWidth := 64
	if m.width > 0 {
		fieldWidth = max(24, min(fieldWidth, m.width-8))
	}
	for index, label := range labels {
		focused := m.formButton < 0 && index == m.fieldCursor
		labelStyle := mutedStyle()
		if focused {
			labelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#38BDF8"))
		}
		lines = append(lines, labelStyle.Render(label), fieldBox(m.fields[index].View(), focused, fieldWidth))
	}
	saveLabel := "Save"
	switch m.screen {
	case initWizardProject:
		saveLabel = "Continue"
	case initWizardColor:
		saveLabel = "Apply"
	}
	actions := button(saveLabel, m.formButton == 0) + "  " + button("Cancel", m.formButton == 1)
	hint := controls(control("Tab", "next"), control("Shift+Tab", "previous"))
	if m.formButton >= 0 {
		hint = controls(control("←/→", "choose"), control("Enter", "confirm"), control("Esc", "cancel"))
	}
	return m.withMessages(strings.Join(lines, "\n") + "\n\n" + actions + "\n\n" + hint)
}

func (m initWizardModel) viewBuilder() string {
	rows := m.builderRows()
	project := panelStyle().Padding(0, 2).Render(
		sectionTitle("PROJECT") + "\n" +
			lipgloss.NewStyle().Bold(true).Render(m.draft.Project) + "\n" + mutedStyle().Render(m.draft.Directory),
	)
	lines := []string{project, ""}
	available := len(rows)
	if m.height > 0 {
		available = max(3, (m.height-13)/2)
	}
	start := 0
	if m.cursor >= available {
		start = m.cursor - available + 1
	}
	end := min(len(rows), start+available)
	if start > 0 {
		lines = append(lines, mutedStyle().Render(fmt.Sprintf("  ↑ %d more", start)))
	}
	previousSection := ""
	for index := start; index < end; index++ {
		row := rows[index]
		section := m.builderSection(row)
		if section != previousSection {
			if previousSection != "" {
				lines = append(lines, "")
			}
			lines = append(lines, sectionTitle(section))
			previousSection = section
		}
		lines = append(lines, m.renderBuilderRow(row, index, index == m.cursor))
	}
	if end < len(rows) {
		lines = append(lines, mutedStyle().Render(fmt.Sprintf("  ↓ %d more", len(rows)-end)))
	}
	return m.withMessages(strings.Join(lines, "\n") + "\n\n" + controls(
		control("↑/↓", "select"), control("Enter", "open"), control("D", "delete"), control("Esc", "project"),
	))
}

func (m initWizardModel) builderSection(row initWizardRow) string {
	switch row.Kind {
	case initRowAppearance:
		return "APPEARANCE"
	case initRowService, initRowAddService:
		return fmt.Sprintf("SERVICES  %d", len(m.draft.Services))
	case initRowAction, initRowAddAction:
		return fmt.Sprintf("ACTIONS  %d", len(m.draft.Actions))
	default:
		return "FINISH"
	}
}

func (m initWizardModel) renderBuilderRow(row initWizardRow, index int, selected bool) string {
	label := m.builderRowLabel(row)
	actions := ""
	switch row.Kind {
	case initRowService, initRowAction:
		deleteHovered := selected && m.hoverRow == index && m.hoverAction == "delete"
		actions = "  " + button("Edit", selected && !deleteHovered) + " " + button("Delete", deleteHovered)
	case initRowAppearance:
		actions = "  " + button("Configure", selected)
	case initRowAddService, initRowAddAction:
		actions = "  " + button("Add", selected)
	case initRowReview:
		actions = "  " + button("Open", selected)
	}
	content := "  " + label + actions
	width := max(40, lipgloss.Width(content)+2)
	if m.width > 0 {
		width = max(28, min(84, m.width-6))
	}
	label = ansi.Truncate(label, max(8, width-lipgloss.Width(actions)-5), "…")
	content = "  " + label + actions
	style := lipgloss.NewStyle().Padding(0, 1).Width(width)
	if selected {
		style = style.Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0C4A6E", Dark: "#E0F2FE"}).
			Background(lipgloss.AdaptiveColor{Light: "#BAE6FD", Dark: "#164E63"})
		content = "› " + label + actions
	}
	return style.Render(content)
}

func (m initWizardModel) builderRowLabel(row initWizardRow) string {
	switch row.Kind {
	case initRowAppearance:
		if !m.draft.AppearanceConfigured {
			return "Appearance   Inherit global defaults"
		}
		return fmt.Sprintf("Appearance   %s · %s · %s", m.draft.Theme, m.draft.BackgroundSource, m.draft.ColorMode)
	case initRowService:
		item := m.draft.Services[row.Index]
		port := ""
		if item.Port != 0 {
			port = fmt.Sprintf(" · :%d", item.Port)
		}
		return fmt.Sprintf("%s   %s · %s%s", item.Name, cleanDraftDir(item.Dir), item.Command, port)
	case initRowAction:
		item := m.draft.Actions[row.Index]
		return fmt.Sprintf("%s/%s   %s", item.Owner, item.Name, item.Command)
	case initRowAddService:
		return "+ Add service"
	case initRowAddAction:
		return "+ Add action"
	case initRowReview:
		return "Review and save"
	default:
		return ""
	}
}

func (m initWizardModel) viewAppearance() string {
	accent := m.draft.AccentSource
	if accent == "custom" {
		accent += " " + m.draft.CustomAccent
	}
	background := m.draft.BackgroundSource
	if background == "custom" {
		background += " " + m.draft.CustomBackground
	}
	configured := "inherit defaults"
	if m.draft.AppearanceConfigured {
		configured = "project"
	}
	values := [][2]string{
		{"Save appearance", configured},
		{"Theme", m.draft.Theme},
		{"Accent", accent},
		{"Background", background},
		{"Color mode", m.draft.ColorMode},
		{"Done", "Return to project"},
	}
	lines := []string{sectionTitle("PROJECT APPEARANCE")}
	for index, value := range values {
		lines = append(lines, renderSettingRow(value[0], value[1], index == m.cursor))
	}
	settings := strings.Join(lines, "\n")
	preview := m.appearancePreview()
	content := settings + "\n\n" + preview
	if m.width >= 104 {
		content = lipgloss.JoinHorizontal(lipgloss.Top, settings, "    ", preview)
	}
	return m.withMessages(titleStyle().Render("Appearance") + "\n\n" + content + "\n\n" + controls(
		control("←/→", "change"), control("Enter", "edit / done"), control("Esc", "back"),
	))
}

func renderSettingRow(label, value string, selected bool) string {
	label = strings.ToUpper(label)
	content := fmt.Sprintf("%-16s │ %s", label, value)
	style := lipgloss.NewStyle().Padding(0, 1).Width(46)
	if selected {
		return style.Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0C4A6E", Dark: "#E0F2FE"}).
			Background(lipgloss.AdaptiveColor{Light: "#BAE6FD", Dark: "#164E63"}).
			Render("› " + content)
	}
	labelCell := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#64748B", Dark: "#94A3B8"}).
		Render(fmt.Sprintf("%-16s", label))
	separator := mutedStyle().Render(" │ ")
	valueCell := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#F1F5F9"}).
		Render(value)
	return style.Render("  " + labelCell + separator + valueCell)
}

func (m initWizardModel) appearancePreview() string {
	accent, background := "", ""
	if m.draft.AccentSource == "custom" {
		accent = m.draft.CustomAccent
	}
	if m.draft.BackgroundSource == "custom" {
		background = m.draft.CustomBackground
	}
	dark := m.draft.ColorMode != config.UIColorModeLight
	theme, err := ui.BuildTheme(m.draft.Theme, accent, background, dark)
	if err != nil {
		return errorStyle().Render(err.Error())
	}
	previewWidth := 54
	if m.width > 0 {
		previewWidth = max(28, min(previewWidth, m.width-8))
	}
	canvas := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Background(lipgloss.Color(theme.Background)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Border)).
		Padding(0, 1).
		Width(previewWidth)
	header := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.AccentText)).Bold(true).Render("KRANZ  PROJECT PREVIEW")
	running := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Green)).Render("● running")
	stopped := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Render("○ stopped")
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Background(lipgloss.Color(theme.Background)).
		Render(" ")
	port := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Data)).
		Background(lipgloss.Color(theme.Background)).
		Render(":3000")
	content := strings.Join([]string{
		header,
		"",
		"web       " + running + separator + port,
		fmt.Sprintf("worker    %s", stopped),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Info)).Render("Ready to start local services"),
	}, "\n")
	mode := m.draft.ColorMode
	if mode == config.UIColorModeAuto {
		mode += " (dark terminal preview)"
	}
	caption := "Preview: background " + m.draft.BackgroundSource + " · " + mode
	if !m.draft.AppearanceConfigured {
		caption += " · not saved (project inherits)"
	}
	caption = ansi.Truncate(caption, previewWidth+2, "…")
	return canvas.Render(content) + "\n" + mutedStyle().Render(caption)
}

func (m initWizardModel) viewReview() string {
	document, err := m.draft.document()
	if err != nil {
		return titleStyle().Render("Cannot review draft") + "\n\n" + errorStyle().Render(err.Error()) + "\n\n" + controls(control("Esc", "back"))
	}
	rendered, err := renderDocument(document)
	if err != nil {
		return errorStyle().Render(err.Error())
	}
	target := m.draft.target(m.outputPath)
	heading := "Create " + filepath.Base(target)
	content := rendered
	if previous, readErr := os.ReadFile(target); readErr == nil {
		heading = "Replace " + filepath.Base(target)
		content = renderLineDiff(string(previous), rendered)
	}
	contentLines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	pageHeight := m.reviewPageHeight()
	maxOffset := max(0, len(contentLines)-pageHeight)
	m.reviewOffset = min(m.reviewOffset, maxOffset)
	end := min(len(contentLines), m.reviewOffset+pageHeight)
	visible := contentLines[m.reviewOffset:end]
	scroll := ""
	if len(contentLines) > pageHeight {
		scroll = fmt.Sprintf(" · ↑/↓ scroll %d-%d/%d", m.reviewOffset+1, end, len(contentLines))
	}
	documentPanel := panelStyle().Render(strings.Join(visible, "\n"))
	return titleStyle().Render(heading) + "\n" + mutedStyle().Render("Review the exact configuration before it is written.") +
		"\n\n" + documentPanel + "\n\n" + button("Save configuration  Enter", true) + "  " + button("Back  Esc", false) +
		"\n\n" + controls(control("↑/↓", "scroll"), control("Ctrl+C", "cancel")) + mutedStyle().Render(scroll)
}

func (m initWizardModel) reviewPageHeight() int {
	if m.height <= 0 {
		return 18
	}
	return max(4, m.height-12)
}

func (m initWizardModel) maxReviewOffset() int {
	document, err := m.draft.document()
	if err != nil {
		return 0
	}
	rendered, err := renderDocument(document)
	if err != nil {
		return 0
	}
	if previous, readErr := os.ReadFile(m.draft.target(m.outputPath)); readErr == nil {
		rendered = renderLineDiff(string(previous), rendered)
	}
	return max(0, len(strings.Split(strings.TrimSuffix(rendered, "\n"), "\n"))-m.reviewPageHeight())
}

func renderLineDiff(before, after string) string {
	left, right := strings.Split(strings.TrimSuffix(before, "\n"), "\n"), strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	rows, columns := len(left)+1, len(right)+1
	lcs := make([][]int, rows)
	for index := range lcs {
		lcs[index] = make([]int, columns)
	}
	for i := len(left) - 1; i >= 0; i-- {
		for j := len(right) - 1; j >= 0; j-- {
			if left[i] == right[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	lines := []string{"--- current", "+++ proposed"}
	for i, j := 0, 0; i < len(left) || j < len(right); {
		switch {
		case i < len(left) && j < len(right) && left[i] == right[j]:
			lines = append(lines, "  "+left[i])
			i, j = i+1, j+1
		case j < len(right) && (i == len(left) || lcs[i][j+1] >= lcs[i+1][j]):
			lines = append(lines, "+ "+right[j])
			j++
		default:
			lines = append(lines, "- "+left[i])
			i++
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m initWizardModel) withMessages(body string) string {
	if m.errorMessage != "" {
		body += "\n\n" + errorStyle().Render(m.errorMessage)
	}
	if m.statusMessage != "" {
		body += "\n\n" + mutedStyle().Render(m.statusMessage)
	}
	return body
}

func titleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#38BDF8")).MarginBottom(0)
}
func mutedStyle() lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color("#8D99A8")) }
func errorStyle() lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color("#FB7185")) }

func panelStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#94A3B8", Dark: "#475569"}).
		Padding(1, 2)
}

func sectionTitle(value string) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#475569", Dark: "#94A3B8"}).
		Render(value)
}

func fieldBox(value string, focused bool, width int) string {
	border := lipgloss.AdaptiveColor{Light: "#CBD5E1", Dark: "#475569"}
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1).Width(width)
	if focused {
		style = style.BorderForeground(lipgloss.Color("#38BDF8"))
	}
	return style.Render(value)
}

func button(label string, primary bool) string {
	style := lipgloss.NewStyle().Bold(true).Padding(0, 1).
		Foreground(lipgloss.AdaptiveColor{Light: "#334155", Dark: "#CBD5E1"}).
		Background(lipgloss.AdaptiveColor{Light: "#E2E8F0", Dark: "#334155"})
	if primary {
		style = style.Foreground(lipgloss.AdaptiveColor{Light: "#082F49", Dark: "#082F49"}).
			Background(lipgloss.Color("#38BDF8"))
	}
	return style.Render(label)
}

func keyCap(key string) string {
	return lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color("#38BDF8")).
		Render("[" + key + "]")
}

func control(key, label string) string {
	return keyCap(key) + " " + mutedStyle().Render(label)
}

func controls(items ...string) string { return strings.Join(items, "   ") }

func runInitWizard(directory, outputPath string, options initOptions, input io.Reader, output io.Writer) (initDraft, bool, error) {
	model := newInitWizardModel(directory, outputPath, options)
	program := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen(), tea.WithMouseCellMotion())
	final, err := program.Run()
	if err != nil {
		return initDraft{}, false, err
	}
	result, ok := final.(initWizardModel)
	if !ok {
		return initDraft{}, false, fmt.Errorf("init wizard returned an unexpected model")
	}
	return result.draft, result.finished && !result.cancelled, nil
}
