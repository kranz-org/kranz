package service

import (
	"context"
	"strings"
	"testing"
)

func TestRunProvenanceSanitizesUntrustedClientIdentity(t *testing.T) {
	provenance := RunProvenanceFromContext(WithRunProvenance(context.Background(), RunProvenance{
		Surface: "socket", ClientLabel: "  agent\n" + strings.Repeat("x", 100),
	}))
	if provenance.Surface != "runtime" {
		t.Fatalf("surface = %q", provenance.Surface)
	}
	if strings.ContainsAny(provenance.ClientLabel, "\r\n") || len([]rune(provenance.ClientLabel)) != maxRunClientLabelRunes {
		t.Fatalf("unsafe label = %q", provenance.ClientLabel)
	}
}
