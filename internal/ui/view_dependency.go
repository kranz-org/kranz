package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

type dependencyModal struct {
	roots        []string
	graph        app.Graph
	plan         bool
	dependencies bool
	dependents   bool
	operation    string
	selectedOnly bool
	confirming   bool
	runID        int
	runMessage   string
	runFailed    bool
	graphOffset  int
	planOffset   int
}

func (state *dependencyModal) scrollOffset() *int {
	if state.plan {
		return &state.planOffset
	}
	return &state.graphOffset
}

func (m *Model) dependencyFocusedService() *app.ServiceSnapshot {
	if m.listMode == listTags {
		return m.focusedTagService()
	}
	if m.focusedAction != nil || m.focusedActionGroup != "" {
		return nil
	}
	return m.FocusedService()
}

func (m *Model) dependencyTargetNames() []string {
	if len(m.selected) > 0 || len(m.selectedTags) > 0 {
		return m.selectedTargetNames()
	}
	if svc := m.dependencyFocusedService(); svc != nil {
		return []string{svc.Name}
	}
	return nil
}

func (m *Model) openDependencies() {
	state := dependencyModal{
		roots: m.dependencyTargetNames(), dependencies: true, dependents: true, operation: "start", graphOffset: -1,
	}
	state.plan = len(state.roots) > 1 || len(state.roots) > 0 && m.dependencyFocusedService() == nil
	if len(state.roots) > 0 {
		// Read the graph once per opening. Live labels use the dashboard's
		// refreshed snapshots, so repainting the modal never needs an RPC.
		state.graph = m.app.Graph()
	}
	m.dependencyModal = state
	m.mode = ModeDependencies
}

func (m *Model) handleDependencyKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := &m.dependencyModal
	switch {
	case key.Matches(msg, m.keys.Up):
		m.scrollDependencies(-1)
	case key.Matches(msg, m.keys.Down):
		m.scrollDependencies(1)
	case msg.String() == "pgup":
		m.scrollDependencies(-m.dependencyBodyHeight(m.dependencyControlsLayout().height()))
	case msg.String() == "pgdown":
		m.scrollDependencies(m.dependencyBodyHeight(m.dependencyControlsLayout().height()))
	case msg.String() == "home":
		*state.scrollOffset() = 0
	case msg.String() == "end":
		*state.scrollOffset() = m.maxDependencyOffset()
	case msg.String() == "esc":
		if state.confirming {
			state.confirming = false
		} else {
			m.mode = ModeNormal
		}
	case msg.String() == "tab":
		state.plan, state.confirming = !state.plan, false
	case msg.String() == "left":
		m.cycleDependencyOperation(-1)
	case msg.String() == "right":
		m.cycleDependencyOperation(1)
	case msg.String() == "s":
		m.chooseDependencyOperation("start")
	case msg.String() == "x":
		m.chooseDependencyOperation("stop")
	case msg.String() == "r":
		m.chooseDependencyOperation("restart")
	case state.runID == 0 && msg.String() == "f":
		state.selectedOnly, state.confirming, state.runMessage, state.planOffset = !state.selectedOnly, false, "", 0
	case msg.String() == "enter":
		return m.runDependencyOperation()
	case !state.plan && msg.String() == "d":
		state.dependencies, state.graphOffset = !state.dependencies, -1
	case !state.plan && msg.String() == "u":
		state.dependents, state.graphOffset = !state.dependents, -1
	}
	return m, nil
}

// A shortcut for the selected operation asks for confirmation. Keeping execution on
// Enter prevents key repeat from running a plan opened with the same shortcut.
func (m *Model) chooseDependencyOperation(operation string) {
	state := &m.dependencyModal
	if state.runID != 0 {
		return
	}
	state.runMessage = ""
	if state.operation == operation {
		preview, err := m.previewDependencyOperation(operation, state.selectedOnly)
		state.confirming = preview.runnable(err)
		return
	}
	state.operation, state.confirming, state.planOffset = operation, false, 0
}

var dependencyOperationOrder = [...]string{"start", "stop", "restart"}

