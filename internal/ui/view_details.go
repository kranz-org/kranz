package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/health"
	"github.com/kranz-org/kranz/internal/service"
)

// The Details panel. Every field is width-aware because the panel is the
// narrowest column and must degrade without truncating meaning.

func (m *Model) mouseInDetails(x, y int) bool {
	if x < 0 || x >= m.dashboardLeftWidth() || y < 1 || y >= m.height-1 {
		return false
	}
	listHeight, _ := m.serviceColumnLayout(m.height - 2)
	return y >= 1+listHeight
}

func (m *Model) renderServiceDetails(svc *service.Service, width, height int) string {
	contentWidth := max(1, width-2)
	contentHeight := max(1, height-2)
	if svc == nil {
		return renderTitledPanel(m.panelStyle(panelDetails), m.panelTitleStyle(panelDetails), contentWidth, contentHeight, "[2] DETAILS", []string{"", "No service selected"})
	}

	lines := m.serviceDetailLines(svc, contentWidth)
	viewportHeight := contentHeight
	maxOffset := max(0, len(lines)-viewportHeight)
	offset := min(m.detailOffset, maxOffset)
	end := min(len(lines), offset+viewportHeight)
	title := "[2] DETAILS"
	if maxOffset > 0 {
		title += ContextBarStyle.Render(fmt.Sprintf(" │ %d–%d/%d · ↑/↓", offset+1, end, len(lines)))
	}
	visible := append([]string(nil), lines[offset:end]...)
	for i := range visible {
		visible[i] = ansi.Truncate(visible[i], contentWidth, "…")
	}
	return renderTitledPanel(m.panelStyle(panelDetails), m.panelTitleStyle(panelDetails), contentWidth, contentHeight, title, visible)
}

func (m *Model) renderActionDetails(width, height int) string {
	contentWidth := max(1, width-2)
	contentHeight := max(1, height-2)
	id, action, state, exists := m.focusedActionDefinition()
	if !exists {
		return renderTitledPanel(m.panelStyle(panelDetails), m.panelTitleStyle(panelDetails), contentWidth, contentHeight, "[2] ACTION", []string{"", "No action selected"})
	}
	lines := m.actionDetailLines(id, action, state, contentWidth)
	return renderDetailViewport(m, "[2] ACTION", lines, contentWidth, contentHeight)
}

func (m *Model) actionDetailLines(id config.ActionID, action config.Action, state service.ActionResult, contentWidth int) []string {
	lines := []string{actionStatusIndicator(state.Status) + " " + ServiceNameStyle.Render(id.Name) + "  " + ContextBarStyle.Render(state.Status.String())}
	owner := "service " + id.Owner
	if id.OwnerKind == config.ActionOwnerGroup {
		owner = "group " + id.Owner
	}
	lines = append(lines, detailFieldLines("OWNER", owner, contentWidth)...)
	if action.Description != "" {
		lines = append(lines, detailFieldLines("ABOUT", action.Description, contentWidth)...)
	}
	lines = append(lines, detailFieldLines("DIRECTORY", displayServiceDirectory(action.Dir, m.workingDirectory), contentWidth)...)
	if action.Timeout > 0 {
		lines = append(lines, detailFieldLines("TIMEOUT", action.Timeout.String(), contentWidth)...)
	}
	mode := "captured"
	if action.ConfirmationRequired() {
		mode += " · confirmation required"
	}
	lines = append(lines, detailFieldLines("MODE", mode, contentWidth)...)
	if !state.StartedAt.IsZero() {
		lines = append(lines, detailFieldLines("LAST RUN", state.StartedAt.Local().Format("15:04:05"), contentWidth)...)
		if state.Status != service.ActionRunning {
			lines = append(lines, detailFieldLines("RESULT", fmt.Sprintf("exit %d · %s", state.ExitCode, state.Duration.Round(time.Millisecond)), contentWidth)...)
		}
	}
	lines = append(lines, detailFieldLines("COMMAND", action.Command, contentWidth)...)
	return lines
}

