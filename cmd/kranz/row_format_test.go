package main

import (
	"bytes"
	"strings"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

func TestRowFormatRendersDockerStyleEscapes(t *testing.T) {
	formatter, err := parseRowFormat("ps", kranzcli.OutputText, []string{"--format", `{{.PID}}\t{{upper .Name}}`})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := formatter.write(&output,
		map[string]any{"PID": "PID", "Name": "NAME"},
		[]map[string]any{{"PID": 42, "Name": "worker"}},
	); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "42\tWORKER\n"; got != want {
		t.Fatalf("formatted output = %q, want %q", got, want)
	}
}

func TestRowFormatTableWritesHeadersAndAlignsColumns(t *testing.T) {
	formatter, err := parseRowFormat("clients", kranzcli.OutputText, []string{"--format=table {{.PID}}\t{{.Client}}"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := formatter.write(&output,
		map[string]any{"PID": "PID", "Client": "CLIENT"},
		[]map[string]any{{"PID": 7, "Client": "MCP: codex"}},
	); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "PID") || !strings.HasSuffix(lines[0], "CLIENT") ||
		!strings.HasPrefix(lines[1], "7") || !strings.HasSuffix(lines[1], "MCP: codex") {
		t.Fatalf("table output = %q", output.String())
	}
}

func TestRowFormatRejectsInvalidUses(t *testing.T) {
	for _, test := range []struct {
		name   string
		output kranzcli.OutputFormat
		args   []string
	}{
		{name: "missing value", output: kranzcli.OutputText, args: []string{"--format"}},
		{name: "empty value", output: kranzcli.OutputText, args: []string{"--format="}},
		{name: "duplicate", output: kranzcli.OutputText, args: []string{"--format", "{{.PID}}", "--format", "{{.Name}}"}},
		{name: "JSON conflict", output: kranzcli.OutputJSON, args: []string{"--format", "{{.PID}}"}},
		{name: "invalid template", output: kranzcli.OutputText, args: []string{"--format", "{{"}},
		{name: "unknown argument", output: kranzcli.OutputText, args: []string{"extra"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseRowFormat("ps", test.output, test.args); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestRowFormatRejectsUnknownFieldEvenWithoutRows(t *testing.T) {
	formatter, err := parseRowFormat("ps", kranzcli.OutputText, []string{"--format", "{{.Missing}}"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := formatter.write(&output, map[string]any{"PID": "PID"}, nil); err == nil {
		t.Fatal("expected an unknown-field error")
	}
}