func (m *Model) cycleDependencyOperation(step int) {
	index := slices.Index(dependencyOperationOrder[:], m.dependencyModal.operation)
	next := (index + step + len(dependencyOperationOrder)) % len(dependencyOperationOrder)
	m.chooseDependencyOperation(dependencyOperationOrder[next])
}

var dependencyOperationLabels = map[string]string{"start": "Start", "stop": "Stop", "restart": "Restart"}

func (m *Model) dependencyControlGroups(runnable bool) [][]mouseKeyBinding {
	state := m.dependencyModal
	operations := []mouseKeyBinding{
		{label: "[s] Start", key: "s"},
		{label: "[x] Stop", key: "x"},
		{label: "[r] Restart", key: "r"},
	}
	navigation := []mouseKeyBinding{
		{label: "[←] Prev plan", key: "left"},
		{label: "[→] Next plan", key: "right"},
		{label: "[↑/k] Up", key: "up"},
		{label: "[↓/j] Down", key: "down"},
		{label: "[Esc] Back", key: "esc"},
	}
	var run, view []mouseKeyBinding
	if state.runID == 0 && state.runMessage == "" && runnable {
		verb := "Run "
		if state.confirming {
			verb = "Confirm "
		}
		label := verb + dependencyOperationLabels[state.operation]
		shortcut := "[Enter] "
		if 2+lipgloss.Width(shortcut+label) > m.dependencyWidth() {
			shortcut = "[↵] "
			if 2+lipgloss.Width(shortcut+label) > m.dependencyWidth() {
				label = strings.TrimSuffix(verb, " ")
			}
		}
		if 2+lipgloss.Width(shortcut+label) > m.dependencyWidth() {
			label = ""
			shortcut = "[↵]"
		}
		run = []mouseKeyBinding{{label: shortcut + label, key: "enter"}}
	}
	if state.plan {
		view = []mouseKeyBinding{
			{label: dependencyToggle("[f] Selected only", state.selectedOnly), key: "f"},
			{label: "[Tab] Graph", key: "tab"},
		}
	} else {
		view = []mouseKeyBinding{
			{label: dependencyToggle("[d] Dependencies", state.dependencies), key: "d"},
			{label: dependencyToggle("[u] Dependents", state.dependents), key: "u"},
			{label: dependencyToggle("[f] Selected only", state.selectedOnly), key: "f"},
			{label: "[Tab] Steps", key: "tab"},
		}
	}
	return [][]mouseKeyBinding{run, operations, view, navigation}
}

func dependencyToggle(label string, enabled bool) string {
	if enabled {
		return label + " ✓"
	}
	return label + " ○"
}

func (m *Model) dependencyWidth() int {
	return flushModalContentWidth(m.width, 84)
}

type dependencyControlsLayout struct {
	action     []string
	operations []string
	footer     []string
	groups     [][]mouseKeyBinding
}

func (layout dependencyControlsLayout) height() int {
	return len(layout.action) + len(layout.operations) + len(layout.footer)
}

