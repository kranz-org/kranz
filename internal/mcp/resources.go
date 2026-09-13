package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	"gopkg.in/yaml.v3"
)

func (s *Server) installResources() {
	definitions := []resourceDefinition{
		{URI: "kranz://runtimes", Name: "runtimes", Title: "Running Kranz runtimes", Description: "Registry sessions visible to this user. Global: it is the discovery primitive an unbound client reads first.", MimeType: "application/json", global: (*Server).runtimesResource},
		{URI: "kranz://capabilities", Name: "capabilities", Title: "MCP capabilities", Description: "Explicit Kranz MCP allow-list, addressing mode, and unavailable unsafe operations.", MimeType: "application/json", global: (*Server).capabilitiesResource},
		{URI: "kranz://session", Name: "session", Title: "Kranz runtime session", Description: "Identity, protocol, and generation of the runtime this read resolved to.", MimeType: "application/json", scoped: (*scope).sessionResource},
		{URI: "kranz://config", Name: "config", Title: "Redacted effective configuration", Description: "Effective config and provenance paths with secret-like environment values redacted.", MimeType: "application/json", scoped: (*scope).configResource},
		{URI: "kranz://services", Name: "services", Title: "Services", Description: "Declared services and their live runtime snapshots.", MimeType: "application/json", scoped: (*scope).servicesResource},
		{URI: "kranz://actions", Name: "actions", Title: "Actions", Description: "Service and project actions in declaration order.", MimeType: "application/json", scoped: (*scope).actionsResource},
		{URI: "kranz://runs", Name: "runs", Title: "Run catalog", Description: "Bounded service and action run summaries with provenance and retention state.", MimeType: "application/json", scoped: (*scope).runsResource},
		{URI: "kranz://graph", Name: "graph", Title: "Dependency and prerequisite graph", Description: "Nodes for services, action groups, and actions, with dependency, prerequisite, and ownership edges.", MimeType: "application/json", scoped: (*scope).graphResource},
		{URI: "kranz://tags", Name: "tags", Title: "Service tags", Description: "The shared service/tag selector index.", MimeType: "application/json", scoped: (*scope).tagsResource},
	}
	s.resources, s.resourceOrder = make(map[string]resourceDefinition, len(definitions)), make([]string, 0, len(definitions))
	for _, definition := range definitions {
		s.resources[definition.URI] = definition
		s.resourceOrder = append(s.resourceOrder, definition.URI)
	}
}

// runtimeScopedURI is the addressed form of a short resource URI:
// kranz://runtimes/{runtime}/config reads one named runtime, while
// kranz://config reads whichever runtime the standard resolution order picks.
func runtimeScopedURI(uri string) (runtime, short string, ok bool) {
	const prefix = "kranz://runtimes/"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(uri, prefix)
	name, resource, found := strings.Cut(rest, "/")
	if !found || name == "" || resource == "" || strings.Contains(resource, "/") {
		return "", "", false
	}
	return name, "kranz://" + resource, true
}

func (s *Server) resourceTemplates() []resourceTemplate {
	templates := make([]resourceTemplate, 0, len(s.resourceOrder))
	for _, uri := range s.resourceOrder {
		definition := s.resources[uri]
		if definition.scoped == nil {
			continue
		}
		templates = append(templates, resourceTemplate{
			URITemplate: "kranz://runtimes/{runtime}/" + definition.Name,
			Name:        definition.Name,
			Title:       definition.Title,
			Description: definition.Description + " Reads the named runtime.",
			MimeType:    definition.MimeType,
		})
	}
	return templates
}

func (s *scope) runsResource(context.Context) ResultEnvelope {
	return s.envelope(map[string]any{"runs": s.api.Runs(), "retention": s.api.RunRetention()})
}

func (s *scope) envelope(data any) ResultEnvelope {
	identity := s.session
	return ResultEnvelope{SchemaVersion: SchemaVersion, Generation: s.api.Project().Generation, Session: &identity, Data: data}
}

