package config

import (
	"sort"
	"strings"
)

// The source map is a surface-neutral provenance projection of a composed
// configuration. It answers the question every delivery surface asks — which
// files were merged, in what order, and which file defined or last overrode
// each service — from one implementation, so the TUI, the CLI, and a future
// MCP adapter cannot disagree about names, ordering, or attribution.

// InMemorySourceLabel names a configuration that was assembled without a
// resolved source list (a programmatic config in tests and embedders), so a
// provenance view stays meaningful instead of empty.
const InMemorySourceLabel = "(in-memory configuration)"

// FieldOverride is one effective field a source last wrote, together with the
// file whose value it replaced when the write was an override.
type FieldOverride struct {
	// Path is the full provenance path, e.g. "services.api.ports".
	Path string `json:"path"`
	// Field is Path with the leading "services.<name>." stripped, so a row
	// reads "api.ports".
	Field string `json:"field"`
	// ReplacedSource is the display path of the file whose value this write
	// replaced; empty when the value was defined rather than overridden.
	ReplacedSource string `json:"replaced_source,omitempty"`
}

// Label renders the override the way a provenance row reads: the field and,
// when a value replaced another file's, the file it replaced.
func (o FieldOverride) Label() string {
	if o.ReplacedSource == "" {
		return o.Field
	}
	return o.Field + " (was " + o.ReplacedSource + ")"
}

// SourceContribution is what one configuration source brought to the effective
// configuration: the services it defined and the fields it last overrode. It
// deliberately carries no service detail (ports, actions, dependencies): the
// map answers "which config, which service", and the effective configuration
// already answers everything else.
type SourceContribution struct {
	Services  []string        `json:"services,omitempty"`
	Overrides []FieldOverride `json:"overrides,omitempty"`
}

// ServiceOverrideGroup is one source's overrides of one service, in
// application order.
type ServiceOverrideGroup struct {
	Source string          `json:"source"`
	Fields []FieldOverride `json:"fields"`
}

// ServiceOrigin attributes one effective service to the file that defined it
// and lists the later files that overrode it.
type ServiceOrigin struct {
	Service   string                 `json:"service"`
	DefinedIn string                 `json:"defined_in"`
	Overrides []ServiceOverrideGroup `json:"overrides,omitempty"`
}

// SourceMap is the provenance projection of one composed configuration.
type SourceMap struct {
	// Sources are the real sources in merge order. When the configuration was
	// not composed from files, Sources holds a single synthetic entry labelled
	// InMemorySourceLabel.
	Sources []ConfigSource
	// PrimarySourceID is the source a service falls back to when no
	// ServiceMetadata attributes it. Empty for an in-memory configuration.
	PrimarySourceID string
	// Contributions is keyed by source ID. Every source in Sources has an
	// entry, even when it contributed nothing.
	Contributions map[string]SourceContribution
	// Services is keyed by service name; ServiceOrder lists those names in
	// declaration order.
	Services     map[string]ServiceOrigin
	ServiceOrder []string
}