func (m *Model) dependencyControlsLayout() dependencyControlsLayout {
	state := m.dependencyModal
	width := m.dependencyWidth()
	preview, err := m.previewDependencyOperation(state.operation, state.selectedOnly)
	groups := m.dependencyControlGroups(preview.runnable(err))
	dim := lipgloss.NewStyle().Foreground(ColorDim)
	accent := PrimaryButtonStyle.Padding(0)
	render := func(controls []mouseKeyBinding, style lipgloss.Style) []string {
		var labels []string
		for _, control := range controls {
			labels = append(labels, control.label)
		}
		return renderModalShortcutRows(labels, width, style)
	}
	// Both views reserve one action row, regardless of availability,
	// confirmation, or operation progress.
	layout := dependencyControlsLayout{groups: groups, action: []string{""}}
	switch {
	case state.runID != 0:
		layout.action = []string{"  " + StartingBadgeStyle.Render("Running "+dependencyOperationLabels[state.operation]+"…")}
	case state.runMessage != "":
		style := accent
		if state.runFailed {
			style = FailedBadgeStyle
		}
		summary := strings.Join(strings.Fields(state.runMessage), " ")
		layout.action = []string{"  " + style.Render(ansi.Truncate(summary, max(1, width-2), "…"))}
	case len(groups[0]) > 0:
		layout.action = render(groups[0], accent)
	case err != nil:
		message := strings.Join(strings.Fields(err.Error()), " ")
		layout.action[0] = "  " + FailedBadgeStyle.Render(ansi.Truncate(message, max(1, width-2), "…"))
	case len(preview.Requested) > 0:
		message := "No service changes"
		if state.operation == "restart" && len(preview.restartTargets) == 0 {
			message = "No running targets"
		}
		layout.action[0] = "  " + dim.Render(ansi.Truncate(message, max(1, width-2), "…"))
	}
	active := "[s] Start"
	switch state.operation {
	case "stop":
		active = "[x] Stop"
	case "restart":
		active = "[r] Restart"
	}
	layout.operations = render(groups[1], dim)
	for i, row := range layout.operations {
		plain := ansi.Strip(row)
		if at := strings.Index(plain, active); at >= 0 {
			start := lipgloss.Width(plain[:at])
			end := start + lipgloss.Width(active)
			layout.operations[i] = ansi.Cut(row, 0, start) + SelectionStyle.Render(active) +
				ansi.Cut(row, end, lipgloss.Width(row))
			break
		}
	}
	layout.footer = append(render(groups[2], dim), render(groups[3], dim)...)

	// Reserve one body row and prioritize the action, view switch, and Back
	// when there is too little height for the complete set of shortcuts.
	budget := max(0, m.height-modalVerticalChrome-2)
	if layout.height() > budget {
		layout.action = layout.action[:min(len(layout.action), budget)]
		budget -= len(layout.action)
		viewSwitch := groups[2][len(groups[2])-1]
		back := groups[3][len(groups[3])-1]
		layout.footer = render([]mouseKeyBinding{viewSwitch, back}, dim)
		layout.footer = layout.footer[:min(len(layout.footer), budget)]
		budget -= len(layout.footer)
		layout.operations = layout.operations[:min(len(layout.operations), budget)]
	}
	return layout
}

func (m *Model) handleDependencyMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	layout := m.dependencyControlsLayout()
	content := m.renderDependencyModal(layout)
	height, width := lipgloss.Height(content), min(m.width, lipgloss.Width(content))
	top, left := max(0, (m.height-height)/2), max(0, (m.width-width)/2)
	msg.X -= left + 2 // flush modal padding
	msg.Y -= top
	operationsRow := ModalStyle.GetPaddingTop() + 1
	actionRow := operationsRow + len(layout.operations)
	footerRow := height - ModalStyle.GetPaddingBottom() - len(layout.footer)

	switch {
	case msg.Y >= footerRow:
		msg.Y -= footerRow
		return m.handleMouseKeyBindings(strings.Join(layout.footer, "\n"), msg, slices.Concat(layout.groups[2:]...), m.handleDependencyKeys)
	case msg.Y >= actionRow:
		msg.Y -= actionRow
		return m.handleMouseKeyBindings(strings.Join(layout.action, "\n"), msg, layout.groups[0], m.handleDependencyKeys)
	case msg.Y >= operationsRow:
		msg.Y -= operationsRow
		return m.handleMouseKeyBindings(strings.Join(layout.operations, "\n"), msg, layout.groups[1], m.handleDependencyKeys)
	default:
		return m, nil
	}
}

func (m *Model) dependencyBodyHeight(controlRows int) int {
	available := m.height - modalVerticalChrome - 1 - controlRows
	if available >= 3 {
		available -= 2
	}
	return min(24, max(1, available))
}

func (m *Model) maxDependencyOffset() int {
	body, _ := m.dependencyBody()
	return max(0, len(body)-m.dependencyBodyHeight(m.dependencyControlsLayout().height()))
}

func (m *Model) scrollDependencies(delta int) {
	body, rootRow := m.dependencyBody()
	offset, maxOffset := m.dependencyViewport(len(body), rootRow, m.dependencyBodyHeight(m.dependencyControlsLayout().height()))
	*m.dependencyModal.scrollOffset() = min(maxOffset, max(0, offset+delta))
}

