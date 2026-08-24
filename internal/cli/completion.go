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

// availableChildren returns the runnable direct children themselves, in the
// same order availableNames lists them.
func availableChildren(command *Command) []*Command {
	children := make([]*Command, 0, len(command.Children))
	for _, child := range command.Children {
		if child.IsPlanned() {
			continue
		}
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name < children[j].Name })
	return children
}

// completionOptions returns the options a shell should offer for a token the
// user has typed. A group that runs a default subcommand bare accepts that
// subcommand's flags under its own name, and `kranz logs --tail 5` is how those
// flags are actually typed, so the group offers them too.
func completionOptions(command *Command) []Option {
	options := append([]Option(nil), command.Options...)
	if command.Default != "" {
		if child := command.Child(command.Default); child != nil && !child.IsPlanned() {
			options = append(options, child.Options...)
		}
	}
	return options
}

// needsOwnOptions reports whether naming a subcommand changes which flags are
// legal. A group's default subcommand does not: its flags are already offered
// under the group's own name, and repeating them makes a shell list every flag
// twice.
func needsOwnOptions(group, subcommand *Command) bool {
	if len(subcommand.Options) == 0 {
		return false
	}
	inherited := strings.Join(completionSpellings(completionOptions(group)), " ")
	return strings.Join(completionSpellings(subcommand.Options), " ") != inherited
}

// completionSpellings flattens the options of a command into the flat list of
// words a shell completes against.
func completionSpellings(options []Option) []string {
	var spellings []string
	for _, option := range options {
		spellings = append(spellings, option.Spellings()...)
	}
	return spellings
}

// valueHint says how a shell should complete the argument of an option: from a
// fixed set, from the filesystem, or not at all. Guessing wrong is worse than
// silence — offering filenames after `--tail` hides that it wants a number.
type valueHint int

const (
	hintNone valueHint = iota
	hintValues
	hintPath
	hintDirectory
	hintFree
)

func optionHint(option Option) valueHint {
	switch {
	case len(option.Values) > 0:
		return hintValues
	case !option.TakesValue():
		return hintNone
	case option.Metavariable() == "PATH":
		return hintPath
	case option.Metavariable() == "DIR":
		return hintDirectory
	default:
		return hintFree
	}
}

// valuedOptions collects every option in the tree whose argument a shell can
// complete, keyed by spelling. Options are matched on the word before the
// cursor, which is a property of the flag rather than of the command it belongs
// to; no two commands spell the same flag with different values.
func valuedOptions(tree *Command) []Option {
	seen := map[string]bool{}
	var collected []Option
	var walk func(command *Command)
	walk = func(command *Command) {
		if command.IsPlanned() {
			return
		}
		for _, option := range command.Options {
			if optionHint(option) == hintNone || optionHint(option) == hintFree {
				continue
			}
			key := strings.Join(option.Spellings(), " ")
			if seen[key] {
				continue
			}
			seen[key] = true
			collected = append(collected, option)
		}
		for _, child := range command.Children {
			walk(child)
		}
	}
	walk(tree)
	for _, option := range GlobalFlags() {
		if hint := optionHint(option); hint == hintNone || hint == hintFree {
			continue
		}
		collected = append(collected, option)
	}
	return collected
}

func globalSpellings() []string { return completionSpellings(GlobalFlags()) }

