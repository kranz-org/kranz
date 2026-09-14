package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kranz-org/kranz/internal/config"
)

// A discovery scope may hold thousands of unrelated files. Its stamp must
// depend only on the configs discovery can actually load, so editing a sibling
// does not trigger a reload while adding, editing, or removing a config still
// does.
func TestReadConfigStampsTracksOnlyDiscoveryConfigs(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "kranz.yaml")
	writeConfig(t, configPath, "project: Root\nservices: {api: {command: \"true\"}}\n")
	noisePath := filepath.Join(directory, "notes.txt")
	writeConfig(t, noisePath, "one")

	before, err := readConfigStamps([]string{directory})
	if err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	if err := os.WriteFile(noisePath, []byte("a much longer unrelated value"), 0o644); err != nil {
		t.Fatal(err)
	}
	touchLater(t, noisePath)
	afterNoise, err := readConfigStamps([]string{directory})
	if err != nil {
		t.Fatalf("scan after unrelated edit: %v", err)
	}
	if !equalConfigStamps(before, afterNoise) {
		t.Fatal("editing an unrelated file changed the discovery stamp")
	}

	nested := filepath.Join(directory, "second", "kranz.yaml")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, nested, "project: Second\nservices: {worker: {command: \"true\"}}\n")
	afterAdd, err := readConfigStamps([]string{directory})
	if err != nil {
		t.Fatalf("scan after added config: %v", err)
	}
	if equalConfigStamps(before, afterAdd) {
		t.Fatal("adding a config did not change the discovery stamp")
	}

	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	afterRemove, err := readConfigStamps([]string{directory})
	if err != nil {
		t.Fatalf("scan after removed config: %v", err)
	}
	if equalConfigStamps(afterAdd, afterRemove) {
		t.Fatal("removing a config did not change the discovery stamp")
	}
}

// A scan failure must keep its cause so errors.Is can see a permission error,
// while the message shown for it must not publish the absolute watch path.
func TestReadConfigStampsPreservesPermissionCauseAndRedactsPath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	scope := filepath.Join(t.TempDir(), "scope")
	if err := os.Mkdir(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(scope, "kranz.yaml"), "project: Root\nservices: {api: {command: \"true\"}}\n")
	if err := os.Chmod(scope, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(scope, 0o755) })

	_, err := readConfigStamps([]string{scope})
	if err == nil {
		t.Fatal("scan of an unreadable scope succeeded")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("scan error lost its permission cause: %v", err)
	}
	if strings.Contains(err.Error(), scope) {
		t.Fatalf("scan error leaked the absolute watch path: %v", err)
	}
}

// A client reloads the effective graph from the runtime's composition request.
// A virtual-root project has no source files to name: it must remember the
// discovery root so the client recomposes rather than skipping the reload.
func TestProjectExposesDiscoveryCompositionForVirtualRoot(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		nested := filepath.Join(directory, name)
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		writeConfig(t, filepath.Join(nested, "kranz.yaml"), "project: "+name+"\nservices: {api: {command: \"true\"}}\n")
	}
	options := config.LoadOptions{Directory: directory}
	cfg, err := config.Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	local := NewLocal(cfg, nil, Options{LoadOptions: &options})
	defer func() { _ = local.Shutdown() }()

	request := local.Project().Composition
	if !request.Configured() {
		t.Fatal("virtual-root project exposes no composition request, so a client cannot replay discovery")
	}
	if request.Directory != directory {
		t.Fatalf("composition directory = %q, want %q", request.Directory, directory)
	}
	if len(request.Sources) != 0 {
		t.Fatalf("virtual root must stay discovery-based, got sources %v", request.Sources)
	}
}
