package main

import (
	"errors"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

func TestParseRunIdentityRequiresAbsolutePositiveNumber(t *testing.T) {
	target, run, err := parseRunIdentity("analytics/stats#42")
	if err != nil || target != "analytics/stats" || run != 42 {
		t.Fatalf("parseRunIdentity = %q, %d, %v", target, run, err)
	}
	for _, invalid := range []string{"api", "#1", "api#0", "api#latest"} {
		if _, _, err := parseRunIdentity(invalid); err == nil {
			t.Errorf("parseRunIdentity(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestRunsDeleteRequiresExplicitConfirmationBeforeRuntimeAccess(t *testing.T) {
	err := runRunsDelete(kranzcli.GlobalOptions{}, []string{"api#1"}, nil)
	var commandErr *kranzcli.Error
	if !errors.As(err, &commandErr) || commandErr.Code != "confirmation_required" {
		t.Fatalf("runRunsDelete error = %#v", err)
	}
}
