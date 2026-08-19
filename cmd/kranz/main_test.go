package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionTextAndJSON(t *testing.T) {
	previousVersion, previousCommit, previousBuildTime := version, commit, buildTime
	version, commit, buildTime = "v1.2.3", "abc123", "2026-07-21T00:00:00Z"
	defer func() { version, commit, buildTime = previousVersion, previousCommit, previousBuildTime }()

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "kranz 1.2.3 (commit abc123, built 2026-07-21T00:00:00Z)\n" {
		t.Fatalf("version = %q", stdout.String())
	}

	stdout.Reset()
	if code := execute([]string{"version", "--output=json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	data := envelope["data"].(map[string]any)
	if data["version"] != "1.2.3" || data["commit"] != "abc123" {
		t.Fatalf("version envelope = %#v", envelope)
	}
}

func TestHelpUsesCommandTree(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"help", "action"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	for _, expected := range []string{"action", "list", "info", "run", "--project VALUE"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("help missing %q:\n%s", expected, stdout.String())
		}
	}
}

func TestPositionalYAMLPrintsMigrationHint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"prod.yaml"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d", code)
	}
	want := "Kranz: unknown command \"prod.yaml\".\nDid you mean `kranz -f prod.yaml`?\n"
	if stderr.String() != want || stdout.Len() != 0 {
		t.Fatalf("stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
}

func TestUsageErrorWithJSONKeepsStderrClean(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"--output=json", "unknown"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d", code)
	}
	if stderr.Len() != 0 || !json.Valid(stdout.Bytes()) {
		t.Fatalf("stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
}
