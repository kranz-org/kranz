package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// TestBackgroundWatcherReloadsWithoutAConnectedClient proves the README's
// "background runtime reload работает без TUI" requirement directly: a
// config file changes on disk while zero clients are connected, and the
// Supervisor's own watcher goroutine — not any client's polling — picks it
// up.
func TestBackgroundWatcherReloadsWithoutAConnectedClient(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kranz.yaml")
	initial := "project: Watcher\nservices:\n  api:\n    command: sleep 60\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	local := app.NewLocal(cfg, []string{path}, app.Options{})
	defer local.Shutdown()
	supervisor := NewSupervisor(local)
	_, socketPath, cleanupDir, err := NewSocketDir()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupDir()
	if err := supervisor.Listen(socketPath); err != nil {
		t.Fatal(err)
	}
	go func() { _ = supervisor.Serve() }()
	defer func() { _ = supervisor.Close() }()

	before := local.Project().Generation

	// mtime resolution on some filesystems is coarse enough that a rewrite
	// within the same tick would not register as "changed"; back-date the
	// original write's apparent mtime so this one is unambiguously later.
	past := time.Now().Add(-2 * time.Second)
	_ = os.Chtimes(path, past, past)

	updated := "project: Watcher\nservices:\n  api:\n    command: sleep 60\n  worker:\n    command: sleep 60\n"
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for local.Project().Generation == before {
		if time.Now().After(deadline) {
			t.Fatalf("background watcher did not reload; generation stayed at %d", before)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, ok := local.Service("worker"); !ok {
		t.Fatal("reloaded config did not add the worker service")
	}
}