func (s *scope) errorEnvelope(err error) ResultEnvelope {
	causal := causalError(err)
	if causal != nil && causal.Code == "selector_not_found" {
		selector, _ := causal.Details["selector"].(string)
		causal.Message = fmt.Sprintf("service or tag %q was not found in runtime %q", selector, s.session.Name)
		causal.Hint = "This answer came from one runtime. Read kranz://runtimes before concluding the service is unavailable."
		causal.Details["current_runtime"] = s.session.Name
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		matches, discoveryErr := s.selectorMatches(ctx, selector, s.session.ID)
		cancel()
		if discoveryErr == nil && len(matches) > 0 {
			causal.Hint = fmt.Sprintf("This selector matches another running runtime; %q does not have it. Repeat the call with the runtime argument from available_in.", s.session.Name)
			if s.resolver != nil && s.resolver.Pinned() {
				// Telling a pinned connection to retry elsewhere would be
				// advice it cannot follow.
				causal.Hint = fmt.Sprintf("This selector matches another running runtime, and this MCP server is pinned to %q. Reach it from an unpinned server, or from the CLI with -p.", s.session.Name)
			}
			causal.Details["available_in"] = matches
		}
	}
	identity := s.session
	return ResultEnvelope{SchemaVersion: SchemaVersion, Generation: s.api.Project().Generation, Session: &identity, Error: causal}
}

func (s *scope) sessionResource(context.Context) ResultEnvelope {
	project := s.api.Project()
	return s.envelope(map[string]any{
		"identity": s.session, "generation": project.Generation,
		"loaded_at": project.LoadedAt, "last_reload_error": project.LastReloadError,
	})
}

// runtimesResource is answered by the MCP process, not by a runtime. It is the
// discovery primitive an unbound client reads first, so it must work before
// any runtime has been addressed.
func (s *Server) runtimesResource(ctx context.Context) ResultEnvelope {
	entries, err := s.runtimeEntries(ctx)
	if err != nil {
		return s.globalError(causalError(err))
	}
	return s.globalEnvelope(s.runtimesPayload(entries))
}

func (s *Server) runtimesPayload(entries []RuntimeEntry) map[string]any {
	payload := map[string]any{"runtimes": entries}
	if s.resolver != nil && s.resolver.Pinned() {
		payload["pinned_runtime"] = s.resolver.pin
	}
	return payload
}

func (s *scope) configResource(context.Context) ResultEnvelope {
	redacted, err := s.api.RedactedConfig()
	if err != nil {
		return s.errorEnvelope(err)
	}
	project := s.api.Project()
	effective, err := yamlValue(redacted)
	if err != nil {
		return s.errorEnvelope(err)
	}
	return s.envelope(map[string]any{
		"effective": effective,
		// Loader diagnostics are the configuration's own complaints about
		// itself. They were only visible to `kranz doctor`, which left a
		// structured client reading a config that had already been questioned.
		"diagnostics":       append([]config.CompositionDiagnostic(nil), s.api.Config().CompositionDiagnostics...),
		"last_reload_error": project.LastReloadError,
		"provenance":        s.api.Config().Provenance,
		"sources":           project.Sources,
		"reload":            map[string]any{"pending": project.Pending, "generation": project.Generation},
	})
}

func yamlValue(value any) (any, error) {
	payload, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result any
	if err := yaml.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

type serviceResourceEntry struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	SourceName      string              `json:"source_name,omitempty"`
	SourceID        string              `json:"source_id,omitempty"`
	SourcePath      string              `json:"source_path,omitempty"`
	RuntimeRevision string              `json:"runtime_revision,omitempty"`
	DesiredRevision string              `json:"desired_revision,omitempty"`
	ReloadState     string              `json:"reload_state"`
	ReloadReason    string              `json:"reload_reason,omitempty"`
	Definition      any                 `json:"definition"`
	State           config.ServiceState `json:"state"`
	DetectedPorts   []int               `json:"detected_ports"`
	DesiredRunning  bool                `json:"desired_running"`
	StatusObserved  bool                `json:"status_observed"`
	CanStart        bool                `json:"can_start"`
	CanStop         bool                `json:"can_stop"`
	Health          app.HealthSnapshot  `json:"health"`
	PrimaryAction   string              `json:"primary_action,omitempty"`
}

func (s *scope) servicesResource(context.Context) ResultEnvelope {
	services := s.api.Services()
	entries := make([]serviceResourceEntry, 0, len(services))
	for _, service := range services {
		entry, err := serviceEntry(service)
		if err != nil {
			return s.errorEnvelope(err)
		}
		entries = append(entries, entry)
	}
	return s.envelope(entries)
}

