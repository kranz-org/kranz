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
