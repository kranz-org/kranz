package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfigFile(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestLoadPreservesNativeServiceDeclarationOrder(t *testing.T) {
	directory := t.TempDir()
	path := writeConfigFile(t, directory, "kranz.yaml", `project: Ordered
services:
  web:
    command: echo web
  api:
    command: echo api
  cache:
    command: echo cache
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if names := strings.Join(cfg.ServiceNames(), ","); names != "web,api,cache" {
		t.Errorf("service order = %s, want web,api,cache", names)
	}
}

func TestLoadPreservesProcessComposeDeclarationOrder(t *testing.T) {
	directory := t.TempDir()
	path := writeConfigFile(t, directory, "process-compose.yaml", `version: "0.5"
processes:
  web:
    command: echo web
  api:
    command: echo api
  cache:
    command: echo cache
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if names := strings.Join(cfg.ServiceNames(), ","); names != "web,api,cache" {
		t.Errorf("service order = %s, want web,api,cache", names)
	}
}

func TestLoadFilesKeepsBaseOrderAndAppendsNewServicesInOverrideOrder(t *testing.T) {
	directory := t.TempDir()
	basePath := writeConfigFile(t, directory, "kranz.yaml", `project: Ordered
services:
  web:
    command: echo web
  db:
    command: echo db
`)
	overridePath := writeConfigFile(t, directory, "kranz.local.yaml", `services:
  cache:
    command: echo cache
  db:
    command: echo db-override
  worker:
    command: echo worker
`)

	cfg, err := LoadFiles([]string{basePath, overridePath})
	if err != nil {
		t.Fatalf("LoadFiles failed: %v", err)
	}

	if names := strings.Join(cfg.ServiceNames(), ","); names != "web,db,cache,worker" {
		t.Errorf("merged order = %s, want web,db,cache,worker", names)
	}
}

func TestServiceNamesReconcilesStaleOrder(t *testing.T) {
	cfg := &Config{
		Services: map[string]Service{
			"web":   {Command: "echo web"},
			"api":   {Command: "echo api"},
			"cache": {Command: "echo cache"},
		},
		ServiceOrder: []string{"web", "removed", "web", "api"},
	}

	if names := strings.Join(cfg.ServiceNames(), ","); names != "web,api,cache" {
		t.Errorf("reconciled order = %s, want web,api,cache", names)
	}
}

func actionIDStrings(cfg *Config) []string {
	ids := cfg.ActionIDs()
	labels := make([]string, 0, len(ids))
	for _, id := range ids {
		labels = append(labels, string(id.OwnerKind)+":"+id.Owner+"/"+id.Name)
	}
	return labels
}

func TestLoadPreservesActionAndActionGroupDeclarationOrder(t *testing.T) {
	directory := t.TempDir()
	path := writeConfigFile(t, directory, "kranz.yaml", `project: Ordered
services:
  app:
    command: echo app
    actions:
      seed:
        command: echo seed
      migrate:
        command: echo migrate
action_groups:
  development:
    actions:
      build:
        command: echo build
      audit:
        command: echo audit
  analytics:
    actions:
      stats:
        command: echo stats
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	want := "service:app/seed,service:app/migrate,group:development/build,group:development/audit,group:analytics/stats"
	if got := strings.Join(actionIDStrings(cfg), ","); got != want {
		t.Errorf("action order = %s, want %s", got, want)
	}
}

func TestLoadFilesKeepsBaseActionOrderAndAppendsNewOnes(t *testing.T) {
	directory := t.TempDir()
	basePath := writeConfigFile(t, directory, "kranz.yaml", `project: Ordered
services:
  app:
    command: echo app
action_groups:
  development:
    actions:
      build:
        command: echo build
      audit:
        command: echo audit
`)
	overridePath := writeConfigFile(t, directory, "kranz.local.yaml", `action_groups:
  analytics:
    actions:
      stats:
        command: echo stats
  development:
    actions:
      zip:
        command: echo zip
      build:
        command: echo build-override
`)

	cfg, err := LoadFiles([]string{basePath, overridePath})
	if err != nil {
		t.Fatalf("LoadFiles failed: %v", err)
	}

	want := "group:development/build,group:development/audit,group:development/zip,group:analytics/stats"
	if got := strings.Join(actionIDStrings(cfg), ","); got != want {
		t.Errorf("merged action order = %s, want %s", got, want)
	}
}

func TestActionGroupNamesReconcilesStaleOrder(t *testing.T) {
	cfg := &Config{
		ActionGroups: map[string]ActionGroup{
			"development": {},
			"analytics":   {},
			"release":     {},
		},
		ActionGroupOrder: []string{"development", "removed", "development", "analytics"},
	}

	if names := strings.Join(cfg.ActionGroupNames(), ","); names != "development,analytics,release" {
		t.Errorf("reconciled group order = %s, want development,analytics,release", names)
	}
}

func TestLoadPreservesProcfileDeclarationOrder(t *testing.T) {
	directory := t.TempDir()
	path := writeConfigFile(t, directory, "Procfile", "web: echo web\napi: echo api\ncache: echo cache\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if names := strings.Join(cfg.ServiceNames(), ","); names != "web,api,cache" {
		t.Errorf("service order = %s, want web,api,cache", names)
	}
}

func TestLoadFilesKeepsBaseServiceActionOrderAndAppendsNewOnes(t *testing.T) {
	directory := t.TempDir()
	basePath := writeConfigFile(t, directory, "kranz.yaml", `project: Ordered
services:
  app:
    command: echo app
    actions:
      migrate:
        command: echo migrate
      seed:
        command: echo seed
`)
	overridePath := writeConfigFile(t, directory, "kranz.local.yaml", `services:
  app:
    actions:
      reset:
        command: echo reset
      migrate:
        command: echo migrate-override
`)

	cfg, err := LoadFiles([]string{basePath, overridePath})
	if err != nil {
		t.Fatalf("LoadFiles failed: %v", err)
	}

	if names := strings.Join(cfg.Services["app"].ActionNames(), ","); names != "migrate,seed,reset" {
		t.Errorf("merged service action order = %s, want migrate,seed,reset", names)
	}
}
