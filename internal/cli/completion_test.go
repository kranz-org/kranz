package cli

import (
	"strings"
	"testing"
)

// A completion script is generated from the command tree, so it must offer
// exactly the runnable surface: completing to a command that only answers "not
// implemented yet" wastes the keystroke completion saved.
func TestCompletionOffersOnlyRunnableCommands(t *testing.T) {
	tree := DefaultTree()
	for _, shell := range CompletionShells() {
		script, err := Completion(tree, shell)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		for _, name := range []string{"ps", "status", "up", "list", "plan", "doctor", "logs", "action", "init"} {
			if !strings.Contains(script, name) {
				t.Errorf("%s completion omits %q", shell, name)
			}
		}
	}
}

// A planned command must stay out of the generated script: completing to
// something that only answers "not implemented yet" wastes the keystroke
// completion saved. DefaultTree has none left, so the rule is checked against
// a tree that does.
func TestCompletionSkipsPlannedCommands(t *testing.T) {
	for _, shell := range CompletionShells() {
		script, err := Completion(plannedTree(), shell)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		if !strings.Contains(script, "ps") {
			t.Errorf("%s completion omits a working command", shell)
		}
		for _, name := range []string{"logs", "remote"} {
			if strings.Contains(script, " "+name+" ") || strings.Contains(script, "-a "+name+" ") {
				t.Errorf("%s completion offers planned command %q", shell, name)
			}
		}
	}
}

// Two runs must produce identical bytes, or a script checked into a dotfiles
// repository churns on every regeneration.
func TestCompletionIsDeterministic(t *testing.T) {
	for _, shell := range CompletionShells() {
		first, err := Completion(DefaultTree(), shell)
		if err != nil {
			t.Fatal(err)
		}
		second, err := Completion(DefaultTree(), shell)
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Errorf("%s completion is not deterministic", shell)
		}
	}
}

func TestCompletionRejectsAnUnknownShell(t *testing.T) {
	_, err := Completion(DefaultTree(), "csh")
	if err == nil {
		t.Fatal("unknown shell was accepted")
	}
	if !strings.Contains(err.Error(), "csh") {
		t.Errorf("error = %v", err)
	}
}

// Subcommands have to complete too, or `kranz action <tab>` offers nothing.
func TestCompletionIncludesSubcommands(t *testing.T) {
	for _, shell := range CompletionShells() {
		script, err := Completion(DefaultTree(), shell)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(script, "run") || !strings.Contains(script, "check") {
			t.Errorf("%s completion omits subcommands", shell)
		}
	}
}

// A shell that completes commands but not their flags leaves the user typing
// the part that is hardest to remember. Every option help documents has to
// reach every script, under the command that accepts it.
func TestCompletionOffersEveryDocumentedOption(t *testing.T) {
	for _, shell := range CompletionShells() {
		script, err := Completion(DefaultTree(), shell)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		var walk func(command *Command, path []string)
		walk = func(command *Command, path []string) {
			for _, option := range command.Options {
				for _, spelling := range option.Spellings() {
					// fish names a flag without its dashes, as -l tail or -s o.
					needle := spelling
					if shell == "fish" {
						needle = "-l " + strings.TrimPrefix(spelling, "--")
						if !strings.HasPrefix(spelling, "--") {
							needle = "-s " + strings.TrimPrefix(spelling, "-")
						}
					}
					if !strings.Contains(script, needle) {
						t.Errorf("%s completion omits %s of `kranz %s`", shell, spelling, PathString(path))
					}
				}
			}
			for _, child := range command.Children {
				walk(child, append(append([]string(nil), path...), child.Name))
			}
		}
		walk(DefaultTree(), nil)
		for _, option := range GlobalFlags() {
			for _, spelling := range option.Spellings() {
				if shell == "fish" {
					continue
				}
				if !strings.Contains(script, spelling) {
					t.Errorf("%s completion omits the global %s", shell, spelling)
				}
			}
		}
	}
}

// An option whose values a shell offers has to offer the values the parser
// takes, or completion writes a command the CLI then rejects.
func TestCompletionOffersTheValuesAnOptionAccepts(t *testing.T) {
	for _, shell := range CompletionShells() {
		script, err := Completion(DefaultTree(), shell)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		for _, option := range valuedOptions(DefaultTree()) {
			if len(option.Values) == 0 {
				continue
			}
			for _, value := range option.Values {
				if !strings.Contains(script, value) {
					t.Errorf("%s completion omits %q for %s", shell, value, option.Flags)
				}
			}
		}
	}
}
