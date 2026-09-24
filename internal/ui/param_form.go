package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/actionparams"
	"github.com/kranz-org/kranz/internal/config"
)

// The parameter form. The inline tree is for glancing and small edits; this is
// the same values laid out as a form, with the command it builds pinned above
// the fields so a long list of settings never pushes it out of sight.

type paramFormState struct {
	ID config.ActionID
	// Field is the focused parameter, and Option the value cursor inside it.
	Field  int
	Option int
	// Editing is true while a text or number field takes keystrokes.
	Editing bool
}

// openParamForm shows one action's parameters as a form.
func (m *Model) openParamForm(id config.ActionID) tea.Cmd {
	if !m.actionHasParams(id) {
		return nil
	}
	m.paramForm = &paramFormState{ID: id}
	m.syncParamFormOption()
	m.mode = ModeParamForm
	return nil
}

// paramFormSpec returns the focused field's declaration.
func (m *Model) paramFormSpec() (paramUISpec, bool) {
	if m.paramForm == nil {
		return paramUISpec{}, false
	}
	specs := m.actionParamSpecs(m.paramForm.ID)
	if m.paramForm.Field < 0 || m.paramForm.Field >= len(specs) {
		return paramUISpec{}, false
	}
	return specs[m.paramForm.Field], true
}

// syncParamFormOption puts the value cursor on the chosen value of the focused
// field, so moving into a row starts from what it currently holds.
func (m *Model) syncParamFormOption() {
	spec, ok := m.paramFormSpec()
	if !ok || len(spec.Options) == 0 {
		m.paramForm.Option = 0
		return
	}
	for index, option := range spec.Options {
		if m.paramSelected(m.paramForm.ID, spec.Name, option.Value) {
			m.paramForm.Option = index
			return
		}
	}
	m.paramForm.Option = 0
}

func (m *Model) moveParamFormField(direction int) {
	specs := m.actionParamSpecs(m.paramForm.ID)
	if len(specs) == 0 {
		return
	}
	m.paramForm.Field = max(0, min(len(specs)-1, m.paramForm.Field+direction))
	m.syncParamFormOption()
}

// moveParamFormValue walks the values of the focused field. A single-choice
// field selects as it moves, because in a form the highlighted radio is the
// chosen one; a set of values only moves the cursor and waits for Space.
func (m *Model) moveParamFormValue(direction int, large bool) {
	spec, ok := m.paramFormSpec()
	if !ok {
		return
	}
	if len(spec.Options) == 0 {
		// A number counts up and down in place; a text field has nothing to
		// walk through and stays still.
		m.stepParamValue(m.paramForm.ID, spec, direction, large)
		return
	}
	m.paramForm.Option = max(0, min(len(spec.Options)-1, m.paramForm.Option+direction))
	if spec.Control != actionparams.Checkbox {
		m.setParamFormValue(spec, spec.Options[m.paramForm.Option].Value)
	}
}

func (m *Model) setParamFormValue(spec paramUISpec, chosen string) {
	values := m.paramValuesFor(m.paramForm.ID)
	value := values[spec.Name]
	value.Text, value.Set = chosen, true
	values[spec.Name] = value
	m.actionParamValues[m.paramForm.ID] = values
}

// toggleParamFormValue answers Space: it checks a box, or adds and removes one
// value of a set.
func (m *Model) toggleParamFormValue() {
	spec, ok := m.paramFormSpec()
	if !ok {
		return
	}
	values := m.paramValuesFor(m.paramForm.ID)
	value := values[spec.Name]
	switch {
	case len(spec.Options) == 0 && spec.Control == actionparams.Checkbox:
		value.Bool, value.Set = !value.Bool, true
	case spec.Control == actionparams.Checkbox:
		value.Strings = toggleListValue(value.Strings, spec.Options[m.paramForm.Option].Value)
		value.Set = true
	default:
		value.Text, value.Set = spec.Options[m.paramForm.Option].Value, true
	}
	values[spec.Name] = value
	m.actionParamValues[m.paramForm.ID] = values
}