func (m *Model) renderActionGroupDetails(group string, width, height int) string {
	contentWidth := max(1, width-2)
	contentHeight := max(1, height-2)
	configured, exists := m.cfg.ActionGroups[group]
	if !exists {
		return renderTitledPanel(m.panelStyle(panelDetails), m.panelTitleStyle(panelDetails), contentWidth, contentHeight, "[2] ACTION GROUP", []string{"", "No action group selected"})
	}
	lines := []string{ServiceNameStyle.Render(group)}
	if configured.Description != "" {
		lines = append(lines, detailFieldLines("ABOUT", configured.Description, contentWidth)...)
	}
	lines = append(lines, detailFieldLines("ACTIONS", strconv.Itoa(len(configured.Actions)), contentWidth)...)
	lines = append(lines, detailFieldLines("DIRECTORY", displayServiceDirectory(configured.Dir, m.workingDirectory), contentWidth)...)
	return renderDetailViewport(m, "[2] ACTION GROUP", lines, contentWidth, contentHeight)
}

func renderDetailViewport(m *Model, title string, lines []string, contentWidth, contentHeight int) string {
	maxOffset := max(0, len(lines)-contentHeight)
	offset := min(m.detailOffset, maxOffset)
	end := min(len(lines), offset+contentHeight)
	if maxOffset > 0 {
		title += ContextBarStyle.Render(fmt.Sprintf(" │ %d–%d/%d · ↑/↓", offset+1, end, len(lines)))
	}
	visible := append([]string(nil), lines[offset:end]...)
	for index := range visible {
		visible[index] = ansi.Truncate(visible[index], contentWidth, "…")
	}
	return renderTitledPanel(m.panelStyle(panelDetails), m.panelTitleStyle(panelDetails), contentWidth, contentHeight, title, visible)
}

func (m *Model) renderTagDetails(tag string, width, height int) string {
	contentWidth := max(1, width-2)
	contentHeight := max(1, height-2)
	if tag == "" {
		return renderTitledPanel(m.panelStyle(panelDetails), m.panelTitleStyle(panelDetails), contentWidth, contentHeight, "[2] TAG DETAILS", []string{"", "No tag selected"})
	}

	lines := m.tagDetailLines(tag, contentWidth)
	viewportHeight := contentHeight
	maxOffset := max(0, len(lines)-viewportHeight)
	offset := min(m.detailOffset, maxOffset)
	end := min(len(lines), offset+viewportHeight)
	title := "[2] TAG DETAILS"
	if maxOffset > 0 {
		title += ContextBarStyle.Render(fmt.Sprintf(" │ %d–%d/%d · ↑/↓", offset+1, end, len(lines)))
	}
	visible := append([]string(nil), lines[offset:end]...)
	for index := range visible {
		visible[index] = ansi.Truncate(visible[index], contentWidth, "…")
	}
	return renderTitledPanel(m.panelStyle(panelDetails), m.panelTitleStyle(panelDetails), contentWidth, contentHeight, title, visible)
}

func (m *Model) tagDetailLines(tag string, contentWidth int) []string {
	services := m.servicesForTag(tag)
	stateCounts := make(map[string]int)
	ports := make(map[int]bool)
	relatedTags := make(map[string]bool)
	serviceNames := make(map[string]bool)
	for _, svc := range services {
		serviceNames[svc.Name] = true
		state := serviceStatusLabel(svc.Status(), m.serviceVisualState(svc))
		stateCounts[state]++
		for _, portNumber := range svc.Config.Ports {
			ports[portNumber] = true
		}
		for _, related := range svc.Config.Tags {
			if !strings.EqualFold(related, tag) {
				relatedTags[related] = true
			}
		}
	}

	statuses := make([]string, 0, len(stateCounts))
	for _, state := range []string{"Running", "Starting", "Queued", "Stopping", "Unhealthy", "Stopped"} {
		if count := stateCounts[state]; count > 0 {
			statuses = append(statuses, fmt.Sprintf("%d %s", count, strings.ToLower(state)))
		}
	}
	lines := []string{
		ServiceNameStyle.Render("#" + tag),
	}
	lines = append(lines, detailFieldLines("SUMMARY", fmt.Sprintf("%d services · %s", len(services), strings.Join(statuses, " · ")), contentWidth)...)

	serviceParts := make([]string, 0, len(services))
	externalDependencies := make(map[string]bool)
	for _, svc := range services {
		state := serviceStatusLabel(svc.Status(), m.serviceVisualState(svc))
		part := serviceStatusIndicator(m.serviceVisualState(svc)) + " " + svc.Name + " · " + strings.ToLower(state)
		if len(svc.Config.Ports) > 0 {
			configuredPorts := make([]string, 0, len(svc.Config.Ports))
			for _, portNumber := range svc.Config.Ports {
				configuredPorts = append(configuredPorts, ":"+strconv.Itoa(portNumber))
			}
			part += " · " + strings.Join(configuredPorts, ", ")
		}
		serviceParts = append(serviceParts, part)
		for _, dependency := range svc.Config.DependsOn {
			if !serviceNames[dependency] {
				externalDependencies[dependency] = true
			}
		}
	}
	lines = append(lines, detailSectionLines("SERVICES", serviceParts, contentWidth)...)

	portNumbers := make([]int, 0, len(ports))
	for portNumber := range ports {
		portNumbers = append(portNumbers, portNumber)
	}
	sort.Ints(portNumbers)
	portParts := make([]string, 0, len(portNumbers))
	for _, portNumber := range portNumbers {
		portParts = append(portParts, strconv.Itoa(portNumber))
	}
	if len(portParts) == 0 {
		portParts = []string{"—"}
	}
	lines = append(lines, detailListItemsLines("PORTS", portParts, ", ", contentWidth)...)

	related := sortedStringSet(relatedTags)
	lines = append(lines, detailListItemsLines("RELATED", related, ", ", contentWidth)...)
	dependencies := sortedStringSet(externalDependencies)
	lines = append(lines, detailListItemsLines("DEPENDS", dependencies, ", ", contentWidth)...)
	return lines
}