func (m *Model) dependencyViewport(rows, rootRow, height int) (offset, maxOffset int) {
	maxOffset = max(0, rows-height)
	offset = *m.dependencyModal.scrollOffset()
	if offset < 0 {
		beforeRoots := min((height-1)/2, max(0, height-len(m.dependencyModal.roots)))
		offset = max(0, rootRow-beforeRoots)
	}
	return min(offset, maxOffset), maxOffset
}

func (m *Model) dependencyModalContent() string {
	return m.renderDependencyModal(m.dependencyControlsLayout())
}

func (m *Model) renderDependencyModal(layout dependencyControlsLayout) string {
	state := m.dependencyModal
	body, rootRow := m.dependencyBody()
	height := m.dependencyBodyHeight(layout.height())
	offset, maxOffset := m.dependencyViewport(len(body), rootRow, height)
	width := m.dependencyWidth()

	title := " Dependency Graph "
	if state.plan {
		label := dependencyOperationLabels[state.operation]
		title = " Operation Plan — " + label + " "
		if lipgloss.Width(title)+ModalTitleStyle.GetHorizontalPadding() > width {
			title = " Plan — " + label + " "
		}
	}
	header := ModalTitleStyle.Render(title)
	if maxOffset > 0 {
		counter := fmt.Sprintf("%d–%d/%d", offset+1, min(offset+height, len(body)), len(body))
		if gap := width - lipgloss.Width(header) - lipgloss.Width(counter); gap >= 2 {
			header += strings.Repeat(" ", gap) + ContextBarStyle.Render(counter)
		}
	}
	lines := []string{ansi.Truncate(header, width, "…")}
	lines = append(lines, layout.operations...)
	lines = append(lines, layout.action...)
	withGaps := m.height >= modalVerticalChrome+4+layout.height()
	if withGaps {
		lines = append(lines, "")
	}
	for row := 0; row < height; row++ {
		line := ""
		if index := offset + row; index < len(body) {
			line = body[index]
		}
		lines = append(lines, padPlainLine(line, width))
	}
	if withGaps {
		lines = append(lines, "")
	}
	lines = append(lines, layout.footer...)
	return m.renderFlushModal(strings.Join(lines, "\n"))
}

func (m *Model) dependencyBody() ([]string, int) {
	width := m.dependencyWidth()
	if m.dependencyModal.plan {
		return m.dependencyPlanBody(width), 0
	}
	return m.dependencyGraphBody(width)
}

