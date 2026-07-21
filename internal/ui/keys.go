package ui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// KeyMap defines every keyboard shortcut used by the application.
type KeyMap struct {
	Up           key.Binding
	Down         key.Binding
	Select       key.Binding
	Toggle       key.Binding
	ForceStart   key.Binding
	FocusList    key.Binding
	FocusDetails key.Binding
	FocusLogs    key.Binding
	PinLogs      key.Binding
	Reload       key.Binding
	Shell        key.Binding
	Quit         key.Binding
	StartAll     key.Binding
	StopAll      key.Binding
	Restart      key.Binding
	RestartAll   key.Binding
	Tags         key.Binding
	ResetTags    key.Binding
	Health       key.Binding
	Notifs       key.Binding
	Search       key.Binding
	WrapLogs     key.Binding
	LogTime      key.Binding
	Freeze       key.Binding
	Clear        key.Binding
	Help         key.Binding
	Escape       key.Binding
	Kill         key.Binding
	Skip         key.Binding
	Cancel       key.Binding
	Yes          key.Binding
	No           key.Binding
}

// DefaultKeyMap returns Kranz's standard keyboard bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Select: key.NewBinding(
			key.WithKeys(" "),
			key.WithHelp("space", "select service"),
		),
		Toggle: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "start/stop selection"),
		),
		ForceStart: key.NewBinding(
			key.WithKeys("S", "shift+s"),
			key.WithHelp("Shift+S", "start without dependencies"),
		),
		FocusList: key.NewBinding(
			key.WithKeys("1"),
			key.WithHelp("1", "focus services/tags"),
		),
		FocusDetails: key.NewBinding(
			key.WithKeys("2"),
			key.WithHelp("2", "focus details"),
		),
		FocusLogs: key.NewBinding(
			key.WithKeys("3"),
			key.WithHelp("3", "focus logs"),
		),
		PinLogs: key.NewBinding(
			key.WithKeys("#", "shift+3"),
			key.WithHelp("Shift+3", "pin/unpin focused logs"),
		),
		Reload: key.NewBinding(
			key.WithKeys("ctrl+l"),
			key.WithHelp("Ctrl+L", "reload config and appearance"),
		),
		Shell: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("Ctrl+O", "open/close command shell"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		StartAll: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "select/clear all"),
		),
		StopAll: key.NewBinding(
			key.WithKeys("A", "shift+a"),
			key.WithHelp("A", "stop all"),
		),
		Restart: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "restart selected"),
		),
		RestartAll: key.NewBinding(
			key.WithKeys("R", "shift+r"),
			key.WithHelp("R", "restart all"),
		),
		Tags: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "services/tags"),
		),
		ResetTags: key.NewBinding(
			key.WithKeys("T", "shift+t"),
			key.WithHelp("T", "clear tag selection"),
		),
		Health: key.NewBinding(
			key.WithKeys("h"),
			key.WithHelp("h", "health history"),
		),
		Notifs: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "notifications"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search logs"),
		),
		WrapLogs: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("w", "wrap log lines"),
		),
		LogTime: key.NewBinding(
			key.WithKeys("i"),
			key.WithHelp("i", "show/hide log timestamps"),
		),
		Freeze: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "pause/resume logs"),
		),
		Clear: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "clear logs"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Escape: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "close"),
		),
		Kill: key.NewBinding(
			key.WithKeys("k"),
			key.WithHelp("k", "kill process"),
		),
		Skip: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "skip"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("c", "esc"),
			key.WithHelp("c", "cancel"),
		),
		Yes: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("y", "yes"),
		),
		No: key.NewBinding(
			key.WithKeys("n", "esc"),
			key.WithHelp("n", "no"),
		),
	}
}

const (
	russianShortcutRunes = "ёйцукенгшщзхъфывапролджэячсмитьбюЁЙЦУКЕНГШЩЗХЪФЫВАПРОЛДЖЭЯЧСМИТЬБЮ"
	latinShortcutRunes   = "`qwertyuiop[]asdfghjkl;'zxcvbnm,.~QWERTYUIOP{}ASDFGHJKL:\"ZXCVBNM<>"
)

// normalizeShortcutKey maps standard Russian JCUKEN input back to the Latin
// key printed in Kranz's shortcuts. Traditional terminal input contains the
// resulting rune, not a physical key code, so layout normalization must be an
// explicit table. Paste and multi-rune IME input are deliberately untouched.
func normalizeShortcutKey(msg tea.KeyMsg) tea.KeyMsg {
	if msg.Type != tea.KeyRunes || msg.Paste || len(msg.Runes) != 1 {
		return msg
	}
	r := msg.Runes[0]
	for index, candidate := range []rune(russianShortcutRunes) {
		if candidate == r {
			msg.Runes = []rune{[]rune(latinShortcutRunes)[index]}
			return msg
		}
	}
	// On JCUKEN the physical / and Shift+/ keys produce . and ,. Neither is a
	// Kranz shortcut in the Latin layout, so these are safe aliases for / and ?.
	switch r {
	case '.':
		msg.Runes = []rune{'/'}
	case ',':
		msg.Runes = []rune{'?'}
	}
	return msg
}
