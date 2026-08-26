package service

import (
	"context"
	"strings"
	"unicode"
)

const maxRunClientLabelRunes = 80

// RunProvenance records which delivery surface initiated work and why. Client
// labels are deliberately short, stable product labels rather than socket,
// process, user, or request identifiers that could disclose local details.
type RunProvenance struct {
	Surface     string `json:"surface"`
	ClientLabel string `json:"client_label,omitempty"`
	StartReason string `json:"start_reason,omitempty"`
}

type runProvenanceContextKey struct{}

func WithRunProvenance(ctx context.Context, provenance RunProvenance) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runProvenanceContextKey{}, normalizeRunProvenance(provenance))
}

func RunProvenanceFromContext(ctx context.Context) RunProvenance {
	if ctx != nil {
		if provenance, ok := ctx.Value(runProvenanceContextKey{}).(RunProvenance); ok {
			return normalizeRunProvenance(provenance)
		}
	}
	return RunProvenance{Surface: "runtime"}
}

func WithStartReason(ctx context.Context, reason string) context.Context {
	provenance := RunProvenanceFromContext(ctx)
	provenance.StartReason = reason
	return WithRunProvenance(ctx, provenance)
}

func normalizeRunProvenance(provenance RunProvenance) RunProvenance {
	switch provenance.Surface {
	case "tui", "cli", "mcp", "runtime":
	default:
		provenance.Surface = "runtime"
	}
	label := []rune(strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, provenance.ClientLabel)))
	if len(label) > maxRunClientLabelRunes {
		label = label[:maxRunClientLabelRunes]
	}
	provenance.ClientLabel = string(label)
	return provenance
}