func (m *Model) serviceDetailLines(svc *service.Service, contentWidth int) []string {
	visualState := m.serviceVisualState(svc)
	lines := []string{
		serviceStatusIndicator(visualState) + " " + ServiceNameStyle.Render(svc.Name) + "  " +
			ContextBarStyle.Render(serviceStatusLabel(svc.Status(), visualState)),
	}
	if svc.Config.Disabled {
		lines = append(lines, StartingBadgeStyle.Render("DISABLED")+" "+detailValue("manual start only"))
	}
	if visualState == visualQueued {
		reason := "Scheduled by the current start operation"
		if len(svc.Config.DependsOn) > 0 {
			reason = "Waiting for dependencies: " + strings.Join(svc.Config.DependsOn, ", ")
		}
		lines = append(lines, detailFieldLines("START", StartingBadgeStyle.Render(reason), contentWidth)...)
	}
	directory := displayServiceDirectory(svc.Config.Dir, m.workingDirectory)
	lines = append(lines, pidDirectoryDetailLines(svc.PID(), directory, contentWidth)...)
	lines = append(lines, runtimeDetailLines(svc, contentWidth)...)
	if svc.Config.Description != "" {
		lines = append(lines, detailFieldLines("ABOUT", svc.Config.Description, contentWidth)...)
	}
	supervision := string(svc.Config.SupervisionMode())
	if svc.Config.IsDetached() {
		if svc.Config.Lifecycle.Status != nil {
			supervision += " · observed"
		} else {
			supervision += " · assumed"
		}
	}
	lines = append(lines, detailFieldLines("SUPERVISION", supervision, contentWidth)...)
	if svc.Config.IsDetached() {
		capabilities := make([]string, 0, 4)
		if svc.Config.Lifecycle.Start != nil {
			capabilities = append(capabilities, "start")
		}
		if svc.Config.Lifecycle.Stop != nil {
			capabilities = append(capabilities, "stop")
		}
		if svc.Config.Lifecycle.Status != nil {
			capabilities = append(capabilities, "status")
		}
		if svc.Config.Lifecycle.Logs != nil {
			capabilities = append(capabilities, "logs")
		}
		lines = append(lines, detailFieldLines("LIFECYCLE", strings.Join(capabilities, ", "), contentWidth)...)
	}
	if len(svc.Config.Tags) == 0 {
		lines = append(lines, detailFieldLines("TAGS", "—", contentWidth)...)
	} else {
		lines = append(lines, detailListItemsLines("TAGS", svc.Config.Tags, ", ", contentWidth)...)
	}
	lines = append(lines, dependencyDetailLines(svc, contentWidth)...)
	lines = append(lines, prerequisiteDetailLines(svc, contentWidth)...)
	lines = append(lines, m.renderPortDetailLines(svc, contentWidth)...)
	detectedPorts := svc.DetectedPorts()
	serviceActive := svc.Status() != config.StatusStopped
	lines = append(lines, m.healthDetailLines("READINESS", healthReadiness(svc), m.readinessSummary(svc), detectedPorts, serviceActive, contentWidth)...)
	lines = append(lines, m.healthDetailLines("LIVENESS", healthLiveness(svc), m.livenessSummary(svc), detectedPorts, serviceActive, contentWidth)...)
	if svc.Config.ReadyLogLine != "" {
		lines = append(lines, detailFieldLines("READY LOG", svc.Config.ReadyLogLine, contentWidth)...)
	}
	lines = append(lines, availabilityDetailLines(svc, contentWidth)...)
	lines = append(lines, shutdownDetailLines(svc, contentWidth)...)
	if len(svc.Config.EnvFiles) > 0 {
		lines = append(lines, detailFieldLines("ENV FILES", strings.Join(svc.Config.EnvFiles, ", "), contentWidth)...)
	}
	if len(svc.Config.SuccessExitCodes) > 0 {
		codes := make([]string, 0, len(svc.Config.SuccessExitCodes))
		for _, code := range svc.Config.SuccessExitCodes {
			codes = append(codes, strconv.Itoa(code))
		}
		lines = append(lines, detailFieldLines("SUCCESS", "0, "+strings.Join(codes, ", "), contentWidth)...)
	}
	lines = append(lines, detailFieldLines("COMMAND", svc.Config.Command, contentWidth)...)
	return lines
}

