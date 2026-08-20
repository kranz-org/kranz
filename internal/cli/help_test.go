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

	for _, name := range []string{"ps", "status", "up", "down", "attach", "version"} {
		if !strings.Contains(commands, "\n  "+name+" ") {
			t.Errorf("working command %q is missing from the Commands section", name)
		}
	}
	for _, name := range []string{"init", "list", "plan", "ports", "logs", "doctor", "completion"} {
		if strings.Contains(commands, "\n  "+name+" ") {
			t.Errorf("planned command %q is advertised as available", name)
		}
		if !strings.Contains(planned, "\n  "+name+" ") {
			t.Errorf("planned command %q is missing from the planned section", name)
		}
	}
}

// A group is only enterable through its subcommands, so a group whose every
// subcommand is planned must itself read as planned rather than look available
// until the user picks one.
func TestPlannedGroupsReportPlanned(t *testing.T) {
	tree := DefaultTree()
	for _, name := range []string{"config", "port", "action"} {
		if !tree.Child(name).IsPlanned() {
			t.Errorf("group %q has no implemented subcommand but does not report as planned", name)
		}
	}
	if tree.Child("ps").IsPlanned() {
		t.Error("ps is implemented but reports as planned")
	}
}

func TestHelpForPlannedCommandSaysSo(t *testing.T) {
	output, err := Help(DefaultTree(), []string{"logs"})
	if err != nil {
		t.Fatalf("Help returned an error: %v", err)
	}
	if !strings.Contains(output, "does not implement it yet") {
		t.Errorf("help for a planned command does not say it is unimplemented:\n%s", output)
	}
}
