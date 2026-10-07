package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
)

func TestInfoModalsWrapLongContentAndAlignInsets(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.height, model.ready = 40, true
	message := "Selection and dependent services stopped; ports released " + strings.Repeat("long-message-", 12)
	model.addNotification("api", message, config.LogWarn)
	svc := model.FocusedService()
	svc.Config.HealthCheck = &config.HealthCheckConfig{Readiness: &config.CheckConfig{
		Type: config.CheckHTTP, URL: "http://127.0.0.1:8080/" + strings.Repeat("health/", 24),
	}}

	for _, width := range []int{40, 64, 100, 180} {
		model.width = width
		for _, testCase := range []struct {
			title string
			body  []string
			first string
		}{
			{"Notifications", model.notificationBodyLines(), model.notifications[0].Time.Format("15:04:05")},
			{"Health: api", model.healthHistoryBodyLines(), "Readiness:"},
		} {
			content := model.infoModalContent(testCase.title, testCase.body)
			if got := lipgloss.Width(content); got > min(84, width-4) {
				t.Fatalf("%s at %d: modal width %d exceeds its limit", testCase.title, width, got)
			}
			columns := []int{}
			for _, label := range []string{testCase.title, testCase.first, "[Esc] Close"} {
				found := false
				for _, line := range strings.Split(ansi.Strip(content), "\n") {
					if index := strings.Index(line, label); index >= 0 {
						columns = append(columns, lipgloss.Width(line[:index]))
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("%s at %d: missing %q", testCase.title, width, label)
				}
			}
			if columns[0] != columns[1] || columns[1] != columns[2] {
				t.Fatalf("%s at %d: unequal title/body/footer insets %v", testCase.title, width, columns)
			}
		}
	}
	got := strings.Join(strings.Fields(ansi.Strip(strings.Join(model.notificationBodyLines(), "\n"))), "")
	if !strings.Contains(got, strings.Join(strings.Fields(message), "")) {
		t.Fatal("notification text was lost during wrapping")
	}
}

func TestInfoModalScrollKeepsFooterVisibleAndReopensAtTop(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 40, 16, true
	for index := range 40 {
		model.addNotification("api", fmt.Sprintf("Event %02d", index), config.LogInfo)
	}
	pressKey(model, 'n')
	for range 100 {
		pressKey(model, 'j')
	}
	if model.infoModalOffset != model.maxInfoModalOffset() || model.infoModalOffset == 0 {
		t.Fatal("notification scroll did not reach the last page")
	}
	content := model.infoModalContent("Notifications", model.notificationBodyLines())
	if lipgloss.Height(content) > model.height {
		t.Fatal("modal exceeds terminal height")
	}
	for _, expected := range []string{"Event 00", "[Esc] Close", "Scroll"} {
		if !strings.Contains(ansi.Strip(content), expected) {
			t.Fatalf("last page is missing %q", expected)
		}
	}
	pressEsc(model)
	pressKey(model, 'n')
	if model.infoModalOffset != 0 {
		t.Fatal("reopened notification center kept the old scroll position")
	}
	pressEsc(model)
	pressKey(model, 'h')
	if model.mode != ModeHealthHistory || model.infoModalOffset != 0 {
		t.Fatal("health history did not open at the top")
	}
	pressKey(model, 'q')
	if model.mode != ModeNormal {
		t.Fatal("q did not close health history")
	}
}
