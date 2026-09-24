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

func TestConfigChangedFindsHigherPriorityRootConfig(t *testing.T) {
	directory := t.TempDir()
	writeConfig(t, filepath.Join(directory, "kranz.yml"), "project: First\nservices: {api: {command: true}}\n")
	options := config.LoadOptions{Directory: directory}
	cfg, err := config.Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	local := NewLocal(cfg, nil, Options{LoadOptions: &options})
	defer local.Shutdown()
	writeConfig(t, filepath.Join(directory, "kranz.yaml"), "project: Second\nservices: {api: {command: true}}\n")
	if changed, err := local.ConfigChanged(); err != nil || !changed {
		t.Fatalf("new higher-priority root was missed: %v, %v", changed, err)
	}
	if _, err := local.Reload(false); err != nil {
		t.Fatal(err)
	}
	if local.Config().Project != "Second" {
		t.Fatalf("reloaded root = %q", local.Config().Project)
	}
}

func TestConfigChangedFindsConfigBelowFollowedSymlink(t *testing.T) {
	directory := t.TempDir()
	root := filepath.Join(directory, "root")
	target := filepath.Join(directory, "target")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(target, "kranz.yaml"), "project: Initial\nservices: {api: {command: true}}\n")
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	options := config.LoadOptions{Directory: root, FollowSymlinks: true}
	cfg, err := config.Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	local := NewLocal(cfg, nil, Options{LoadOptions: &options})
	defer local.Shutdown()
	nested := filepath.Join(target, "nested", "kranz.yaml")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, nested, "project: Added\nservices: {worker: {command: true}}\n")
	if changed, err := local.ConfigChanged(); err != nil || !changed {
		t.Fatalf("new linked config was missed: %v, %v", changed, err)
	}
}

