package ui

import (
	"reflect"
	"testing"

	"github.com/kranz-org/kranz/internal/config"
)

func TestDependencyOperationPreview(t *testing.T) {
	cfg := &config.Config{Project: "Test", Services: map[string]config.Service{
		"database": {Command: "true"},
		"api":      {Command: "true", DependsOn: []string{"database"}},
		"web":      {Command: "true", DependsOn: []string{"api"}},
		"worker":   {Command: "true", DependsOn: []string{"api"}},
	}}
	model := NewModel(cfg, "test")
	defer model.Shutdown()

	for _, name := range []string{"api", "web"} {
		model.app.SetServiceStatusForTest(name, config.StatusRunning)
	}
	model.refreshServices()

	tests := []struct {
		name         string
		operation    string
		selectedOnly bool
		requested    []string
		steps        []dependencyPreviewStep
	}{
		{
			name: "start includes dependencies and skips running services", operation: "start",
			requested: []string{"worker"},
			steps:     []dependencyPreviewStep{{Action: "Start", Waves: [][]string{{"database"}, {"worker"}}}},
		},
		{
			name: "exact start has only requested services", operation: "start", selectedOnly: true,
			requested: []string{"worker"},
			steps:     []dependencyPreviewStep{{Action: "Start", Waves: [][]string{{"worker"}}, Sequential: true}},
		},
		{
			name: "stop includes running dependents in reverse order", operation: "stop",
			requested: []string{"api"},
			steps:     []dependencyPreviewStep{{Action: "Stop", Waves: [][]string{{"web", "api"}}, Sequential: true}},
		},
		{
			name: "exact stop leaves dependents alone", operation: "stop", selectedOnly: true,
			requested: []string{"api"},
			steps:     []dependencyPreviewStep{{Action: "Stop", Waves: [][]string{{"api"}}, Sequential: true}},
		},
		{
			name: "restart includes active dependents and deduplicates targets", operation: "restart",
			requested: []string{"database", "api"},
			steps: []dependencyPreviewStep{
				{Action: "Stop", Waves: [][]string{{"web", "api"}}, Sequential: true},
				{Action: "Start", Waves: [][]string{{"database", "api", "web"}}, Sequential: true},
			},
		},
		{
			name: "exact restart orders stopped and running targets together", operation: "restart", selectedOnly: true,
			requested: []string{"database", "api"},
			steps: []dependencyPreviewStep{
				{Action: "Stop", Waves: [][]string{{"api"}}, Sequential: true},
				{Action: "Start", Waves: [][]string{{"database", "api"}}, Sequential: true},
			},
		},
		{
			name: "exact restart of a stopped service is a start", operation: "restart", selectedOnly: true,
			requested: []string{"worker"},
			steps:     []dependencyPreviewStep{{Action: "Start", Waves: [][]string{{"worker"}}, Sequential: true}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model.selected = nameSet(test.requested)
			preview, err := model.previewDependencyOperation(test.operation, test.selectedOnly)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(nameSet(preview.Requested), model.selected) {
				t.Errorf("requested services = %v, want %v", preview.Requested, test.requested)
			}
			if !reflect.DeepEqual(preview.Steps, test.steps) {
				t.Errorf("plan steps = %+v, want %+v", preview.Steps, test.steps)
			}
		})
	}
}

func TestDependencyPlanLinksOnlyIncludeDisplayedServices(t *testing.T) {
	model := &Model{cfg: &config.Config{Services: map[string]config.Service{
		"api": {DependsOn: []string{"cache", "database"}},
		"web": {DependsOn: []string{"api"}},
	}}}
	got := model.dependencyPlanLinks([]string{"web", "api", "database"})
	want := map[string][]string{"web": {"api"}, "api": {"database"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan links = %v, want %v", got, want)
	}
}