func (m *Model) beginParamFormEdit() tea.Cmd {
	spec, ok := m.paramFormSpec()
	if !ok || len(spec.Options) > 0 || spec.Control == actionparams.Checkbox {
		return nil
	}
	value := m.paramValuesFor(m.paramForm.ID)[spec.Name]
	m.paramForm.Editing = true
	m.paramInput.SetValue(value.Text)
	m.paramInput.CursorEnd()
	m.paramInput.Width = paramEditorWidth
	return m.paramInput.Focus()
}

func (m *Model) commitParamFormEdit(keep bool) {
	spec, ok := m.paramFormSpec()
	if ok && keep {
		values := m.paramValuesFor(m.paramForm.ID)
		value := values[spec.Name]
		value.Text, value.Set = m.paramInput.Value(), true
		values[spec.Name] = value
		m.actionParamValues[m.paramForm.ID] = values
	}
	m.paramForm.Editing = false
	m.paramInput.Blur()
	m.paramInput.SetValue("")
}

func (m *Model) closeParamForm() {
	if m.paramForm != nil && m.paramForm.Editing {
		m.commitParamFormEdit(false)
	}
	m.paramForm = nil
	m.mode = ModeNormal
}

func (m *Model) handleParamFormKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.paramForm == nil {
		m.mode = ModeNormal
		return m, nil
	}
	if m.paramForm.Editing {
		switch msg.Type {
		case tea.KeyEsc:
			m.commitParamFormEdit(false)
			return m, nil
		case tea.KeyEnter:
			m.commitParamFormEdit(true)
			return m, nil
		case tea.KeySpace:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
		}
		var command tea.Cmd
		m.paramInput, command = m.paramInput.Update(msg)
		return m, command
	}
	switch msg.String() {
	case "esc":
		m.closeParamForm()
	case "up", "k", "shift+tab":
		m.moveParamFormField(-1)
	case "down", "j", "tab":
		m.moveParamFormField(1)
	case "left", "h":
		m.moveParamFormValue(-1, false)
	case "right", "l":
		m.moveParamFormValue(1, false)
	case "shift+left", "H":
		m.moveParamFormValue(-1, true)
	case "shift+right", "L":
		m.moveParamFormValue(1, true)
	case "d":
		spec, ok := m.paramFormSpec()
		if ok {
			m.toggleParamDisabled(m.paramForm.ID, spec.Name)
		}
	case " ":
		m.toggleParamFormValue()
	case "enter":
		return m, m.beginParamFormEdit()
	case "s":
		id := m.paramForm.ID
		m.closeParamForm()
		return m, m.runParameterizedAction(id)
	}
	return m, nil
}

// paramFormWidth is the form's inner width. It is fixed so that typing into a
// field, or moving to one with a longer hint, never resizes the modal under the
// hands using it.
func (m *Model) paramFormWidth() int {
	return max(44, min(66, m.width-14))
}