// prerequisiteDetailLines renders before_start so the reason a service runs
// extra work before starting is visible without opening the configuration.
func prerequisiteDetailLines(svc *service.Service, contentWidth int) []string {
	if len(svc.Config.BeforeStart) == 0 {
		return nil
	}
	parts := make([]string, 0, len(svc.Config.BeforeStart))
	for _, prerequisite := range svc.Config.BeforeStart {
		id := prerequisite.ActionID(svc.Name)
		part := id.Name
		if id.Owner != svc.Name {
			part = id.Owner + " · " + id.Name
		}
		parts = append(parts, part+" · "+string(prerequisite.RunPolicy()))
	}
	combined := strings.Join(parts, "; ")
	if lipgloss.Width("BEFORE START "+combined) <= contentWidth {
		return detailFieldLines("BEFORE START", combined, contentWidth)
	}
	lines := []string{DetailLabelStyle.Render("BEFORE START")}
	for _, part := range parts {
		lines = append(lines, ContextBarStyle.Render("  ↳ ")+detailValue(part))
	}
	return lines
}

func dependencyDetailLines(svc *service.Service, contentWidth int) []string {
	if len(svc.Config.DependsOn) == 0 {
		return detailFieldLines("DEPENDS", "—", contentWidth)
	}
	parts := make([]string, 0, len(svc.Config.DependsOn))
	conditions := make([]string, 0, len(svc.Config.DependsOn))
	for _, dependency := range svc.Config.DependsOn {
		condition := config.DependencyHealthy
		if configured, ok := svc.Config.DependencyConditions[dependency]; ok && configured.Condition != "" {
			condition = configured.Condition
		}
		parts = append(parts, dependency+" · "+string(condition))
		conditions = append(conditions, string(condition))
	}
	combined := strings.Join(parts, "; ")
	if lipgloss.Width("DEPENDS "+combined) <= contentWidth {
		return detailFieldLines("DEPENDS", combined, contentWidth)
	}

	lines := []string{DetailLabelStyle.Render("DEPENDS")}
	for index, dependency := range svc.Config.DependsOn {
		part := parts[index]
		if lipgloss.Width("  ↳ "+part) <= contentWidth {
			lines = append(lines, ContextBarStyle.Render("  ↳ ")+detailValue(part))
			continue
		}
		lines = append(lines, detailArrowValueLines(dependency, contentWidth)...)
		lines = append(lines, detailIndentedValueLines(conditions[index], "    ", contentWidth)...)
	}
	return lines
}

func availabilityDetailLines(svc *service.Service, contentWidth int) []string {
	availability := svc.Config.Availability
	policy := availability.Restart
	if policy == "" {
		policy = "no"
	}
	parts := []string{"restart " + policy}
	if policy == "always" || policy == "on_failure" {
		backoff := availability.Backoff
		if backoff <= 0 {
			backoff = time.Second
		}
		limit := "unlimited"
		if availability.MaxRestarts > 0 {
			limit = strconv.Itoa(availability.MaxRestarts)
		}
		parts = append(parts, "backoff "+backoff.String(), fmt.Sprintf("restarts %d/%s", svc.GetState().RestartCount, limit))
	}
	if availability.ExitOnEnd {
		parts = append(parts, "exit on end")
	}
	if availability.ExitOnSkipped {
		parts = append(parts, "exit on skipped")
	}
	return detailSectionLines("RECOVERY", parts, contentWidth)
}

