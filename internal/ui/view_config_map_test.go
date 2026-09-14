package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/sourceview"
)

// configMapTestModel builds a synthetic project with two root-level sources, a
// nested include, and an override layer, so the tree has both a following
// sibling and a deeper child to draw. Paths are fictional and relative, per the
// repository's privacy rules.
func configMapTestModel() *Model {
	cfg := &config.Config{
		Project: "Shop",
		Services: map[string]config.Service{
			"api": {
				Command:   "exit 0",
				Dir:       ".",
				Shell:     "sh",
				Ports:     []int{8080},
				DependsOn: []string{"database"},
				Actions:   map[string]config.Action{"migrate": {Command: "true"}},
			},
			"database": {Command: "exit 0", Dir: ".", Shell: "sh", Ports: []int{5432}},
			"mod":      {Command: "exit 0", Dir: ".", Shell: "sh"},
		},
		Sources: []config.ConfigSource{
			{ID: "root", DisplayPath: "kranz.yaml", Kind: config.SourceExplicit, Order: 0},
			{ID: "api", DisplayPath: "services/api.yaml", Kind: config.SourceNestedInclude, ParentID: "root", Depth: 1, Order: 1},
			{ID: "override", DisplayPath: "services/api.override.yaml", Kind: config.SourceOverride, ParentID: "api", Depth: 2, Order: 2, Truncated: true},
			{ID: "mod", DisplayPath: "mod.yaml", Kind: config.SourceNestedInclude, ParentID: "root", Depth: 1, Order: 3},
		},
		ServiceMetadata: map[string]config.EffectiveService{
			"api":      {ID: "svc-api", SourceID: "api", SourceName: "api", DisplayName: "api"},
			"database": {ID: "svc-db", SourceID: "root", SourceName: "database", DisplayName: "database"},
			"mod":      {ID: "svc-mod", SourceID: "mod", SourceName: "mod", DisplayName: "mod"},
		},
		Provenance: []config.FieldProvenance{
			{ServiceID: "svc-api", FieldPath: "services.api.ports", ValueSourceID: "override", Stage: config.StageOverride, ReplacedSourceID: "api", OriginalValue: 9090, EffectiveValue: 8080},
			{ServiceID: "svc-api", FieldPath: "services.api.command", ValueSourceID: "override", Stage: config.StageOverride, ReplacedSourceID: "api", OriginalValue: "old", EffectiveValue: "exit 0"},
		},
	}
	model := NewModel(cfg, "test")
	model.width, model.height, model.ready = 100, 30, true
	return model
}

// plainModalText joins every rendered cell into one whitespace-separated
// stream, so assertions survive the wrap points the modal introduces.
func plainModalText(value string) string {
	return strings.Join(strings.Fields(ansi.Strip(value)), " ")
}

// configMapBodyText is the modal's own body, free of the dimmed dashboard the
// full frame composites behind it.
func configMapBodyText(model *Model) string {
	return plainModalText(strings.Join(model.configMapBodyLines(), "\n"))
}

func TestConfigMapOpensAndClosesOnKeys(t *testing.T) {
	model := configMapTestModel()
	defer model.Shutdown()

	pressKey(model, 'm')
	if model.mode != ModeConfigMap {
		t.Fatalf("m opened mode %v, want ModeConfigMap", model.mode)
	}
	if model.configMapView != configMapBySource || model.configMapOffset != 0 {
		t.Fatalf("m opened view=%v offset=%d, want by-source at the top", model.configMapView, model.configMapOffset)
	}

	// A second press must not stack a second overlay or change direction.
	pressKey(model, 'm')
	if model.mode != ModeConfigMap || model.configMapView != configMapBySource {
		t.Fatalf("second m changed mode=%v view=%v", model.mode, model.configMapView)
	}

	pressEsc(model)
	if model.mode != ModeNormal {
		t.Fatalf("esc left mode %v, want ModeNormal", model.mode)
	}
}

// pressEsc sends a raw escape key, which pressKey's rune helper cannot express.
func pressEsc(model *Model) {
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEsc})
}

