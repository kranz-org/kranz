package config

import "testing"

// sourceMapConfig builds a synthetic two-source composition with an override
// layer, so the projection's ordering, attribution, and override resolution are
// all exercised. Paths are fictional and relative, per the repository's privacy
// rules.
func sourceMapConfig() *Config {
	return &Config{
		Services: map[string]Service{
			"api":      {Command: "exit 0", Dir: ".", Shell: "sh", Ports: []int{8080}},
			"database": {Command: "exit 0", Dir: ".", Shell: "sh", Ports: []int{5432}},
		},
		// Deliberately out of order: the projection must sort by Order.
		Sources: []ConfigSource{
			{ID: "override", DisplayPath: "services/api.override.yaml", Kind: SourceOverride, ParentID: "api", Depth: 1, Order: 2, Truncated: true},
			{ID: "root", DisplayPath: "kranz.yaml", Kind: SourceExplicit, Order: 0},
			{ID: "api", DisplayPath: "services/api.yaml", Kind: SourceNestedInclude, ParentID: "root", Depth: 1, Order: 1},
		},
		ServiceOrder: []string{"database", "api"},
		ServiceMetadata: map[string]EffectiveService{
			"api":      {ID: "svc-api", SourceID: "api", SourceName: "api", DisplayName: "api"},
			"database": {ID: "svc-db", SourceID: "root", SourceName: "database", DisplayName: "database"},
		},
		Provenance: []FieldProvenance{
			{ServiceID: "svc-api", FieldPath: "services.api.ports", ValueSourceID: "override", Stage: StageOverride, ReplacedSourceID: "api"},
			{ServiceID: "svc-api", FieldPath: "services.api.command", ValueSourceID: "override", Stage: StageProtected, ReplacedSourceID: "api"},
			// A non-override stage must not be reported as an override.
			{ServiceID: "svc-db", FieldPath: "services.database.command", ValueSourceID: "root", Stage: StageExplicit},
		},
	}
}

func TestSourceMapOrdersSourcesAndAttributesContributions(t *testing.T) {
	sourceMap := sourceMapConfig().SourceMap()

	if got := []string{sourceMap.Sources[0].ID, sourceMap.Sources[1].ID, sourceMap.Sources[2].ID}; got[0] != "root" || got[1] != "api" || got[2] != "override" {
		t.Fatalf("sources are not in merge order: %v", got)
	}
	if sourceMap.PrimarySourceID != "root" {
		t.Fatalf("primary source = %q, want root", sourceMap.PrimarySourceID)
	}

	root := sourceMap.Contributions["root"]
	if len(root.Services) != 1 || root.Services[0] != "database" || len(root.Overrides) != 0 {
		t.Fatalf("root contribution = %+v", root)
	}
	api := sourceMap.Contributions["api"]
	if len(api.Services) != 1 || api.Services[0] != "api" {
		t.Fatalf("api services = %v", api.Services)
	}
	override := sourceMap.Contributions["override"]
	if len(override.Services) != 0 {
		t.Fatalf("a pure override layer must define no services: %v", override.Services)
	}
	if len(override.Overrides) != 2 {
		t.Fatalf("override fields = %v, want two", override.Overrides)
	}
	if override.Overrides[0].Field != "api.command" || override.Overrides[0].ReplacedSource != "services/api.yaml" {
		t.Fatalf("override field = %+v", override.Overrides[0])
	}
	if override.Overrides[0].Label() != "api.command (was services/api.yaml)" {
		t.Fatalf("override label = %q", override.Overrides[0].Label())
	}
}

func TestSourceMapAttributesServicesAndOverrides(t *testing.T) {
	sourceMap := sourceMapConfig().SourceMap()

	if got := sourceMap.ServiceOrder; len(got) != 2 || got[0] != "database" || got[1] != "api" {
		t.Fatalf("service order = %v", got)
	}
	api := sourceMap.Services["api"]
	if api.DefinedIn != "services/api.yaml" {
		t.Fatalf("api defined in %q", api.DefinedIn)
	}
	if len(api.Overrides) != 1 || api.Overrides[0].Source != "services/api.override.yaml" {
		t.Fatalf("api override groups = %+v", api.Overrides)
	}
	if len(api.Overrides[0].Fields) != 2 {
		t.Fatalf("api override fields = %+v", api.Overrides[0].Fields)
	}
	database := sourceMap.Services["database"]
	if database.DefinedIn != "kranz.yaml" || len(database.Overrides) != 0 {
		t.Fatalf("database origin = %+v", database)
	}
}

func TestSourceMapHandlesProgrammaticConfig(t *testing.T) {
	cfg := &Config{
		Services: map[string]Service{"solo": {Command: "exit 0"}},
	}
	sourceMap := cfg.SourceMap()

	if len(sourceMap.Sources) != 1 || sourceMap.Sources[0].DisplayPath != InMemorySourceLabel {
		t.Fatalf("sources = %+v", sourceMap.Sources)
	}
	if sourceMap.PrimarySourceID != "" {
		t.Fatalf("primary source = %q, want empty", sourceMap.PrimarySourceID)
	}
	if origin := sourceMap.Services["solo"]; origin.DefinedIn != InMemorySourceLabel {
		t.Fatalf("solo defined in %q", origin.DefinedIn)
	}
}

// The include hierarchy is a tree, not a flat indent: a branch that has a later
// sibling keeps its rail so the line below it reads as a sibling, not a child.
func TestSourceTreeDrawsRailsForFollowingSiblings(t *testing.T) {
	sources := []ConfigSource{
		{ID: "root", DisplayPath: "kranz.yaml", Order: 0},
		{ID: "api", DisplayPath: "api.yaml", ParentID: "root", Order: 1},
		{ID: "api-override", DisplayPath: "api.override.yaml", ParentID: "api", Order: 2},
		{ID: "mod", DisplayPath: "mod.yaml", ParentID: "root", Order: 3},
	}
	tree := SourceTree(sources)

	cases := map[string]SourceTreeNode{
		"root":         {Rails: "", Connector: "", Last: true},
		"api":          {Rails: "", Connector: "├─", Last: false},
		"api-override": {Rails: "│ ", Connector: "└─", Last: true},
		"mod":          {Rails: "", Connector: "└─", Last: true},
	}
	for id, want := range cases {
		if got := tree[id]; got != want {
			t.Errorf("node %q = %+v, want %+v", id, got, want)
		}
	}
}

// A source whose parent is missing must still render as a root rather than
// disappear from the tree.
func TestSourceTreeTreatsMissingParentAsRoot(t *testing.T) {
	tree := SourceTree([]ConfigSource{{ID: "orphan", DisplayPath: "orphan.yaml", ParentID: "gone", Order: 0}})
	if node, ok := tree["orphan"]; !ok || node.Connector != "" {
		t.Fatalf("orphan node = %+v, present=%t", node, ok)
	}
}

func TestProvenanceFieldLabelStripsServicePrefix(t *testing.T) {
	for path, want := range map[string]string{
		"services.api.ports": "api.ports",
		"services.api":       "services.api",
		"defaults.env.A":     "defaults.env.A",
	} {
		if got := provenanceFieldLabel(path); got != want {
			t.Errorf("provenanceFieldLabel(%q) = %q, want %q", path, got, want)
		}
	}
}