func runtimeDetailLines(svc *service.Service, contentWidth int) []string {
	state := svc.GetState()
	if state.StartedAt.IsZero() {
		return nil
	}

	lines := detailFieldLines("LAST START", state.StartedAt.Local().Format("15:04:05"), contentWidth)
	if svc.Status() != config.StatusStopped {
		elapsed := time.Since(state.StartedAt)
		if elapsed < 0 {
			elapsed = 0
		}
		uptime := elapsed.Round(time.Second).String()
		if elapsed < time.Second {
			uptime = "<1s"
		}
		lines = append(lines, detailFieldLines("UPTIME", uptime, contentWidth)...)
	}
	if state.Completed {
		exit := fmt.Sprintf("code %d", state.ExitCode)
		if state.ExitError != "" {
			exit += " · " + state.ExitError
		}
		lines = append(lines, detailFieldLines("LAST EXIT", exit, contentWidth)...)
	}
	return lines
}

func shutdownDetailLines(svc *service.Service, contentWidth int) []string {
	shutdown := svc.Config.Shutdown
	timeout := shutdown.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	target := "process group"
	if shutdown.ParentOnly {
		target = "parent only"
	}
	if shutdown.Command != "" {
		return detailSectionLines("SHUTDOWN", []string{"command " + shutdown.Command, "timeout " + timeout.String()}, contentWidth)
	}
	signal := shutdown.Signal
	if signal == 0 {
		signal = 15
	}
	return detailSectionLines("SHUTDOWN", []string{fmt.Sprintf("signal %d", signal), "timeout " + timeout.String(), "target " + target}, contentWidth)
}

func detailSectionLines(label string, parts []string, contentWidth int) []string {
	lines := []string{DetailLabelStyle.Render(label)}
	for _, part := range parts {
		lines = append(lines, detailArrowValueLines(part, contentWidth)...)
	}
	return lines
}

func detailListItemsLines(label string, parts []string, separator string, contentWidth int) []string {
	combined := strings.Join(parts, separator)
	if lipgloss.Width(label+" "+combined) <= contentWidth {
		return detailFieldLines(label, combined, contentWidth)
	}
	return detailSectionLines(label, parts, contentWidth)
}

func (m *Model) healthDetailLines(label string, check *config.CheckConfig, status string, detectedPorts []int, serviceActive bool, contentWidth int) []string {
	lines := detailFieldLines(label, status, contentWidth)
	if check != nil {
		lines = append(lines, detailArrowValueLines(checkDescription(check, detectedPorts, serviceActive), contentWidth)...)
	}
	return lines
}

func healthReadiness(svc *service.Service) *config.CheckConfig {
	if svc.Config.HealthCheck == nil {
		return nil
	}
	return svc.Config.HealthCheck.Readiness
}

func healthLiveness(svc *service.Service) *config.CheckConfig {
	if svc.Config.HealthCheck == nil {
		return nil
	}
	return svc.Config.HealthCheck.Liveness
}

func (m *Model) scrollDetails(direction int) {
	_, detailHeight := m.serviceColumnLayout(m.height - 2)
	viewportHeight := max(1, detailHeight-2)
	contentWidth := max(1, m.dashboardLeftWidth()-2)
	lineCount := 0
	if m.listMode == listServices && m.focusedAction != nil {
		id, action, state, exists := m.focusedActionDefinition()
		if exists {
			lineCount = len(m.actionDetailLines(id, action, state, contentWidth))
		}
	} else if m.listMode == listServices && m.focusedActionGroup != "" {
		lineCount = 4
	} else if m.listMode == listTags {
		if svc := m.focusedTagService(); svc != nil {
			lineCount = len(m.serviceDetailLines(svc, contentWidth))
		} else {
			lineCount = len(m.tagDetailLines(m.focusedTag(), contentWidth))
		}
	} else if svc := m.FocusedService(); svc != nil {
		lineCount = len(m.serviceDetailLines(svc, contentWidth))
	}
	maxOffset := max(0, lineCount-viewportHeight)
	m.detailOffset = min(maxOffset, max(0, m.detailOffset+direction))
}

