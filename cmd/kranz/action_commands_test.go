package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

const actionProject = `project: Actions
services:
  api:
    command: sleep 60
    actions:
      seed:
        description: Load fixtures.
        command: echo seeded
      migrate:
        command: echo migrated
        interactive: true
action_groups:
  toolbox:
    actions:
      seed:
        command: echo group seeded
`

func actionDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(actionProject), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestActionListAndFilterByOwner(t *testing.T) {
	directory := actionDirectory(t)

	all := runInspection(t, directory, "action", "list")
	for _, id := range []string{"api/seed", "api/migrate", "toolbox/seed"} {
		if !strings.Contains(all, id) {
			t.Errorf("action list omits %q: %q", id, all)
		}
	}

	owned := runInspection(t, directory, "action", "list", "toolbox")
	if !strings.Contains(owned, "toolbox/seed") || strings.Contains(owned, "api/seed") {
		t.Errorf("action list toolbox = %q", owned)
	}
}

func TestActionListRejectsAnUnknownOwner(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", actionDirectory(t), "action", "list", "nope"}, &stdout, &stderr); code != kranzcli.ExitNotFound {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

// A service action and an action-group action can share a name, so the owner
// is part of the identity rather than a prefix to be guessed at.
func TestActionInfoDistinguishesOwnersOfTheSameName(t *testing.T) {
	directory := actionDirectory(t)

	service := runInspection(t, directory, "action", "info", "api/seed")
	if !strings.Contains(service, "(service)") || !strings.Contains(service, "echo seeded") {
		t.Errorf("api/seed = %q", service)
	}
	group := runInspection(t, directory, "action", "info", "toolbox/seed")
	if !strings.Contains(group, "(group)") || !strings.Contains(group, "echo group seeded") {
		t.Errorf("toolbox/seed = %q", group)
	}
}

// A bare action name cannot identify an action, and the error is the only place
// the user learns the OWNER/ACTION shape.
func TestActionInfoOnABareNameTeachesTheShape(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", actionDirectory(t), "action", "info", "seed"}, &stdout, &stderr); code != kranzcli.ExitNotFound {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "OWNER/ACTION") {
		t.Errorf("error does not teach the shape: %q", stderr.String())
	}
}

// An interactive action needs a terminal handed to it under a supervisor lease.
// Running it without one would block on a prompt nobody can answer, so it is
// refused before any runtime is contacted.
func TestActionRunRefusesAnInteractiveAction(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", actionDirectory(t), "action", "run", "api/migrate"}, &stdout, &stderr); code != kranzcli.ExitUsage {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "interactive") || !strings.Contains(stderr.String(), "kranz attach") {
		t.Errorf("refusal = %q", stderr.String())
	}
}

func TestActionRunNeedsARuntime(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := execute([]string{"-C", actionDirectory(t), "action", "run", "api/seed"}, &stdout, &stderr)
	if code != kranzcli.ExitNotFound {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}