func TestIgnoredBrokenSymlinkDoesNotBlockConfigReload(t *testing.T) {
	directory := t.TempDir()
	root := filepath.Join(directory, "kranz.yaml")
	writeConfig(t, root, "project: First\nservices: {api: {command: true}}\n")
	options := config.LoadOptions{Directory: directory}
	cfg, err := config.Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	local := NewLocal(cfg, nil, Options{LoadOptions: &options})
	defer local.Shutdown()
	if err := os.Symlink(filepath.Join(directory, "missing"), filepath.Join(directory, "unrelated")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeConfig(t, root, "project: Second\nservices: {api: {command: true}}\n")
	touchLater(t, root)
	if changed, err := local.ConfigChanged(); err != nil || !changed {
		t.Fatalf("valid edit was blocked by ignored link: %v, %v", changed, err)
	}
	if _, err := local.Reload(false); err != nil {
		t.Fatal(err)
	}
	if local.Config().Project != "Second" {
		t.Fatal("valid edit was not applied")
	}
}

func TestDiscoveryStampSkipsDirectoriesBeyondMaxDepth(t *testing.T) {
	directory := t.TempDir()
	writeConfig(t, filepath.Join(directory, "kranz.yaml"), "project: Root\nservices: {api: {command: true}}\n")
	nested := filepath.Join(directory, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(directory, "missing"), filepath.Join(nested, "broken")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	policies := map[string][]discoveryWatchPolicy{directory: {{followSymlinks: true, maxDepth: 0}}}
	before, err := readConfigStampsWithPolicies([]string{directory}, policies)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(nested, "kranz.yaml"), "project: Nested\nservices: {worker: {command: true}}\n")
	after, err := readConfigStampsWithPolicies([]string{directory}, policies)
	if err != nil || !equalConfigStamps(before, after) {
		t.Fatalf("excluded depth changed stamp or blocked scan: changed=%v err=%v", !equalConfigStamps(before, after), err)
	}
}

func TestDuplicateDiscoveryScopesKeepFollowedLinks(t *testing.T) {
	directory := t.TempDir()
	target := t.TempDir()
	writeConfig(t, filepath.Join(target, "kranz.yaml"), "project: Linked\nservices: {api: {command: true}}\n")
	if err := os.Symlink(target, filepath.Join(directory, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	depth := 0
	cfg := &config.Config{DiscoveryScopes: []config.DiscoveryScope{
		{Path: directory, FollowSymlinks: true},
		{Path: directory, FollowSymlinks: false, MaxDepth: &depth},
	}}
	policies := discoveryWatchPolicies(cfg)
	before, err := readConfigStampsWithPolicies([]string{directory}, policies)
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(target, "nested", "kranz.yaml")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, nested, "project: Added\nservices: {worker: {command: true}}\n")
	after, err := readConfigStampsWithPolicies([]string{directory}, policies)
	if err != nil || equalConfigStamps(before, after) {
		t.Fatalf("followed scope missed linked config: changed=%v err=%v", !equalConfigStamps(before, after), err)
	}
}

func TestExplicitGlobDetectsNewMatchingSource(t *testing.T) {
	directory := t.TempDir()
	sources := filepath.Join(directory, "sources")
	if err := os.MkdirAll(sources, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(sources, "one.yaml"), "project: One\nservices: {api: {command: true}}\n")
	options := config.LoadOptions{Directory: directory, Sources: []string{"sources/*.yaml"}}
	cfg, err := config.Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	local := NewLocal(cfg, nil, Options{LoadOptions: &options})
	defer local.Shutdown()
	writeConfig(t, filepath.Join(sources, "two.yaml"), "project: Two\nservices: {worker: {command: true}}\n")
	if changed, err := local.ConfigChanged(); err != nil || !changed {
		t.Fatalf("new glob source was missed: %v, %v", changed, err)
	}
	if _, err := local.Reload(false); err != nil {
		t.Fatal(err)
	}
	if _, exists := local.Config().Services["worker"]; !exists {
		t.Fatal("new glob source was not applied")
	}
}

func TestExplicitSymlinkRetargetIsDetected(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.yaml")
	second := filepath.Join(directory, "second.yaml")
	link := filepath.Join(directory, "selected.yaml")
	writeConfig(t, first, "project: First\nservices: {api: {command: true}}\n")
	writeConfig(t, second, "project: Other\nservices: {api: {command: true}}\n")
	if err := os.Symlink(first, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	options := config.LoadOptions{Directory: directory, Sources: []string{"selected.yaml"}}
	cfg, err := config.Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	local := NewLocal(cfg, nil, Options{LoadOptions: &options})
	defer local.Shutdown()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	if changed, err := local.ConfigChanged(); err != nil || !changed {
		t.Fatalf("retargeted source was missed: %v, %v", changed, err)
	}
	if _, err := local.Reload(false); err != nil {
		t.Fatal(err)
	}
	if local.Config().Project != "Other" {
		t.Fatal("retargeted source was not applied")
	}
}

func TestConfigChangedDetectsSameSizeEditWithPreservedTimestamp(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "kranz.yaml")
	first := "project: First\nservices: {api: {command: true}}\n"
	second := "project: Other\nservices: {api: {command: true}}\n"
	writeConfig(t, path, first)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	options := config.LoadOptions{Directory: directory}
	cfg, err := config.Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	local := NewLocal(cfg, nil, Options{LoadOptions: &options})
	defer local.Shutdown()
	writeConfig(t, path, second)
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if changed, err := local.ConfigChanged(); err != nil || !changed {
		t.Fatalf("same-size edit was missed: %v, %v", changed, err)
	}
	if _, err := local.Reload(false); err != nil || local.Config().Project != "Other" {
		t.Fatalf("same-size edit did not apply: %v", err)
	}
}

func TestShadowedDiscoveryFileDoesNotTriggerReload(t *testing.T) {
	directory := t.TempDir()
	writeConfig(t, filepath.Join(directory, "kranz.yaml"), "project: Primary\nservices: {api: {command: true}}\n")
	shadowed := filepath.Join(directory, "kranz.yml")
	writeConfig(t, shadowed, "project: Shadow\nservices: {worker: {command: true}}\n")
	options := config.LoadOptions{Directory: directory}
	cfg, err := config.Compose(options)
	if err != nil {
		t.Fatal(err)
	}
	local := NewLocal(cfg, nil, Options{LoadOptions: &options})
	defer local.Shutdown()
	writeConfig(t, shadowed, "project: Edited\nservices: {worker: {command: true}}\n")
	if changed, err := local.ConfigChanged(); err != nil || changed {
		t.Fatalf("shadowed edit changed effective stamp: %v, %v", changed, err)
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
