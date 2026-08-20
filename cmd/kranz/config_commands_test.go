package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

const secretsProject = `project: Secrets
defaults:
  env:
    LOG_LEVEL: debug
    DATABASE_PASSWORD: hunter2
services:
  zulu:
    command: sleep 60
    env:
      API_TOKEN: abc123
      PUBLIC_URL: http://localhost
  alpha:
    command: sleep 60
`

func secretsDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(secretsProject), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

// Printing the effective configuration must not print the credentials in it,
// and redaction is by key because a password is indistinguishable from any
// other string once it is out of context.
func TestConfigShowRedactsSecretEnvironmentValues(t *testing.T) {
	output := runInspection(t, secretsDirectory(t), "config", "show")

	for _, secret := range []string{"hunter2", "abc123"} {
		if strings.Contains(output, secret) {
			t.Errorf("config show leaked %q:\n%s", secret, output)
		}
	}
	for _, kept := range []string{"debug", "http://localhost"} {
		if !strings.Contains(output, kept) {
			t.Errorf("config show redacted the non-secret %q", kept)
		}
	}
	if !strings.Contains(output, "[redacted]") {
		t.Errorf("config show does not mark redaction:\n%s", output)
	}
}

// Declaration order is meaning in a Kranz configuration, and encoding the
// struct alone would sort the service map alphabetically.
func TestConfigShowPreservesDeclarationOrder(t *testing.T) {
	output := runInspection(t, secretsDirectory(t), "config", "show")
	if strings.Index(output, "zulu:") > strings.Index(output, "alpha:") {
		t.Errorf("config show sorted the services instead of keeping declaration order:\n%s", output)
	}
}

func TestConfigShowRejectsUnknownOptions(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", secretsDirectory(t), "config", "show", "--all"}, &stdout, &stderr); code != kranzcli.ExitUsage {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

// Provenance is a question about the files, so a field set by a later layer has
// to be attributed to that layer rather than to the base.
func TestConfigExplainAttributesFieldsToTheLayerThatSetThem(t *testing.T) {
	directory := secretsDirectory(t)
	override := filepath.Join(directory, "override.yaml")
	if err := os.WriteFile(override, []byte("project: Overridden\nservices:\n  alpha:\n    command: sleep 90\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := execute([]string{"-C", directory, "-f", filepath.Join(directory, "kranz.yaml"), "-f", override, "config", "explain"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	output := stdout.String()
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "project", "services.alpha.command":
			if fields[1] != "override.yaml" {
				t.Errorf("%s attributed to %s, want override.yaml", fields[0], fields[1])
			}
		case "defaults.env.LOG_LEVEL":
			if fields[1] != "kranz.yaml" {
				t.Errorf("%s attributed to %s, want kranz.yaml", fields[0], fields[1])
			}
		}
	}
}

func TestConfigExplainScopesToOneService(t *testing.T) {
	output := runInspection(t, secretsDirectory(t), "config", "explain", "--all", "zulu")
	if !strings.Contains(output, "services.zulu.command") {
		t.Errorf("explain zulu omits its own fields:\n%s", output)
	}
	if strings.Contains(output, "services.alpha") {
		t.Errorf("explain zulu leaked another service:\n%s", output)
	}
}

func TestConfigExplainRejectsAnUnknownService(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", secretsDirectory(t), "config", "explain", "nope"}, &stdout, &stderr); code != kranzcli.ExitNotFound {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestSecretKeyDetection(t *testing.T) {
	for name, want := range map[string]bool{
		"DATABASE_PASSWORD": true,
		"api_token":         true,
		"STRIPE_SECRET_KEY": true,
		"AWS_ACCESS_KEY_ID": true,
		"LOG_LEVEL":         false,
		"PUBLIC_URL":        false,
		"PORT":              false,
	} {
		if got := isSecretKey(name); got != want {
			t.Errorf("isSecretKey(%q) = %t, want %t", name, got, want)
		}
	}
}

// Provenance is a question about layers. With one layer the answer is the same
// for every field, and printing it once per field buries it in a wall of
// identical rows.
func TestConfigExplainOnASingleLayerSaysSoInsteadOfListing(t *testing.T) {
	output := runInspection(t, secretsDirectory(t), "config", "explain")

	if !strings.Contains(output, "one configuration layer") {
		t.Errorf("single-layer explain does not say so:\n%s", output)
	}
	if strings.Contains(output, "services.zulu.command") {
		t.Errorf("single-layer explain listed every field anyway:\n%s", output)
	}
	if !strings.Contains(output, "--all") {
		t.Errorf("single-layer explain does not offer the full listing:\n%s", output)
	}
}

// Reporting that `services` and `services.api` also come from a file adds a row
// for every mapping on the way down and answers nothing the leaves do not.
func TestConfigExplainListsOnlyLeafFields(t *testing.T) {
	output := runInspection(t, secretsDirectory(t), "config", "explain", "--all")

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] == "FIELD" {
			continue
		}
		if fields[0] == "services" || fields[0] == "defaults" || fields[0] == "services.zulu" {
			t.Errorf("intermediate mapping %q was listed as a field", fields[0])
		}
	}
	if !strings.Contains(output, "services.zulu.command") {
		t.Errorf("leaf fields are missing:\n%s", output)
	}
}