func bashCompletion(tree *Command) string {
	var output strings.Builder
	// The reply is collected in a loop rather than with mapfile because macOS
	// still ships bash 3.2, where mapfile does not exist and every completion
	// would answer "command not found" instead of a word list.
	output.WriteString(`# kranz bash completion
_kranz_reply() {
  local word
  COMPREPLY=()
  while IFS= read -r word; do
    COMPREPLY+=("$word")
  done < <(compgen "$@" -- "$cur")
}
_kranz_reply_paths() {
  if type compopt >/dev/null 2>&1; then
    compopt -o filenames
  fi
  _kranz_reply "$@"
}
`)
	output.WriteString("_kranz() {\n")
	output.WriteString("  local cur prev commands globals subcommands options values\n")
	output.WriteString("  cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	output.WriteString("  prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	fmt.Fprintf(&output, "  commands=%q\n", strings.Join(availableNames(tree), " "))
	fmt.Fprintf(&output, "  globals=%q\n", strings.Join(globalSpellings(), " "))
	output.WriteString("  subcommands=\"\"\n  options=\"\"\n  values=\"\"\n")

	// The first word after `kranz` decides both what subcommands exist and,
	// through the group default, which flags are already legal.
	output.WriteString("  case \"${COMP_WORDS[1]}\" in\n")
	for _, child := range availableChildren(tree) {
		subcommands := strings.Join(availableNames(child), " ")
		options := strings.Join(completionSpellings(completionOptions(child)), " ")
		if subcommands == "" && options == "" {
			continue
		}
		fmt.Fprintf(&output, "    %s) subcommands=%q; options=%q ;;\n", child.Name, subcommands, options)
	}
	output.WriteString("  esac\n")

	// A named subcommand replaces the group's flags with its own: `logs clear`
	// takes neither --tail nor --follow.
	output.WriteString("  case \"${COMP_WORDS[1]} ${COMP_WORDS[2]}\" in\n")
	for _, child := range availableChildren(tree) {
		for _, grandchild := range availableChildren(child) {
			if !needsOwnOptions(child, grandchild) {
				continue
			}
			fmt.Fprintf(&output, "    %q) options=%q ;;\n",
				child.Name+" "+grandchild.Name,
				strings.Join(completionSpellings(grandchild.Options), " "))
		}
	}
	output.WriteString("  esac\n")

	// An option that takes a value is answered from the value, not from the
	// command: after `--source` the only useful reply is a source name.
	output.WriteString("  case \"$prev\" in\n")
	for _, option := range valuedOptions(tree) {
		pattern := strings.Join(option.Spellings(), "|")
		switch optionHint(option) {
		case hintValues:
			fmt.Fprintf(&output, "    %s) values=%q ;;\n", pattern, strings.Join(option.Values, " "))
		case hintPath:
			fmt.Fprintf(&output, "    %s) _kranz_reply_paths -f; return ;;\n", pattern)
		case hintDirectory:
			fmt.Fprintf(&output, "    %s) _kranz_reply_paths -d; return ;;\n", pattern)
		}
	}
	output.WriteString(`  esac
  if [ -n "$values" ]; then
    _kranz_reply -W "$values"
    return
  fi
  case "$cur" in
    -*)
      _kranz_reply -W "$options $globals"
      return
      ;;
  esac
  if [ "$COMP_CWORD" -eq 1 ]; then
    _kranz_reply -W "$commands"
    return
  fi
  if [ -n "$subcommands" ] && [ "$COMP_CWORD" -eq 2 ]; then
    _kranz_reply -W "$subcommands"
    return
  fi
  COMPREPLY=()
}
complete -F _kranz kranz
`)
	return output.String()
}

func zshCompletion(tree *Command) string {
	var output strings.Builder
	output.WriteString("#compdef kranz\n# kranz zsh completion\n_kranz() {\n")
	output.WriteString("  local -a commands sub opts\n  local previous=\"${words[CURRENT-1]}\"\n")
	output.WriteString("  commands=(\n")
	for _, child := range availableChildren(tree) {
		fmt.Fprintf(&output, "    '%s:%s'\n", child.Name, quoteZsh(child.Summary))
	}
	output.WriteString("  )\n")

	output.WriteString("  case \"${words[2]}\" in\n")
	for _, child := range availableChildren(tree) {
		children, options := availableChildren(child), completionOptions(child)
		if len(children) == 0 && len(options) == 0 {
			continue
		}
		fmt.Fprintf(&output, "    %s)\n", child.Name)
		writeZshArray(&output, "sub", describeCommands(children))
		writeZshArray(&output, "opts", describeOptions(options))
		output.WriteString("      ;;\n")
	}
	output.WriteString("  esac\n")

	output.WriteString("  case \"${words[2]} ${words[3]}\" in\n")
	for _, child := range availableChildren(tree) {
		for _, grandchild := range availableChildren(child) {
			if !needsOwnOptions(child, grandchild) {
				continue
			}
			fmt.Fprintf(&output, "    '%s %s')\n", child.Name, grandchild.Name)
			writeZshArray(&output, "opts", describeOptions(grandchild.Options))
			output.WriteString("      ;;\n")
		}
	}
	output.WriteString("  esac\n")

	output.WriteString("  case \"$previous\" in\n")
	for _, option := range valuedOptions(tree) {
		pattern := strings.Join(option.Spellings(), "|")
		switch optionHint(option) {
		case hintValues:
			fmt.Fprintf(&output, "    %s) compadd -- %s; return ;;\n", pattern, strings.Join(option.Values, " "))
		case hintPath:
			fmt.Fprintf(&output, "    %s) _files; return ;;\n", pattern)
		case hintDirectory:
			fmt.Fprintf(&output, "    %s) _files -/; return ;;\n", pattern)
		}
	}
	output.WriteString("  esac\n")

	output.WriteString("  if [[ \"${words[CURRENT]}\" == -* ]]; then\n    opts+=(\n")
	for _, entry := range describeOptions(GlobalFlags()) {
		fmt.Fprintf(&output, "      %s\n", entry)
	}
	output.WriteString("    )\n    _describe 'option' opts\n    return\n  fi\n")
	output.WriteString("  if (( CURRENT == 2 )); then\n    _describe 'command' commands\n    return\n  fi\n")
	output.WriteString("  if (( CURRENT == 3 )) && (( ${#sub} )); then\n    _describe 'subcommand' sub\n    return\n  fi\n")
	output.WriteString("}\n_kranz \"$@\"\n")
	return output.String()
}

// describeCommands and describeOptions build `value:description` entries, the
// form _describe reads.
func describeCommands(commands []*Command) []string {
	entries := make([]string, 0, len(commands))
	for _, command := range commands {
		entries = append(entries, fmt.Sprintf("'%s:%s'", command.Name, quoteZsh(command.Summary)))
	}
	return entries
}

func describeOptions(options []Option) []string {
	var entries []string
	for _, option := range options {
		for _, spelling := range option.Spellings() {
			entries = append(entries, fmt.Sprintf("'%s:%s'", spelling, quoteZsh(option.Summary)))
		}
	}
	return entries
}

func writeZshArray(output *strings.Builder, name string, entries []string) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprintf(output, "      %s=(\n", name)
	for _, entry := range entries {
		fmt.Fprintf(output, "        %s\n", entry)
	}
	output.WriteString("      )\n")
}

