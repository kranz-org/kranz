package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kranz-org/kranz/internal/actionparams"
)

func TestEnvironmentFilesRefreshWithoutReload(t *testing.T) {
	for _, composed := range []bool{false, true} {
		name := "load"
		if composed {
			name = "compose"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, text string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(".env", "GLOBAL=old\nREMOVED=old\nHOST_VALUE=file\n")
			write("shared.env", "DEFAULT_FILE=old\nPRIORITY=default-file\n")
			write("service.env", "LOCAL=old\nPRIORITY=service-file\n")
			write("action.env", "ACTION=old\n")
			write("kranz.yaml", `project: demo
defaults:
  dir: `+dir+`
  env: {STATIC: accepted, PRIORITY: default-inline}
  env_files: [shared.env]
services:
  api:
    command: echo ${GLOBAL}
    env_files: [service.env]
    env: {PRIORITY: service-inline}
    actions:
      check:
        command: echo check
        env_files: [action.env]
        env: {ACTION_INLINE: accepted}
  isolated:
    command: echo isolated
    is_dotenv_disabled: true
`)
			t.Setenv("HOST_VALUE", "host")
			var cfg *Config
			var err error
			if composed {
				cfg, err = Compose(LoadOptions{Directory: dir, Sources: []string{"kranz.yaml"}})
			} else {
				cfg, err = Load(filepath.Join(dir, "kranz.yaml"))
			}
			if err != nil {
				t.Fatal(err)
			}
			before := cloneStringMap(cfg.Services["api"].Env)
			write(".env", "GLOBAL=new\nHOST_VALUE=changed\n")
			write("shared.env", "DEFAULT_FILE=new\nPRIORITY=changed\n")
			write("service.env", "LOCAL=new\nPRIORITY=changed\nADDED=new\n")
			write("action.env", "ACTION=new\n")
			// Inline edits and command interpolation still need an explicit reload.
			write("kranz.yaml", "services: {api: {command: different, env: {STATIC: unaccepted}}}\n")
			svc := cfg.Services["api"]
			env, err := svc.StartAction().ResolveEnvironment()
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"GLOBAL": "new", "DEFAULT_FILE": "new", "STATIC": "accepted", "LOCAL": "new", "ADDED": "new", "PRIORITY": "service-inline"}
			if !reflect.DeepEqual(env, want) {
				t.Fatalf("environment = %v, want %v", env, want)
			}
			if svc.Command != "echo old" || !reflect.DeepEqual(svc.Env, before) {
				t.Fatal("launch resolution modified the accepted snapshot")
			}
			env, err = svc.Actions["check"].ResolveEnvironment()
			if err != nil || env["ACTION"] != "new" || env["ACTION_INLINE"] != "accepted" || env["LOCAL"] != "new" {
				t.Fatalf("action environment = %v, err = %v", env, err)
			}
			env, err = cfg.Services["isolated"].StartAction().ResolveEnvironment()
			if err != nil {
				t.Fatal(err)
			}
			if _, found := env["GLOBAL"]; found {
				t.Fatal("disabled dotenv was loaded")
			}
			if err := os.Remove(filepath.Join(dir, "service.env")); err != nil {
				t.Fatal(err)
			}
			env, err = svc.StartAction().ResolveEnvironment()
			if err != nil {
				t.Fatal(err)
			}
			if _, found := env["LOCAL"]; found {
				t.Fatal("deleted file left stale variables")
			}
			write("service.env", "invalid dotenv line\n")
			if _, err := svc.StartAction().ResolveEnvironment(); err == nil {
				t.Fatal("invalid file accepted at launch")
			}
		})
	}
}

