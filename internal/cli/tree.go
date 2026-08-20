// Package cli defines Kranz's command grammar independently from command
// execution. The parser, help renderer, and shell completion generator all
// consume the same tree so their view of the public surface cannot drift.
package cli

import (
	"fmt"
	"sort"
	"strings"
)

// Command describes one node in the public command tree.
type Command struct {
	Name     string
	Summary  string
	Usage    string
	Children []*Command

	// Planned marks a command whose grammar is reserved but whose execution a
	// later feature stream still has to attach. Help lists planned commands
	// apart from working ones and the dispatcher refuses them, so the tree
	// stays the single place that decides which surface actually exists.
	Planned bool
}

// DefaultTree returns the complete v0.8 command vocabulary. Feature streams
// attach execution to these nodes incrementally; reserving the grammar here
// keeps unknown-command handling, help, and future completions deterministic.
// A stream that implements a command clears its Planned flag in the same
// change, which is what moves the command into the working help section.
func DefaultTree() *Command {
	return &Command{Name: "kranz", Summary: "a local service orchestrator", Children: []*Command{
		{Name: "init", Summary: "create a Kranz configuration", Usage: "kranz init [OPTIONS]", Planned: true},
		{Name: "config", Summary: "inspect effective configuration", Children: []*Command{
			{Name: "check", Summary: "load and validate configuration", Planned: true},
			{Name: "show", Summary: "print redacted effective configuration", Planned: true},
			{Name: "explain", Summary: "show field provenance", Usage: "kranz config explain [SERVICE]", Planned: true},
		}},
		{Name: "doctor", Summary: "run project preflight checks", Planned: true},
		{Name: "ps", Summary: "list active project runtimes"},
		{Name: "list", Summary: "list services, actions, or tags", Usage: "kranz list [services|actions|tags]", Planned: true},
		{Name: "info", Summary: "show project or service details", Usage: "kranz info [SERVICE]", Planned: true},
		{Name: "status", Summary: "show runtime status", Usage: "kranz status [SELECTOR ...]"},
		{Name: "plan", Summary: "show the resolved start plan", Usage: "kranz plan [SELECTOR ...]", Planned: true},
		{Name: "graph", Summary: "print the dependency graph", Planned: true},
		{Name: "ports", Summary: "list configured and detected ports", Usage: "kranz ports [SELECTOR ...]", Planned: true},
		{Name: "port", Summary: "inspect a local port", Children: []*Command{
			{Name: "inspect", Summary: "identify a port listener", Usage: "kranz port inspect PORT", Planned: true},
		}},
		{Name: "up", Summary: "create a project runtime", Usage: "kranz up [SELECTOR ...]"},
		{Name: "start", Summary: "start services", Usage: "kranz start SELECTOR ..."},
		{Name: "stop", Summary: "stop services", Usage: "kranz stop SELECTOR ..."},
		{Name: "restart", Summary: "restart services", Usage: "kranz restart SELECTOR ..."},
		{Name: "reload", Summary: "reload runtime configuration"},
		{Name: "down", Summary: "stop a project runtime"},
		{Name: "attach", Summary: "open the TUI for an active runtime"},
		{Name: "logs", Summary: "show service logs", Usage: "kranz logs [SELECTOR ...]", Planned: true},
		{Name: "action", Summary: "inspect and run actions", Children: []*Command{
			{Name: "list", Summary: "list actions", Usage: "kranz action list [OWNER]", Planned: true},
			{Name: "info", Summary: "show action details", Usage: "kranz action info OWNER/ACTION", Planned: true},
			{Name: "run", Summary: "run an action", Usage: "kranz action run OWNER/ACTION", Planned: true},
		}},
		{Name: "completion", Summary: "generate shell completion", Usage: "kranz completion bash|zsh|fish", Planned: true},
		{Name: "help", Summary: "show command help", Usage: "kranz help [COMMAND]"},
		{Name: "version", Summary: "show version and build metadata"},
	}}
}

// Child resolves a direct child by its exact public name.
func (c *Command) Child(name string) *Command {
	for _, child := range c.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
}

// Resolve returns the deepest command matching path.
func (c *Command) Resolve(path []string) (*Command, error) {
	current := c
	for _, name := range path {
		next := current.Child(name)
		if next == nil {
			return nil, fmt.Errorf("command %q has no subcommand %q", current.Name, name)
		}
		current = next
	}
	return current, nil
}

// IsPlanned reports whether a command cannot be run yet. A parent is planned
// when every subcommand below it is, so a group nobody can enter is listed as
// planned instead of appearing to work until the user picks a subcommand.
func (c *Command) IsPlanned() bool {
	if len(c.Children) == 0 {
		return c.Planned
	}
	for _, child := range c.Children {
		if !child.IsPlanned() {
			return false
		}
	}
	return true
}

// CommandNames returns sorted direct-child names for errors and completion.
func (c *Command) CommandNames() []string {
	names := make([]string, 0, len(c.Children))
	for _, child := range c.Children {
		names = append(names, child.Name)
	}
	sort.Strings(names)
	return names
}

// PathString formats a command path without exposing implementation details.
func PathString(path []string) string { return strings.Join(path, " ") }
