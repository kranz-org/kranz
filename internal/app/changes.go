package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kranz-org/kranz/internal/service"
)

// maxChangeEvents bounds one changes answer the way maxLogEvents bounds one
// logs answer.
const maxChangeEvents = 500

// ChangeQuery addresses a window of recorded runtime transitions. Since is the
// cursor a previous result returned; SinceGeneration is the alternative anchor
// for a caller that knows which configuration generation it last observed but
// not which sequence.
type ChangeQuery struct {
	Since           uint64   `json:"since,omitempty"`
	SinceGeneration uint64   `json:"since_generation,omitempty"`
	Kinds           []string `json:"kinds,omitempty"`
	Selectors       []string `json:"selectors,omitempty"`
	Limit           int      `json:"limit,omitempty"`
}

// ChangeResult is what happened after the requested point, oldest first.
type ChangeResult struct {
	Generation uint64               `json:"generation"`
	Changes    []service.Transition `json:"changes"`
	// Cursor is the sequence to pass as Since next time. It advances even when
	// filtering dropped every change, so a caller polling a narrow filter does
	// not re-read the same journal window forever.
	Cursor uint64 `json:"cursor"`
	Oldest uint64 `json:"oldest,omitempty"`
	Latest uint64 `json:"latest,omitempty"`
	// Truncated reports that transitions after the requested point had already
	// aged out of the bounded journal, so the answer has a hole in it.
	Truncated bool `json:"truncated"`
}

// ChangeQueryError gives the causal code for an unusable change query.
type ChangeQueryError struct {
	Code, Message, Hint string
}

func (e *ChangeQueryError) Error() string { return e.Message }

var knownChangeKinds = []string{
	service.TransitionServiceState,
	service.TransitionServicePorts,
	service.TransitionActionState,
	service.TransitionConfigReload,
}

// Changes implements API.Changes.
func (l *Local) Changes(query ChangeQuery) (ChangeResult, error) {
	project := l.Project()
	result := ChangeResult{Generation: project.Generation, Changes: []service.Transition{}}
	if query.Limit < 0 || query.Limit > maxChangeEvents {
		return result, &ChangeQueryError{Code: "invalid_change_query", Message: fmt.Sprintf("limit must be between 0 and %d", maxChangeEvents)}
	}
	for index, kind := range query.Kinds {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if !slices.Contains(knownChangeKinds, kind) {
			return result, &ChangeQueryError{Code: "invalid_change_kind", Message: fmt.Sprintf("unknown change kind %q", kind), Hint: "Kinds are " + strings.Join(knownChangeKinds, ", ") + "."}
		}
		query.Kinds[index] = kind
	}
	var addresses map[string]bool
	if len(query.Selectors) > 0 {
		targets, err := resolveLogTargets(l.Config(), query.Selectors, true)
		if err != nil {
			return result, err
		}
		addresses = make(map[string]bool, len(targets))
		for _, target := range targets {
			addresses[target.address] = true
		}
	}

	journal := l.manager.Journal()
	limit := query.Limit
	if limit == 0 {
		limit = maxChangeEvents
	}
	since := query.Since
	generationTruncated := false
	if query.SinceGeneration > 0 && query.Since == 0 {
		anchor, found := l.generationAnchor(journal, query.SinceGeneration)
		// A generation whose reload record has aged out cannot be anchored
		// exactly. Reporting everything the journal still holds, marked
		// truncated, beats silently answering from the wrong point.
		since, generationTruncated = anchor, !found
	}
	transitions, oldest, latest, truncated := journal.Since(since, 0)
	result.Oldest, result.Latest, result.Truncated = oldest, latest, truncated || generationTruncated
	result.Cursor = latest
	if latest < since {
		result.Cursor = since
	}
	filtered := make([]service.Transition, 0, len(transitions))
	for _, transition := range transitions {
		if len(query.Kinds) > 0 && !slices.Contains(query.Kinds, transition.Kind) {
			continue
		}
		if addresses != nil && !changeMatchesAddresses(transition, addresses) {
			continue
		}
		filtered = append(filtered, transition)
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
		result.Truncated = true
	}
	result.Changes = filtered
	return result, nil
}

// changeMatchesAddresses reports whether a transition concerns any selected
// stream. A service-owned action matches both its own address and its owner,
// because "what changed for api" includes the actions api ran.
func changeMatchesAddresses(transition service.Transition, addresses map[string]bool) bool {
	if transition.Action != "" && addresses[transition.Action] {
		return true
	}
	return transition.Service != "" && addresses[transition.Service]
}

// generationAnchor finds the sequence just before the reload that moved the
// runtime to generation. It reports whether that record was still retained.
func (l *Local) generationAnchor(journal *service.Journal, generation uint64) (uint64, bool) {
	transitions, _, _, _ := journal.Since(0, 0)
	for _, transition := range transitions {
		if transition.Kind == service.TransitionConfigReload && transition.Generation >= generation {
			return transition.Sequence - 1, true
		}
	}
	return 0, false
}

// recordReloadTransition journals a configuration generation change so a
// reader can tell an environment that changed under it from one that did not.
func (l *Local) recordReloadTransition(generation uint64, result ReloadResult) {
	summary := fmt.Sprintf("configuration reloaded to generation %d", generation)
	if changed := reloadSummary(result); changed != "" {
		summary += " · " + changed
	}
	l.manager.Journal().Record(service.Transition{
		Kind:       service.TransitionConfigReload,
		To:         "loaded",
		Generation: generation,
		Summary:    summary,
	})
}

func reloadSummary(result ReloadResult) string {
	parts := make([]string, 0, 3)
	for label, names := range map[string][]string{"added": result.Added, "removed": result.Removed, "updated": result.Updated} {
		if len(names) > 0 {
			parts = append(parts, label+" "+strings.Join(names, ","))
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, " · ")
}