// An informational modal keeps its own priority: the config map shortcut must
// not steal a key from a confirmation that is already open.
func TestConfigMapShortcutRespectsOpenModals(t *testing.T) {
	model := configMapTestModel()
	defer model.Shutdown()

	model.mode = ModeConfirmQuit
	pressKey(model, 'm')
	if model.mode != ModeConfirmQuit {
		t.Fatalf("m overrode ModeConfirmQuit: mode %v", model.mode)
	}

	model.mode = ModeHelp
	pressKey(model, 'm')
	if model.mode != ModeHelp {
		t.Fatalf("m overrode ModeHelp: mode %v", model.mode)
	}
}

// The map is a config-to-service view, not a second copy of the effective
// configuration: it names the files, the services they define, and the fields
// they overrode, and carries no service detail such as ports or actions.
func TestConfigMapShowsSourcesTreeAndContributions(t *testing.T) {
	model := configMapTestModel()
	defer model.Shutdown()
	model.mode = ModeConfigMap

	rendered := model.renderConfigMapView()
	plain := configMapBodyText(model)
	for _, expected := range []string{
		"kranz.yaml",
		"services/api.yaml",
		"api.override.yaml",
		"mod.yaml",
		"4 sources · 3 services · 2 overrides",
		"(explicit)",
		"(override)",
		"(truncated)",
		"services: database",
		"overrides services/api.yaml: api.command, api.ports",
	} {
		if !strings.Contains(plain, expected) {
			t.Errorf("source view is missing %q:\n%s", expected, plain)
		}
	}
	for _, unexpected := range []string{"ports:", "actions:", "depends:", "8080", "5432", "migrate"} {
		if strings.Contains(plain, unexpected) {
			t.Errorf("source view still shows service detail %q:\n%s", unexpected, plain)
		}
	}
	if footer := ansi.Strip(rendered); !strings.Contains(footer, "[Tab] By service") || !strings.Contains(footer, "[Esc] Close") {
		t.Errorf("source view footer is missing its controls:\n%s", footer)
	}
	if !strings.Contains(ansi.Strip(rendered), "Config Map") {
		t.Errorf("source view is missing its title:\n%s", ansi.Strip(rendered))
	}

	// The hierarchy is a tree: every file has a marker the rails run down from,
	// a following sibling keeps its rail, and the deeper child hangs off it.
	// Rows under a file repeat the rails, so a branch is not cut by the services
	// it lists.
	body := ansi.Strip(strings.Join(model.configMapBodyLines(), "\n"))
	for _, branch := range []string{"● kranz.yaml", "├─● services/api.yaml", "│ │   services: api", "│ └─● api.override.yaml", "└─● mod.yaml"} {
		if !strings.Contains(body, branch) {
			t.Errorf("tree is missing branch %q:\n%s", branch, body)
		}
	}

	// Merge order is explicit; the override layer comes last.
	rootOrder := strings.Index(plain, "kranz.yaml")
	overrideOrder := strings.Index(plain, "api.override.yaml")
	if rootOrder < 0 || overrideOrder < 0 || overrideOrder < rootOrder {
		t.Fatalf("merge order is not top to bottom: root=%d override=%d", rootOrder, overrideOrder)
	}
}

func TestConfigMapByServiceShowsDefiningAndOverridingSource(t *testing.T) {
	model := configMapTestModel()
	defer model.Shutdown()
	model.mode = ModeConfigMap

	_, _ = model.handleConfigMapKeys(tea.KeyMsg{Type: tea.KeyTab})
	if model.configMapView != configMapByService {
		t.Fatalf("tab selected view %v, want by-service", model.configMapView)
	}
	plain := configMapBodyText(model)
	for _, expected := range []string{
		"3 services · 1 overridden",
		"api services/api.yaml",
		"↳ override services/api.override.yaml", "overrides services/api.yaml: command, ports",
		"database kranz.yaml", "mod mod.yaml",
	} {
		if !strings.Contains(plain, expected) {
			t.Errorf("service view is missing %q:\n%s", expected, plain)
		}
	}
	for _, unexpected := range []string{"ports:", "actions:", "depends:"} {
		if strings.Contains(plain, unexpected) {
			t.Errorf("service view still shows service detail %q:\n%s", unexpected, plain)
		}
	}
	if footer := ansi.Strip(model.renderConfigMapView()); !strings.Contains(footer, "[Tab] By source") {
		t.Errorf("service view footer does not offer the reverse direction:\n%s", footer)
	}
}