func TestComposedEnvironmentRefreshPreservesSourceAndOverrides(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("kranz.yaml", "project: demo\ninclude: [{path: child/kranz.yaml}]\nservices: {root: {command: echo root}}\n")
	write(".env", "ROOT_ONLY=old\n")
	write("child/.env", "CHILD_ONLY=old\n")
	write("child/service.env", "LOCAL=old\n")
	write("child/kranz.yaml", `project: demo
defaults:
  env: {DEFAULT: child}
services:
  api:
    command: echo child
    env_files: [service.env]
    env: {INLINE: child}
    actions:
      check: {command: echo check}
`)
	write("override.yaml", "services: {api: {env: {INLINE: override}, description: patched}}\n")
	cfg, err := Compose(LoadOptions{Directory: dir, Sources: []string{"kranz.yaml"}, Overrides: []string{"override.yaml"}})
	if err != nil {
		t.Fatal(err)
	}
	write(".env", "ROOT_ONLY=new\n")
	write("child/.env", "CHILD_ONLY=new\n")
	write("child/service.env", "LOCAL=new\n")
	for _, action := range []Action{*cfg.Services["api"].StartAction(), cfg.Services["api"].Actions["check"]} {
		env, err := action.ResolveEnvironment()
		want := map[string]string{"CHILD_ONLY": "new", "DEFAULT": "child", "LOCAL": "new", "INLINE": "override"}
		if err != nil || !reflect.DeepEqual(env, want) {
			t.Fatalf("environment = %v, want %v, err = %v", env, want, err)
		}
	}
	env, err := cfg.Services["root"].StartAction().ResolveEnvironment()
	if err != nil || !reflect.DeepEqual(env, map[string]string{"ROOT_ONLY": "new"}) {
		t.Fatalf("root environment = %v, err = %v", env, err)
	}
}

func TestEnvironmentRefreshKeepsParameterProjectionLiteral(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "action.env")
	if err := os.WriteFile(path, []byte("LIVE=new\nPROJECTED=file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := Action{Env: map[string]string{"LIVE": "old", "PROJECTED": "old", "TEMPLATE": "{{value}}"}, Params: map[string]ActionParam{"value": {Env: "PROJECTED"}}, environment: &environmentSpec{files: []string{path}}}
	invocation := a.RenderedAction(actionparams.Rendered{Env: map[string]string{"LIVE": "old", "PROJECTED": "$LITERAL", "TEMPLATE": "$LITERAL"}}, nil)
	env, err := invocation.ResolveEnvironment()
	if err != nil || env["LIVE"] != "new" || env["PROJECTED"] != "$LITERAL" || env["TEMPLATE"] != "$LITERAL" {
		t.Fatalf("invocation environment = %v, err = %v", env, err)
	}
}

func TestEnvironmentRecipesMatchAcceptedOverrides(t *testing.T) {
	for _, patch := range []string{
		"description: patched",
		"env: {INLINE: override}",
		"env: {}",
		"env: {INLINE: null}",
		"env_files: [replacement.env]",
		"is_dotenv_disabled: true",
		"lifecycle: {start: {env: {START_ONLY: inline}, env_files: [replacement.env]}}",
	} {
		t.Run(patch, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, text string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(".env", "GLOBAL=dotenv\n")
			write("service.env", "LOCAL=file\n")
			write("replacement.env", "REPLACED=file\n")
			write("kranz.yaml", "project: demo\ndefaults: {env: {DEFAULT: inline}}\nservices: {api: {command: echo test, env_files: [service.env], env: {INLINE: inline}}}\n")
			write("override.yaml", "services: {api: {"+patch+"}}\n")
			cfg, err := Compose(LoadOptions{Directory: dir, Sources: []string{"kranz.yaml"}, Overrides: []string{"override.yaml"}})
			if err != nil {
				t.Fatal(err)
			}
			start := cfg.Services["api"].StartAction()
			env, err := start.ResolveEnvironment()
			if err != nil || !reflect.DeepEqual(env, start.Env) {
				t.Fatalf("live = %v, accepted = %v, err = %v", env, start.Env, err)
			}
		})
	}
}
