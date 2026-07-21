package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveUIAppearancePreservesDocumentAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kranz.yaml")
	data := `# project comment
project: Demo
ui:
  theme: forest # theme comment
  accent: "#2AB630"
services:
  api:
    command: exit 0
`
	if err := os.WriteFile(path, []byte(data), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := SaveUIAppearance(path, UIConfig{Theme: "nord", Background: "theme", ColorMode: "dark"}); err != nil {
		t.Fatal(err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	for _, expected := range []string{"# project comment", "# theme comment", "services:", "command: exit 0", "theme: nord", "background: theme", "color_mode: dark"} {
		if !strings.Contains(text, expected) {
			t.Errorf("saved config lost %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "accent:") {
		t.Fatalf("empty accent was not removed:\n%s", text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("config permissions = %o, want 640", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UI != (UIConfig{Theme: "nord", Background: "theme", ColorMode: "dark"}) {
		t.Fatalf("saved appearance = %#v", loaded.UI)
	}
}

func TestSaveUIAppearanceRejectsProcessCompose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process-compose.yaml")
	if err := os.WriteFile(path, []byte("version: 0.5\nprocesses:\n  api:\n    command: exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := SaveUIAppearance(path, UIConfig{Theme: "nord", Background: "terminal"})
	if err == nil || !strings.Contains(err.Error(), "native Kranz") {
		t.Fatalf("Process Compose save error = %v", err)
	}
}

func TestSaveUIAppearancePreservesConfigurationSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "shared.yaml")
	link := filepath.Join(directory, "kranz.yaml")
	if err := os.WriteFile(target, []byte("project: Demo\nservices:\n  app:\n    command: exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := SaveUIAppearance(link, UIConfig{Theme: "cream", Background: "theme"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("saving project appearance replaced the configuration symlink")
	}
	loaded, err := Load(link)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UI != (UIConfig{Theme: "cream", Background: "theme"}) {
		t.Fatalf("appearance through symlink = %#v", loaded.UI)
	}
}
