// Package cli defines Kranz's command grammar independently from command
// execution. The parser, help renderer, and shell completion generator all
// consume the same tree so their view of the public surface cannot drift.
package cli

import (
	"fmt"
	"sort"
	"strings"
)

// Option documents one flag a command accepts. The usage line lists the
// spellings; this is where the meaning lives, because a name alone cannot say
// what a value means — `--run N` reads as a run number until help explains
// that a negative N counts back from the latest execution.
type Option struct {
	Flags   string
	Summary string

	// Values lists the fixed values the option's argument may take, for shells
	// that can offer them. It is completion metadata rather than display text:
	// Flags stays what help prints, because a set worth completing is often too
	// long to read in a description column.
	Values []string
}

// Spellings lists the flag forms an option accepts, in the order help prints
// them and without the metavariable trailing the last one.
func (o Option) Spellings() []string {
	var spellings []string
	for _, field := range optionFields(o.Flags) {
		if strings.HasPrefix(field, "-") {
			spellings = append(spellings, field)
		}
	}
	return spellings
}

// Metavariable returns the placeholder standing for the option's value, or the
// empty string when the option takes none. Completion needs the distinction:
// offering a filename after a flag that takes no argument is worse than
// offering nothing.
func (o Option) Metavariable() string {
	fields := optionFields(o.Flags)
	if len(fields) == 0 {
		return ""
	}
	if last := fields[len(fields)-1]; !strings.HasPrefix(last, "-") {
		return last
	}
	return ""
}

// TakesValue reports whether a spelling has to be followed by an argument.
func (o Option) TakesValue() bool { return o.Metavariable() != "" }

func optionFields(flags string) []string {
	return strings.Fields(strings.ReplaceAll(flags, ",", " "))
}

// GlobalFlags are the options every command accepts. Help and the completion
// scripts read them from here rather than each spelling them out, so the two
// cannot describe the same flag differently.
func GlobalFlags() []Option {
	return []Option{
		{Flags: "-f, --config PATH", Summary: "configuration layer; repeatable"},
		{Flags: "-C, --directory DIR", Summary: "working directory for discovery"},
		{Flags: "-p, --project VALUE", Summary: "runtime name, ID, or unique ID prefix"},
		{Flags: "--output text|json", Summary: "output format", Values: []string{"text", "json"}},
		{Flags: "-h, --help", Summary: "show command help"},
		{Flags: "-v, --version", Summary: "show version and build metadata"},
	}
}