func (m *Model) renderPortDetailLines(svc *service.Service, contentWidth int) []string {
	entries := mergePortDetailEntries(svc.Config.Ports, svc.DetectedPorts())
	if len(entries) == 0 {
		if !svc.Config.PortDiscoveryEnabled() {
			return []string{DetailLabelStyle.Render("PORTS") + " " + ContextBarStyle.Render("detection off")}
		}
		return []string{DetailLabelStyle.Render("PORTS") + " " + detailValue("—")}
	}

	portWidth := portDetailNumberWidth(entries)
	lines := make([]string, 0, len(entries))
	for index, entry := range entries {
		label := "     "
		if index == 0 {
			label = "PORTS"
		}
		if entry.detected {
			lines = append(lines, renderDetectedPortDetail(entry, label, portWidth, contentWidth)...)
			continue
		}
		lines = append(lines, m.renderPortDetail(svc, entry.port, label, portWidth, contentWidth)...)
	}
	return lines
}

type portDetailEntry struct {
	port       int
	configured bool
	detected   bool
}

func portDetailNumberWidth(entries []portDetailEntry) int {
	width := 1
	for _, entry := range entries {
		width = max(width, len(strconv.Itoa(entry.port)))
	}
	return width
}

func mergePortDetailEntries(configured, detected []int) []portDetailEntry {
	if len(configured) == 0 && len(detected) == 0 {
		return nil
	}
	entries := make([]portDetailEntry, 0, len(configured)+len(detected))
	indices := make(map[int]int, len(configured)+len(detected))
	for _, portNumber := range configured {
		if _, exists := indices[portNumber]; exists {
			continue
		}
		indices[portNumber] = len(entries)
		entries = append(entries, portDetailEntry{port: portNumber, configured: true})
	}
	for _, portNumber := range detected {
		if index, exists := indices[portNumber]; exists {
			entries[index].detected = true
			continue
		}
		indices[portNumber] = len(entries)
		entries = append(entries, portDetailEntry{port: portNumber, detected: true})
	}
	return entries
}

func renderDetectedPortDetail(entry portDetailEntry, label string, portWidth, contentWidth int) []string {
	role := "detected"
	if entry.configured {
		role = "declared"
	}
	base := DetailLabelStyle.Render(label) + " " + PortStyle.Render(fmt.Sprintf("%*d", portWidth, entry.port)) + " "
	return renderPortStatus(base, role, RunningBadgeStyle.Render("listening"), contentWidth)
}

func (m *Model) renderPortDetail(svc *service.Service, portNumber int, label string, portWidth, contentWidth int) []string {
	base := DetailLabelStyle.Render(label) + " " + PortStyle.Render(fmt.Sprintf("%*d", portWidth, portNumber)) + " "
	if m.portService != svc.Name || (m.portScanBusy && m.portChecked.IsZero()) {
		return renderPortStatus(base, "declared", StartingBadgeStyle.Render("checking…"), contentWidth)
	}
	if m.portError != nil {
		return renderPortStatus(base, "declared", FailedBadgeStyle.Render("unavailable"), contentWidth)
	}
	if info := m.portDetails[portNumber]; info != nil {
		prefix := base + ContextBarStyle.Render("declared · ")
		if lipgloss.Width(prefix+"listening") <= contentWidth {
			return renderListeningPort(prefix, info, m.manager.ManagedServiceForPID(info.PID), contentWidth)
		}
		lines := []string{base + ContextBarStyle.Render("declared")}
		return append(lines, renderListeningPort(ContextBarStyle.Render("  ↳ "), info, m.manager.ManagedServiceForPID(info.PID), contentWidth)...)
	}
	return renderPortStatus(base, "declared", StoppedBadgeStyle.Render("free"), contentWidth)
}

func renderPortStatus(base, role, status string, contentWidth int) []string {
	inline := base + ContextBarStyle.Render(role+" · ") + status
	if lipgloss.Width(inline) <= contentWidth {
		return []string{inline}
	}
	return []string{
		base + ContextBarStyle.Render(role),
		ContextBarStyle.Render("  ↳ ") + status,
	}
}

