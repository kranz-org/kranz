package cli

import (
	"fmt"
	"strings"
)

func Help(tree *Command, path []string) (string, error) {
	command, err := tree.Resolve(path)
	if err != nil {
		return "", usageError("unknown_command", err.Error())
	}
	name := "kranz"
	if len(path) > 0 {
		name += " " + PathString(path)
	}
	usage := command.Usage
	if usage == "" {
		usage = name
		if len(command.Children) > 0 {
			usage += " COMMAND"
		}
	}

	var output strings.Builder
	fmt.Fprintf(&output, "%s — %s.\n", name, command.Summary)
	if command.IsPlanned() {
		output.WriteString("\nThis command is planned for v0.8.0 and this build does not implement it yet.\n")
	}
	fmt.Fprintf(&output, "\nUsage:\n  %s\n", usage)

	// Working and planned commands are listed apart so the help never presents
	// reserved grammar as something this build can actually run.
	var available, planned []*Command
	for _, child := range command.Children {
		if child.IsPlanned() {
			planned = append(planned, child)
			continue
		}
		available = append(available, child)
	}
	writeSection(&output, "Commands", available)
	writeSection(&output, "Planned for v0.8.0 (not implemented yet)", planned)
	writeOptions(&output, "Options", command.Options)
	writeOptions(&output, "Global options", GlobalFlags())
	return output.String(), nil
}

func writeSection(output *strings.Builder, title string, commands []*Command) {
	if len(commands) == 0 {
		return
	}
	fmt.Fprintf(output, "\n%s:\n", title)
	for _, command := range commands {
		fmt.Fprintf(output, "  %-12s %s\n", command.Name, command.Summary)
	}
}

// Option descriptions share the column the global options block already uses,
// so a command's own flags and the global ones read as one list rather than two
// tables that happen to sit next to each other.
const (
	optionColumn = 26
	optionWidth  = 78
)

// writeOptions documents the flags a command parses. A usage line can only
// spell an option; what its value means has to be written down somewhere the
// user looks, and --help is where they look.
func writeOptions(output *strings.Builder, title string, options []Option) {
	if len(options) == 0 {
		return
	}
	fmt.Fprintf(output, "\n%s:\n", title)
	// Where a block mixes short and long spellings, the long-only flags are
	// indented past the empty short-form column so every `--name` starts in the
	// same place; a block with no short forms at all needs no such gap.
	indent := ""
	for _, option := range options {
		if spellings := option.Spellings(); len(spellings) > 0 && !strings.HasPrefix(spellings[0], "--") {
			indent = "    "
			break
		}
	}
	for _, option := range options {
		flags := option.Flags
		if spellings := option.Spellings(); len(spellings) > 0 && strings.HasPrefix(spellings[0], "--") {
			flags = indent + flags
		}
		fmt.Fprintf(output, "  %-*s", optionColumn-2, flags)
		// A flag too wide for the column starts its description on the next
		// line rather than pushing the whole block out of alignment.
		if len(flags) > optionColumn-3 {
			fmt.Fprintf(output, "\n%*s", optionColumn, "")
		}
		for index, line := range wrapText(option.Summary, optionWidth-optionColumn) {
			if index > 0 {
				fmt.Fprintf(output, "%*s", optionColumn, "")
			}
			output.WriteString(line + "\n")
		}
	}
}

// wrapText breaks a description into lines no longer than width, on spaces.
func wrapText(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	lines := []string{words[0]}
	for _, word := range words[1:] {
		last := len(lines) - 1
		if len(lines[last])+1+len(word) <= width {
			lines[last] += " " + word
			continue
		}
		lines = append(lines, word)
	}
	return lines
}