// renderParamFormView draws the command above the fields that build it. Every
// line is padded to one width, so the frame stays where it was put.
func (m *Model) renderParamFormView() string {
	id := m.paramForm.ID
	specs := m.actionParamSpecs(id)
	width := m.paramFormWidth()
	lines := []string{padFormLine(ModalTitleStyle.Render(" Configure "+id.Owner+"/"+id.Name+" "), width), padFormLine("", width)}

	render, err := m.renderActionParams(id)
	command, commandStyle := render.Preview, ParamCommandStyle
	if err != nil {
		command, commandStyle = m.paramFallbackCommand(id), ParamErrorStyle
	}
	for index, part := range wrapDetailValue(command, width-4) {
		prefix := "    "
		if index == 0 {
			prefix = "  " + ContextBarStyle.Render(commandPromptMarker) + " "
		}
		lines = append(lines, padFormLine(prefix+commandStyle.Render(part), width))
	}
	// Each field says what is wrong with itself, so the command only explains
	// itself when no field owns the problem.
	if err != nil && !m.paramFormFieldOwnsProblem() {
		for _, part := range wrapDetailValue(m.paramCommandProblem(id), width-4) {
			lines = append(lines, padFormLine("    "+ParamErrorStyle.Render(part), width))
		}
	}
	lines = append(lines, padFormLine("", width))

	labelWidth := 0
	for _, spec := range specs {
		labelWidth = max(labelWidth, len([]rune(spec.Name)))
	}
	for index, spec := range specs {
		lines = append(lines, m.paramFormField(spec, index, labelWidth, width)...)
	}
	// Grouped the way the other modals group theirs, and wrapped inside the
	// same width so the frame never changes size with what it has to say.
	lines = append(lines, paramFormFooter(m.paramForm != nil && m.paramForm.Editing, width)...)
	return m.placeOverlay(renderFlushModal(strings.Join(lines, "\n")))
}

// paramFormFieldOwnsProblem reports whether some field already carries the
// reason the command cannot be built.
func (m *Model) paramFormFieldOwnsProblem() bool {
	for _, spec := range m.actionParamSpecs(m.paramForm.ID) {
		if m.paramRowError(m.paramForm.ID, spec.Name) != "" {
			return true
		}
	}
	return false
}

// paramFormField renders one field as its own block: the label and value on one
// line, and underneath the line that separates it from the next field. That
// line carries the field's hint, or its error, and grows when the text needs
// more than one row rather than being dropped.
func (m *Model) paramFormField(spec paramUISpec, index, labelWidth, width int) []string {
	id := m.paramForm.ID
	focused := index == m.paramForm.Field
	label := padParamName(spec.Name, labelWidth)
	valueIndent := "  " + strings.Repeat(" ", labelWidth+2)
	values := m.paramFormFieldValues(spec, index, max(1, width-len([]rune(valueIndent))))

	// The note says what the field accepts, and turns into what is wrong with
	// it the moment the value stops satisfying that.
	note, noteStyle := spec.Prompt, ContextBarStyle
	if spec.Constraint != "" {
		if note != "" {
			note += " · "
		}
		note += spec.Constraint
	}
	if reason := m.paramRowError(id, spec.Name); reason != "" {
		note, noteStyle = reason, ParamErrorStyle
	}
	noteIndent := "  " + strings.Repeat(" ", labelWidth+2)
	notes := []string{}
	for _, part := range wrapDetailValue(note, max(8, width-len([]rune(noteIndent)))) {
		notes = append(notes, noteIndent+noteStyle.Render(part))
	}
	if len(notes) == 0 {
		notes = []string{""}
	}

	rows := []string{"  " + m.paramLabelStyle(id, spec.Name).Render(label) + "  " + values[0]}
	for _, value := range values[1:] {
		rows = append(rows, valueIndent+value)
	}
	rows = append(rows, notes...)
	for rowIndex, row := range rows {
		if focused {
			// Disabled text has its own muted foreground while idle. Under the
			// cursor the selected row owns the foreground so that muted text
			// cannot disappear into the selection background.
			if m.paramDisabled(id, spec.Name) {
				row = ansi.Strip(row)
			}
			rows[rowIndex] = ParamFieldFocusStyle.Render(preserveStyleAfterReset(padFormLine(row, width), ParamFieldFocusStyle))
			continue
		}
		rows[rowIndex] = padFormLine(row, width)
	}
	// The note is part of the field, so the gap that separates one field from
	// the next comes after it.
	return append(rows, padFormLine("", width))
}

// padFormLine gives every line the form's width so the modal never resizes.
func padFormLine(line string, width int) string {
	if gap := width - lipgloss.Width(line); gap > 0 {
		return line + strings.Repeat(" ", gap)
	}
	return ansi.Truncate(line, width, "…")
}

