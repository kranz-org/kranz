package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

// optionValues supplies a plausible value per metavariable, so an option that
// takes one is exercised the way a user would spell it rather than failing for
// a missing argument. An option that names its own values needs no entry here:
// those are tried directly, which is also what proves a shell completing them
// writes a command the CLI accepts.
var optionValues = map[string]string{
	"N":       "1",
	"NAME":    "sample",
	"COMMAND": "true",
	"D":       "5m",
}

// usageFailures are the codes that mean the CLI did not recognize what it was
// handed. Anything else — no configuration here, no runtime running — means the
// option was accepted and the command simply had nothing to work with.
var usageFailures = map[string]bool{
	"unknown_option":       true,
	"invalid_arguments":    true,
	"missing_option_value": true,
	"unknown_command":      true,
	"unknown_subcommand":   true,
}

// Help that lists an option the command cannot parse is worse than help that
// omits it: the user spells what they were told to and gets an error. This is
// not hypothetical — `logs` documented a -f shorthand for --follow that the
// global parser claimed as --config before logs ever saw it.
func TestEveryDocumentedOptionIsAcceptedByItsCommand(t *testing.T) {
	directory := t.TempDir()
	var walk func(command *kranzcli.Command, path []string)
	walk = func(command *kranzcli.Command, path []string) {
		for _, option := range command.Options {
			for _, spelling := range optionSpellings(option, directory) {
				t.Run(strings.Join(append(append([]string(nil), path...), spelling...), " "), func(t *testing.T) {
					args := append([]string{"-C", directory, "--output=json"}, path...)
					var stdout, stderr bytes.Buffer
					if code := execute(append(args, spelling...), &stdout, &stderr); code == 0 {
						return
					}
					var envelope struct {
						Error struct {
							Code string `json:"code"`
						} `json:"error"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
						t.Fatalf("error output is not JSON: %s%s", stdout.String(), stderr.String())
					}
					if usageFailures[envelope.Error.Code] {
						t.Errorf("help documents %s for `kranz %s`, but the command rejects it: %s",
							option.Flags, kranzcli.PathString(path), stdout.String())
					}
				})
			}
		}
		for _, child := range command.Children {
			walk(child, append(append([]string(nil), path...), child.Name))
		}
	}
	walk(kranzcli.DefaultTree(), nil)
}

// optionSpellings expands one documented option into the argument lists a user
// could type: every spelling it lists, each with a value when it takes one.
func optionSpellings(option kranzcli.Option, directory string) [][]string {
	values := option.Values
	if len(values) == 0 {
		value := ""
		if metavariable := option.Metavariable(); metavariable == "PATH" {
			value = filepath.Join(directory, "written.yaml")
		} else if metavariable != "" {
			value = optionValues[metavariable]
		}
		if value != "" {
			values = []string{value}
		}
	}
	var spellings [][]string
	for _, flag := range option.Spellings() {
		if len(values) == 0 {
			spellings = append(spellings, []string{flag})
			continue
		}
		for _, value := range values {
			spellings = append(spellings, []string{flag, value})
		}
	}
	return spellings
}

// actsOnARuntime names the commands that change something when they run. The
// value check below drives real invocations against a real configuration, so it
// stays away from these; their flags are still covered by the parse check
// above, which runs where no configuration exists.
var actsOnARuntime = map[string]bool{
	"init": true, "up": true, "start": true, "stop": true,
	"restart": true, "reload": true, "down": true, "attach": true,
}

// A shell completes an option's value from the same table help reads, so a
// value listed there has to be one the command accepts. Completing `--format`
// to a word the parser then rejects is worse than completing nothing: the shell
// wrote the error.
func TestCompletionValuesAreAcceptedByTheirCommand(t *testing.T) {
	directory := t.TempDir()
	configuration := "version: 1\nproject: sample\nservices:\n  api:\n    command: sleep 60\n"
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}

	check := func(t *testing.T, path []string, option kranzcli.Option) {
		t.Helper()
		for _, spelling := range option.Spellings() {
			for _, value := range option.Values {
				name := strings.Join(append(append([]string(nil), path...), spelling, value), " ")
				t.Run(name, func(t *testing.T) {
					// The value is checked in text mode: --output json is a
					// machine contract that answers before a format-specific
					// renderer ever runs.
					args := append([]string{"-C", directory}, path...)
					var stdout, stderr bytes.Buffer
					if code := execute(append(args, spelling, value), &stdout, &stderr); code == kranzcli.ExitUsage {
						t.Errorf("completion offers %s %s, which `kranz %s` rejects: %s",
							spelling, value, kranzcli.PathString(path), strings.TrimSpace(stderr.String()))
					}
				})
			}
		}
	}

	var walk func(command *kranzcli.Command, path []string)
	walk = func(command *kranzcli.Command, path []string) {
		if len(path) == 0 || !actsOnARuntime[path[0]] {
			for _, option := range command.Options {
				check(t, path, option)
			}
		}
		for _, child := range command.Children {
			walk(child, append(append([]string(nil), path...), child.Name))
		}
	}
	walk(kranzcli.DefaultTree(), nil)
	// A global option belongs to no command, so it is checked through one that
	// only reads.
	for _, option := range kranzcli.GlobalFlags() {
		check(t, []string{"list"}, option)
	}
}