func renderListeningPort(prefix string, info *config.PortInfo, managedService string, contentWidth int) []string {
	line := prefix + RunningBadgeStyle.Render("listening")
	lines := []string{line}
	if endpoint := listenerEndpoint(info); endpoint != "" {
		withEndpoint := line + ContextBarStyle.Render(" · "+endpoint)
		if lipgloss.Width(withEndpoint) <= contentWidth {
			lines[0] = withEndpoint
		} else {
			lines = append(lines, detailArrowValueLines(endpoint, contentWidth)...)
		}
	}
	owner := make([]string, 0, 2)
	if info.Process != "" {
		owner = append(owner, info.Process)
	}
	if info.PID > 0 {
		owner = append(owner, fmt.Sprintf("PID %d", info.PID))
	}
	if len(owner) > 0 {
		ownership := "owner: unknown"
		if managedService != "" {
			ownership = "owner: kranz"
		} else if info.PID > 0 {
			ownership = "owner: external"
		}
		ownershipWarning := managedService == ""
		combined := strings.Join(append(append([]string(nil), owner...), ownership), " · ")
		if lipgloss.Width("  ↳ "+combined) <= contentWidth {
			line := ContextBarStyle.Render("  ↳ ") + detailValue(strings.Join(owner, " · ")) + ContextBarStyle.Render(" · ")
			lines = append(lines, line+renderPortOwnership(ownership, ownershipWarning))
		} else {
			for _, part := range owner {
				lines = append(lines, detailArrowValueLines(part, contentWidth)...)
			}
			lines = append(lines, ContextBarStyle.Render("  ↳ ")+renderPortOwnership(ownership, ownershipWarning))
		}
	}
	return lines
}

func renderPortOwnership(ownership string, warning bool) string {
	if warning {
		return StartingBadgeStyle.Render(ownership)
	}
	return detailValue(ownership)
}

func detailFieldLines(label, value string, contentWidth int) []string {
	inline := label + " " + value
	if lipgloss.Width(inline) <= contentWidth {
		return []string{DetailLabelStyle.Render(label) + " " + detailValue(value)}
	}
	lines := []string{DetailLabelStyle.Render(label)}
	return append(lines, detailArrowValueLines(value, contentWidth)...)
}

func pidDirectoryDetailLines(pid int, directory string, contentWidth int) []string {
	pidValue := strconv.Itoa(pid)
	lines := []string{DetailLabelStyle.Render("PID") + " " + detailValue(pidValue)}
	return append(lines, wrappedLabeledDetailLines("DIR", directory, contentWidth)...)
}

// displayServiceDirectory keeps project-local paths readable while preserving
// absolute paths for services configured outside the directory Kranz runs in.
func displayServiceDirectory(directory, workingDirectory string) string {
	if directory == "" || workingDirectory == "" || !filepath.IsAbs(directory) {
		return directory
	}
	relative, err := filepath.Rel(workingDirectory, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return directory
	}
	return relative
}

func wrappedLabeledDetailLines(label, value string, contentWidth int) []string {
	prefixWidth := lipgloss.Width(label + " ")
	available := max(1, contentWidth-prefixWidth)
	wrapped := wrapDetailValue(value, available)
	lines := make([]string, 0, len(wrapped))
	for index, part := range wrapped {
		if index == 0 {
			lines = append(lines, DetailLabelStyle.Render(label)+" "+detailValue(part))
			continue
		}
		lines = append(lines, strings.Repeat(" ", prefixWidth)+detailValue(part))
	}
	return lines
}

func detailArrowValueLines(value string, contentWidth int) []string {
	const (
		arrowPrefix        = "  ↳ "
		continuationPrefix = "    "
	)
	available := max(1, contentWidth-lipgloss.Width(arrowPrefix))
	wrapped := wrapDetailValue(value, available)
	lines := make([]string, 0, len(wrapped))
	for index, part := range wrapped {
		prefix := continuationPrefix
		if index == 0 {
			prefix = arrowPrefix
		}
		lines = append(lines, ContextBarStyle.Render(prefix)+detailValue(part))
	}
	return lines
}

func detailIndentedValueLines(value, prefix string, contentWidth int) []string {
	available := max(1, contentWidth-lipgloss.Width(prefix))
	wrapped := wrapDetailValue(value, available)
	lines := make([]string, 0, len(wrapped))
	for _, part := range wrapped {
		lines = append(lines, ContextBarStyle.Render(prefix)+detailValue(part))
	}
	return lines
}

func wrapDetailValue(value string, width int) []string {
	wordWrapped := strings.Split(ansi.Wordwrap(value, width, "/,"), "\n")
	lines := make([]string, 0, len(wordWrapped))
	for _, line := range wordWrapped {
		if lipgloss.Width(line) <= width {
			lines = append(lines, line)
			continue
		}
		lines = append(lines, strings.Split(ansi.Hardwrap(line, width, false), "\n")...)
	}
	return lines
}

