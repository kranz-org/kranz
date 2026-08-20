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

	output.WriteString(`
Global options:
  -f, --config PATH       configuration layer; repeatable
  -C, --directory DIR     working directory for discovery
  -p, --project VALUE     runtime name, ID, or unique ID prefix
      --output text|json  output format
  -h, --help              show command help
  -v, --version           show version and build metadata
`)
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
