package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/config"
)

// Command-shell handoff: Kranz hands the terminal to an interactive shell and
// resumes afterwards without disturbing managed services.

func (m *Model) openCommandShell() tea.Cmd {
	command, cleanup, err := commandShell()
	if err != nil {
		return func() tea.Msg { return shellFinishedMsg{err: err} }
	}
	m.addNotification("shell", "Command shell opened; Ctrl+O returns to Kranz", config.LogInfo)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		cleanup()
		return shellFinishedMsg{err: err}
	})
}

func commandShell() (*exec.Cmd, func(), error) {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}
	resolved, err := exec.LookPath(shell)
	if err != nil {
		return nil, func() {}, fmt.Errorf("find shell %q: %w", shell, err)
	}
	cleanup := func() {}
	name := filepath.Base(resolved)
	var command *exec.Cmd
	switch name {
	case "zsh":
		tempDir, err := os.MkdirTemp("", "kranz-shell-")
		if err != nil {
			return nil, cleanup, fmt.Errorf("prepare zsh handoff: %w", err)
		}
		cleanup = func() { _ = os.RemoveAll(tempDir) }
		userConfigDir := strings.TrimSpace(os.Getenv("ZDOTDIR"))
		if userConfigDir == "" {
			userConfigDir = os.Getenv("HOME")
		}
		rc := "ZDOTDIR=${KRANZ_ORIGINAL_ZDOTDIR:-$HOME}\n" +
			"[[ -r \"$KRANZ_USER_ZSHRC\" ]] && source \"$KRANZ_USER_ZSHRC\"\n" +
			"bindkey -s '^O' 'exit\\n'\n" +
			"PROMPT='%F{cyan}[Kranz shell]%f %~ %# '\n"
		if err := os.WriteFile(filepath.Join(tempDir, ".zshrc"), []byte(rc), 0o600); err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("prepare zsh handoff: %w", err)
		}
		command = exec.Command(resolved, "-i")
		command.Env = commandEnvironment(
			"ZDOTDIR="+tempDir,
			"KRANZ_ORIGINAL_ZDOTDIR="+userConfigDir,
			"KRANZ_USER_ZSHRC="+filepath.Join(userConfigDir, ".zshrc"),
		)
	case "bash":
		tempDir, err := os.MkdirTemp("", "kranz-shell-")
		if err != nil {
			return nil, cleanup, fmt.Errorf("prepare bash handoff: %w", err)
		}
		cleanup = func() { _ = os.RemoveAll(tempDir) }
		rcPath := filepath.Join(tempDir, "bashrc")
		rc := "[[ -r \"$KRANZ_USER_BASHRC\" ]] && source \"$KRANZ_USER_BASHRC\"\n" +
			"bind -x '\"\\C-o\":exit'\n" +
			"PS1='[Kranz shell] \\w \\$ '\n"
		if err := os.WriteFile(rcPath, []byte(rc), 0o600); err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("prepare bash handoff: %w", err)
		}
		command = exec.Command(resolved, "--rcfile", rcPath, "-i")
		command.Env = commandEnvironment("KRANZ_USER_BASHRC=" + filepath.Join(os.Getenv("HOME"), ".bashrc"))
	case "fish":
		command = exec.Command(resolved, "-C", "bind \\co exit", "-i")
	default:
		command = exec.Command(resolved, "-i")
	}
	return command, cleanup, nil
}

func commandEnvironment(overrides ...string) []string {
	names := make(map[string]bool, len(overrides))
	for _, override := range overrides {
		if separator := strings.IndexByte(override, '='); separator >= 0 {
			names[override[:separator]] = true
		}
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, value := range os.Environ() {
		separator := strings.IndexByte(value, '=')
		if separator >= 0 && names[value[:separator]] {
			continue
		}
		environment = append(environment, value)
	}
	return append(environment, overrides...)
}