// quoteZsh makes a summary safe inside a single-quoted _describe entry: a
// colon would end the display name, and a single quote would end the quoting.
// Each entry is quoted as a whole because an unquoted summary containing spaces
// becomes several array elements rather than one description.
func quoteZsh(summary string) string {
	rewritten := strings.ReplaceAll(strings.ReplaceAll(summary, ":", " -"), "'", "")
	return strings.Join(strings.Fields(rewritten), " ")
}

func fishCompletion(tree *Command) string {
	var output strings.Builder
	output.WriteString("# kranz fish completion\n")
	output.WriteString("complete -c kranz -f\n")
	for _, child := range availableChildren(tree) {
		fmt.Fprintf(&output, "complete -c kranz -n __fish_use_subcommand -a %s -d %q\n", child.Name, child.Summary)
		// Once a subcommand has been named, the others are no longer candidates:
		// `kranz config check` is not on its way to `kranz config show`.
		condition := fishGroupCondition(child, availableNames(child))
		for _, grandchild := range availableChildren(child) {
			fmt.Fprintf(&output, "complete -c kranz -n '%s' -a %s -d %q\n", condition, grandchild.Name, grandchild.Summary)
		}
	}
	for _, child := range availableChildren(tree) {
		// A group's flags belong to it and to the subcommand it runs bare. Any
		// other subcommand replaces them, so seeing one of those rules the
		// group's own flags out: `logs clear` takes no --tail.
		var replaced []string
		for _, grandchild := range availableChildren(child) {
			if grandchild.Name != child.Default {
				replaced = append(replaced, grandchild.Name)
			}
		}
		writeFishOptions(&output, fishGroupCondition(child, replaced), completionOptions(child))
		for _, grandchild := range availableChildren(child) {
			if !needsOwnOptions(child, grandchild) {
				continue
			}
			// Two groups can own a subcommand of the same name — `config show`
			// and `logs show` — so the condition names both words.
			nested := fmt.Sprintf("__fish_seen_subcommand_from %s; and __fish_seen_subcommand_from %s", child.Name, grandchild.Name)
			writeFishOptions(&output, nested, grandchild.Options)
		}
	}
	writeFishOptions(&output, "", GlobalFlags())
	return output.String()
}

// fishGroupCondition matches a command by name, optionally ruling it out once
// one of the subcommands that supersede it has been typed.
func fishGroupCondition(command *Command, superseded []string) string {
	condition := "__fish_seen_subcommand_from " + command.Name
	if len(superseded) > 0 {
		condition += "; and not __fish_seen_subcommand_from " + strings.Join(superseded, " ")
	}
	return condition
}

// writeFishOptions renders one option per line, telling fish whether the flag
// takes a value and what may follow it.
func writeFishOptions(output *strings.Builder, condition string, options []Option) {
	for _, option := range options {
		var line strings.Builder
		line.WriteString("complete -c kranz")
		if condition != "" {
			fmt.Fprintf(&line, " -n '%s'", condition)
		}
		for _, spelling := range option.Spellings() {
			if strings.HasPrefix(spelling, "--") {
				fmt.Fprintf(&line, " -l %s", strings.TrimPrefix(spelling, "--"))
				continue
			}
			fmt.Fprintf(&line, " -s %s", strings.TrimPrefix(spelling, "-"))
		}
		switch optionHint(option) {
		case hintValues:
			fmt.Fprintf(&line, " -x -a '%s'", strings.Join(option.Values, " "))
		case hintPath:
			line.WriteString(" -r -F")
		case hintDirectory:
			line.WriteString(" -x -a '(__fish_complete_directories)'")
		case hintFree:
			line.WriteString(" -x")
		}
		fmt.Fprintf(&line, " -d %q\n", option.Summary)
		output.WriteString(line.String())
	}
}