func listenerEndpoint(info *config.PortInfo) string {
	if info == nil || info.Protocol == "" || info.Address == "" {
		return ""
	}
	address := info.Address
	if strings.Contains(address, ":") && !strings.HasPrefix(address, "[") {
		address = "[" + address + "]"
	}
	return strings.ToLower(info.Protocol) + "://" + address + fmt.Sprintf(":%d", info.Port)
}

func (m *Model) readinessSummary(svc *service.Service) string {
	if svc.Config.HealthCheck == nil || svc.Config.HealthCheck.Readiness == nil {
		return detailValue("not configured")
	}
	healthData := m.healthChecker.GetHealth(svc.Name)
	if healthData == nil {
		return StoppedBadgeStyle.Render("inactive")
	}
	if !healthData.IsReady() {
		return StartingBadgeStyle.Render("waiting")
	}
	return RunningBadgeStyle.Render("ready")
}

func (m *Model) livenessSummary(svc *service.Service) string {
	if svc.Config.HealthCheck == nil || svc.Config.HealthCheck.Liveness == nil {
		return detailValue("not configured")
	}
	healthData := m.healthChecker.GetHealth(svc.Name)
	if healthData == nil {
		return StoppedBadgeStyle.Render("inactive")
	}
	if healthData.GetLastCheck().IsZero() {
		return StartingBadgeStyle.Render("checking")
	}
	if healthData.IsAlive() {
		return RunningBadgeStyle.Render("alive")
	}
	return FailedBadgeStyle.Render("failed")
}

func checkDescription(check *config.CheckConfig, detectedPorts []int, serviceActive bool) string {
	resolved, err := health.ResolveCheckTarget(check, detectedPorts)
	if err != nil {
		if strings.HasPrefix(err.Error(), "waiting for") {
			return detectingCheckDescription(check, serviceActive)
		}
		return FailedBadgeStyle.Render(err.Error())
	}

	switch resolved.Type {
	case config.CheckHTTP:
		if check.UsesDetectedPort() {
			return highlightEndpointPort(resolved.URL, resolved.Port)
		}
		return resolved.URL
	case config.CheckTCP:
		if check.UsesDetectedPort() {
			return "tcp://localhost:" + PortStyle.Render(strconv.Itoa(resolved.Port))
		}
		return fmt.Sprintf("tcp://localhost:%d", resolved.Port)
	case config.CheckCommand:
		return "$ " + resolved.Command
	default:
		return string(resolved.Type)
	}
}

func detectingCheckDescription(check *config.CheckConfig, serviceActive bool) string {
	marker := ContextBarStyle.Render("[PORT]")
	if serviceActive {
		marker = StartingBadgeStyle.Render("[DETECTING]")
	}
	switch check.Type {
	case config.CheckHTTP:
		return insertEndpointPort(check.URL, marker)
	case config.CheckTCP:
		return "tcp://localhost:" + marker
	default:
		return marker
	}
}

func insertEndpointPort(endpoint, renderedPort string) string {
	schemeEnd := strings.Index(endpoint, "://")
	if schemeEnd < 0 {
		return endpoint
	}
	authorityStart := schemeEnd + len("://")
	authorityEnd := len(endpoint)
	if offset := strings.IndexAny(endpoint[authorityStart:], "/?#"); offset >= 0 {
		authorityEnd = authorityStart + offset
	}
	return endpoint[:authorityEnd] + ":" + renderedPort + endpoint[authorityEnd:]
}

func highlightEndpointPort(endpoint string, port int) string {
	schemeEnd := strings.Index(endpoint, "://")
	if schemeEnd < 0 {
		return endpoint
	}
	authorityStart := schemeEnd + len("://")
	authorityEnd := len(endpoint)
	if offset := strings.IndexAny(endpoint[authorityStart:], "/?#"); offset >= 0 {
		authorityEnd = authorityStart + offset
	}
	portText := strconv.Itoa(port)
	relativePortStart := strings.LastIndex(endpoint[authorityStart:authorityEnd], ":"+portText)
	if relativePortStart < 0 {
		return endpoint
	}
	portStart := authorityStart + relativePortStart + 1
	return endpoint[:portStart] + PortStyle.Render(portText) + endpoint[portStart+len(portText):]
}

func detailValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}
