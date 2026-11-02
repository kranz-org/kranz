package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogCommandConfigurationCompositionAndShellEscapes(t *testing.T) {
	directory := t.TempDir()
	base := filepath.Join(directory, "kranz.yaml")
	override := filepath.Join(directory, "local.yaml")
	data := `project: Sample
ui:
  logs:
    command: 'cat "$$KRANZ_LOG_FILE"'
    mode: terminal
services:
  api:
    command: true
    ui:
      logs:
        command: 'sed "s/error/ERROR/"'
`
	if err := os.WriteFile(base, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte("ui:\n  logs:\n    command: cat\n    preserve_lines: true\nservices:\n  api:\n    ui:\n      logs: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(base)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UI.Logs.Command != `cat "$KRANZ_LOG_FILE"` || loaded.UI.Logs.Mode != LogCommandTerminal || loaded.Services["api"].UI.Logs == nil {
		t.Fatal("log tool configuration or escaped shell variable was lost")
	}
	merged, err := LoadFiles([]string{base, override})
	if err != nil {
		t.Fatal(err)
	}
	if merged.UI.Logs.Command != "cat" || merged.UI.Logs.Mode != "" || !merged.UI.Logs.PreserveLines || merged.Services["api"].UI.Logs.Command != "" {
		t.Fatal("log command replacement did not keep the command and its options atomic")
	}
	composed, err := Compose(LoadOptions{Directory: directory, Sources: []string{"kranz.yaml"}})
	if err != nil {
		t.Fatal(err)
	}
	if composed.UI.Logs == nil || composed.UI.Logs.Command != loaded.UI.Logs.Command || composed.Services["api"].UI.Logs == nil {
		t.Fatal("composition discarded log tool presentation")
	}
}

func TestLogCommandValidation(t *testing.T) {
	for _, value := range []*LogCommandConfig{nil, {}, {Command: "cat"}, {Command: "cat", Mode: LogCommandStream, PreserveLines: true}, {Command: "less", Mode: LogCommandTerminal}} {
		if err := ValidateLogCommand(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []*LogCommandConfig{{Command: "cat", Mode: "unknown"}, {Mode: LogCommandStream}, {Shell: "sh"}, {PreserveLines: true}} {
		if ValidateLogCommand(value) == nil {
			t.Fatal("invalid log command options were accepted")
		}
	}
}
