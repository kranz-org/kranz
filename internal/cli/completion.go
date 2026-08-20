package cli

import (
	"fmt"
	"sort"
	"strings"
)

// Completion scripts are generated from the same command tree the parser and
// help read, so a shell can never offer a command the binary does not have.
// Planned commands are left out for the same reason help separates them:
// completing to something that only answers "not implemented yet" wastes the
// keystroke it saved.

// CompletionShells lists the shells Completion can generate for.
func CompletionShells() []string { return []string{"bash", "zsh", "fish"} }

// Completion renders a completion script for one shell.
func Completion(tree *Command, shell string) (string, error) {
	switch shell {
	case "bash":
		return bashCompletion(tree), nil
	case "zsh":
		return zshCompletion(tree), nil
	case "fish":
		return fishCompletion(tree), nil
	default:
		return "", &Error{
			Code:     "invalid_arguments",
			Message:  fmt.Sprintf("unsupported shell %q", shell),
			Hint:     "Use one of " + strings.Join(CompletionShells(), ", ") + ".",
			ExitCode: ExitUsage,
		}
	}
}

// availableNames returns the runnable direct children of a command, sorted so
// the generated script is byte-identical between runs.
func availableNames(command *Command) []string {
	var names []string
	for _, child := range command.Children {
		if child.IsPlanned() {
			continue
		}
		names = append(names, child.Name)
	}
	sort.Strings(names)
	return names
}

const globalFlags = "-f --config -C --directory -p --project --output -h --help -v --version"

func bashCompletion(tree *Command) string {
	var output strings.Builder
	output.WriteString("# kranz bash completion\n_kranz() {\n")
	fmt.Fprintf(&output, "  local commands=%q\n", strings.Join(availableNames(tree), " "))
	fmt.Fprintf(&output, "  local globals=%q\n", globalFlags)
	output.WriteString("  local subcommands=\"\"\n  case \"${COMP_WORDS[1]}\" in\n")
	for _, child := range tree.Children {
		if child.IsPlanned() || len(child.Children) == 0 {
			continue
		}
		fmt.Fprintf(&output, "    %s) subcommands=%q ;;\n", child.Name, strings.Join(availableNames(child), " "))
	}
	output.WriteString(`  esac
  if [ "$COMP_CWORD" -eq 1 ]; then
    mapfile -t COMPREPLY < <(compgen -W "$commands $globals" -- "${COMP_WORDS[COMP_CWORD]}")
  elif [ -n "$subcommands" ] && [ "$COMP_CWORD" -eq 2 ]; then
    mapfile -t COMPREPLY < <(compgen -W "$subcommands" -- "${COMP_WORDS[COMP_CWORD]}")
  else
    mapfile -t COMPREPLY < <(compgen -W "$globals" -- "${COMP_WORDS[COMP_CWORD]}")
  fi
}
complete -F _kranz kranz
`)
	return output.String()
}

func zshCompletion(tree *Command) string {
	var output strings.Builder
	output.WriteString("#compdef kranz\n# kranz zsh completion\n_kranz() {\n  local -a commands\n  commands=(\n")
	for _, child := range tree.Children {
		if child.IsPlanned() {
			continue
		}
		fmt.Fprintf(&output, "    '%s:%s'\n", child.Name, quoteZsh(child.Summary))
	}
	output.WriteString("  )\n  if (( CURRENT == 2 )); then\n    _describe 'command' commands\n    return\n  fi\n  case \"${words[2]}\" in\n")
	for _, child := range tree.Children {
		if child.IsPlanned() || len(child.Children) == 0 {
			continue
		}
		fmt.Fprintf(&output, "    %s)\n      local -a sub\n      sub=(\n", child.Name)
		for _, grandchild := range child.Children {
			if grandchild.IsPlanned() {
				continue
			}
			fmt.Fprintf(&output, "        '%s:%s'\n", grandchild.Name, quoteZsh(grandchild.Summary))
		}
		output.WriteString("      )\n      _describe 'subcommand' sub\n      ;;\n")
	}
	output.WriteString("  esac\n}\n_kranz \"$@\"\n")
	return output.String()
}

// quoteZsh makes a summary safe inside a single-quoted _describe entry: a
// colon would end the display name, and a single quote would end the quoting.
// Each entry is quoted as a whole because an unquoted summary containing spaces
// becomes several array elements rather than one description.
func quoteZsh(summary string) string {
	return strings.ReplaceAll(strings.ReplaceAll(summary, ":", " "), "'", "")
}

func fishCompletion(tree *Command) string {
	var output strings.Builder
	output.WriteString("# kranz fish completion\n")
	output.WriteString("complete -c kranz -f\n")
	for _, child := range tree.Children {
		if child.IsPlanned() {
			continue
		}
		fmt.Fprintf(&output, "complete -c kranz -n __fish_use_subcommand -a %s -d %q\n", child.Name, child.Summary)
		for _, grandchild := range child.Children {
			if grandchild.IsPlanned() {
				continue
			}
			fmt.Fprintf(&output, "complete -c kranz -n '__fish_seen_subcommand_from %s' -a %s -d %q\n", child.Name, grandchild.Name, grandchild.Summary)
		}
	}
	output.WriteString("complete -c kranz -s f -l config -r -d 'configuration layer'\n")
	output.WriteString("complete -c kranz -s C -l directory -r -d 'working directory'\n")
	output.WriteString("complete -c kranz -s p -l project -r -d 'runtime name or ID'\n")
	output.WriteString("complete -c kranz -l output -x -a 'text json' -d 'output format'\n")
	return output.String()
}
