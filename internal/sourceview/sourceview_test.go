package sourceview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
)

// sampleMap is a root with a nested include that carries an override layer,
// followed by a sibling include, so the tree has a stem, a following sibling,
// and a deeper child. Paths are fictional and relative, per the repository's
// privacy rules.
func sampleMap() config.SourceMap {
	cfg := &config.Config{
		Services: map[string]config.Service{
			"api": {Command: "true"},
			"db":  {Command: "true", Disabled: true},
			"mod": {Command: "true"},
		},
		Sources: []config.ConfigSource{
			{ID: "root", DisplayPath: "kranz.yaml", Kind: config.SourceExplicit, Order: 0},
			{ID: "api", DisplayPath: "services/api.yaml", Kind: config.SourceNestedInclude, ParentID: "root", Depth: 1, Order: 1},
			{ID: "override", DisplayPath: "services/api.override.yaml", Kind: config.SourceOverride, ParentID: "api", Depth: 2, Order: 2, Truncated: true},
			{ID: "mod", DisplayPath: "mod.yaml", Kind: config.SourceNestedInclude, ParentID: "root", Depth: 1, Order: 3},
		},
		ServiceMetadata: map[string]config.EffectiveService{
			"api": {ID: "svc-api", SourceID: "api"},
			"db":  {ID: "svc-db", SourceID: "root"},
			"mod": {ID: "svc-mod", SourceID: "mod"},
		},
		Provenance: []config.FieldProvenance{
			{ServiceID: "svc-api", FieldPath: "services.api.ports", ValueSourceID: "override", Stage: config.StageOverride, ReplacedSourceID: "api"},
			{ServiceID: "svc-api", FieldPath: "services.api.command", ValueSourceID: "override", Stage: config.StageOverride, ReplacedSourceID: "api"},
			{ServiceID: "svc-api", FieldPath: "services.api.env.DEBUG", ValueSourceID: "override", Stage: config.StageOverride},
		},
	}
	return cfg.SourceMap()
}

// wideCatalogMap is one file defining many services, so its list cannot share
// the label's line at a narrow width.
func wideCatalogMap() config.SourceMap {
	cfg := &config.Config{
		Services:        map[string]config.Service{},
		Sources:         []config.ConfigSource{{ID: "root", DisplayPath: "kranz.yaml", Kind: config.SourceExplicit}},
		ServiceMetadata: map[string]config.EffectiveService{},
	}
	for _, name := range []string{"catalog-api", "catalog-worker", "search-indexer", "migrator"} {
		cfg.Services[name] = config.Service{Command: "true"}
		cfg.ServiceMetadata[name] = config.EffectiveService{ID: "svc-" + name, SourceID: "root"}
	}
	return cfg.SourceMap()
}

