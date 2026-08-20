package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/port"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
	"github.com/kranz-org/kranz/internal/service"
)

// The inspection commands answer questions about a project from its
// configuration alone. None of them needs a runtime, so they work before the
// first `up` and never disturb a running one.

// loadProject reads the effective configuration the command should describe,
// honoring -C and repeated -f exactly like the runtime commands do.
func loadProject(options kranzcli.GlobalOptions) (*config.Config, []string, error) {
	original, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	if err := os.Chdir(options.Directory); err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.Chdir(original) }() // best effort; the configuration is fully read before returning
	paths := options.ConfigPaths
	if len(paths) == 0 {
		paths, err = config.DiscoverFiles(".")
		if err != nil {
			return nil, nil, &kranzcli.Error{
				Code:     "no_project",
				Message:  "no Kranz configuration was found in this directory",
				Hint:     "Run from a project directory or pass -f PATH.",
				ExitCode: kranzcli.ExitUsage,
				Cause:    err,
			}
		}
	}
	cfg, err := config.LoadFiles(paths)
	if err != nil {
		return nil, nil, &kranzcli.Error{Code: "invalid_config", Message: "configuration is not valid", ExitCode: kranzcli.ExitConfig, Cause: err}
	}
	absolute := make([]string, 0, len(paths))
	for _, path := range paths {
		if filepath.IsAbs(path) {
			absolute = append(absolute, path)
			continue
		}
		absolute = append(absolute, filepath.Join(options.Directory, path))
	}
	return cfg, absolute, nil
}

// selectServices resolves positional selectors, which name either a service or
// a tag. An empty selector means every configured service, which is safe here
// because no inspection command changes anything.
func selectServices(cfg *config.Config, selectors []string) ([]string, error) {
	if len(selectors) == 0 {
		return cfg.ServiceNames(), nil
	}
	seen := make(map[string]bool)
	var selected []string
	for _, selector := range selectors {
		matched := false
		for _, name := range cfg.ServiceNames() {
			svc := cfg.Services[name]
			if name != selector && !containsString(svc.Tags, selector) {
				continue
			}
			matched = true
			if !seen[name] {
				seen[name] = true
				selected = append(selected, name)
			}
		}
		if !matched {
			return nil, &kranzcli.Error{
				Code:     "selector_not_found",
				Message:  fmt.Sprintf("no service or tag matches %q", selector),
				Hint:     "Run `kranz list services` or `kranz list tags` to see what this project defines.",
				ExitCode: kranzcli.ExitNotFound,
			}
		}
	}
	return selected, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func runConfigCheck(options kranzcli.GlobalOptions, stdout io.Writer) error {
	cfg, paths, err := loadProject(options)
	if err != nil {
		return err
	}
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, struct {
			Project     string   `json:"project"`
			Runtime     string   `json:"runtime"`
			Layers      []string `json:"layers"`
			Services    int      `json:"services"`
			Actions     int      `json:"actions"`
			Diagnostics []string `json:"diagnostics"`
		}{cfg.Project, cfg.RuntimeName(), paths, len(cfg.Services), len(cfg.ActionIDs()), cfg.Diagnostics})
	}
	_, _ = fmt.Fprintf(stdout, "Configuration is valid.\n\nProject:  %s\nRuntime:  %s\nServices: %d\nActions:  %d\n", cfg.Project, cfg.RuntimeName(), len(cfg.Services), len(cfg.ActionIDs()))
	_, _ = fmt.Fprintf(stdout, "\nLayers:\n")
	for _, path := range paths {
		_, _ = fmt.Fprintf(stdout, "  %s\n", path)
	}
	if len(cfg.Diagnostics) > 0 {
		_, _ = fmt.Fprintf(stdout, "\nDiagnostics:\n")
		for _, diagnostic := range cfg.Diagnostics {
			_, _ = fmt.Fprintf(stdout, "  %s\n", diagnostic)
		}
	}
	return nil
}

func runList(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	kind := "services"
	if len(args) > 0 {
		kind = args[0]
	}
	if len(args) > 1 {
		return &kranzcli.Error{Code: "invalid_arguments", Message: "list accepts one of services, actions, or tags", ExitCode: kranzcli.ExitUsage}
	}
	cfg, _, err := loadProject(options)
	if err != nil {
		return err
	}
	switch kind {
	case "services":
		return listServices(cfg, options, stdout)
	case "actions":
		return listActions(cfg, options, stdout)
	case "tags":
		return listTags(cfg, options, stdout)
	default:
		return &kranzcli.Error{
			Code:     "invalid_arguments",
			Message:  fmt.Sprintf("unknown list kind %q", kind),
			Hint:     "Use `kranz list services`, `kranz list actions`, or `kranz list tags`.",
			ExitCode: kranzcli.ExitUsage,
		}
	}
}

