package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

// Tests for the Details panel and port inspection.

func TestExternalPortConflictOffersVerifiedStopAction(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 32, true
	model.operationID = 7
	conflict := &service.PortConflictError{
		Service: "api", Port: 8080, PID: 4242, Process: "outside", Command: "outside --serve", External: true,
	}
	_, _ = model.Update(operationResultMsg{id: 7, target: "selection", err: conflict})
	if model.mode != ModePortConflict || !model.conflictExternal || model.conflictService != "api" {
		t.Fatalf("conflict state = mode %v external %v service %q", model.mode, model.conflictExternal, model.conflictService)
	}
	plain := ansi.Strip(model.renderPortConflictView())
	for _, expected := range []string{"external process", "PID: 4242", "outside --serve", "[k] Stop this external process and retry"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("port conflict modal does not contain %q:\n%s", expected, plain)
		}
	}
	model.portChecker = fakePortChecker{details: map[int]*config.PortInfo{8080: {Port: 8080, PID: 4242}}}
	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if command == nil {
		t.Fatal("k did not schedule a verified external-process stop")
	}
}

func TestPortReleaseRefusesChangedOwner(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.portChecker = fakePortChecker{details: map[int]*config.PortInfo{8080: {Port: 8080, PID: 5252}}}
	message := model.releaseExternalPort(8080, 4242)().(releasePortResultMsg)
	if message.err == nil || !strings.Contains(message.err.Error(), "owner changed") {
		t.Fatalf("changed-owner result = %v", message.err)
	}
}

func TestReleasePortResultReportsErrorsWithoutRetry(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.mode = ModePortConflict
	_, command := model.Update(releasePortResultMsg{port: 8080, pid: 42, err: errors.New("denied")})
	if command != nil || model.mode != ModePortConflict {
		t.Fatalf("failed release changed mode/command: %v/%v", model.mode, command)
	}
}

func TestServiceDetailsUseAsyncPortInspection(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.FocusedService().Config.HealthCheck = &config.HealthCheckConfig{
		Readiness: &config.CheckConfig{Type: config.CheckHTTP, URL: "http://127.0.0.1:8080/ready"},
		Liveness:  &config.CheckConfig{Type: config.CheckTCP, Port: 8080},
	}
	model.portChecker = fakePortChecker{details: map[int]*config.PortInfo{
		8080: {Port: 8080, Address: "127.0.0.1", Protocol: "tcp", PID: 4321, Process: "test-api"},
	}}

	command := model.scanFocusedPorts(true)
	if command == nil {
		t.Fatal("port scan was not scheduled")
	}
	message := command().(portDetailsMsg)
	_, _ = model.Update(message)
	plain := ansi.Strip(model.renderServiceDetails(model.FocusedService(), 72, 24))
	for _, expected := range []string{"tcp://127.0.0.1:8080", "listening", "test-api", "PID 4321", "backend", "http://127.0.0.1:8080/ready", "tcp://localhost:8080", "COMMAND exit 0"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("service details do not contain %q:\n%s", expected, plain)
		}
	}
}

func TestListeningPortUsesConciseOwnershipParameter(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		info           *config.PortInfo
		managedService string
		want           string
	}{
		{name: "managed", info: &config.PortInfo{Process: "node", PID: 4321}, managedService: "api", want: "owner: kranz"},
		{name: "external", info: &config.PortInfo{Process: "postgres", PID: 5432}, want: "owner: external"},
		{name: "unknown", info: &config.PortInfo{Process: "listener"}, want: "owner: unknown"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			rendered := strings.Join(renderListeningPort("", testCase.info, testCase.managedService, 80), "\n")
			plain := ansi.Strip(rendered)
			if !strings.Contains(plain, testCase.want) {
				t.Fatalf("port ownership = %q, want %q", plain, testCase.want)
			}
			if strings.Contains(plain, "Kranz ·") || strings.Contains(plain, "· api") {
				t.Fatalf("port ownership repeats product/service context: %q", plain)
			}
			if testCase.managedService == "" && !strings.Contains(rendered, StartingBadgeStyle.Render(testCase.want)) {
				t.Fatalf("non-Kranz ownership is not highlighted: %q", rendered)
			}
		})
	}
}

func TestListeningPortWrapsOwnershipToAvailableWidth(t *testing.T) {
	info := &config.PortInfo{Process: "node", PID: 4321}
	narrow := renderListeningPort("PORTS 3000 ", info, "", 30)
	plain := ansi.Strip(strings.Join(narrow, "\n"))
	for _, expected := range []string{"\n  ↳ node", "\n  ↳ PID 4321", "\n  ↳ owner: external"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("narrow port ownership does not contain %q:\n%s", expected, plain)
		}
	}
	for _, line := range narrow {
		if width := lipgloss.Width(line); width > 30 {
			t.Fatalf("narrow port line width = %d, want at most 30: %q", width, ansi.Strip(line))
		}
	}

	wide := ansi.Strip(strings.Join(renderListeningPort("PORTS 3000 ", info, "", 80), "\n"))
	if !strings.Contains(wide, "\n  ↳ node · PID 4321 · owner: external") {
		t.Fatalf("wide port ownership was needlessly split:\n%s", wide)
	}
}

func TestLongDetailFieldWrapsBelowItsLabel(t *testing.T) {
	lines := detailFieldLines("ABOUT", "A long service description that cannot fit beside its label", 28)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.HasPrefix(plain, "ABOUT\n  ↳ ") {
		t.Fatalf("long ABOUT field is not structured below its label:\n%s", plain)
	}
	for _, line := range lines {
		if width := lipgloss.Width(line); width > 28 {
			t.Fatalf("ABOUT line width = %d, want at most 28: %q", width, ansi.Strip(line))
		}
	}

	short := ansi.Strip(strings.Join(detailFieldLines("ABOUT", "Worker", 28), "\n"))
	if short != "ABOUT Worker" {
		t.Fatalf("short ABOUT field = %q, want inline value", short)
	}
}

