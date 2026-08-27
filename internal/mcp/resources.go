package mcp

import (
	"context"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	"gopkg.in/yaml.v3"
)

func (s *Server) installResources() {
	definitions := []resourceDefinition{
		{URI: "kranz://session", Name: "session", Title: "Kranz runtime session", Description: "Runtime identity, ownership mode, protocol, and generation.", MimeType: "application/json", handler: s.sessionResource},
		{URI: "kranz://config", Name: "config", Title: "Redacted effective configuration", Description: "Effective config and provenance paths with secret-like environment values redacted.", MimeType: "application/json", handler: s.configResource},
		{URI: "kranz://services", Name: "services", Title: "Services", Description: "Declared services and their live runtime snapshots.", MimeType: "application/json", handler: s.servicesResource},
		{URI: "kranz://actions", Name: "actions", Title: "Actions", Description: "Service and project actions in declaration order.", MimeType: "application/json", handler: s.actionsResource},
		{URI: "kranz://runs", Name: "runs", Title: "Run catalog", Description: "Bounded service and action run summaries with provenance and retention state.", MimeType: "application/json", handler: s.runsResource},
		{URI: "kranz://graph", Name: "graph", Title: "Dependency and prerequisite graph", Description: "Nodes for services, action groups, and actions, with dependency, prerequisite, and ownership edges.", MimeType: "application/json", handler: s.graphResource},
		{URI: "kranz://tags", Name: "tags", Title: "Service tags", Description: "The shared service/tag selector index.", MimeType: "application/json", handler: s.tagsResource},
		{URI: "kranz://capabilities", Name: "capabilities", Title: "MCP capabilities", Description: "Explicit Kranz MCP allow-list and unavailable unsafe operations.", MimeType: "application/json", handler: s.capabilitiesResource},
	}
	s.resources, s.resourceOrder = make(map[string]resourceDefinition, len(definitions)), make([]string, 0, len(definitions))
	for _, definition := range definitions {
		s.resources[definition.URI] = definition
		s.resourceOrder = append(s.resourceOrder, definition.URI)
	}
}

func (s *Server) runsResource(context.Context) ResultEnvelope {
	return s.envelope(map[string]any{"runs": s.api.Runs(), "retention": s.api.RunRetention()})
}

func (s *Server) envelope(data any) ResultEnvelope {
	return ResultEnvelope{SchemaVersion: SchemaVersion, Generation: s.api.Project().Generation, Session: s.session, Data: data}
}

func (s *Server) errorEnvelope(err error) ResultEnvelope {
	return ResultEnvelope{SchemaVersion: SchemaVersion, Generation: s.api.Project().Generation, Session: s.session, Error: causalError(err)}
}

func (s *Server) sessionResource(context.Context) ResultEnvelope {
	project := s.api.Project()
	return s.envelope(map[string]any{"identity": s.session, "generation": project.Generation, "loaded_at": project.LoadedAt, "last_reload_error": project.LastReloadError})
}

func (s *Server) configResource(context.Context) ResultEnvelope {
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
		"diagnostics":       append([]string(nil), s.api.Config().Diagnostics...),
		"last_reload_error": project.LastReloadError,
		"provenance":        map[string]any{"config_paths": project.ConfigPaths, "watch_paths": project.WatchPaths, "source": project.Source},
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
	Name           string              `json:"name"`
	Definition     any                 `json:"definition"`
	State          config.ServiceState `json:"state"`
	DetectedPorts  []int               `json:"detected_ports"`
	DesiredRunning bool                `json:"desired_running"`
	StatusObserved bool                `json:"status_observed"`
	CanStart       bool                `json:"can_start"`
	CanStop        bool                `json:"can_stop"`
	Health         app.HealthSnapshot  `json:"health"`
	PrimaryAction  string              `json:"primary_action,omitempty"`
}

func (s *Server) servicesResource(context.Context) ResultEnvelope {
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
	return serviceResourceEntry{Name: service.Name, Definition: definition, State: service.State, DetectedPorts: service.DetectedPorts, DesiredRunning: service.DesiredRunning, StatusObserved: service.StatusObserved, CanStart: service.CanStart, CanStop: service.CanStop, Health: service.Health, PrimaryAction: app.PrimaryServiceAction(service)}, nil
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

func (s *Server) actionsResource(context.Context) ResultEnvelope {
	return s.envelope(s.actionEntries(""))
}

func (s *Server) actionEntries(owner string) []actionResourceEntry {
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

func (s *Server) graphResource(context.Context) ResultEnvelope {
	return s.envelope(s.api.Graph())
}

func (s *Server) tagsResource(context.Context) ResultEnvelope {
	cfg := s.api.Config()
	index := make(map[string][]string)
	for _, tag := range cfg.GetAllTags() {
		selected, _ := app.ResolveServiceSelectors(cfg, []string{tag})
		index[tag] = selected
	}
	return s.envelope(index)
}

func (s *Server) capabilitiesResource(context.Context) ResultEnvelope {
	return s.envelope(map[string]any{
		"transport": "stdio", "connection_mode": s.session.ConnectionMode,
		"resources": append([]string(nil), s.resourceOrder...), "tools": append([]string(nil), s.toolOrder...),
		"unavailable": []string{"toggle", "StopAll", "ForceStartServices", "ForceStopServices", "Shutdown", "down", "down --force", "ReleaseExternalPort", "external PID management", "arbitrary shell execution", "interactive actions", "interactive leases", "test-only app.API methods", "generic dispatch"},
	})
}
