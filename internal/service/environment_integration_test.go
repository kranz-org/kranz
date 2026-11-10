package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

func TestRestartReadsEnvironmentFilesWithoutConfigReload(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(".env", "LIVE=first\nREMOVED=old\nINLINE=file\n")
	write("kranz.yaml", `project: demo
defaults:
  dir: `+dir+`
services:
  api:
    command: 'printf "env:%s:%s:%s\n" "$$LIVE" "$${REMOVED-unset}" "$$INLINE"; exec sleep 60'
    env: {INLINE: accepted}
`)
	cfg, err := config.Load(filepath.Join(dir, "kranz.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(cfg)
	t.Cleanup(func() {
		if err := manager.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	svc, _ := manager.GetService("api")
	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(strings.Join(svc.LogLines(), "\n"), want) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("missing output %q", want)
	}
	wait("env:first:old:accepted")
	write(".env", "LIVE=second\nINLINE=changed\n")
	write("kranz.yaml", "services: {api: {command: echo unaccepted}}\n")
	if err := manager.RestartService("api"); err != nil {
		t.Fatal(err)
	}
	wait("env:second:unset:accepted")
	if svc.Config.Env["LIVE"] != "first" {
		t.Fatal("restart rewrote the accepted definition")
	}
}
