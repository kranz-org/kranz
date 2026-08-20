package cli

import (
	"strings"
	"testing"
)

// plannedTree mirrors the shape the real tree had while v0.8 was being built:
// some commands runnable, some grammar reserved ahead of its implementation.
// The mechanism is tested here rather than against DefaultTree so it keeps
// working for the next release that reserves a command before writing it.
func plannedTree() *Command {
	return &Command{Name: "kranz", Summary: "a local service orchestrator", Children: []*Command{
		{Name: "ps", Summary: "list active project runtimes"},
		{Name: "logs", Summary: "show service logs", Planned: true},
		{Name: "config", Summary: "inspect effective configuration", Children: []*Command{
			{Name: "check", Summary: "load and validate configuration"},
			{Name: "show", Summary: "print effective configuration", Planned: true},
		}},
		{Name: "remote", Summary: "control a remote runtime", Children: []*Command{
			{Name: "add", Summary: "register a remote", Planned: true},
		}},
	}}
}

// Help must never present reserved grammar as a command this build can run.
// Advertising the full vocabulary in one undifferentiated list sent users to
// commands that only answer "not implemented yet".
func TestHelpSeparatesPlannedCommands(t *testing.T) {
	output, err := Help(plannedTree(), nil)
	if err != nil {
		t.Fatalf("Help returned an error: %v", err)
	}

	commands, planned, found := strings.Cut(output, "Planned for v0.8.0 (not implemented yet):")
	if !found {
		t.Fatalf("help does not list planned commands separately:\n%s", output)
	}
	for _, name := range []string{"ps", "config"} {
		if !strings.Contains(commands, "\n  "+name+" ") {
			t.Errorf("working command %q is missing from the Commands section", name)
		}
	}
	for _, name := range []string{"logs", "remote"} {
		if strings.Contains(commands, "\n  "+name+" ") {
			t.Errorf("planned command %q is advertised as available", name)
		}
		if !strings.Contains(planned, "\n  "+name+" ") {
			t.Errorf("planned command %q is missing from the planned section", name)
		}
	}
}

// A group is only enterable through its subcommands, so what it reports follows
// from them: every subcommand planned makes the group planned, and one working
// subcommand makes the group available.
func TestPlannedGroupsFollowTheirSubcommands(t *testing.T) {
	tree := plannedTree()
	if !tree.Child("remote").IsPlanned() {
		t.Error("a group with no implemented subcommand does not report as planned")
	}
	if tree.Child("config").IsPlanned() {
		t.Error("a group with an implemented subcommand reports as planned")
	}
	if tree.Child("ps").IsPlanned() {
		t.Error("an implemented leaf reports as planned")
	}
}

func TestHelpForPlannedCommandSaysSo(t *testing.T) {
	output, err := Help(plannedTree(), []string{"logs"})
	if err != nil {
		t.Fatalf("Help returned an error: %v", err)
	}
	if !strings.Contains(output, "does not implement it yet") {
		t.Errorf("help for a planned command does not say it is unimplemented:\n%s", output)
	}
}

// Every command v0.8.0 promises is now implemented. A planned command
// reappearing here means a release is about to ship grammar it cannot run.
func TestReleaseSurfaceHasNoPlannedCommands(t *testing.T) {
	var planned []string
	var walk func(command *Command, path []string)
	walk = func(command *Command, path []string) {
		if len(command.Children) == 0 {
			if command.Planned {
				planned = append(planned, PathString(path))
			}
			return
		}
		for _, child := range command.Children {
			walk(child, append(append([]string(nil), path...), child.Name))
		}
	}
	walk(DefaultTree(), nil)
	if len(planned) > 0 {
		t.Errorf("commands are advertised but not implemented: %s", strings.Join(planned, ", "))
	}
	if output, err := Help(DefaultTree(), nil); err != nil {
		t.Fatal(err)
	} else if strings.Contains(output, "Planned for") {
		t.Errorf("help still shows a planned section:\n%s", output)
	}
}

func TestHelpDocumentsLifecycleOptionsThatChangeCommandMeaning(t *testing.T) {
	for _, test := range []struct {
		command []string
		want    []string
	}{
		{[]string{"init"}, []string{"-y|--yes"}},
		{[]string{"config", "explain"}, []string{"--all"}},
		{[]string{"up"}, []string{"-d|--detach", "--no-start"}},
		{[]string{"down"}, []string{"--force"}},
	} {
		output, err := Help(DefaultTree(), test.command)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range test.want {
			if !strings.Contains(output, want) {
				t.Errorf("help %v omits %q:\n%s", test.command, want, output)
			}
		}
	}
}