func serviceEntry(service *app.ServiceSnapshot) (serviceResourceEntry, error) {
	redacted, err := config.RedactedCopy(&config.Config{Services: map[string]config.Service{"service": service.Config}, ServiceOrder: []string{"service"}})
	if err != nil {
		return serviceResourceEntry{}, err
	}
	definition, err := yamlValue(redacted.Services["service"])
	if err != nil {
		return serviceResourceEntry{}, err
	}
	return serviceResourceEntry{ID: service.ID, Name: service.Name, SourceName: service.SourceName, SourceID: service.SourceID, SourcePath: service.SourcePath, RuntimeRevision: service.RuntimeRevision, DesiredRevision: service.DesiredRevision, ReloadState: service.ReloadState, ReloadReason: service.ReloadReason, Definition: definition, State: service.State, DetectedPorts: service.DetectedPorts, DesiredRunning: service.DesiredRunning, StatusObserved: service.StatusObserved, CanStart: service.CanStart, CanStop: service.CanStop, Health: service.Health, PrimaryAction: app.PrimaryServiceAction(service)}, nil
}

type actionResourceEntry struct {
	ID        string                 `json:"id"`
	Owner     string                 `json:"owner"`
	OwnerKind config.ActionOwnerKind `json:"owner_kind"`
	// OwnerDescription carries the owning service or action group's own
	// description, which is the only place a group describes itself outside
	// the full configuration.
	OwnerDescription string           `json:"owner_description,omitempty"`
	Name             string           `json:"name"`
	Description      string           `json:"description,omitempty"`
	Interactive      bool             `json:"interactive"`
	Confirm          bool             `json:"confirm"`
	State            app.ActionResult `json:"state"`
}

func (s *scope) actionsResource(context.Context) ResultEnvelope {
	return s.envelope(s.actionEntries(""))
}

func (s *scope) actionEntries(owner string) []actionResourceEntry {
	cfg := s.api.Config()
	entries := make([]actionResourceEntry, 0, len(cfg.ActionIDs()))
	for _, id := range cfg.ActionIDs() {
		if owner != "" && id.Owner != owner {
			continue
		}
		definition, _ := cfg.ResolveAction(id)
		state, _ := s.api.ActionState(id)
		entries = append(entries, actionResourceEntry{ID: id.Owner + "/" + id.Name, Owner: id.Owner, OwnerKind: id.OwnerKind, OwnerDescription: actionOwnerDescription(cfg, id), Name: id.Name, Description: definition.Description, Interactive: definition.InteractiveEnabled(), Confirm: definition.ConfirmationRequired(), State: state})
	}
	return entries
}

func actionOwnerDescription(cfg *config.Config, id config.ActionID) string {
	switch id.OwnerKind {
	case config.ActionOwnerGroup:
		return cfg.ActionGroups[id.Owner].Description
	case config.ActionOwnerService:
		return cfg.Services[id.Owner].Description
	default:
		return ""
	}
}

func (s *scope) graphResource(context.Context) ResultEnvelope {
	return s.envelope(s.api.Graph())
}

func (s *scope) tagsResource(context.Context) ResultEnvelope {
	cfg := s.api.Config()
	index := make(map[string][]string)
	for _, tag := range cfg.GetAllTags() {
		selected, _ := app.ResolveServiceSelectors(cfg, []string{tag})
		index[tag] = selected
	}
	return s.envelope(index)
}

func (s *Server) capabilitiesResource(context.Context) ResultEnvelope {
	addressing := "per_call"
	if s.resolver != nil && s.resolver.Pinned() {
		addressing = "pinned"
	}
	return s.globalEnvelope(map[string]any{
		"transport": "stdio", "addressing": addressing,
		"resources": append([]string(nil), s.resourceOrder...), "tools": append([]string(nil), s.toolOrder...),
		"resource_templates": s.resourceTemplates(),
		"unavailable": []string{"toggle", "StopAll", "ForceStartServices", "ForceStopServices", "Shutdown",
			"down for a runtime this MCP process did not start", "down --force", "ReleaseExternalPort",
			"external PID management", "arbitrary shell execution", "interactive actions", "interactive leases",
			"test-only app.API methods", "generic dispatch"},
	})
}