func (m *Model) dependencyGraphBody(width int) ([]string, int) {
	state := m.dependencyModal
	if len(state.roots) == 0 {
		return modalTextRows("Select services or focus a service to inspect its dependency graph.", width), 0
	}
	nodes := make(map[string]app.GraphNode)
	for _, node := range state.graph.Nodes {
		if node.Kind == app.GraphNodeService {
			nodes[node.ID] = node
		}
	}
	rootNumbers := make(map[string]int, len(state.roots))
	for index, root := range state.roots {
		rootNumbers[root] = index + 1
	}
	dependencies, dependents := make(map[string][]string), make(map[string][]string)
	conditions := make(map[[2]string]string)
	var rootEdges []app.GraphEdge
	for _, edge := range state.graph.Edges {
		_, fromService := nodes[edge.From]
		_, toService := nodes[edge.To]
		if edge.Type != app.GraphEdgeDependency || !fromService || !toService {
			continue
		}
		pair := [2]string{edge.From, edge.To}
		if _, exists := conditions[pair]; exists {
			continue
		}
		conditions[pair] = dependencyConditionLabel(edge.Condition)
		switch {
		case rootNumbers[edge.From] > 0 && rootNumbers[edge.To] > 0:
			rootEdges = append(rootEdges, edge)
		case rootNumbers[edge.From] > 0:
			dependents[edge.From] = append(dependents[edge.From], edge.To)
		case rootNumbers[edge.To] > 0:
			dependencies[edge.To] = append(dependencies[edge.To], edge.From)
		default:
			dependencies[edge.To] = append(dependencies[edge.To], edge.From)
			dependents[edge.From] = append(dependents[edge.From], edge.To)
		}
	}
	statuses := m.dependencyStatuses()
	graphStatus := func(name string) dependencyServiceStatus {
		if status, ok := statuses[name]; ok {
			return status
		}
		if node, ok := nodes[name]; ok {
			return m.dependencyStatusSnapshot(&app.ServiceSnapshot{State: config.ServiceState{Status: node.State}})
		}
		return dependencyStatus(name, statuses)
	}
	label := func(name string) string {
		return graphStatus(name).render(ServiceNameStyle.Render(name))
	}

	var lines []string
	if state.dependencies && len(nodes) > 0 {
		lines = append(lines, dependencyBranches(dependencies, conditions, state.roots, rootNumbers, label, width, true)...)
	}
	rootRow := len(lines)
	rootStyle := lipgloss.NewStyle().Foreground(ColorAccentText).Bold(true)
	for _, name := range state.roots {
		value := graphStatus(name).render(rootStyle.Render(name))
		if len(state.roots) > 1 {
			value = rootStyle.Render(fmt.Sprintf("[%d] ", rootNumbers[name])) + value
		}
		lines = appendDependencyItem(lines, rootStyle.Render("  ◆─ "), rootStyle.Render("  │  "), value, width)
	}
	for _, edge := range rootEdges {
		value := fmt.Sprintf("[%d] requires [%d]", rootNumbers[edge.To], rootNumbers[edge.From])
		if condition := conditions[[2]string{edge.From, edge.To}]; condition != "" {
			value += " (" + condition + ")"
		}
		lines = appendDependencyItem(lines, rootStyle.Render("  │  "), rootStyle.Render("  │  "), value, width)
	}
	if state.dependents && len(nodes) > 0 {
		lines = append(lines, dependencyBranches(dependents, conditions, state.roots, rootNumbers, label, width, false)...)
	}
	if len(nodes) == 0 {
		lines = append(lines, modalTextRows("Dependency graph unavailable.", width)...)
	} else if !state.dependencies && !state.dependents {
		lines = append(lines, "  Both directions are hidden.")
	}
	return lines, rootRow
}

