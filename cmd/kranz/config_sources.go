package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/sourceview"
)

// config sources answers "which files became my configuration, and which
// services did each define or override?" from the same provenance projection
// the dashboard's config map renders, so the two surfaces cannot disagree about
// merge order or attribution. It never reads a file the composer did not
// already read and never mutates anything.

// configSourcesWidth is the line width of the text map. CLI output is not
// sized to the terminal, so it is fixed and wide enough for typical paths.
const configSourcesWidth = 100

func runConfigSources(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	formatter, args, err := extractRowFormat("config sources", options.Output, args)
	if err != nil {
		return err
	}
	byService := false
	for _, arg := range args {
		if arg == "--by-service" {
			byService = true
			continue
		}
		return &kranzcli.Error{
			Code:     "invalid_arguments",
			Message:  fmt.Sprintf("unknown config sources argument %q", arg),
			Hint:     "The command takes options --by-service and --format only.",
			ExitCode: kranzcli.ExitUsage,
		}
	}
	cfg, _, err := loadProject(options)
	if err != nil {
		return err
	}
	sourceMap := cfg.SourceMap()
	if options.Output == kranzcli.OutputJSON {
		// The document already carries both directions.
		return kranzcli.WriteJSON(stdout, sourceMapDocument(sourceMap))
	}
	if formatter != nil && byService {
		rows := make([]map[string]any, 0, len(sourceMap.ServiceOrder))
		for _, name := range sourceMap.ServiceOrder {
			rows = append(rows, serviceOriginRow(sourceMap.Services[name]))
		}
		return formatter.write(stdout, map[string]any{"Service": "SERVICE", "DefinedIn": "DEFINED IN", "Overrides": "OVERRIDES"}, rows)
	}
	if formatter != nil {
		rows := make([]map[string]any, 0, len(sourceMap.Sources))
		for _, source := range sourceMap.Sources {
			rows = append(rows, sourceMapRow(source, sourceMap.Contributions[source.ID]))
		}
		return formatter.write(stdout, map[string]any{
			"Path": "PATH", "Kind": "KIND", "Order": "ORDER", "Depth": "DEPTH",
			"Parent": "PARENT", "Truncated": "TRUNCATED", "Services": "SERVICES", "Overrides": "OVERRIDES",
		}, rows)
	}

	// The text map is the dashboard's config map without colour.
	view := sourceview.Options{
		Width:    configSourcesWidth,
		Disabled: func(name string) bool { return cfg.Services[name].Disabled },
	}
	lines := sourceview.BySource(sourceMap, view)
	if byService {
		lines = sourceview.ByService(sourceMap, view)
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(stdout, line)
	}
	return nil
}

// serviceOriginRow is one --by-service --format record. Overrides reads
// "source: field, field" per overriding source, separated by "; ".
func serviceOriginRow(origin config.ServiceOrigin) map[string]any {
	groups := make([]string, 0, len(origin.Overrides))
	for _, group := range origin.Overrides {
		groups = append(groups, group.Source+": "+strings.Join(joinOverrides(group.Fields), ", "))
	}
	return map[string]any{
		"Service":   origin.Service,
		"DefinedIn": origin.DefinedIn,
		"Overrides": strings.Join(groups, "; "),
	}
}

func sourceMapRow(source config.ConfigSource, contribution config.SourceContribution) map[string]any {
	return map[string]any{
		"Path":      source.DisplayPath,
		"Kind":      source.Kind.Label(),
		"Order":     strconv.Itoa(source.Order + 1),
		"Depth":     strconv.Itoa(source.Depth),
		"Parent":    source.ParentID,
		"Truncated": strconv.FormatBool(source.Truncated),
		"Services":  strings.Join(contribution.Services, ", "),
		"Overrides": joinOverrides(contribution.Overrides),
	}
}

// sourceMapDocument is the structured shape: each source with the services it
// defined and overrode, plus the per-service attribution so a consumer never
// has to re-derive it.
func sourceMapDocument(sourceMap config.SourceMap) map[string]any {
	type sourceEntry struct {
		config.ConfigSource
		Contribution config.SourceContribution `json:"contribution"`
	}
	sources := make([]sourceEntry, 0, len(sourceMap.Sources))
	for _, source := range sourceMap.Sources {
		sources = append(sources, sourceEntry{ConfigSource: source, Contribution: sourceMap.Contributions[source.ID]})
	}
	return map[string]any{
		"primary_source_id": sourceMap.PrimarySourceID,
		"sources":           sources,
		"services":          sourceMap.Services,
		"service_order":     sourceMap.ServiceOrder,
	}
}

func joinOverrides(overrides []config.FieldOverride) []string {
	result := make([]string, 0, len(overrides))
	for _, override := range overrides {
		result = append(result, override.Label())
	}
	return result
}
