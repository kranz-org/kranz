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
	// future change still has to attach. Help lists planned commands
	// apart from working ones and the dispatcher refuses them, so the tree
	// stays the single place that decides which surface actually exists.
	Planned bool
}

// DefaultTree returns the command vocabulary implemented by this build.
// Reserving future grammar here is optional; a command marked Planned stays
// out of completions and is clearly separated in help until implemented.
func DefaultTree() *Command {
	return &Command{Name: "kranz", Summary: "a local service orchestrator", Children: []*Command{
		{Name: "init", Summary: "author a Kranz configuration", Usage: "kranz init [DIRECTORY] [--from PATH] [--name NAME] [--service NAME] [--command COMMAND] [-o PATH] [-y|--yes] [--force]", Options: []Option{
			{Flags: "--from PATH", Summary: "explicitly convert an existing Procfile or compose file"},
			{Flags: "--name NAME", Summary: "project name to write"},
			{Flags: "--service NAME", Summary: "name of the first service"},
			{Flags: "--command COMMAND", Summary: "command that first service runs"},
			{Flags: "-o, --output-file PATH", Summary: "file to write; defaults to kranz.yaml"},
			{Flags: "-y, --yes", Summary: "write a fully specified non-interactive configuration"},
			{Flags: "--force", Summary: "replace an existing file in non-interactive mode"},
		}},
		{Name: "config", Summary: "inspect effective configuration", Default: "show", Children: []*Command{
			{Name: "check", Summary: "load and validate configuration"},
			{Name: "show", Summary: "print redacted effective configuration", Usage: "kranz config show [--provenance]", Options: []Option{
				{Flags: "--provenance", Summary: "annotate each field with the layer it came from"},
			}},
			{Name: "explain", Summary: "show field provenance", Usage: "kranz config explain [SERVICE] [--all] [--format TEMPLATE]", Options: []Option{
				{Flags: "--all", Summary: "explain every service instead of one"},
				{Flags: "--format TEMPLATE", Summary: "render each field with a Go template; prefix with 'table ' for headers"},
			}},
		}},
		{Name: "doctor", Summary: "run project preflight checks", Usage: "kranz doctor [--format TEMPLATE]", Options: []Option{
			{Flags: "--format TEMPLATE", Summary: "render each finding with a Go template; prefix with 'table ' for headers"},
		}},
		{Name: "ps", Summary: "list active project runtimes", Usage: "kranz ps [--filter KEY=VALUE] [--watch] [--interval D] [--count N] [--format TEMPLATE]", Options: []Option{
			{Flags: "--filter KEY=VALUE", Summary: "filter by name, project, state, or client; repeatable"},
			{Flags: "--watch", Summary: "refresh until interrupted"},
			{Flags: "--interval D", Summary: "watch refresh interval; defaults to 1s"},
			{Flags: "--count N", Summary: "stop watch after N snapshots"},
			{Flags: "--format TEMPLATE", Summary: "render each runtime with a Go template; prefix with 'table ' for headers"},
		}},
		{Name: "clients", Summary: "list clients attached to project runtimes", Usage: "kranz clients [--filter KEY=VALUE] [--watch] [--interval D] [--count N] [--format TEMPLATE]", Options: []Option{
			{Flags: "--filter KEY=VALUE", Summary: "filter by runtime, project, client, surface, or label; repeatable"},
			{Flags: "--watch", Summary: "refresh until interrupted"},
			{Flags: "--interval D", Summary: "watch refresh interval; defaults to 1s"},
			{Flags: "--count N", Summary: "stop watch after N snapshots"},
			{Flags: "--format TEMPLATE", Summary: "render each client with a Go template; prefix with 'table ' for headers"},
		}},
		{Name: "services", Summary: "inspect configured services", Default: "list", Children: []*Command{
			{Name: "list", Summary: "list configured services", Usage: "kranz services [--format TEMPLATE]", Options: []Option{
				{Flags: "--format TEMPLATE", Summary: "render each service with a Go template; prefix with 'table ' for headers"},
			}},
			{Name: "info", Summary: "show service details", Usage: "kranz services info SERVICE"},
		}},
		{Name: "tags", Summary: "list configured service tags", Usage: "kranz tags [--format TEMPLATE]", Options: []Option{
			{Flags: "--format TEMPLATE", Summary: "render each tag with a Go template; prefix with 'table ' for headers"},
		}},
		{Name: "project", Summary: "show project details", Usage: "kranz project"},
		{Name: "status", Summary: "show runtime status", Usage: "kranz status [SELECTOR ...] [--filter KEY=VALUE] [--watch] [--interval D] [--count N] [--format TEMPLATE]", Options: []Option{
			{Flags: "--filter KEY=VALUE", Summary: "filter selected services by name, state, or health; repeatable"},
			{Flags: "--watch", Summary: "refresh until interrupted"},
			{Flags: "--interval D", Summary: "watch refresh interval; defaults to 1s"},
			{Flags: "--count N", Summary: "stop watch after N snapshots"},
			{Flags: "--format TEMPLATE", Summary: "render each service with a Go template; prefix with 'table ' for headers"},
		}},
		{Name: "runs", Summary: "inspect and delete retained runs", Default: "list", Children: []*Command{
			{Name: "list", Summary: "list retained service and action runs", Usage: "kranz runs [TARGET ...] [--limit N] [--since D] [--status STATUS] [--format TEMPLATE]", Options: []Option{
				{Flags: "--limit N", Summary: "keep only the newest N matching runs"},
				{Flags: "--since D", Summary: "keep runs started within a duration such as 30m or 2h"},
				{Flags: "--status STATUS", Summary: "keep comma-separated statuses; repeatable"},
				{Flags: "--format TEMPLATE", Summary: "render each run with a Go template; prefix with 'table ' for headers"},
			}},
			{Name: "retention", Summary: "show per-target run retention", Usage: "kranz runs retention [TARGET ...] [--format TEMPLATE]", Options: []Option{
				{Flags: "--format TEMPLATE", Summary: "render each retention boundary with a Go template; prefix with 'table ' for headers"},
			}},
			{Name: "delete", Summary: "delete one completed run", Usage: "kranz runs delete TARGET#N --confirm", Options: []Option{
				{Flags: "--confirm", Summary: "confirm permanent removal of the run and its retained output"},
			}},
		}},
		{Name: "plan", Summary: "show a resolved lifecycle plan", Usage: "kranz plan [SELECTOR ...] [--operation start|stop|restart]", Options: []Option{
			{Flags: "--operation OPERATION", Summary: "operation to preview; defaults to start", Values: []string{"start", "stop", "restart"}},
		}},
		{Name: "graph", Summary: "print the dependency graph", Usage: "kranz graph [--format text|json|dot]", Options: []Option{
			{Flags: "--format FORMAT", Summary: "text, json, or dot; defaults to text", Values: []string{"text", "json", "dot"}},
		}},
		{Name: "ports", Summary: "inspect ports", Default: "list", Children: []*Command{
			{Name: "list", Summary: "list configured and detected ports", Usage: "kranz ports [SELECTOR ...] [--format TEMPLATE]", Options: []Option{
				{Flags: "--format TEMPLATE", Summary: "render each port with a Go template; prefix with 'table ' for headers"},
			}},
			{Name: "inspect", Summary: "identify a local port listener", Usage: "kranz ports inspect PORT"},
		}},
		{Name: "up", Summary: "create a project runtime", Usage: "kranz up [SELECTOR ...] [-d|--detach]\n  kranz up --start [-d|--detach]", Options: []Option{
			{Flags: "-d, --detach", Summary: "return after starting the independent runtime"},
			{Flags: "--start", Summary: "start every enabled service"},
		}},
		{Name: "start", Summary: "start services", Usage: "kranz start SELECTOR ..."},
		{Name: "stop", Summary: "stop services", Usage: "kranz stop SELECTOR ..."},
		{Name: "restart", Summary: "restart services", Usage: "kranz restart SELECTOR ..."},
		{Name: "reload", Summary: "reload runtime configuration"},
		{Name: "down", Summary: "stop a project runtime", Usage: "kranz down [--force]", Options: []Option{
			{Flags: "--force", Summary: "discard a runtime that no longer answers its socket"},
		}},
		{Name: "attach", Summary: "open the TUI for an active runtime"},
		{Name: "mcp", Summary: "serve project runtimes over MCP stdio; global -C/-p pin it to one", Usage: "kranz mcp"},
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
		{Name: "actions", Summary: "inspect and run actions", Default: "list", Children: []*Command{
			{Name: "list", Summary: "list actions", Usage: "kranz actions [OWNER] [--format TEMPLATE]", Options: []Option{
				{Flags: "--format TEMPLATE", Summary: "render each action with a Go template; prefix with 'table ' for headers"},
			}},
			{Name: "info", Summary: "show action details", Usage: "kranz actions info OWNER/ACTION"},
			{Name: "run", Summary: "run an action", Usage: "kranz actions run OWNER/ACTION"},
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
