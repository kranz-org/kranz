package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseBareTUIWithGlobalCoordinates(t *testing.T) {
	invocation, err := Parse(DefaultTree(), []string{"-C", "shop", "-f", "base.yaml", "--config=dev.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invocation.CommandPath) != 0 || invocation.Globals.Directory != "shop" {
		t.Fatalf("invocation = %#v", invocation)
	}
	if !reflect.DeepEqual(invocation.Globals.ConfigPaths, []string{"base.yaml", "dev.yaml"}) {
		t.Fatalf("config paths = %v", invocation.Globals.ConfigPaths)
	}
}

func TestParseNestedCommandAndGlobalsAfterCommand(t *testing.T) {
	invocation, err := Parse(DefaultTree(), []string{"-p", "shop", "config", "show", "--output", "json", "--provenance"})
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Command() != "config show" || invocation.Globals.Project != "shop" || invocation.Globals.Output != OutputJSON {
		t.Fatalf("invocation = %#v", invocation)
	}
	if !reflect.DeepEqual(invocation.Args, []string{"--provenance"}) {
		t.Fatalf("args = %v", invocation.Args)
	}
}

func TestPositionalConfigurationGetsDirectedHint(t *testing.T) {
	_, err := Parse(DefaultTree(), []string{"prod.yaml"})
	commandError := AsError(err)
	if commandError.Code != "unknown_command" || commandError.Hint != "Did you mean `kranz -f prod.yaml`?" {
		t.Fatalf("error = %#v", commandError)
	}
}

func TestParseRejectsMissingSubcommandAndInvalidOutput(t *testing.T) {
	// A group without a default still has to be told which subcommand to run.
	grouped := &Command{Name: "kranz", Children: []*Command{
		{Name: "remote", Summary: "control a remote runtime", Children: []*Command{
			{Name: "add", Summary: "register a remote"},
		}},
	}}
	if _, err := Parse(grouped, []string{"remote"}); err == nil {
		t.Fatal("Parse([remote]) succeeded without a default subcommand")
	}
	for _, args := range [][]string{{"--output", "xml", "ps"}, {"version", "extra"}} {
		if _, err := Parse(DefaultTree(), args); err == nil {
			t.Fatalf("Parse(%v) succeeded", args)
		}
	}
}

// A group exists to organize commands, but one of them is usually what the user
// meant. `kranz config` asking which subcommand to use turned "show me the
// configuration" into a usage error.
func TestGroupsRunTheirDefaultSubcommand(t *testing.T) {
	for group, want := range map[string]string{
		"config": "config show",
		"action": "action list",
		"port":   "port inspect",
	} {
		invocation, err := Parse(DefaultTree(), []string{group})
		if err != nil {
			t.Errorf("Parse([%s]) = %v", group, err)
			continue
		}
		if invocation.Command() != want {
			t.Errorf("Parse([%s]) resolved to %q, want %q", group, invocation.Command(), want)
		}
	}

	// An explicit subcommand still wins over the default.
	invocation, err := Parse(DefaultTree(), []string{"config", "check"})
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Command() != "config check" {
		t.Errorf("explicit subcommand resolved to %q", invocation.Command())
	}

	// Arguments reach the default subcommand.
	invocation, err = Parse(DefaultTree(), []string{"port", "8080"})
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Command() != "port inspect" || len(invocation.Args) != 1 || invocation.Args[0] != "8080" {
		t.Errorf("port 8080 resolved to %q with args %v", invocation.Command(), invocation.Args)
	}
}

func TestUnknownOptionIsNotReportedAsACommand(t *testing.T) {
	_, err := Parse(DefaultTree(), []string{"--wat"})
	if commandError := AsError(err); commandError.Code != "unknown_option" {
		t.Fatalf("error = %#v", commandError)
	}
}

func TestHelpAndVersionCompatibility(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help", "config"}, {"config", "show", "--help"}, {"--version"}, {"version"}} {
		if _, err := Parse(DefaultTree(), args); err != nil {
			t.Fatalf("Parse(%v): %v", args, err)
		}
	}
	help, err := Help(DefaultTree(), []string{"config"})
	if err != nil || !strings.Contains(help, "config check") && !strings.Contains(help, "check") {
		t.Fatalf("help = %q, %v", help, err)
	}
}

func TestJSONErrorIsVersionedAndDoesNotUseStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := WriteError(&stdout, &stderr, OutputJSON, usageError("bad", "broken"))
	if exitCode != ExitUsage || stderr.Len() != 0 {
		t.Fatalf("exit/stderr = %d/%q", exitCode, stderr.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["schema_version"] != float64(SchemaVersion) {
		t.Fatalf("envelope = %#v", envelope)
	}
}