// Command describes one node in the public command tree.
type Command struct {
	Name     string
	Summary  string
	Usage    string
	Options  []Option
	Children []*Command

	// Default names the subcommand to run when a group is invoked bare. A group
	// exists to organize related commands, but one of them is usually the
	// obvious thing the user meant, and demanding it be spelled out turns a
	// natural request into a usage error.
	Default string

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
		{Name: "init", Summary: "create a Kranz configuration", Usage: "kranz init [--from PATH] [--project NAME] [--service NAME] [--command COMMAND] [-o PATH] [-y|--yes]", Options: []Option{
			{Flags: "--from PATH", Summary: "convert an existing Procfile or compose file"},
			{Flags: "--project NAME", Summary: "project name to write"},
			{Flags: "--service NAME", Summary: "name of the first service"},
			{Flags: "--command COMMAND", Summary: "command that first service runs"},
			{Flags: "-o, --output-file PATH", Summary: "file to write; defaults to kranz.yaml"},
			{Flags: "-y, --yes", Summary: "answer every prompt yes, overwriting any file"},
		}},
		{Name: "config", Summary: "inspect effective configuration", Default: "show", Children: []*Command{
			{Name: "check", Summary: "load and validate configuration"},
			{Name: "show", Summary: "print redacted effective configuration", Usage: "kranz config show [--provenance]", Options: []Option{
				{Flags: "--provenance", Summary: "annotate each field with the layer it came from"},
			}},
			{Name: "explain", Summary: "show field provenance", Usage: "kranz config explain [SERVICE] [--all]", Options: []Option{
				{Flags: "--all", Summary: "explain every service instead of one"},
			}},
		}},
		{Name: "doctor", Summary: "run project preflight checks"},
		{Name: "ps", Summary: "list active project runtimes"},
		{Name: "list", Summary: "list services, actions, or tags", Usage: "kranz list [services|actions|tags]"},
		{Name: "info", Summary: "show project or service details", Usage: "kranz info [SERVICE]"},
		{Name: "status", Summary: "show runtime status", Usage: "kranz status [SELECTOR ...]"},
		{Name: "runs", Summary: "list retained service and action runs", Usage: "kranz runs [TARGET ...]"},
		{Name: "plan", Summary: "show the resolved start plan", Usage: "kranz plan [SELECTOR ...]"},
		{Name: "graph", Summary: "print the dependency graph", Usage: "kranz graph [--format text|json|dot]", Options: []Option{
			{Flags: "--format FORMAT", Summary: "text, json, or dot; defaults to text", Values: []string{"text", "json", "dot"}},
		}},
		{Name: "ports", Summary: "list configured and detected ports", Usage: "kranz ports [SELECTOR ...]"},
		{Name: "port", Summary: "inspect a local port", Default: "inspect", Children: []*Command{
			{Name: "inspect", Summary: "identify a port listener", Usage: "kranz port inspect PORT"},
		}},
		{Name: "up", Summary: "create a project runtime", Usage: "kranz up [SELECTOR ...] [-d|--detach]\n  kranz up --no-start [-d|--detach]", Options: []Option{
			{Flags: "-d, --detach", Summary: "leave the runtime in the background and return"},
			{Flags: "--no-start", Summary: "create the runtime without starting any service"},
		}},
		{Name: "start", Summary: "start services", Usage: "kranz start SELECTOR ..."},
		{Name: "stop", Summary: "stop services", Usage: "kranz stop SELECTOR ..."},
		{Name: "restart", Summary: "restart services", Usage: "kranz restart SELECTOR ..."},
		{Name: "reload", Summary: "reload runtime configuration"},
		{Name: "down", Summary: "stop a project runtime", Usage: "kranz down [--force]", Options: []Option{
			{Flags: "--force", Summary: "discard a runtime that no longer answers its socket"},
		}},
		{Name: "attach", Summary: "open the TUI for an active runtime"},
		{Name: "mcp", Summary: "serve the selected runtime over MCP stdio", Usage: "kranz mcp [--attach-only]", Options: []Option{
			{Flags: "--attach-only", Summary: "fail instead of creating a missing runtime; recommended for agent clients"},
		}},
		{Name: "logs", Summary: "show and clear logs", Default: "show", Children: []*Command{
			{Name: "show", Summary: "show service and action logs", Usage: "kranz logs [SELECTOR ...] [--tail N | --all] [--since D]\n  [--run N | --runs N] [--source S] [--with-actions]\n  [--plain | --no-timestamps | --no-labels] [--follow]", Options: []Option{
				{Flags: "--tail N", Summary: "show the last N lines; a service defaults to 50"},
				{Flags: "--all", Summary: "show every buffered line, however far back it goes"},
				{Flags: "--since D", Summary: "show lines newer than a duration such as 5m or 2h"},
				{Flags: "--run N", Summary: "show one execution of a service or action: run number N, or a negative offset from the newest buffered run, so -1 is the latest and -2 the one before it"},
				{Flags: "--runs N", Summary: "show the last N executions of a service or action"},
				{Flags: "--source S", Summary: "keep only stdout, stderr, or kranz; comma-separated", Values: []string{"stdout", "stderr", "kranz"}},
				{Flags: "--with-actions", Summary: "fold the actions an owner has run into its timeline"},
				{Flags: "--plain", Summary: "print the output as the command printed it"},
				{Flags: "--no-timestamps", Summary: "drop the time column"},
				{Flags: "--no-labels", Summary: "drop the stream-name column"},
				{Flags: "--follow", Summary: "keep printing new lines until interrupted"},
			}},
			{Name: "clear", Summary: "discard buffered logs", Usage: "kranz logs clear [SELECTOR ...] [--with-actions] [--force]", Options: []Option{
				{Flags: "--with-actions", Summary: "clear the actions an owner has run as well"},
				{Flags: "--force", Summary: "required to clear every buffer at once"},
			}},
		}},
		{Name: "action", Summary: "inspect and run actions", Default: "list", Children: []*Command{
			{Name: "list", Summary: "list actions", Usage: "kranz action list [OWNER]"},
			{Name: "info", Summary: "show action details", Usage: "kranz action info OWNER/ACTION"},
			{Name: "run", Summary: "run an action", Usage: "kranz action run OWNER/ACTION"},
		}},
		{Name: "completion", Summary: "generate shell completion", Usage: "kranz completion bash|zsh|fish"},
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
