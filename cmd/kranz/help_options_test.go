package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

// optionValues supplies a plausible value per metavariable, so an option that
// takes one is exercised the way a user would spell it rather than failing for
// a missing argument.
var optionValues = map[string]string{
	"N":       "1",
	"D":       "5m",
	"S":       "stdout",
	"NAME":    "sample",
	"COMMAND": "true",
	"FORMAT":  "text",
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
				t.Run(strings.Join(append(path, spelling[0]), " "), func(t *testing.T) {
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
	var flags []string
	value := ""
	for _, field := range strings.Fields(strings.ReplaceAll(option.Flags, ",", " ")) {
		if strings.HasPrefix(field, "-") {
			flags = append(flags, field)
			continue
		}
		if field == "PATH" {
			value = filepath.Join(directory, "written.yaml")
			continue
		}
		value = optionValues[field]
	}
	spellings := make([][]string, 0, len(flags))
	for _, flag := range flags {
		if value == "" {
			spellings = append(spellings, []string{flag})
			continue
		}
		spellings = append(spellings, []string{flag, value})
	}
	return spellings
}
