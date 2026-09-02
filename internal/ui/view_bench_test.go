package ui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// benchModel mirrors a working dashboard: a wide terminal, both panels
// populated, and enough log history that the visible window is full.
func benchModel(b *testing.B, width, height, logLines int) *Model {
	b.Helper()
	model := newTestModel()
	_, _ = model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	for i := range logLines {
		appendTestLog(model, "api", fmt.Sprintf("2026-09-02 11:00:00 api request %d completed in %dms", i, i%97))
	}
	return model
}

func benchActionModel(b *testing.B, width, height, logLines int) *Model {
	b.Helper()
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "tools", Name: "report"}
	model := NewModel(&config.Config{Project: "Benchmark", ActionGroups: map[string]config.ActionGroup{
		"tools": {Actions: map[string]config.Action{"report": {Command: "report --all"}}},
	}}, "test")
	b.Cleanup(func() { _ = model.Shutdown() })
	_, _ = model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	model.focusedAction = &id
	target := app.ActionRunTarget(id)
	lines := make([]cachedActionLogLine, logLines)
	for index := range lines {
		lines[index] = cachedActionLogLine{sequence: uint64(index + 1), run: 1, text: fmt.Sprintf("report line %d", index)}
	}
	model.actionLogLines[target] = lines
	model.actionRunLogLines[target] = map[uint32][]cachedActionLogLine{1: lines}
	model.actionStates[id] = app.ActionResult{ID: id, Run: 1, Status: app.ActionSucceeded}
	model.runs = []app.RunSummary{{Target: target, Run: 1, Status: app.ActionSucceeded.String()}}
	return model
}

// BenchmarkViewHistory guards the contract that matters for CPU at idle: a
// frame costs what the terminal shows, not what retention holds.
func BenchmarkViewHistory(b *testing.B) {
	for _, lines := range []int{500, 10000} {
		b.Run(fmt.Sprintf("%dlines", lines), func(b *testing.B) {
			model := benchModel(b, 200, 60, lines)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = model.View()
			}
		})
	}
}

func BenchmarkActionViewHistory(b *testing.B) {
	for _, lines := range []int{500, 10000} {
		b.Run(fmt.Sprintf("%dlines", lines), func(b *testing.B) {
			model := benchActionModel(b, 200, 60, lines)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = model.View()
			}
		})
	}
}

func BenchmarkView(b *testing.B) {
	for _, size := range []struct {
		name          string
		width, height int
	}{
		{"80x24", 80, 24},
		{"200x60", 200, 60},
	} {
		b.Run(size.name, func(b *testing.B) {
			model := benchModel(b, size.width, size.height, 500)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = model.View()
			}
		})
	}
}
