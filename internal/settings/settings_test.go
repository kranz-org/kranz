package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingSettingsReturnsEmpty(t *testing.T) {
	value, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil || value != (Settings{}) {
		t.Fatalf("Load missing = %#v, %v", value, err)
	}
}

func TestSaveAndLoadSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.yaml")
	want := Settings{Theme: "nord", Accent: "#88C0D0", Background: "theme", ColorMode: "dark"}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != want {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings permissions = %o, want 600", info.Mode().Perm())
	}
}
