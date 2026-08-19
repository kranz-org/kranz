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
	for _, args := range [][]string{{"config"}, {"--output", "xml", "ps"}, {"version", "extra"}} {
		if _, err := Parse(DefaultTree(), args); err == nil {
			t.Fatalf("Parse(%v) succeeded", args)
		}
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