func TestDirectoryMovesBelowPIDAndWrapsWithoutArrow(t *testing.T) {
	lines := pidDirectoryDetailLines(0, "apps/event-processor/a-very-long-subdirectory", 28)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.HasPrefix(plain, "PID 0\nDIR apps/") || strings.Contains(plain, "↳") {
		t.Fatalf("narrow PID/DIR layout is not a plain labeled block:\n%s", plain)
	}
	for _, line := range lines {
		if width := lipgloss.Width(line); width > 28 {
			t.Fatalf("PID/DIR line width = %d, want at most 28: %q", width, ansi.Strip(line))
		}
	}

	wide := ansi.Strip(strings.Join(pidDirectoryDetailLines(0, "apps/event-processor", 80), "\n"))
	if wide != "PID 0   DIR apps/event-processor" {
		t.Fatalf("wide PID/DIR layout = %q, want one line", wide)
	}
}

func TestServiceDetailBlocksRespectAvailableWidth(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	svc := model.FocusedService()
	svc.Config.Tags = []string{"backend", "payments", "internal", "critical", "worker"}
	svc.Config.DependsOn = []string{"database-primary", "message-broker"}
	svc.Config.DependencyConditions = map[string]config.DependencyConfig{
		"database-primary": {Condition: config.DependencyHealthy},
		"message-broker":   {Condition: config.DependencyLogReady},
	}
	svc.Config.ReadyLogLine = "Application started and ready to accept incoming connections"
	svc.Config.EnvFiles = []string{"config/base.env", "config/development.env", "config/secrets.local.env"}
	svc.Config.Command = "node --enable-source-maps ./src/workers/event-processor.js"
	svc.Config.Shutdown.Command = "scripts/gracefully-stop-event-processor --wait-for-jobs"
	svc.Config.HealthCheck = &config.HealthCheckConfig{Readiness: &config.CheckConfig{
		Type: config.CheckHTTP,
		URL:  "http://localhost:8080/internal/health/readiness",
	}}

	const width = 28
	lines := model.serviceDetailLines(svc, width)
	for _, line := range lines {
		if lineWidth := lipgloss.Width(line); lineWidth > width {
			t.Fatalf("detail line width = %d, want at most %d: %q", lineWidth, width, ansi.Strip(line))
		}
	}
	plain := ansi.Strip(strings.Join(lines, "\n"))
	for _, expected := range []string{"TAGS\n  ↳ ", "DEPENDS\n  ↳ database-primary", "READY LOG\n  ↳ ", "ENV FILES\n  ↳ ", "COMMAND\n  ↳ "} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("responsive details do not contain %q:\n%s", expected, plain)
		}
	}
	for _, item := range []string{"backend", "payments", "internal", "critical", "worker", "database-primary", "message-broker"} {
		if !strings.Contains(plain, "\n  ↳ "+item) {
			t.Fatalf("wrapped list item %q is not on its own line:\n%s", item, plain)
		}
	}
	for _, condition := range []string{"process_healthy", "process_log_ready"} {
		if !strings.Contains(plain, "\n    "+condition) {
			t.Fatalf("wrapped dependency condition %q is not on its own line:\n%s", condition, plain)
		}
	}

	wideTags := ansi.Strip(strings.Join(detailListItemsLines("TAGS", []string{"backend", "worker"}, ", ", 80), "\n"))
	if wideTags != "TAGS backend, worker" {
		t.Fatalf("wide tags = %q, want inline list", wideTags)
	}
}

func TestServiceDetailsScrollWhenContentOverflows(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	svc := model.FocusedService()
	svc.Config.Ports = []int{8001, 8002, 8003, 8004, 8005, 8006, 8007, 8008, 8009, 8010, 8011, 8012}
	model.portService = svc.Name
	model.portChecked = time.Now()
	model.width, model.height, model.ready = 80, 24, true

	_, detailHeight := model.serviceColumnLayout(model.height - 2)
	initial := ansi.Strip(model.renderServiceDetails(svc, 48, detailHeight))
	if strings.Contains(initial, "COMMAND") {
		t.Fatalf("command should initially be below the viewport:\n%s", initial)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyDown})
	if model.detailOffset != 1 {
		t.Fatalf("] changed detail offset to %d, want 1", model.detailOffset)
	}
	for range 20 {
		model.movePanelCursor(1)
	}
	scrolled := ansi.Strip(model.renderServiceDetails(svc, 48, detailHeight))
	if !strings.Contains(scrolled, "COMMAND") || !strings.Contains(scrolled, "↑/↓") {
		t.Fatalf("scrolled details do not expose the end and scroll hint:\n%s", scrolled)
	}
}

func TestHealthTargetsStartAtFirstColumn(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	for _, testCase := range []struct {
		check *config.CheckConfig
		want  string
	}{
		{check: &config.CheckConfig{Type: config.CheckHTTP, URL: "http://localhost:3801/healthz"}, want: "http://localhost:3801/healthz"},
		{check: &config.CheckConfig{Type: config.CheckTCP, Port: 3801}, want: "tcp://localhost:3801"},
	} {
		lines := model.healthDetailLines("READINESS", testCase.check, "waiting", 80)
		if got, want := ansi.Strip(lines[1]), "  ↳ "+testCase.want; got != want {
			t.Errorf("health target line = %q, want %q", got, want)
		}
	}
}