func dependencyBranches(edges map[string][]string, conditions map[[2]string]string, roots []string, rootNumbers map[string]int, label func(string) string, width int, above bool) []string {
	// A neighbor directly connected to several roots appears once on the
	// shared rail, with its own condition shown for each root.
	parents := make(map[string][]string)
	var neighbors []string
	for _, root := range roots {
		for _, child := range edges[root] {
			if len(parents[child]) == 0 {
				neighbors = append(neighbors, child)
			}
			parents[child] = append(parents[child], root)
		}
	}
	slices.Sort(neighbors)
	depth, heading, empty := 1, "↓ Dependents", "No dependents"
	elbow, joint, relationship := "└", "┬", "requires"
	if above {
		depth = 0
		heading, empty = "↑ Dependencies", "No dependencies"
		elbow, joint, relationship = "┌", "┴", "required by"
	}
	branchStyle := dependencyDepthStyle(lipgloss.DefaultRenderer(), depth, 2)
	headingRows := modalTextRows(branchStyle.Bold(true).Render(heading), width)
	if len(neighbors) == 0 {
		emptyLine := branchStyle.Render("  │  ") + ContextBarStyle.Render(empty)
		if above {
			return append(headingRows, emptyLine)
		}
		return append([]string{emptyLine}, headingRows...)
	}
	conditionFor := func(parent, child string) string {
		if above {
			return conditions[[2]string{child, parent}]
		}
		return conditions[[2]string{parent, child}]
	}
	seen := make(map[string]bool)
	blocks := make([][]string, 0)
	maxIndent := max(0, (width-28)/2)
	var walk func(children []string, parent, indent string, depth int)
	walk = func(children []string, parent, indent string, depth int) {
		for index, child := range children {
			start, extension := "├", "│ "
			if index == len(children)-1 {
				start, extension = elbow, "  "
			}
			value := label(child)
			next := edges[child]
			slices.Sort(next)
			branchEnd := "─"
			if len(next) > 0 && !seen[child] {
				branchEnd = joint
			}
			if depth == 0 && len(roots) > 1 {
				var links []string
				for _, root := range parents[child] {
					link := fmt.Sprintf("[%d]", rootNumbers[root])
					if condition := conditionFor(root, child); condition != "" {
						link += " (" + condition + ")"
					}
					links = append(links, link)
				}
				value += branchStyle.Render(", " + relationship + " " + strings.Join(links, ", "))
			} else {
				parentName := parent
				if depth == 0 {
					parentName = parents[child][0]
				}
				if condition := conditionFor(parentName, child); condition != "" {
					wait := "required condition "
					if !above {
						wait = "parent condition "
					}
					value += branchStyle.Render(", " + wait + condition)
				}
			}
			if seen[child] {
				value += ContextBarStyle.Render("  ↪ shared")
			}
			continuation := extension
			childRail := " "
			if above {
				continuation = "│ "
			} else if branchEnd == joint {
				childRail = "│"
			}
			prefix := branchStyle.Render(indent + start + "─" + branchEnd + " ")
			wrappedPrefix := branchStyle.Render(indent + continuation + childRail + " ")
			blocks = append(blocks, appendDependencyItem(nil, prefix, wrappedPrefix, value, width))
			if seen[child] {
				continue
			}
			seen[child] = true
			nextIndent := indent
			if depth < maxIndent {
				nextIndent += extension
			} else if depth == maxIndent {
				nextIndent += "… "
			}
			walk(next, child, nextIndent, depth+1)
		}
	}
	walk(neighbors, "", "  ", 0)
	var lines []string
	if above {
		slices.Reverse(blocks)
		lines = append(lines, headingRows...)
	}
	for _, block := range blocks {
		lines = append(lines, block...)
	}
	if !above {
		lines = append(lines, headingRows...)
	}
	return lines
}

func dependencyConditionLabel(condition string) string {
	switch config.DependencyCondition(condition) {
	case "":
		return ""
	case config.DependencyStarted:
		return "started"
	case config.DependencyHealthy:
		return "healthy"
	case config.DependencyCompleted:
		return "completed"
	case config.DependencyCompletedSuccessfully:
		return "completed successfully"
	case config.DependencyLogReady:
		return "log ready"
	default:
		return condition
	}
}

func dependencyStatus(name string, statuses map[string]dependencyServiceStatus) dependencyServiceStatus {
	if status, ok := statuses[name]; ok {
		return status
	}
	return dependencyServiceStatus{
		indicator: ContextBarStyle.Render("?"),
		detail:    ContextBarStyle.Render("status unavailable"),
	}
}

func appendDependencyItem(lines []string, prefix, continuation, value string, width int) []string {
	available := max(1, width-lipgloss.Width(prefix))
	wrapped := ansi.Hardwrap(ansi.Wordwrap(value, available, ""), available, true)
	for _, line := range strings.Split(wrapped, "\n") {
		lines = append(lines, prefix+line)
		prefix = continuation
	}
	return lines
}

func appendPlanLinks(lines []string, rail string, links []string, style lipgloss.Style, width int) []string {
	if len(links) == 0 {
		return lines
	}
	return appendDependencyItem(lines, rail+style.Render("╰─ "), rail+"   ", ContextBarStyle.Render("needs "+strings.Join(links, ", ")), width)
}

func appendDependencySequence(lines []string, names []string, links map[string][]string, label func(string) string, width int, renderer *lipgloss.Renderer) []string {
	for index, name := range names {
		style := dependencyDepthStyle(renderer, index, len(names))
		number := fmt.Sprintf("%d.", index+1)
		prefix := "    " + style.Bold(true).Render(number) + " "
		rail := strings.Repeat(" ", lipgloss.Width(prefix))
		lines = appendDependencyItem(lines, prefix, rail, label(name), width)
		lines = appendPlanLinks(lines, rail, links[name], style, width)
	}
	return lines
}

