package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

const inspectionProject = `project: Inspection
services:
  db:
    description: Data store.
    command: sleep 60
    tags: [infra]
    ports: [65123]
  migrate:
    command: sleep 1
    depends_on: [db]
    tags: [setup]
  api:
    command: sleep 60
    depends_on: [migrate]
    tags: [backend]
    actions:
      seed:
        command: echo seeding
`

func inspectionDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(inspectionProject), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func runInspection(t *testing.T, directory string, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	full := append([]string{"-C", directory}, args...)
	if code := execute(full, &stdout, &stderr); code != 0 {
		t.Fatalf("%v exit=%d stderr=%s", args, code, stderr.String())
	}
	return stdout.String()
}

func TestListReportsServicesActionsAndTags(t *testing.T) {
	directory := inspectionDirectory(t)

	services := runInspection(t, directory, "list", "services")
	for _, name := range []string{"db", "migrate", "api"} {
		if !strings.Contains(services, name) {
			t.Errorf("list services omits %q: %q", name, services)
		}
	}
	// Declaration order is part of the configuration's meaning, so the list
	// must not fall back to sorting.
	if strings.Index(services, "db") > strings.Index(services, "api") {
		t.Errorf("list services lost declaration order: %q", services)
	}

	if actions := runInspection(t, directory, "list", "actions"); !strings.Contains(actions, "api/seed") {
		t.Errorf("list actions omits the service action: %q", actions)
	}
	if tags := runInspection(t, directory, "list", "tags"); !strings.Contains(tags, "infra") || !strings.Contains(tags, "backend") {
		t.Errorf("list tags is incomplete: %q", tags)
	}
}

func TestListRejectsAnUnknownKind(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", inspectionDirectory(t), "list", "widgets"}, &stdout, &stderr); code != kranzcli.ExitUsage {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "kranz list services") {
		t.Errorf("rejection does not name the valid kinds: %q", stderr.String())
	}
}

// The plan is the runtime's own dependency ordering, grouped into the waves it
// gates readiness on, so a selection has to pull in what it depends on.
func TestPlanGroupsWavesAndIncludesDependencies(t *testing.T) {
	directory := inspectionDirectory(t)

	full := runInspection(t, directory, "plan")
	if index := strings.Index(full, "db"); index > strings.Index(full, "api") {
		t.Errorf("plan does not start with the dependency: %q", full)
	}
	if !strings.Contains(full, "Wave 3") {
		t.Errorf("plan does not group waves: %q", full)
	}

	selected := runInspection(t, directory, "plan", "api")
	for _, name := range []string{"db", "migrate", "api"} {
		if !strings.Contains(selected, name) {
			t.Errorf("plan api omits its dependency %q: %q", name, selected)
		}
	}
}

func TestPlanRejectsAnUnknownSelector(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", inspectionDirectory(t), "plan", "nope"}, &stdout, &stderr); code != kranzcli.ExitNotFound {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestInfoDescribesProjectAndService(t *testing.T) {
	directory := inspectionDirectory(t)

	if project := runInspection(t, directory, "info"); !strings.Contains(project, "Inspection") {
		t.Errorf("info omits the project: %q", project)
	}

	service := runInspection(t, directory, "info", "migrate")
	if !strings.Contains(service, "db") {
		t.Errorf("info does not report the dependency: %q", service)
	}
	if !strings.Contains(service, "api") {
		t.Errorf("info does not report the dependent: %q", service)
	}
}

func TestGraphRendersTextDotAndJSON(t *testing.T) {
	directory := inspectionDirectory(t)

	if text := runInspection(t, directory, "graph"); !strings.Contains(text, "depends on db") {
		t.Errorf("text graph is wrong: %q", text)
	}
	if dot := runInspection(t, directory, "graph", "--format", "dot"); !strings.Contains(dot, `"db" -> "migrate"`) {
		t.Errorf("dot graph is wrong: %q", dot)
	}
	var nodes []map[string]any
	envelope := map[string]any{}
	if err := json.Unmarshal([]byte(runInspection(t, directory, "graph", "--output", "json")), &envelope); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(envelope["data"])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &nodes); err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Errorf("json graph has %d nodes", len(nodes))
	}
}

func TestConfigCheckAndDoctorPassOnAValidProject(t *testing.T) {
	directory := inspectionDirectory(t)

	if output := runInspection(t, directory, "config", "check"); !strings.Contains(output, "Configuration is valid") {
		t.Errorf("config check output = %q", output)
	}
	if output := runInspection(t, directory, "doctor"); !strings.Contains(output, "no cycles") {
		t.Errorf("doctor output = %q", output)
	}
}

// A preflight failure has to be a distinct exit code, otherwise a script cannot
// tell a broken project from a healthy one.
func TestDoctorFailsOnAMissingServiceDirectory(t *testing.T) {
	directory := t.TempDir()
	text := "project: Broken\nservices:\n  api:\n    command: sleep 60\n    dir: ./nowhere\n"
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"-C", directory, "doctor"}, &stdout, &stderr); code != kranzcli.ExitConfig {
		t.Fatalf("exit = %d, stdout = %q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "is not a directory") {
		t.Errorf("doctor did not report the directory: %q", stdout.String())
	}
}

func TestInspectionCommandsOutsideAProjectExplainThemselves(t *testing.T) {
	for _, command := range [][]string{{"list"}, {"plan"}, {"graph"}, {"info"}, {"doctor"}, {"config", "check"}} {
		var stdout, stderr bytes.Buffer
		args := append([]string{"-C", t.TempDir()}, command...)
		if code := execute(args, &stdout, &stderr); code != kranzcli.ExitUsage {
			t.Errorf("%v exit = %d, stderr = %q", command, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "no Kranz configuration was found") {
			t.Errorf("%v error = %q", command, stderr.String())
		}
	}
}
