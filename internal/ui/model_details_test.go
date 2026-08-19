package ui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/muesli/termenv"
)

func TestPortDetailsMergeConfiguredAndDetectedWithoutDuplicates(t *testing.T) {
	tests := []struct {
		name       string
		configured []int
		detected   []int
		want       []portDetailEntry
	}{
		{name: "empty"},
		{name: "detected only", detected: []int{3000, 8080}, want: []portDetailEntry{
			{port: 3000, detected: true}, {port: 8080, detected: true},
		}},
		{name: "configured only", configured: []int{8080}, want: []portDetailEntry{
			{port: 8080, configured: true},
		}},
		{name: "matching and different", configured: []int{8080, 8080, 9000}, detected: []int{3000, 8080}, want: []portDetailEntry{
			{port: 8080, configured: true, detected: true},
			{port: 9000, configured: true},
			{port: 3000, detected: true},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergePortDetailEntries(tt.configured, tt.detected); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("entries = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestDetectedPortDetailsExplainRuntimeRole(t *testing.T) {
	detected := ansi.Strip(strings.Join(renderDetectedPortDetail(portDetailEntry{port: 3000, detected: true}, "PORTS", 4, 80), "\n"))
	if detected != "PORTS 3000 detected · listening" {
		t.Fatalf("detected detail = %q", detected)
	}
	combined := ansi.Strip(strings.Join(renderDetectedPortDetail(portDetailEntry{port: 8080, configured: true, detected: true}, "PORTS", 4, 80), "\n"))
	if combined != "PORTS 8080 declared · listening" {
		t.Fatalf("combined detail = %q", combined)
	}
}

func TestPortDetailsAlignMixedWidthNumbers(t *testing.T) {
	entries := []portDetailEntry{
		{port: 300, detected: true},
		{port: 8080, configured: true, detected: true},
		{port: 49152, detected: true},
	}
	portWidth := portDetailNumberWidth(entries)
	lines := []string{
		strings.Join(renderDetectedPortDetail(entries[0], "PORTS", portWidth, 80), "\n"),
		strings.Join(renderDetectedPortDetail(entries[1], "     ", portWidth, 80), "\n"),
		strings.Join(renderDetectedPortDetail(entries[2], "     ", portWidth, 80), "\n"),
	}
	plain := ansi.Strip(strings.Join(lines, "\n"))
	want := "PORTS   300 detected · listening\n" +
		"       8080 declared · listening\n" +
		"      49152 detected · listening"
	if plain != want {
		t.Fatalf("aligned details:\n%s\nwant:\n%s", plain, want)
	}
}

func TestPortDetailsShowExplicitDetectionOptOut(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	disabled := false
	svc := model.FocusedService()
	svc.Config.Ports = nil
	svc.Config.DetectPorts = &disabled
	plain := ansi.Strip(strings.Join(model.renderPortDetailLines(svc, 80), "\n"))
	if plain != "PORTS detection off" {
		t.Fatalf("opt-out port detail = %q", plain)
	}
}

// Tests for the Details panel and port inspection.

func TestExternalPortConflictOffersVerifiedStopAction(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 32, true
	model.operationID = 7
	conflict := &app.PortConflictError{
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
	model.app.SetPortChecker(fakePortChecker{details: map[int]*config.PortInfo{8080: {Port: 8080, PID: 4242}}})
	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if command == nil {
		t.Fatal("k did not schedule a verified external-process stop")
	}
}

func TestPortReleaseRefusesChangedOwner(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.app.SetPortChecker(fakePortChecker{details: map[int]*config.PortInfo{8080: {Port: 8080, PID: 5252}}})
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
	model.FocusedService().Config.Description = "HTTP API"
	model.FocusedService().Config.HealthCheck = &config.HealthCheckConfig{
		Readiness: &config.CheckConfig{Type: config.CheckHTTP, URL: "http://127.0.0.1:8080/ready"},
		Liveness:  &config.CheckConfig{Type: config.CheckTCP, Port: 8080},
	}
	model.app.SetPortChecker(fakePortChecker{details: map[int]*config.PortInfo{
		8080: {Port: 8080, Address: "127.0.0.1", Protocol: "tcp", PID: 4321, Process: "test-api"},
	}})

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

	allDetails := ansi.Strip(strings.Join(model.serviceDetailLines(model.FocusedService(), 72), "\n"))
	previous := -1
	for _, field := range []string{"PID ", "DIR ", "ABOUT ", "TAGS ", "DEPENDS ", "PORTS ", "READINESS ", "LIVENESS ", "RECOVERY", "SHUTDOWN", "COMMAND "} {
		index := strings.Index(allDetails, field)
		if index < 0 {
			t.Fatalf("service details do not contain ordered field %q:\n%s", field, allDetails)
		}
		if index <= previous {
			t.Fatalf("service detail field %q is out of order:\n%s", field, allDetails)
		}
		previous = index
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

func TestPIDAndDirectoryUseSeparateLinesAndDirectoryWraps(t *testing.T) {
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
	if wide != "PID 0\nDIR apps/event-processor" {
		t.Fatalf("wide PID/DIR layout = %q, want separate lines", wide)
	}
}

func TestDisplayServiceDirectoryUsesProjectRelativePaths(t *testing.T) {
	workingDirectory := "/projects/kranz"
	for _, test := range []struct {
		name      string
		directory string
		want      string
	}{
		{name: "current", directory: "/projects/kranz", want: "."},
		{name: "child", directory: "/projects/kranz/apps/api", want: "apps/api"},
		{name: "external", directory: "/projects/shared/api", want: "/projects/shared/api"},
		{name: "configured relative", directory: "apps/api", want: "apps/api"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := displayServiceDirectory(test.directory, workingDirectory); got != test.want {
				t.Fatalf("displayServiceDirectory(%q, %q) = %q, want %q", test.directory, workingDirectory, got, test.want)
			}
		})
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
		name          string
		check         *config.CheckConfig
		detectedPorts []int
		serviceActive bool
		want          string
	}{
		{name: "static http", check: &config.CheckConfig{Type: config.CheckHTTP, URL: "http://localhost:3801/healthz"}, want: "http://localhost:3801/healthz"},
		{name: "static tcp", check: &config.CheckConfig{Type: config.CheckTCP, Port: 3801}, want: "tcp://localhost:3801"},
		{name: "resolved implicit tcp", check: &config.CheckConfig{Type: config.CheckTCP}, detectedPorts: []int{3802}, serviceActive: true, want: "tcp://localhost:3802"},
		{name: "resolved implicit http selector", check: &config.CheckConfig{Type: config.CheckHTTP, URL: "http://localhost/healthz", DetectedPortIndex: intPointer(1)}, detectedPorts: []int{4800, 3801}, serviceActive: true, want: "http://localhost:4800/healthz"},
		{name: "detecting tcp while active", check: &config.CheckConfig{Type: config.CheckTCP}, serviceActive: true, want: "tcp://localhost:[DETECTING]"},
		{name: "dynamic tcp port while stopped", check: &config.CheckConfig{Type: config.CheckTCP}, want: "tcp://localhost:[PORT]"},
		{name: "detecting http preserves path and query", check: &config.CheckConfig{Type: config.CheckHTTP, URL: "http://localhost/healthz?deep=1"}, serviceActive: true, want: "http://localhost:[DETECTING]/healthz?deep=1"},
		{name: "dynamic ipv6 http port while stopped", check: &config.CheckConfig{Type: config.CheckHTTP, URL: "https://[::1]/health"}, want: "https://[::1]:[PORT]/health"},
		{name: "ambiguous", check: &config.CheckConfig{Type: config.CheckTCP, PortFrom: config.PortFromDetected}, detectedPorts: []int{3801, 4800}, serviceActive: true, want: "detected port is ambiguous: [3801 4800]; set detected_port_index"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			lines := model.healthDetailLines("READINESS", testCase.check, "waiting", testCase.detectedPorts, testCase.serviceActive, 100)
			if got, want := ansi.Strip(lines[1]), "  ↳ "+testCase.want; got != want {
				t.Errorf("health target line = %q, want %q", got, want)
			}
			if strings.Contains(ansi.Strip(lines[1]), "<detected") || strings.Contains(ansi.Strip(lines[1]), "port: detected") {
				t.Errorf("health target exposes configuration placeholder: %q", ansi.Strip(lines[1]))
			}
		})
	}
}

func TestDynamicHealthTargetHighlightsOnlyResolvedPort(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)

	dynamic := checkDescription(&config.CheckConfig{
		Type: config.CheckHTTP, URL: "http://127.0.0.1/health", PortFrom: config.PortFromDetected,
	}, []int{3801}, true)
	if want := "http://127.0.0.1:" + PortStyle.Render("3801") + "/health"; dynamic != want {
		t.Fatalf("styled dynamic target = %q, want %q", dynamic, want)
	}
	if dynamic == ansi.Strip(dynamic) {
		t.Fatalf("dynamic target does not highlight its resolved port: %q", dynamic)
	}
	if got := ansi.Strip(dynamic); got != "http://127.0.0.1:3801/health" {
		t.Fatalf("dynamic target = %q", got)
	}
	pending := checkDescription(&config.CheckConfig{
		Type: config.CheckHTTP, URL: "http://127.0.0.1/health",
	}, nil, true)
	if want := "http://127.0.0.1:" + StartingBadgeStyle.Render("[DETECTING]") + "/health"; pending != want {
		t.Fatalf("styled detecting target = %q, want %q", pending, want)
	}
	static := checkDescription(&config.CheckConfig{
		Type: config.CheckHTTP, URL: "http://127.0.0.1:3801/health",
	}, nil, false)
	if static != ansi.Strip(static) {
		t.Fatalf("static target unexpectedly highlighted configured port: %q", static)
	}
}

func intPointer(value int) *int {
	return &value
}
