package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
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
	if err != nil || !handled || !strings.Contains(output, "--config PATH") || !strings.Contains(output, "Procfile.dev, Procfile") {
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

func TestExplicitProcfilePathsAndOrderedYAMLMerge(t *testing.T) {
	directory := t.TempDir()
	procfilePath := filepath.Join(directory, "Procfile")
	yamlPath := filepath.Join(directory, "kranz.yaml")
	if err := os.WriteFile(procfilePath, []byte("worker: echo worker\nweb: echo procfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yamlPath, []byte(`project: Explicit merge
services:
  web:
    command: echo yaml
  api:
    command: echo api
`), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{procfilePath}, {"-f", procfilePath}} {
		paths, err := configPaths(args)
		if err != nil {
			t.Fatalf("configPaths(%v) error = %v", args, err)
		}
		cfg, err := config.LoadFiles(paths)
		if err != nil {
			t.Fatalf("LoadFiles(%v) error = %v", paths, err)
		}
		if cfg.Services["web"].Command != "echo procfile" {
			t.Errorf("web command = %q", cfg.Services["web"].Command)
		}
	}

	paths, err := configPaths([]string{"-f", procfilePath, "-f", yamlPath})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFiles(paths)
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if cfg.Source != config.SourceKranz || cfg.Project != "Explicit merge" {
		t.Errorf("merged metadata = source %q project %q", cfg.Source, cfg.Project)
	}
	if cfg.Services["web"].Command != "echo yaml" {
		t.Errorf("ordered merge web command = %q, want echo yaml", cfg.Services["web"].Command)
	}
	if shutdown := cfg.Services["web"].Shutdown; shutdown.Signal != 15 || shutdown.Timeout != 30*time.Second {
		t.Errorf("ordered merge Procfile shutdown = %#v", shutdown)
	}
	if !reflect.DeepEqual(cfg.ServiceNames(), []string{"worker", "web", "api"}) {
		t.Errorf("merged service order = %v", cfg.ServiceNames())
	}
}
