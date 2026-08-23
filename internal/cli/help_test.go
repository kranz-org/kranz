package cli

import (
	"regexp"
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

// usageFlag finds the option spellings a usage line advertises.
var usageFlag = regexp.MustCompile(`--?[a-zA-Z][a-zA-Z-]*`)

// A usage line can only spell an option. Every flag it spells therefore has to
// have an entry saying what it means, or help names something it never explains
// — which is how `--run N` shipped without anywhere saying that a negative N
// counts back from the latest run.
func TestEveryFlagInAUsageLineIsDocumented(t *testing.T) {
	var walk func(command *Command, path []string)
	walk = func(command *Command, path []string) {
		documented := map[string]bool{}
		for _, option := range command.Options {
			for _, field := range strings.Fields(strings.ReplaceAll(option.Flags, ",", " ")) {
				if strings.HasPrefix(field, "-") {
					documented[field] = true
				}
			}
		}
		for _, flag := range usageFlag.FindAllString(command.Usage, -1) {
			if !documented[flag] {
				t.Errorf("`kranz %s` spells %s in its usage line but documents no such option", PathString(path), flag)
			}
		}
		for _, child := range command.Children {
			walk(child, append(append([]string(nil), path...), child.Name))
		}
	}
	walk(DefaultTree(), nil)
}

// The option block is what the user reads instead of the source, so it has to
// reach them: rendered under its own heading, with a description beside every
// flag.
func TestHelpRendersDocumentedOptions(t *testing.T) {
	var walk func(command *Command, path []string)
	walk = func(command *Command, path []string) {
		if len(command.Options) > 0 {
			output, err := Help(DefaultTree(), path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, "\nOptions:\n") {
				t.Errorf("help for `kranz %s` has no options section:\n%s", PathString(path), output)
			}
			for _, option := range command.Options {
				if option.Summary == "" {
					t.Errorf("`kranz %s` documents %s with no description", PathString(path), option.Flags)
				}
				if !strings.Contains(output, option.Flags) {
					t.Errorf("help for `kranz %s` omits %s:\n%s", PathString(path), option.Flags, output)
				}
			}
		}
		for _, child := range command.Children {
			walk(child, append(append([]string(nil), path...), child.Name))
		}
	}
	walk(DefaultTree(), nil)
}

// A described flag has to keep its description on one screen. Wrapping is what
// lets an entry explain a value instead of restating its name.
func TestOptionDescriptionsWrapWithinTheColumn(t *testing.T) {
	output, err := Help(DefaultTree(), []string{"logs", "show"})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(output, "\n") {
		if len(line) > optionWidth {
			t.Errorf("help line runs past %d columns: %q", optionWidth, line)
		}
	}
	// The sentence wraps, so the assertion is on words that survive wrapping:
	// what matters is that help says a negative run is an offset at all.
	for _, want := range []string{"negative offset", "before it"} {
		if !strings.Contains(output, want) {
			t.Errorf("logs help does not explain what a negative --run means (missing %q):\n%s", want, output)
		}
	}
}