// shortcutGroup is one labelled row of the form's footer.
type shortcutGroup struct {
	label string
	keys  string
}

func paramFormShortcuts(editing bool) []shortcutGroup {
	if editing {
		return []shortcutGroup{{label: "TYPING", keys: "[Enter] Keep  [Esc] Discard"}}
	}
	return []shortcutGroup{
		{label: "MOVE", keys: "[↑/k] Up  [↓/j] Down"},
		{label: "SET", keys: "[←/h] [→/l] Value  [Shift] ×10  [Space] Toggle"},
		{label: "EDIT", keys: "[Enter] Type  [d] Disable"},
		{label: "RUN", keys: "[s] Run  [Esc] Close"},
	}
}

// paramFormFooter lays the shortcut groups out inside the form's width, and
// reserves the height of the taller of the two states. Wrapping keeps every
// key readable instead of trailing off in an ellipsis, and the reservation
// keeps the form from resizing when it starts or stops taking text.
func paramFormFooter(editing bool, width int) []string {
	layout := func(groups []shortcutGroup) []string {
		var out []string
		for _, group := range groups {
			gutter := "  " + padParamName(group.label, 6) + "  "
			indent := len([]rune(gutter))
			for index, part := range wrapDetailValue(group.keys, max(10, width-indent)) {
				prefix := strings.Repeat(" ", indent)
				if index == 0 {
					prefix = "  " + DetailLabelStyle.Render(padParamName(group.label, 6)) + "  "
				}
				out = append(out, padFormLine(prefix+renderModalShortcuts(part, lipgloss.NewStyle().Foreground(ColorDim)), width))
			}
		}
		return out
	}
	lines := layout(paramFormShortcuts(editing))
	reserved := max(len(layout(paramFormShortcuts(false))), len(layout(paramFormShortcuts(true))))
	for len(lines) < reserved {
		lines = append(lines, padFormLine("", width))
	}
	return lines
}

func padParamName(name string, width int) string {
	for len([]rune(name)) < width {
		name += " "
	}
	return name
}

// paramFormFieldValues keeps options together when they fit and puts each one
// on its own aligned line when the whole choice list exceeds the form width.
func (m *Model) paramFormFieldValues(spec paramUISpec, index, width int) []string {
	id := m.paramForm.ID
	focused := index == m.paramForm.Field
	if focused && m.paramForm.Editing {
		return []string{SearchInputStyle.Render(preserveStyleAfterReset(m.paramInput.View(), SearchInputStyle))}
	}
	if len(spec.Options) > 0 {
		parts := make([]string, 0, len(spec.Options)+1)
		// A switched-off field still shows what it holds, but the whole row
		// recedes and says so first, so it cannot be mistaken for a live one.
		disabled := m.paramDisabled(id, spec.Name)
		if disabled {
			parts = append(parts, ParamDisabledStyle.Render(disabledParamLabel))
		}
		for optionIndex, option := range spec.Options {
			label := option.Value
			if option.Label != "" {
				label = option.Label
			}
			part := m.paramValueMarker(id, spec.Name, option.Value) + " " + label
			switch {
			case disabled:
				part = ParamDisabledStyle.Render(part)
			case focused && optionIndex == m.paramForm.Option:
				part = SelectionStyle.Render(part)
			}
			parts = append(parts, part)
		}
		joined := strings.Join(parts, "   ")
		if lipgloss.Width(joined) <= width {
			return []string{joined}
		}
		var rows []string
		for _, part := range parts {
			rows = append(rows, wrapDetailValue(part, width)...)
		}
		return rows
	}
	value := m.paramRowText(id, spec.Name)
	if m.paramRowBlocked(id, spec.Name) {
		return []string{ParamErrorStyle.Render(ansi.Strip(value))}
	}
	return []string{value}
}
