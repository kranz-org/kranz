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
	fmt.Fprintf(&output, "%s — %s.\n\nUsage:\n  %s\n", name, command.Summary, usage)
	if len(command.Children) > 0 {
		output.WriteString("\nCommands:\n")
		for _, child := range command.Children {
			fmt.Fprintf(&output, "  %-12s %s\n", child.Name, child.Summary)
		}
	}
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