func (m *Model) dependencyPlanBody(width int) []string {
	preview, err := m.previewDependencyOperation(m.dependencyModal.operation, m.dependencyModal.selectedOnly)
	if len(preview.Requested) == 0 && err == nil {
		return modalTextRows("Select services or focus a service to preview an operation.", width)
	}
	statuses := m.dependencyStatuses()
	statusLabel := func(name string) string {
		return dependencyStatus(name, statuses).render(ServiceNameStyle.Render(name))
	}
	phaseLinks := make([]map[string][]string, len(preview.Steps))
	shownLinks := make(map[[2]string]bool)
	for index, step := range preview.Steps {
		phaseLinks[index] = m.dependencyPlanLinks(slices.Concat(step.Waves...))
		for name, dependencies := range phaseLinks[index] {
			for _, dependency := range dependencies {
				shownLinks[[2]string{name, dependency}] = true
			}
		}
	}
	var lines []string
	if m.dependencyModal.runFailed && m.dependencyModal.runMessage != "" {
		lines = append(lines, modalTextRows(FailedBadgeStyle.Render(m.dependencyModal.runMessage), width)...)
		lines = append(lines, "")
	}
	if len(preview.Requested) > 0 {
		lines = append(lines, modalTextRows(HelpSectionStyle.Render("Requested"), width)...)
		requestedLinks := m.dependencyPlanLinks(preview.Requested)
		for _, name := range preview.Requested {
			lines = appendDependencyItem(lines, "    ", "    ", statusLabel(name), width)
			var links []string
			for _, dependency := range requestedLinks[name] {
				if !shownLinks[[2]string{name, dependency}] {
					links = append(links, dependency)
				}
			}
			lines = appendPlanLinks(lines, "    ", links, ContextBarStyle, width)
		}
	}
	if err != nil {
		return append(lines, modalTextRows(FailedBadgeStyle.Render(err.Error()), width)...)
	}
	if len(preview.Steps) == 0 {
		message := "No service changes for this operation."
		if m.dependencyModal.operation == "restart" {
			message = "No running services to restart. Use Start to launch stopped services."
		}
		lines = append(lines, "")
		lines = append(lines, modalTextRows(message, width)...)
	}
	requested := nameSet(preview.Requested)
	expansion := "dependent"
	if m.dependencyModal.operation == "start" {
		expansion = "dependency"
	}
	serviceLabel := func(name string) string {
		label := statusLabel(name)
		if requested[name] {
			return label
		}
		return label + ContextBarStyle.Render(" ["+expansion+"]")
	}
	for index, step := range preview.Steps {
		links := phaseLinks[index]
		lines = append(lines, "")
		lines = append(lines, modalTextRows(HelpSectionStyle.Render(step.Action), width)...)
		if step.Sequential {
			if m.dependencyModal.selectedOnly && step.Action == "Start" && len(links) > 0 {
				lines = append(lines, modalTextRows(ContextBarStyle.Render("Selected dependencies go first. Readiness gates are skipped."), width)...)
			}
			lines = appendDependencySequence(lines, slices.Concat(step.Waves...), links, serviceLabel, width, lipgloss.DefaultRenderer())
			continue
		}
		for index, wave := range step.Waves {
			waveStyle := dependencyDepthStyle(lipgloss.DefaultRenderer(), index, len(step.Waves))
			lines = append(lines, waveStyle.Bold(true).Render(fmt.Sprintf("    Wave %d", index+1)))
			for nameIndex, name := range wave {
				branch, rail := "      ├─ ", "      │  "
				if nameIndex == len(wave)-1 {
					branch, rail = "      └─ ", "         "
				}
				rail = waveStyle.Render(rail)
				lines = appendDependencyItem(lines, waveStyle.Render(branch), rail, serviceLabel(name), width)
				lines = appendPlanLinks(lines, rail, links[name], waveStyle, width)
			}
		}
	}
	for _, note := range preview.Notes {
		lines = append(lines, "")
		lines = append(lines, modalTextRows(StartingBadgeStyle.Render(note), width)...)
	}
	return lines
}