// SourceMap projects the composed configuration into per-source and per-service
// provenance. It is pure: it reads only the receiver and never touches disk.
func (c *Config) SourceMap() SourceMap {
	sources := append([]ConfigSource(nil), c.Sources...)
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Order < sources[j].Order })

	primaryID := ""
	if len(sources) > 0 {
		primaryID = sources[0].ID
	}
	displayByID := make(map[string]string, len(sources))
	for _, source := range sources {
		displayByID[source.ID] = source.DisplayPath
	}
	sourceLabel := func(id string) string {
		if id == "" {
			return ""
		}
		return displayByID[id]
	}

	// A programmatic configuration carries no sources, but the map must stay
	// meaningful: substitute one synthetic source that owns no real file.
	projected := sources
	if len(projected) == 0 {
		projected = []ConfigSource{{DisplayPath: InMemorySourceLabel, Kind: SourceExplicit}}
	}

	contributions := make(map[string]SourceContribution, len(projected))
	for _, source := range projected {
		contributions[source.ID] = SourceContribution{}
	}

	nameByServiceID := make(map[string]string, len(c.ServiceMetadata))
	for name, metadata := range c.ServiceMetadata {
		nameByServiceID[metadata.ID] = name
	}

	// Overrides are grouped per service and per overriding source, keeping the
	// order in which the sources first touched the service.
	type overrideGroup struct {
		sourceID string
		fields   []FieldOverride
	}
	groupsByService := make(map[string][]*overrideGroup)
	indexByService := make(map[string]map[string]*overrideGroup)
	for _, entry := range c.Provenance {
		if entry.Stage != StageOverride && entry.Stage != StageProtected {
			continue
		}
		name := nameByServiceID[entry.ServiceID]
		if name == "" {
			continue
		}
		override := FieldOverride{
			Path:  entry.FieldPath,
			Field: provenanceFieldLabel(entry.FieldPath),
		}
		if entry.ReplacedSourceID != "" && entry.ReplacedSourceID != entry.ValueSourceID {
			override.ReplacedSource = sourceLabel(entry.ReplacedSourceID)
		}
		if contribution, ok := contributions[entry.ValueSourceID]; ok {
			contribution.Overrides = append(contribution.Overrides, override)
			contributions[entry.ValueSourceID] = contribution
		}
		index := indexByService[name]
		if index == nil {
			index = make(map[string]*overrideGroup)
			indexByService[name] = index
		}
		group := index[entry.ValueSourceID]
		if group == nil {
			group = &overrideGroup{sourceID: entry.ValueSourceID}
			index[entry.ValueSourceID] = group
			groupsByService[name] = append(groupsByService[name], group)
		}
		group.fields = append(group.fields, override)
	}

	// A service's defining source comes from ServiceMetadata. When that is
	// absent (a programmatic config) every service is attributed to the primary
	// source, which is the only file there is.
	services := make(map[string]ServiceOrigin, len(c.Services))
	order := make([]string, 0, len(c.Services))
	for _, name := range c.ServiceNames() {
		sourceID := primaryID
		if metadata, ok := c.ServiceMetadata[name]; ok {
			sourceID = metadata.SourceID
		}
		definedIn := sourceLabel(sourceID)
		if definedIn == "" {
			definedIn = InMemorySourceLabel
		}
		origin := ServiceOrigin{Service: name, DefinedIn: definedIn}
		for _, group := range groupsByService[name] {
			label := sourceLabel(group.sourceID)
			if label == "" {
				label = InMemorySourceLabel
			}
			origin.Overrides = append(origin.Overrides, ServiceOverrideGroup{Source: label, Fields: group.fields})
		}
		services[name] = origin
		order = append(order, name)

		contribution, ok := contributions[sourceID]
		if !ok {
			continue
		}
		contribution.Services = append(contribution.Services, name)
		contributions[sourceID] = contribution
	}

	for id, contribution := range contributions {
		sort.Strings(contribution.Services)
		sort.SliceStable(contribution.Overrides, func(i, j int) bool {
			return contribution.Overrides[i].Field < contribution.Overrides[j].Field
		})
		contributions[id] = contribution
	}

	return SourceMap{
		Sources:         projected,
		PrimarySourceID: primaryID,
		Contributions:   contributions,
		Services:        services,
		ServiceOrder:    order,
	}
}

// SourceTreeNode describes where one source sits in the include tree, so every
// surface draws the same hierarchy. Rails carries the vertical continuation for
// each ancestor level ("│ " when that ancestor has a later sibling, "  "
// otherwise), so every rail runs down from the marker drawn after its
// ancestor's connector; Connector is the branch drawn before the source
// ("├─", "└─"), empty for a root.
type SourceTreeNode struct {
	Rails     string `json:"rails"`
	Connector string `json:"connector"`
	Last      bool   `json:"last"`
}

// SourceTree lays the resolved sources out as a pre-order tree keyed by source
// ID. A source whose parent is not in the list is treated as a root, so a
// partially resolved list still renders instead of vanishing.
func SourceTree(sources []ConfigSource) map[string]SourceTreeNode {
	known := make(map[string]bool, len(sources))
	for _, source := range sources {
		known[source.ID] = true
	}
	children := make(map[string][]ConfigSource, len(sources))
	roots := make([]ConfigSource, 0, 1)
	for _, source := range sources {
		if source.ParentID != "" && known[source.ParentID] {
			children[source.ParentID] = append(children[source.ParentID], source)
			continue
		}
		roots = append(roots, source)
	}

	nodes := make(map[string]SourceTreeNode, len(sources))
	var walk func(branch []ConfigSource, rails string, top bool)
	walk = func(branch []ConfigSource, rails string, top bool) {
		for index, source := range branch {
			last := index == len(branch)-1
			node := SourceTreeNode{Rails: rails, Last: last}
			if !top {
				node.Connector = "├─"
				if last {
					node.Connector = "└─"
				}
			}
			nodes[source.ID] = node
			childRails := rails
			if !top {
				if last {
					childRails += "  "
				} else {
					childRails += "│ "
				}
			}
			// A root's children inherit no rails: the root has no branch above.
			walk(children[source.ID], childRails, false)
		}
	}
	walk(roots, "", true)
	return nodes
}

// provenanceFieldLabel strips the leading services.<name>. from a provenance
// field path, so a row reads api.ports rather than services.api.ports.
func provenanceFieldLabel(fieldPath string) string {
	parts := strings.Split(fieldPath, ".")
	if len(parts) >= 3 && parts[0] == "services" {
		return strings.Join(parts[1:], ".")
	}
	return fieldPath
}
