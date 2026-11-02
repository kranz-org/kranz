package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kranz-org/kranz/internal/config"
)

func TestLoadMissingSettingsReturnsEmpty(t *testing.T) {
	value, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil || value != (Settings{}) {
		t.Fatalf("Load missing = %#v, %v", value, err)
	}
}

func TestPersonalLogCommandRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	want := Settings{Theme: "nord", Logs: &config.LogCommandConfig{Command: `lnav "$KRANZ_LOG_FILE"`, Mode: config.LogCommandTerminal}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("personal log command was not preserved")
	}
	if err := os.WriteFile(path, []byte("logs:\n  command: cat\n  mode: invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("invalid personal log command was accepted")
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
