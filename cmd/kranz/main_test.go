package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestConfigPathsSupportsRepeatedFlagsAndPositionalFiles(t *testing.T) {
	paths, err := configPaths([]string{"-f", "base.yaml", "--config=dev.yaml", "local.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"base.yaml", "dev.yaml", "local.yaml"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("config paths = %v, want %v", paths, want)
	}
}

func TestCommandInformation(t *testing.T) {
	previousVersion, previousCommit, previousBuildTime := version, commit, buildTime
	version, commit, buildTime = "v1.2.3", "abc123", "2026-07-21T00:00:00Z"
	defer func() { version, commit, buildTime = previousVersion, previousCommit, previousBuildTime }()

	output, handled, err := commandInformation([]string{"--version"})
	if err != nil || !handled || output != "kranz 1.2.3 (commit abc123, built 2026-07-21T00:00:00Z)\n" {
		t.Fatalf("version output = %q/%v/%v", output, handled, err)
	}
	output, handled, err = commandInformation([]string{"--help"})
	if err != nil || !handled || !strings.Contains(output, "--config PATH") {
		t.Fatalf("help output = %q/%v/%v", output, handled, err)
	}
	if _, handled, err = commandInformation([]string{"project.yaml"}); err != nil || handled {
		t.Fatalf("config argument treated as information = %v/%v", handled, err)
	}
}

func TestConfigPathsRejectsUnknownOptions(t *testing.T) {
	if _, err := configPaths([]string{"--wat"}); err == nil {
		t.Fatal("unknown option was accepted")
	}
}
