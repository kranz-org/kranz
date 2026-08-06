package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadProcfileUsesCommonConfigPipeline(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "Procfile")
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("KRANZ_PROCFILE_DOTENV_VALUE=4567\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("worker: echo worker\nweb: echo $KRANZ_PROCFILE_DOTENV_VALUE\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Source != SourceProcfile {
		t.Errorf("Source = %q, want %q", cfg.Source, SourceProcfile)
	}
	if cfg.Services["web"].Command != "echo $KRANZ_PROCFILE_DOTENV_VALUE" {
		t.Errorf("Procfile command was rewritten: %q", cfg.Services["web"].Command)
	}
	if cfg.Services["web"].Env["KRANZ_PROCFILE_DOTENV_VALUE"] != "4567" {
		t.Errorf("dotenv value = %q, want 4567", cfg.Services["web"].Env["KRANZ_PROCFILE_DOTENV_VALUE"])
	}
	if cfg.Services["web"].Shell != "/bin/bash" {
		t.Errorf("default shell = %q, want /bin/bash", cfg.Services["web"].Shell)
	}
	if shutdown := cfg.Services["web"].Shutdown; shutdown.Signal != 15 || shutdown.Timeout != 30*time.Second || shutdown.ParentOnly {
		t.Errorf("Procfile shutdown default = %#v", shutdown)
	}
	if !cfg.Services["web"].PortDiscoveryEnabled() {
		t.Error("Procfile service without ports did not enable runtime discovery")
	}
	if !reflect.DeepEqual(cfg.ServiceNames(), []string{"worker", "web"}) {
		t.Errorf("ServiceNames() = %v", cfg.ServiceNames())
	}
}

func TestLoadFilesMergesProcfileLayersInOrder(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "Procfile")
	overrideDirectory := filepath.Join(directory, "override")
	if err := os.Mkdir(overrideDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(overrideDirectory, "Procfile.dev")
	if err := os.WriteFile(basePath, []byte("web: old\nworker: work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridePath, []byte("web: new\nassets: build\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFiles([]string{basePath, overridePath})
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if !reflect.DeepEqual(cfg.ServiceNames(), []string{"web", "worker", "assets"}) {
		t.Errorf("ServiceNames() = %v", cfg.ServiceNames())
	}
	if service := cfg.Services["web"]; service.Command != "new" || service.Dir != overrideDirectory {
		t.Errorf("merged web service = %#v", service)
	} else if service.Shutdown.Signal != 15 || service.Shutdown.Timeout != 30*time.Second {
		t.Errorf("merged Procfile shutdown = %#v", service.Shutdown)
	}
}

func TestDiscoverProcfileFallbacksUseReleaseOrder(t *testing.T) {
	t.Parallel()

	priority := []string{
		"kranz.yaml",
		"kranz.yml",
		"process-compose.yaml",
		"process-compose.yml",
		"Procfile.dev",
		"Procfile",
	}
	for expectedIndex, expectedName := range priority {
		expectedIndex := expectedIndex
		expectedName := expectedName
		t.Run(expectedName, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			for _, name := range priority[expectedIndex:] {
				if err := os.WriteFile(filepath.Join(directory, name), []byte("test"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			paths, err := DiscoverFiles(directory)
			if err != nil {
				t.Fatalf("DiscoverFiles() error = %v", err)
			}
			want := []string{filepath.Join(directory, expectedName)}
			if !reflect.DeepEqual(paths, want) {
				t.Errorf("DiscoverFiles() = %v, want %v", paths, want)
			}
		})
	}
}

func TestDiscoverDoesNotMergeProcfileNames(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	for _, name := range []string{"Procfile", "Procfile.dev"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("web: cmd\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := DiscoverFiles(directory)
	if err != nil {
		t.Fatalf("DiscoverFiles() error = %v", err)
	}
	want := []string{filepath.Join(directory, "Procfile.dev")}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("DiscoverFiles() = %v, want %v", paths, want)
	}
}

func TestDiscoverErrorListsProcfileFallbacks(t *testing.T) {
	t.Parallel()

	_, err := DiscoverFiles(t.TempDir())
	if err == nil {
		t.Fatal("DiscoverFiles() succeeded without a configuration")
	}
	for _, name := range []string{"kranz.yaml", "process-compose.yaml", "Procfile.dev", "Procfile"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("discovery error %q does not mention %s", err, name)
		}
	}
}

func TestParseProcfileEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contents string
		want     []string
		commands map[string]string
	}{
		{
			name:     "basic entry",
			contents: "web: go run ./cmd/web\n",
			want:     []string{"web"},
			commands: map[string]string{"web": "go run ./cmd/web"},
		},
		{
			name:     "additional colons remain in command",
			contents: "api: http://example.test:8080\n",
			want:     []string{"api"},
			commands: map[string]string{"api": "http://example.test:8080"},
		},
		{
			name:     "blank lines and comments are ignored",
			contents: "\n   \n  # comment\nweb: cmd\n",
			want:     []string{"web"},
			commands: map[string]string{"web": "cmd"},
		},
		{
			name:     "outer whitespace is trimmed and order is retained",
			contents: "  worker_1 :  bundle exec worker --queue=a:b  \nweb-2: npm run dev\n",
			want:     []string{"worker_1", "web-2"},
			commands: map[string]string{
				"worker_1": "bundle exec worker --queue=a:b",
				"web-2":    "npm run dev",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join("project", "Procfile")
			cfg, err := parseProcfile(path, []byte(test.contents))
			if err != nil {
				t.Fatalf("parseProcfile() error = %v", err)
			}
			if !reflect.DeepEqual(cfg.ServiceNames(), test.want) {
				t.Fatalf("ServiceNames() = %v, want %v", cfg.ServiceNames(), test.want)
			}
			for name, wantCommand := range test.commands {
				service := cfg.Services[name]
				if service.Command != wantCommand {
					t.Errorf("service %q command = %q, want %q", name, service.Command, wantCommand)
				}
			}
		})
	}
}

func TestParseProcfileErrorsIncludePathAndLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		contents   string
		wantLine   string
		wantDetail string
	}{
		{name: "empty command", contents: "web:\n", wantLine: "line 1", wantDetail: "empty command"},
		{name: "missing colon", contents: "web npm run dev\n", wantLine: "line 1", wantDetail: "expected <name>: <command>"},
		{name: "empty name", contents: "  : cmd\n", wantLine: "line 1", wantDetail: "empty name"},
		{name: "invalid name", contents: "web app: cmd\n", wantLine: "line 1", wantDetail: "invalid name"},
		{name: "duplicate name", contents: "web: one\n# ignored\nweb: two\n", wantLine: "line 3", wantDetail: "duplicate name"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join("somewhere", "Procfile.dev")
			cfg, err := parseProcfile(path, []byte(test.contents))
			if err == nil {
				t.Fatalf("parseProcfile() config = %#v, want error", cfg)
			}
			if cfg != nil {
				t.Errorf("parseProcfile() returned partial config: %#v", cfg)
			}
			message := err.Error()
			for _, want := range []string{path, test.wantLine, test.wantDetail} {
				if !strings.Contains(message, want) {
					t.Errorf("error %q does not contain %q", message, want)
				}
			}
		})
	}
}