func listServices(cfg *config.Config, options kranzcli.GlobalOptions, stdout io.Writer) error {
	type entry struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		DependsOn   []string `json:"depends_on"`
		Ports       []int    `json:"ports"`
		Disabled    bool     `json:"disabled"`
	}
	entries := make([]entry, 0, len(cfg.Services))
	for _, name := range cfg.ServiceNames() {
		svc := cfg.Services[name]
		entries = append(entries, entry{name, svc.Description, svc.Tags, svc.DependsOn, svc.Ports, svc.Disabled})
	}
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, entries)
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tTAGS\tDEPENDS ON\tPORTS\tDESCRIPTION")
	for _, item := range entries {
		name := item.Name
		if item.Disabled {
			name += " (disabled)"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", name, joinOrDash(item.Tags), joinOrDash(item.DependsOn), joinPortsOrDash(item.Ports), orDash(item.Description))
	}
	return w.Flush()
}

func listActions(cfg *config.Config, options kranzcli.GlobalOptions, stdout io.Writer) error {
	type entry struct {
		ID          string `json:"id"`
		Owner       string `json:"owner"`
		OwnerKind   string `json:"owner_kind"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Interactive bool   `json:"interactive"`
	}
	ids := cfg.ActionIDs()
	entries := make([]entry, 0, len(ids))
	for _, id := range ids {
		action, ok := cfg.ResolveAction(id)
		if !ok {
			continue
		}
		entries = append(entries, entry{actionIDString(id), id.Owner, string(id.OwnerKind), id.Name, action.Description, action.Interactive != nil && *action.Interactive})
	}
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, entries)
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ACTION\tOWNER\tINTERACTIVE\tDESCRIPTION")
	for _, item := range entries {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%t\t%s\n", item.ID, item.Owner, item.Interactive, orDash(item.Description))
	}
	return w.Flush()
}

func listTags(cfg *config.Config, options kranzcli.GlobalOptions, stdout io.Writer) error {
	counts := make(map[string]int)
	for _, name := range cfg.ServiceNames() {
		for _, tag := range cfg.Services[name].Tags {
			counts[tag]++
		}
	}
	tags := make([]string, 0, len(counts))
	for tag := range counts {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	type entry struct {
		Tag      string `json:"tag"`
		Services int    `json:"services"`
	}
	entries := make([]entry, 0, len(tags))
	for _, tag := range tags {
		entries = append(entries, entry{tag, counts[tag]})
	}
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, entries)
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "TAG\tSERVICES")
	for _, item := range entries {
		_, _ = fmt.Fprintf(w, "%s\t%d\n", item.Tag, item.Services)
	}
	return w.Flush()
}

func actionIDString(id config.ActionID) string {
	if id.Owner == "" {
		return id.Name
	}
	return id.Owner + "/" + id.Name
}

func joinOrDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ",")
}

func joinPortsOrDash(ports []int) string {
	if len(ports) == 0 {
		return "-"
	}
	texts := make([]string, 0, len(ports))
	for _, value := range ports {
		texts = append(texts, strconv.Itoa(value))
	}
	return strings.Join(texts, ",")
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func runInfo(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	if len(args) > 1 {
		return &kranzcli.Error{Code: "invalid_arguments", Message: "info accepts at most one service", ExitCode: kranzcli.ExitUsage}
	}
	cfg, paths, err := loadProject(options)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return projectInfo(cfg, paths, options, stdout)
	}
	name := args[0]
	svc, ok := cfg.Services[name]
	if !ok {
		return &kranzcli.Error{
			Code:     "service_not_found",
			Message:  fmt.Sprintf("service %q was not found", name),
			Hint:     "Run `kranz list services` to see what this project defines.",
			ExitCode: kranzcli.ExitNotFound,
		}
	}
	return serviceInfo(cfg, name, svc, options, stdout)
}

func projectInfo(cfg *config.Config, paths []string, options kranzcli.GlobalOptions, stdout io.Writer) error {
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, struct {
			Project  string   `json:"project"`
			Runtime  string   `json:"runtime"`
			Source   string   `json:"source"`
			Layers   []string `json:"layers"`
			Services []string `json:"services"`
			Tags     []string `json:"tags"`
			Actions  int      `json:"actions"`
		}{cfg.Project, cfg.RuntimeName(), string(cfg.Source), paths, cfg.ServiceNames(), projectTags(cfg), len(cfg.ActionIDs())})
	}
	_, _ = fmt.Fprintf(stdout, "Project:  %s\nRuntime:  %s\nSource:   %s\nServices: %s\nTags:     %s\nActions:  %d\n",
		cfg.Project, cfg.RuntimeName(), cfg.Source, joinOrDash(cfg.ServiceNames()), joinOrDash(projectTags(cfg)), len(cfg.ActionIDs()))
	_, _ = fmt.Fprintf(stdout, "\nLayers:\n")
	for _, path := range paths {
		_, _ = fmt.Fprintf(stdout, "  %s\n", path)
	}
	return nil
}

func projectTags(cfg *config.Config) []string {
	seen := make(map[string]bool)
	var tags []string
	for _, name := range cfg.ServiceNames() {
		for _, tag := range cfg.Services[name].Tags {
			if !seen[tag] {
				seen[tag] = true
				tags = append(tags, tag)
			}
		}
	}
	sort.Strings(tags)
	return tags
}

func serviceInfo(cfg *config.Config, name string, svc config.Service, options kranzcli.GlobalOptions, stdout io.Writer) error {
	dependents := directDependents(cfg, name)
	actions := make([]string, 0, len(svc.ActionOrder))
	actions = append(actions, svc.ActionOrder...)

	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Command     string   `json:"command"`
			Dir         string   `json:"dir"`
			Shell       string   `json:"shell"`
			Supervision string   `json:"supervision"`
			Tags        []string `json:"tags"`
			DependsOn   []string `json:"depends_on"`
			Dependents  []string `json:"dependents"`
			Ports       []int    `json:"ports"`
			Actions     []string `json:"actions"`
			Disabled    bool     `json:"disabled"`
			Healthcheck bool     `json:"healthcheck"`
		}{name, svc.Description, svc.Command, svc.Dir, svc.Shell, string(svc.Supervision), svc.Tags, svc.DependsOn, dependents, svc.Ports, actions, svc.Disabled, svc.HealthCheck != nil})
	}

	_, _ = fmt.Fprintf(stdout, "Service:     %s\n", name)
	if svc.Description != "" {
		_, _ = fmt.Fprintf(stdout, "Description: %s\n", svc.Description)
	}
	_, _ = fmt.Fprintf(stdout, "Command:     %s\n", orDash(svc.Command))
	_, _ = fmt.Fprintf(stdout, "Directory:   %s\n", orDash(svc.Dir))
	_, _ = fmt.Fprintf(stdout, "Supervision: %s\n", orDash(string(svc.Supervision)))
	_, _ = fmt.Fprintf(stdout, "Tags:        %s\n", joinOrDash(svc.Tags))
	_, _ = fmt.Fprintf(stdout, "Depends on:  %s\n", joinOrDash(svc.DependsOn))
	_, _ = fmt.Fprintf(stdout, "Dependents:  %s\n", joinOrDash(dependents))
	_, _ = fmt.Fprintf(stdout, "Ports:       %s\n", joinPortsOrDash(svc.Ports))
	_, _ = fmt.Fprintf(stdout, "Actions:     %s\n", joinOrDash(actions))
	_, _ = fmt.Fprintf(stdout, "Healthcheck: %t\n", svc.HealthCheck != nil)
	if svc.Disabled {
		_, _ = fmt.Fprintf(stdout, "Disabled:    true\n")
	}
	if len(svc.BeforeStart) > 0 {
		_, _ = fmt.Fprintf(stdout, "\nPrerequisites:\n")
		for _, prerequisite := range svc.BeforeStart {
			_, _ = fmt.Fprintf(stdout, "  %s\n", actionIDString(prerequisite.ActionID(name)))
		}
	}
	return nil
}

func directDependents(cfg *config.Config, name string) []string {
	var dependents []string
	for _, candidate := range cfg.ServiceNames() {
		if containsString(cfg.Services[candidate].DependsOn, name) {
			dependents = append(dependents, candidate)
		}
	}
	return dependents
}

// runPlan shows the waves a start would use: everything in one wave depends
// only on earlier waves, which is exactly how the runtime gates readiness.
func runPlan(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	cfg, _, err := loadProject(options)
	if err != nil {
		return err
	}
	selected, err := selectServices(cfg, args)
	if err != nil {
		return err
	}
	order, err := service.TopologicalOrder(cfg)
	if err != nil {
		return &kranzcli.Error{Code: "dependency_cycle", Message: err.Error(), ExitCode: kranzcli.ExitConfig}
	}

	// A plan has to include the dependencies of what was selected, because
	// starting the selection starts them too.
	wanted := make(map[string]bool)
	for _, name := range selected {
		addWithDependencies(cfg, name, wanted)
	}
	planned := make([]string, 0, len(wanted))
	for _, name := range order {
		if wanted[name] {
			planned = append(planned, name)
		}
	}
	waves := service.DependencyLevels(cfg, planned)

	if options.Output == kranzcli.OutputJSON {
		type wave struct {
			Wave     int      `json:"wave"`
			Services []string `json:"services"`
		}
		entries := make([]wave, 0, len(waves))
		for index, names := range waves {
			entries = append(entries, wave{index + 1, names})
		}
		return kranzcli.WriteJSON(stdout, entries)
	}
	for index, names := range waves {
		if len(names) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(stdout, "Wave %d:\n", index+1)
		for _, name := range names {
			svc := cfg.Services[name]
			gate := ""
			if len(svc.DependsOn) > 0 {
				gate = "  (after " + strings.Join(svc.DependsOn, ", ") + ")"
			}
			_, _ = fmt.Fprintf(stdout, "  %s%s\n", name, gate)
		}
	}
	return nil
}

func addWithDependencies(cfg *config.Config, name string, seen map[string]bool) {
	if seen[name] {
		return
	}
	seen[name] = true
	for _, dependency := range cfg.Services[name].DependsOn {
		addWithDependencies(cfg, dependency, seen)
	}
}

func runGraph(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	format := "text"
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "--format" && index+1 < len(args):
			format = args[index+1]
			index++
		case strings.HasPrefix(args[index], "--format="):
			format = strings.TrimPrefix(args[index], "--format=")
		default:
			return &kranzcli.Error{Code: "invalid_arguments", Message: fmt.Sprintf("unknown graph argument %q", args[index]), Hint: "Use `kranz graph [--format text|json|dot]`.", ExitCode: kranzcli.ExitUsage}
		}
	}
	if options.Output == kranzcli.OutputJSON {
		format = "json"
	}
	cfg, _, err := loadProject(options)
	if err != nil {
		return err
	}
	switch format {
	case "text":
		for _, name := range cfg.ServiceNames() {
			_, _ = fmt.Fprintf(stdout, "%s\n", name)
			for _, dependency := range cfg.Services[name].DependsOn {
				_, _ = fmt.Fprintf(stdout, "  depends on %s\n", dependency)
			}
		}
		return nil
	case "dot":
		_, _ = fmt.Fprintln(stdout, "digraph kranz {")
		for _, name := range cfg.ServiceNames() {
			_, _ = fmt.Fprintf(stdout, "  %q;\n", name)
			for _, dependency := range cfg.Services[name].DependsOn {
				_, _ = fmt.Fprintf(stdout, "  %q -> %q;\n", dependency, name)
			}
		}
		_, _ = fmt.Fprintln(stdout, "}")
		return nil
	case "json":
		type node struct {
			Name      string   `json:"name"`
			DependsOn []string `json:"depends_on"`
		}
		nodes := make([]node, 0, len(cfg.Services))
		for _, name := range cfg.ServiceNames() {
			nodes = append(nodes, node{name, cfg.Services[name].DependsOn})
		}
		return kranzcli.WriteJSON(stdout, nodes)
	default:
		return &kranzcli.Error{Code: "invalid_arguments", Message: fmt.Sprintf("unknown graph format %q", format), Hint: "Use text, json, or dot.", ExitCode: kranzcli.ExitUsage}
	}
}

func runPorts(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	cfg, _, err := loadProject(options)
	if err != nil {
		return err
	}
	selected, err := selectServices(cfg, args)
	if err != nil {
		return err
	}

	// A port a service picked at runtime is the one the user actually needs,
	// and it exists only in the running runtime. Reading the configuration
	// alone would answer a question nobody asked: what the file says, rather
	// than what is listening.
	detected := detectedPorts(options)

	type entry struct {
		Service string `json:"service"`
		Port    int    `json:"port"`
		Origin  string `json:"origin"`
		State   string `json:"state"`
		PID     int    `json:"pid"`
		Process string `json:"process"`
	}
	type wanted struct {
		service string
		port    int
		origin  string
	}

	var requested []wanted
	seen := make(map[string]bool)
	for _, name := range selected {
		for _, number := range cfg.Services[name].Ports {
			key := fmt.Sprintf("%s/%d", name, number)
			seen[key] = true
			requested = append(requested, wanted{name, number, "declared"})
		}
		for _, number := range detected[name] {
			key := fmt.Sprintf("%s/%d", name, number)
			if seen[key] {
				continue
			}
			seen[key] = true
			requested = append(requested, wanted{name, number, "detected"})
		}
	}

	numbers := make([]int, 0, len(requested))
	for _, item := range requested {
		numbers = append(numbers, item.port)
	}
	listeners := map[int]*config.PortInfo{}
	if len(numbers) > 0 {
		listeners, err = port.NewChecker().CheckPorts(numbers)
		if err != nil {
			return err
		}
	}

	entries := make([]entry, 0, len(requested))
	for _, item := range requested {
		row := entry{Service: item.service, Port: item.port, Origin: item.origin, State: "free"}
		if info := listeners[item.port]; info != nil {
			row.State = "listening"
			row.PID = info.PID
			row.Process = info.Process
		}
		entries = append(entries, row)
	}

	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, entries)
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(stdout, "No ports to report.")
		_, _ = fmt.Fprintln(stdout, "No selected service declares a port, and no running runtime has detected one.")
		if detected == nil {
			_, _ = fmt.Fprintln(stdout, "Start the project with `kranz up -d` to see ports detected at runtime.")
		}
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SERVICE\tPORT\tORIGIN\tSTATE\tPID\tPROCESS")
	for _, item := range entries {
		pid := "-"
		if item.PID != 0 {
			pid = strconv.Itoa(item.PID)
		}
		_, _ = fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\n", item.Service, item.Port, item.Origin, item.State, pid, orDash(item.Process))
	}
	return w.Flush()
}

// detectedPorts is a variable so a test can exercise the merge of declared and
// detected ports without standing up a runtime that opens real sockets.
var detectedPorts = detectedPortsByService

// detectedPortsByService asks the running runtime which ports its services
// actually opened. A project that is not running is the ordinary case here, not
// a failure, so every way of not reaching a runtime returns nil rather than an
// error: `kranz ports` must still answer from the configuration alone.
func detectedPortsByService(options kranzcli.GlobalOptions) map[string][]int {
	record, err := resolveSession(options)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := kranzruntime.DialContext(ctx, record.Socket, version)
	if err != nil {
		return nil
	}
	defer func() { _ = client.Close() }()
	ports := make(map[string][]int)
	for _, snapshot := range client.Services() {
		if len(snapshot.DetectedPorts) > 0 {
			ports[snapshot.Name] = snapshot.DetectedPorts
		}
	}
	return ports
}

func runPortInspect(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return &kranzcli.Error{Code: "invalid_arguments", Message: "port inspect takes exactly one port", Hint: "Run `kranz port inspect 8080`.", ExitCode: kranzcli.ExitUsage}
	}
	number, err := strconv.Atoi(args[0])
	if err != nil || number < 1 || number > 65535 {
		return &kranzcli.Error{Code: "invalid_arguments", Message: fmt.Sprintf("%q is not a port number", args[0]), ExitCode: kranzcli.ExitUsage}
	}
	info, err := port.NewChecker().CheckPort(number)
	if err != nil {
		return err
	}
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, struct {
			Port      int    `json:"port"`
			Listening bool   `json:"listening"`
			PID       int    `json:"pid"`
			Process   string `json:"process"`
			Address   string `json:"address"`
			Protocol  string `json:"protocol"`
		}{number, info != nil, pidOf(info), processOf(info), addressOf(info), protocolOf(info)})
	}
	if info == nil {
		_, _ = fmt.Fprintf(stdout, "Port %d is free.\n", number)
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "Port %d is held by %s (PID %d) on %s/%s.\n", number, orDash(info.Process), info.PID, orDash(info.Address), orDash(info.Protocol))
	return nil
}

func pidOf(info *config.PortInfo) int {
	if info == nil {
		return 0
	}
	return info.PID
}

func processOf(info *config.PortInfo) string {
	if info == nil {
		return ""
	}
	return info.Process
}

func addressOf(info *config.PortInfo) string {
	if info == nil {
		return ""
	}
	return info.Address
}

func protocolOf(info *config.PortInfo) string {
	if info == nil {
		return ""
	}
	return info.Protocol
}

// runDoctor runs preflight checks that do not start anything. It reports every
// finding rather than stopping at the first, because a preflight that hides
// the second problem behind the first costs another run to discover it.
func runDoctor(options kranzcli.GlobalOptions, stdout io.Writer) error {
	type finding struct {
		Check   string `json:"check"`
		Subject string `json:"subject"`
		Status  string `json:"status"`
		Detail  string `json:"detail"`
	}
	var findings []finding
	record := func(check, subject, status, detail string) {
		findings = append(findings, finding{check, subject, status, detail})
	}

	cfg, paths, err := loadProject(options)
	if err != nil {
		return err
	}
	record("config", strings.Join(paths, ", "), "ok", fmt.Sprintf("%d services, %d actions", len(cfg.Services), len(cfg.ActionIDs())))
	for _, diagnostic := range cfg.Diagnostics {
		record("config", "diagnostic", "warn", diagnostic)
	}

	if _, err := service.TopologicalOrder(cfg); err != nil {
		record("dependencies", "graph", "fail", err.Error())
	} else {
		record("dependencies", "graph", "ok", "no cycles")
	}

	base := options.Directory
	var declaredPorts []int
	for _, name := range cfg.ServiceNames() {
		svc := cfg.Services[name]
		declaredPorts = append(declaredPorts, svc.Ports...)

		if svc.Dir != "" {
			directory := svc.Dir
			if !filepath.IsAbs(directory) {
				directory = filepath.Join(base, directory)
			}
			if info, statErr := os.Stat(directory); statErr != nil || !info.IsDir() {
				record("directory", name, "fail", fmt.Sprintf("%s is not a directory", svc.Dir))
			}
		}
		// The loader resolves env files against the service directory and
		// tolerates a missing one, so an absent file is a warning about
		// variables that will silently not arrive, not a hard failure.
		for _, envFile := range config.ServiceEnvFiles(cfg, svc) {
			path := envFile
			if !filepath.IsAbs(path) {
				path = filepath.Join(base, path)
			}
			if _, statErr := os.Stat(path); statErr != nil {
				record("env_file", name, "warn", fmt.Sprintf("%s is missing; its variables will not be set", envFile))
			}
		}
		if svc.Command == "" && svc.Lifecycle.Start.Command == "" {
			record("command", name, "warn", "no start command")
			continue
		}
		shell := svc.Shell
		if shell == "" {
			shell = cfg.Defaults.Shell
		}
		if shell != "" {
			if _, lookErr := exec.LookPath(shell); lookErr != nil {
				record("shell", name, "fail", fmt.Sprintf("%s is not executable", shell))
			}
		}
	}

	if len(declaredPorts) > 0 {
		listeners, portErr := port.NewChecker().CheckPorts(declaredPorts)
		if portErr != nil {
			record("ports", "check", "warn", portErr.Error())
		} else {
			for _, name := range cfg.ServiceNames() {
				for _, number := range cfg.Services[name].Ports {
					if info := listeners[number]; info != nil {
						record("port", fmt.Sprintf("%s:%d", name, number), "warn", fmt.Sprintf("already held by %s (PID %d)", info.Process, info.PID))
					}
				}
			}
		}
	}

	failed := 0
	for _, item := range findings {
		if item.Status == "fail" {
			failed++
		}
	}

	if options.Output == kranzcli.OutputJSON {
		if err := kranzcli.WriteJSON(stdout, findings); err != nil {
			return err
		}
	} else {
		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "CHECK\tSUBJECT\tSTATUS\tDETAIL")
		for _, item := range findings {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.Check, item.Subject, item.Status, orDash(item.Detail))
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	if failed > 0 {
		return &kranzcli.Error{Code: "preflight_failed", Message: fmt.Sprintf("%d preflight check(s) failed", failed), ExitCode: kranzcli.ExitConfig}
	}
	return nil
}
