package cli

import (
	"strings"
	"testing"
)

// Help must never present reserved grammar as a command this build can run.
// Advertising the full v0.8 vocabulary in one undifferentiated list sent users
// to commands that only answer "not implemented yet".
func TestHelpSeparatesPlannedCommands(t *testing.T) {
	output, err := Help(DefaultTree(), nil)
	if err != nil {
		t.Fatalf("Help returned an error: %v", err)
	}

	commands, planned, found := strings.Cut(output, "Planned for v0.8.0 (not implemented yet):")
	if !found {
		t.Fatalf("help does not list planned commands separately:\n%s", output)
	}

	for _, name := range []string{"ps", "status", "up", "down", "attach", "version", "list", "info", "plan", "graph", "ports", "doctor", "config", "port", "action", "completion", "logs"} {
		if !strings.Contains(commands, "\n  "+name+" ") {
			t.Errorf("working command %q is missing from the Commands section", name)
		}
	}
	for _, name := range []string{"init"} {
		if strings.Contains(commands, "\n  "+name+" ") {
			t.Errorf("planned command %q is advertised as available", name)
		}
		if !strings.Contains(planned, "\n  "+name+" ") {
			t.Errorf("planned command %q is missing from the planned section", name)
		}
	}
}

// A group is only enterable through its subcommands, so what it reports
// follows from them: every subcommand planned makes the group planned, and one
// working subcommand makes the group available.
func TestPlannedGroupsReportPlanned(t *testing.T) {
	tree := DefaultTree()
	// A group with at least one working subcommand is not planned, even while
	// its remaining subcommands are.
	for _, name := range []string{"ps", "config", "port", "action", "completion", "logs"} {
		if tree.Child(name).IsPlanned() {
			t.Errorf("%s has an implemented subcommand but reports as planned", name)
		}
	}
	if !tree.Child("config").Child("show").Planned {
		t.Error("config show is not implemented but does not say so")
	}
}

func TestHelpForPlannedCommandSaysSo(t *testing.T) {
	output, err := Help(DefaultTree(), []string{"init"})
	if err != nil {
		t.Fatalf("Help returned an error: %v", err)
	}
	if !strings.Contains(output, "does not implement it yet") {
		t.Errorf("help for a planned command does not say it is unimplemented:\n%s", output)
	}
}