// Scrolling must not resize the modal: a centered overlay that changes width
// jumps sideways under the cursor. The content is therefore fixed-height and
// every line is padded to one width, whatever the scroll offset.
func TestConfigMapScrollDoesNotResizeTheModal(t *testing.T) {
	model := configMapTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 16, true
	model.mode = ModeConfigMap

	maxOffset := model.maxConfigMapOffset()
	if maxOffset < 2 {
		t.Fatalf("fixture is too short to exercise scrolling: maxOffset=%d", maxOffset)
	}

	var wantWidth, wantHeight int
	for _, offset := range []int{0, 1, maxOffset / 2, maxOffset} {
		model.configMapOffset = offset
		lines := strings.Split(model.configMapContent(), "\n")
		width := 0
		for _, line := range lines {
			width = max(width, ansi.StringWidth(line))
		}
		if wantWidth == 0 {
			wantWidth, wantHeight = width, len(lines)
			continue
		}
		if width != wantWidth || len(lines) != wantHeight {
			t.Fatalf("offset %d resized the modal to %dx%d, want %dx%d", offset, width, len(lines), wantWidth, wantHeight)
		}
	}
}

func TestConfigMapHandlesMissingSourcesAndServices(t *testing.T) {
	model := NewModel(&config.Config{}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	model.mode = ModeConfigMap

	// The source view substitutes a single in-memory source.
	model.configMapView = configMapBySource
	rendered := model.renderConfigMapView()
	if !strings.Contains(ansi.Strip(rendered), config.InMemorySourceLabel) {
		t.Fatalf("source view does not name the in-memory configuration:\n%s", ansi.Strip(rendered))
	}

	// The service view has nothing to attribute but must still render.
	model.configMapView = configMapByService
	rendered = model.renderConfigMapView()
	if !strings.Contains(ansi.Strip(rendered), "0 services") {
		t.Fatalf("service view does not report an empty effective config:\n%s", ansi.Strip(rendered))
	}
	if height := lipgloss.Height(rendered); height != model.height {
		t.Fatalf("empty service view height = %d, want %d", height, model.height)
	}
}

func TestConfigMapScrollClampsToContent(t *testing.T) {
	model := configMapTestModel()
	defer model.Shutdown()
	model.height = 16
	model.mode = ModeConfigMap

	maxOffset := model.maxConfigMapOffset()
	if maxOffset == 0 {
		t.Fatal("fixture is too short to exercise scrolling")
	}
	for range maxOffset + 5 {
		pressKey(model, 'j')
	}
	if model.configMapOffset != maxOffset {
		t.Fatalf("offset = %d, want clamped to %d", model.configMapOffset, maxOffset)
	}
	pressKey(model, 'k')
	if model.configMapOffset != maxOffset-1 {
		t.Fatalf("offset = %d, want %d after scrolling up", model.configMapOffset, maxOffset-1)
	}
	// The window reports its position once the body overflows.
	if !strings.Contains(ansi.Strip(model.renderConfigMapView()), "–") {
		t.Fatal("scroll position indicator is missing from the footer")
	}
}

func TestConfigMapFrameContract(t *testing.T) {
	for _, size := range [][2]int{{64, 14}, {80, 24}, {120, 40}, {200, 60}} {
		model := configMapTestModel()
		model.width, model.height, model.ready = size[0], size[1], true
		model.mode = ModeConfigMap
		for _, view := range []configMapView{configMapBySource, configMapByService} {
			model.configMapView = view
			rendered := model.renderConfigMapView()
			if height := lipgloss.Height(rendered); height != size[1] {
				t.Fatalf("%dx%d view %v height = %d", size[0], size[1], view, height)
			}
			for index, line := range strings.Split(rendered, "\n") {
				if width := ansi.StringWidth(line); width > size[0] {
					t.Fatalf("%dx%d view %v line %d width = %d", size[0], size[1], view, index, width)
				}
			}
		}
		model.Shutdown()
	}
}

func TestConfigMapShortcutIsDiscoverableInHelp(t *testing.T) {
	found := false
	for _, section := range helpSections() {
		for _, entry := range section.entries {
			if entry.key != "m" {
				continue
			}
			found = true
			if !strings.Contains(entry.desc, "config") {
				t.Errorf("help entry for m does not describe the config map: %q", entry.desc)
			}
		}
	}
	if !found {
		t.Fatal("the config map shortcut is missing from help")
	}
}

// A composed workspace easily outgrows one screen, so the map pages and jumps
// to either end instead of forcing a line-by-line crawl.
func TestConfigMapPagesAndJumpsToEnds(t *testing.T) {
	model := configMapTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 16, true
	model.mode = ModeConfigMap

	page := model.configMapVisibleBodyHeight()
	maxOffset := model.maxConfigMapOffset()
	if maxOffset <= page {
		model.height = 14
		page, maxOffset = model.configMapVisibleBodyHeight(), model.maxConfigMapOffset()
	}
	if maxOffset == 0 {
		t.Fatal("fixture is too short to exercise paging")
	}

	press := func(keyType tea.KeyType) { _, _ = model.handleConfigMapKeys(tea.KeyMsg{Type: keyType}) }
	press(tea.KeyPgDown)
	if want := min(page, maxOffset); model.configMapOffset != want {
		t.Fatalf("pgdown offset = %d, want %d", model.configMapOffset, want)
	}
	press(tea.KeyEnd)
	if model.configMapOffset != maxOffset {
		t.Fatalf("end offset = %d, want %d", model.configMapOffset, maxOffset)
	}
	press(tea.KeyPgDown)
	if model.configMapOffset != maxOffset {
		t.Fatalf("pgdown past the end offset = %d, want %d", model.configMapOffset, maxOffset)
	}
	pressKey(model, 'g')
	if model.configMapOffset != 0 {
		t.Fatalf("g offset = %d, want 0", model.configMapOffset)
	}
	pressKey(model, 'G')
	if model.configMapOffset != maxOffset {
		t.Fatalf("G offset = %d, want %d", model.configMapOffset, maxOffset)
	}
	press(tea.KeyPgUp)
	if want := max(0, maxOffset-page); model.configMapOffset != want {
		t.Fatalf("pgup offset = %d, want %d", model.configMapOffset, want)
	}
	press(tea.KeyHome)
	if model.configMapOffset != 0 {
		t.Fatalf("home offset = %d, want 0", model.configMapOffset)
	}
}

// The dashboard and `kranz config sources` share one layout: without colour,
// the map body is exactly what internal/sourceview renders for the CLI.
func TestConfigMapMatchesTheSharedLayout(t *testing.T) {
	model := configMapTestModel()
	defer model.Shutdown()
	model.mode = ModeConfigMap

	options := sourceview.Options{
		Width:    model.configMapContentWidth(),
		Disabled: func(name string) bool { return model.cfg.Services[name].Disabled },
	}
	for view, want := range map[configMapView][]string{
		configMapBySource:  sourceview.BySource(model.cfg.SourceMap(), options),
		configMapByService: sourceview.ByService(model.cfg.SourceMap(), options),
	} {
		model.configMapView = view
		if got := ansi.Strip(strings.Join(model.configMapBodyLines(), "\n")); got != strings.Join(want, "\n") {
			t.Errorf("view %v differs from the shared layout:\ngot:\n%s\nwant:\n%s", view, got, strings.Join(want, "\n"))
		}
	}
}
