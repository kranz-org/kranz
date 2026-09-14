package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The project snapshot is delivered as JSON, so it must never carry the
// composition request's absolute paths. The request is fetched separately
// through ProjectComposition, and its own fields are excluded from JSON too.
func TestProjectSnapshotAndCompositionRequestOmitAbsolutePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-root")
	request := CompositionRequest{
		Directory:      root,
		Sources:        []string{filepath.Join(root, "kranz.yaml")},
		Overrides:      []string{filepath.Join(root, "theme.yaml")},
		FollowSymlinks: true,
	}

	encodedRequest, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedRequest), root) {
		t.Fatalf("composition request JSON leaked its absolute path: %s", encodedRequest)
	}

	snapshot := ProjectSnapshot{Name: "demo", Composition: &request}
	encodedSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedSnapshot), root) {
		t.Fatalf("project snapshot JSON leaked the composition path: %s", encodedSnapshot)
	}
	if strings.Contains(string(encodedSnapshot), "composition") {
		t.Fatalf("project snapshot JSON embedded the composition request: %s", encodedSnapshot)
	}
}
