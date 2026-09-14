package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

func writeAppearanceFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runAppearanceReload executes the command reloadSavedAppearance schedules and
// feeds its message back through Update, the same way Bubble Tea would.
func runAppearanceReload(t *testing.T, model *Model) {
	t.Helper()
	command := model.reloadSavedAppearance()
	if command == nil {
		t.Fatal("reloadSavedAppearance scheduled no command")
	}
	model.Update(command())
}

// reloadSavedAppearance must rebuild the effective appearance through the shared
// composer. Reading only the first source drops whatever an ordered override
// layer changed, which is exactly how a project customizes its theme.
func TestReloadSavedAppearanceComposesOverrideLayers(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "kranz.yaml")
	overridePath := filepath.Join(directory, "theme.yaml")
	writeAppearanceFile(t, basePath, "project: Composed\nui:\n  theme: forest\nservices:\n  api:\n    command: sleep 60\n")
	writeAppearanceFile(t, overridePath, "ui:\n  theme: dracula\n")

	options := config.LoadOptions{Directory: directory, Sources: []string{basePath}, Overrides: []string{overridePath}}
	cfg, err := config.Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Theme != "dracula" {
		t.Fatalf("setup theme = %q, want the override layer's dracula", cfg.UI.Theme)
	}

	local := app.NewLocal(cfg, nil, app.Options{LoadOptions: &options})
	defer func() { _ = local.Shutdown() }()
	model := NewModelWithOptions(cfg, "test", ModelOptions{App: local, SettingsPath: filepath.Join(directory, "settings.yaml")})
	defer model.Shutdown()

	writeAppearanceFile(t, overridePath, "ui:\n  theme: tokyo-night\n")
	runAppearanceReload(t, model)

	if model.cfg.UI.Theme != "tokyo-night" {
		t.Fatalf("reloaded project theme = %q, want the override layer's tokyo-night", model.cfg.UI.Theme)
	}
	if model.activeTheme.Name != "tokyo-night" {
		t.Fatalf("active theme = %q, want tokyo-night", model.activeTheme.Name)
	}
}

// compositionCountingAPI records ProjectComposition reads so a test can prove
// the read left the Update goroutine.
type compositionCountingAPI struct {
	app.API
	compositionCalls int
}

func (c *compositionCountingAPI) ProjectComposition() *app.CompositionRequest {
	c.compositionCalls++
	return c.API.ProjectComposition()
}

// An attached runtime answers ProjectComposition over RPC on a connection with
// no deadline. reloadSavedAppearance runs from Update, so it must not call it on
// that goroutine: a hung connection would freeze the whole TUI. The read belongs
// in the scheduled command instead.
func TestReloadSavedAppearanceDefersCompositionReadToCommand(t *testing.T) {
	directory := t.TempDir()
	cfg := &config.Config{Project: "Remote", UI: config.UIConfig{Theme: "forest"}}
	local := app.NewLocal(cfg, nil, app.Options{})
	counting := &compositionCountingAPI{API: local}
	model := NewModelWithOptions(cfg, "test", ModelOptions{App: counting, SettingsPath: filepath.Join(directory, "settings.yaml")})
	defer model.Shutdown()

	var command tea.Cmd
	if command = model.reloadSavedAppearance(); command == nil {
		t.Fatal("reloadSavedAppearance scheduled no command")
	}
	if counting.compositionCalls != 0 {
		t.Fatalf("Update path read the composition synchronously %d times", counting.compositionCalls)
	}
	if message := command(); message == nil {
		t.Fatal("reload command produced no message")
	}
	if counting.compositionCalls == 0 {
		t.Fatal("reload command never read the composition")
	}
}