func TestBySourceDrawsTheTreeFromMarkers(t *testing.T) {
	got := strings.Join(BySource(sampleMap(), Options{Width: 80}), "\n")
	want := strings.Join([]string{
		"4 sources · 3 services · 3 overrides · resolved order; field writes shown below",
		"",
		"● kranz.yaml  (explicit)",
		"│   services: db",
		"├─● services/api.yaml",
		// Rails run down from the markers, and rows under a file repeat them.
		"│ │   services: api",
		// A nested file is named below its includer's directory.
		"│ └─● api.override.yaml  (override) (truncated)",
		// Fields are grouped by the file whose value they replaced, named once
		// in the label rather than beside every field.
		"│       overrides services/api.yaml: api.command, api.ports",
		"│       sets: api.env.DEBUG",
		"└─● mod.yaml",
		"      services: mod",
	}, "\n")
	if got != want {
		t.Errorf("source view:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// A kind reads as the include key that pulled the file in; an include is left
// untagged because the tree shows it, and brackets would read as a key hint.
func TestKindTag(t *testing.T) {
	for kind, want := range map[config.ConfigSourceKind]string{
		config.SourceNestedInclude: "",
		config.SourceGlob:          "(via glob)",
		config.SourceDiscovery:     "(via discover)",
		config.SourceOverride:      "(override)",
		config.SourceExplicit:      "(explicit)",
	} {
		if got := kindTag(kind); got != want {
			t.Errorf("kindTag(%q) = %q, want %q", kind, got, want)
		}
	}
}

// A list that does not fit after its label puts the label alone and every item
// beneath it, as the service details do, instead of wrapping mid-list.
func TestBySourceListsServicesBeneathTheLabelWhenTheyDoNotFit(t *testing.T) {
	wide := strings.Join(BySource(wideCatalogMap(), Options{Width: 80}), "\n")
	if !strings.Contains(wide, "services: catalog-api, catalog-worker, migrator, search-indexer") {
		t.Errorf("a list that fits left its label's line:\n%s", wide)
	}
	narrow := strings.Join(BySource(wideCatalogMap(), Options{Width: 40}), "\n")
	for _, line := range []string{
		"    services:\n",
		"      ↳ catalog-api\n",
		"      ↳ catalog-worker\n",
		"      ↳ migrator\n",
		"      ↳ search-indexer",
	} {
		if !strings.Contains(narrow, line) {
			t.Errorf("narrow list is missing %q:\n%s", line, narrow)
		}
	}
}

// An override group follows the same rule, and its listed fields keep the
// rails down to the next sibling.
func TestBySourceListsOverriddenFieldsBeneathTheLabelWhenTheyDoNotFit(t *testing.T) {
	lines := BySource(sampleMap(), Options{Width: 40})
	got := strings.Join(lines, "\n")
	for _, line := range []string{
		"│       overrides services/api.yaml:\n",
		"│         ↳ api.command\n",
		"│         ↳ api.ports\n",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("narrow override group is missing %q:\n%s", line, got)
		}
	}
}

// Global override layers hang off no file, so each is a root of its own; a
// blank line keeps them from reading as the last branch of the tree.
func TestBySourceSeparatesLaterRoots(t *testing.T) {
	cfg := &config.Config{
		Services: map[string]config.Service{"api": {Command: "true"}},
		Sources: []config.ConfigSource{
			{ID: "root", DisplayPath: "kranz.yaml", Kind: config.SourceExplicit},
			{ID: "api", DisplayPath: "api/kranz.yaml", Kind: config.SourceNestedInclude, ParentID: "root", Order: 1},
			{ID: "one", DisplayPath: "cli/one.yaml", Kind: config.SourceOverride, Order: 2},
			{ID: "two", DisplayPath: "cli/two.yaml", Kind: config.SourceOverride, Order: 3},
		},
		ServiceMetadata: map[string]config.EffectiveService{"api": {ID: "svc-api", SourceID: "api"}},
	}
	got := strings.Join(BySource(cfg.SourceMap(), Options{Width: 80}), "\n")
	for _, block := range []string{
		"└─● api\n      services: api\n\n● cli/one.yaml  (override)",
		"no services\n\n● cli/two.yaml  (override)",
	} {
		if !strings.Contains(got, block) {
			t.Errorf("later root is not set apart, missing %q:\n%s", block, got)
		}
	}
	if strings.HasPrefix(strings.SplitN(got, "\n\n", 2)[1], "\n") {
		t.Errorf("the first root gained a blank line:\n%s", got)
	}
}

func TestShortNames(t *testing.T) {
	sources := []config.ConfigSource{
		{ID: "root", DisplayPath: "kranz.yaml"},
		{ID: "workspace", DisplayPath: "workspace/kranz.yaml", ParentID: "root"},
		{ID: "local", DisplayPath: "workspace/overrides/local.yaml", ParentID: "workspace"},
		{ID: "platform", DisplayPath: "workspace/platform/kranz.yml", ParentID: "workspace"},
		// Included by platform but not inside its directory: named below the
		// nearest ancestor that contains it, never with "../".
		{ID: "logging", DisplayPath: "workspace/shared/logging/kranz.yaml", ParentID: "platform"},
		{ID: "compose", DisplayPath: "workspace/legacy/process-compose.yaml", ParentID: "workspace"},
		// Each file is trimmed by its own ancestors, so the same directory
		// reads differently under different includers.
		{ID: "shop-api", DisplayPath: "workspace/shop/api/kranz.yaml", ParentID: "workspace"},
		{ID: "shop-nested", DisplayPath: "workspace/shop/kranz.yaml", ParentID: "workspace"},
		{ID: "nested-api", DisplayPath: "workspace/shop/api/kranz.yml", ParentID: "shop-nested"},
	}
	want := map[string]string{
		"root":        "kranz.yaml",
		"workspace":   "workspace",
		"local":       "overrides/local.yaml",
		"platform":    "platform",
		"logging":     "shared/logging",
		"compose":     "legacy/process-compose.yaml",
		"shop-api":    "shop/api",
		"shop-nested": "shop",
		"nested-api":  "api",
	}
	got := shortNames(sources)
	for id, name := range want {
		if got[id] != name {
			t.Errorf("shortNames[%q] = %q, want %q", id, got[id], name)
		}
	}

	colliding := shortNames([]config.ConfigSource{
		{ID: "root", DisplayPath: "kranz.yaml"},
		{ID: "a", DisplayPath: "a/kranz.yaml", ParentID: "root"},
		{ID: "a-api", DisplayPath: "a/api/kranz.yaml", ParentID: "a"},
		{ID: "b", DisplayPath: "b/kranz.yaml", ParentID: "root"},
		{ID: "b-api", DisplayPath: "b/api/kranz.yaml", ParentID: "b"},
	})
	if colliding["a-api"] != "a/api/kranz.yaml" || colliding["b-api"] != "b/api/kranz.yaml" {
		t.Errorf("colliding names did not fall back to full paths: %v", colliding)
	}
}

func TestByServiceIsOneRowPerServiceWithOverridesBeneath(t *testing.T) {
	got := strings.Join(ByService(sampleMap(), Options{Width: 80, Disabled: func(name string) bool { return name == "db" }}), "\n")
	for _, line := range []string{
		"3 services · 1 overridden · defining file, then later overrides",
		"api             services/api.yaml",
		"  ↳ override    services/api.override.yaml",
		// The heading carries the service name, so fields drop it, and the
		// replaced file is named once in the group's label.
		"                  overrides services/api.yaml: command, ports",
		"                  sets: env.DEBUG",
		"db (disabled)   kranz.yaml",
		"mod             mod.yaml",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("service view is missing %q:\n%s", line, got)
		}
	}
}

// Short service names once pushed the field indent below zero and panicked.
func TestByServiceHandlesShortNames(t *testing.T) {
	cfg := &config.Config{
		Services:        map[string]config.Service{"a": {Command: "true"}},
		Sources:         []config.ConfigSource{{ID: "root", DisplayPath: "kranz.yaml", Kind: config.SourceExplicit}, {ID: "o", DisplayPath: "o.yaml", Kind: config.SourceOverride, Order: 1}},
		ServiceMetadata: map[string]config.EffectiveService{"a": {ID: "svc-a", SourceID: "root"}},
		Provenance:      []config.FieldProvenance{{ServiceID: "svc-a", FieldPath: "services.a.ports", ValueSourceID: "o", Stage: config.StageOverride, ReplacedSourceID: "root"}},
	}
	got := strings.Join(ByService(cfg.SourceMap(), Options{Width: 60}), "\n")
	if !strings.Contains(got, "overrides kranz.yaml: ports") {
		t.Fatalf("short-named service lost its override:\n%s", got)
	}
}

func TestEveryLineFitsTheWidth(t *testing.T) {
	for _, width := range []int{minWidth, 40, 80, 120} {
		for name, lines := range map[string][]string{
			"source":  BySource(sampleMap(), Options{Width: width}),
			"catalog": BySource(wideCatalogMap(), Options{Width: width}),
			"service": ByService(sampleMap(), Options{Width: width}),
		} {
			for _, line := range lines {
				if ansi.StringWidth(line) > width {
					t.Errorf("%s view at width %d overflows: %q", name, width, line)
				}
			}
		}
	}
}

func TestStylesWrapTextWithoutChangingLayout(t *testing.T) {
	tag := func(name string) func(string) string {
		return func(text string) string { return "\x1b[" + name + "m" + text + "\x1b[0m" }
	}
	styled := Options{Styles: Styles{Muted: tag("2"), Strong: tag("1"), Label: tag("3"), Accent: tag("33"), Warning: tag("31"), Disabled: tag("90")}}
	for _, width := range []int{80, 40} {
		styled.Width = width
		plainOptions := Options{Width: width}
		for name, pair := range map[string][2][]string{
			"source":  {BySource(sampleMap(), plainOptions), BySource(sampleMap(), styled)},
			"catalog": {BySource(wideCatalogMap(), plainOptions), BySource(wideCatalogMap(), styled)},
			"service": {ByService(sampleMap(), plainOptions), ByService(sampleMap(), styled)},
		} {
			plain, coloured := strings.Join(pair[0], "\n"), ansi.Strip(strings.Join(pair[1], "\n"))
			if plain != coloured {
				t.Errorf("%s view at width %d changes layout when styled:\nplain:\n%s\nstyled:\n%s", name, width, plain, coloured)
			}
		}
	}
}

func TestSharedDir(t *testing.T) {
	for _, tc := range []struct{ path, parent, want string }{
		{"services/api.override.yaml", "services/api.yaml", "services/"},
		{"services/api.yaml", "kranz.yaml", ""},
		{"shared/logging/kranz.yaml", "platform/kranz.yaml", ""},
		{"kranz.yaml", "", ""},
	} {
		if got := sharedDir(tc.path, tc.parent); got != tc.want {
			t.Errorf("sharedDir(%q, %q) = %q, want %q", tc.path, tc.parent, got, tc.want)
		}
	}
}
