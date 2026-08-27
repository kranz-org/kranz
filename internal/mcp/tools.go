package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

const maxLogEvents = 1000

// waitTransportGrace is how long past the runtime's own wait timeout the
// request deadline waits before giving up on the transport itself.
const waitTransportGrace = 5 * time.Second

// toolNames is the allow-list in the order tools/list publishes them. It is
// the specification; installTools verifies the implemented set matches it
// exactly, so a tool cannot appear without being listed here, and a listed
// name cannot resolve to nothing.
var toolNames = []string{
	"runtimes", "status", "runs", "changes", "plan", "graph", "ports", "port_inspect", "logs", "wait", "health",
	"action_list", "action_info", "action_result",
	"doctor", "start", "stop", "restart", "action_run", "action_cancel", "logs_clear", "reload",
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

var (
	selectorsProperty    = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Exact service names or case-insensitive tags."}
	confirmationProperty = map[string]any{"type": "string", "description": "One-shot token returned with the exact resolved plan."}
)

func (s *Server) installTools() {
	definitions := []toolDefinition{
		{Name: "runtimes", Description: "List Kranz runtime sessions visible to this user and flag the runtime this MCP connection is bound to.", InputSchema: objectSchema(map[string]any{}), handler: s.runtimesTool},
		{Name: "status", Description: "Return live status for selected services, including the structured cause of a state whose reason is not the state itself.", InputSchema: objectSchema(map[string]any{"selectors": selectorsProperty}), handler: s.statusTool},
		{Name: "runs", Description: "Return the bounded service and action run catalog with provenance and output retention state.", InputSchema: objectSchema(map[string]any{}), handler: s.runsTool},
		{Name: "changes", Description: "Return what changed in the runtime after a cursor: service and action transitions, detected-port changes, and configuration reloads.", InputSchema: objectSchema(map[string]any{
			"since":            map[string]any{"type": "integer", "minimum": 0, "description": "Cursor from a previous changes or wait result. Zero reads the whole retained journal."},
			"since_generation": map[string]any{"type": "integer", "minimum": 0, "description": "Alternative anchor: read everything after the reload that produced this configuration generation."},
			"kinds":            map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"service_state", "service_ports", "action_state", "config_reload"}}},
			"selectors":        selectorsProperty,
			"limit":            map[string]any{"type": "integer", "minimum": 0, "maximum": maxChangeEvents},
		}), handler: s.changesTool},
		{Name: "plan", Description: "Resolve a versioned start, stop, restart, or action plan without executing it.", InputSchema: objectSchema(map[string]any{"operation": map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "action"}}, "selectors": selectorsProperty, "include_dependencies": map[string]any{"type": "boolean"}, "action": map[string]any{"type": "string"}}, "operation"), handler: s.planTool},
		{Name: "graph", Description: "Return the declared dependency, prerequisite, and ownership graph with live service state folded in.", InputSchema: objectSchema(map[string]any{}), handler: s.graphTool},
		{Name: "ports", Description: "Inspect declared and detected service ports without changing their owners.", InputSchema: objectSchema(map[string]any{"selectors": selectorsProperty}), handler: s.portsTool},
		{Name: "port_inspect", Description: "Identify the current listener on explicit port numbers, including processes Kranz does not manage.", InputSchema: objectSchema(map[string]any{"ports": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535}}}, "ports"), handler: s.portInspectTool},
		{Name: "logs", Description: "Query bounded normalized service and action logs.", InputSchema: objectSchema(map[string]any{"selectors": selectorsProperty, "tail": map[string]any{"type": "integer", "minimum": 0, "maximum": maxLogEvents}, "since": map[string]any{"type": "string", "description": "RFC3339 timestamp or duration such as 5m."}, "run": map[string]any{"type": "integer"}, "runs": map[string]any{"type": "integer", "minimum": 0}, "sources": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"stdout", "stderr", "kranz"}}}, "with_actions": map[string]any{"type": "boolean"}, "cursor": map[string]any{"type": "string"}}), handler: s.logsTool},
		{Name: "wait", Description: "Wait cancellably for ready, running, stopped, healthy, or unhealthy.", InputSchema: objectSchema(map[string]any{"selectors": selectorsProperty, "condition": map[string]any{"type": "string", "enum": []string{"ready", "running", "stopped", "healthy", "unhealthy"}}, "timeout": map[string]any{"type": "string", "description": "Duration such as 60s."}}, "selectors", "condition"), handler: s.waitTool},
		{Name: "health", Description: "Return readiness and liveness probe state, including the probed target and its last error, with recorded health history.", InputSchema: objectSchema(map[string]any{"selectors": selectorsProperty, "history": map[string]any{"type": "boolean", "description": "Include the recorded probe event lines."}}), handler: s.healthTool},
		{Name: "action_list", Description: "List actions, optionally filtered by exact owner.", InputSchema: objectSchema(map[string]any{"owner": map[string]any{"type": "string"}}), handler: s.actionListTool},
		{Name: "action_info", Description: "Describe one exact OWNER/ACTION.", InputSchema: objectSchema(map[string]any{"action": map[string]any{"type": "string"}}, "action"), handler: s.actionInfoTool},
		{Name: "action_result", Description: "Return a current or retained action result by absolute or negative-relative run.", InputSchema: objectSchema(map[string]any{"action": map[string]any{"type": "string"}, "run": map[string]any{"type": "integer"}}, "action"), handler: s.actionResultTool},
		{Name: "doctor", Description: "Run project preflight checks: configuration diagnostics, dependency cycles, service directories, env files, shells, and declared ports.", InputSchema: objectSchema(map[string]any{}), handler: s.doctorTool},
		{Name: "start", Description: "Start selected services using the resolved application plan.", InputSchema: mutationSchema(true), handler: s.startTool},
		{Name: "stop", Description: "Stop selected services and affected dependents using the resolved application plan.", InputSchema: mutationSchema(false), handler: s.stopTool},
		{Name: "restart", Description: "Restart selected services and affected dependents using the resolved application plan.", InputSchema: mutationSchema(false), handler: s.restartTool},
		{Name: "action_run", Description: "Run one non-interactive action. MCP request cancellation does not cancel the action.", InputSchema: objectSchema(map[string]any{"action": map[string]any{"type": "string"}, "confirmation_token": confirmationProperty}, "action"), handler: s.actionRunTool},
		{Name: "action_cancel", Description: "Explicitly cancel a currently running non-interactive action.", InputSchema: objectSchema(map[string]any{"action": map[string]any{"type": "string"}}, "action"), handler: s.actionCancelTool},
		{Name: "reload", Description: "Re-read the configuration from disk and reconcile it into the running services. Use it after editing a Kranz configuration file.", InputSchema: objectSchema(map[string]any{"force": map[string]any{"type": "boolean", "description": "Reload even when no watched file changed."}}), handler: s.reloadTool},
		{Name: "logs_clear", Description: "Clear explicitly selected bounded service/action log buffers.", InputSchema: objectSchema(map[string]any{"selectors": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}}, "with_actions": map[string]any{"type": "boolean"}}, "selectors"), handler: s.logsClearTool},
	}
	s.tools, s.toolOrder = make(map[string]toolDefinition, len(definitions)), append([]string(nil), toolNames...)
	for _, definition := range definitions {
		definition.OutputSchema = envelopeSchema()
		s.tools[definition.Name] = definition
	}
	// The allow-list and the implemented set are two statements of the same
	// fact, and a mismatch is a programming error rather than a runtime one:
	// an unlisted tool would be unreachable and a listed one without a handler
	// would publish an empty definition.
	if len(s.tools) != len(toolNames) {
		panic(fmt.Sprintf("MCP tool allow-list lists %d tools, %d are implemented", len(toolNames), len(s.tools)))
	}
	for _, name := range toolNames {
		if _, ok := s.tools[name]; !ok {
			panic("MCP tool allow-list names an unimplemented tool: " + name)
		}
	}
}

func (s *Server) runtimesTool(ctx context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	entries, err := s.runtimeEntries(ctx)
	if err != nil {
		return s.errorEnvelope(err)
	}
	return s.envelope(map[string]any{"runtimes": entries})
}

func (s *Server) runsTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	return s.envelope(map[string]any{"runs": s.api.Runs(), "retention": s.api.RunRetention()})
}

func mutationSchema(includeDependencies bool) map[string]any {
	properties := map[string]any{
		"selectors": map[string]any{
			"type":        "array",
			"minItems":    1,
			"items":       map[string]any{"type": "string"},
			"description": "One or more explicit service names or case-insensitive tags.",
		},
		"confirmation_token": confirmationProperty,
	}
	if includeDependencies {
		properties["include_dependencies"] = map[string]any{"type": "boolean", "description": "Defaults to true; false starts only the resolved targets through the versioned plan."}
	}
	return objectSchema(properties, "selectors")
}

func decodeArgs(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid_arguments: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid_arguments: multiple JSON values")
	}
	return nil
}

func (s *Server) argError(err error) ResultEnvelope {
	envelope := s.errorEnvelope(err)
	envelope.Error.Code = "invalid_arguments"
	return envelope
}

func (s *Server) resolveAction(address string) (config.ActionID, config.Action, error) {
	for _, id := range s.api.Config().ActionIDs() {
		if id.Owner+"/"+id.Name == address {
			action, _ := s.api.Config().ResolveAction(id)
			return id, action, nil
		}
	}
	return config.ActionID{}, config.Action{}, fmt.Errorf("%w: %s", app.ErrActionNotFound, address)
}

func (s *Server) selectedServices(selectors []string) ([]*app.ServiceSnapshot, error) {
	if len(selectors) == 0 {
		return s.api.Services(), nil
	}
	names, err := app.ResolveServiceSelectors(s.api.Config(), selectors)
	if err != nil {
		return nil, err
	}
	services := make([]*app.ServiceSnapshot, 0, len(names))
	for _, name := range names {
		service, ok := s.api.Service(name)
		if !ok || service == nil {
			// The configuration knows this name, and the runtime does not
			// answer for it: a reload race, or an attached runtime that went
			// away mid-call. Say so causally rather than dereferencing nil.
			return nil, &app.LogQueryError{Code: "service_unavailable", Selector: name,
				Message: fmt.Sprintf("service %q is configured but the runtime did not return its state", name),
				Hint:    "Read kranz://session and retry; the runtime may have reloaded or stopped."}
		}
		services = append(services, service)
	}
	return services, nil
}

func (s *Server) statusTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Selectors []string `json:"selectors"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	services, err := s.selectedServices(args.Selectors)
	if err != nil {
		return s.errorEnvelope(err)
	}
	result := make([]serviceResourceEntry, 0, len(services))
	for _, service := range services {
		entry, err := serviceEntry(service)
		if err != nil {
			return s.errorEnvelope(err)
		}
		result = append(result, entry)
	}
	return s.envelope(result)
}

type planArgs struct {
	Operation           string   `json:"operation"`
	Selectors           []string `json:"selectors"`
	IncludeDependencies *bool    `json:"include_dependencies"`
	Action              string   `json:"action"`
}

func (s *Server) planRequest(args planArgs) (app.PlanRequest, error) {
	request := app.PlanRequest{Operation: args.Operation, Selectors: args.Selectors}
	if args.Operation == "start" {
		request.IncludeDependencies = true
		if args.IncludeDependencies != nil {
			request.IncludeDependencies = *args.IncludeDependencies
		}
	}
	if args.Operation == "action" {
		id, _, err := s.resolveAction(args.Action)
		if err != nil {
			return request, err
		}
		request.Action = id
	}
	return request, nil
}

func (s *Server) planTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args planArgs
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	request, err := s.planRequest(args)
	if err != nil {
		return s.argError(err)
	}
	plan, err := s.api.Plan(request)
	if err != nil {
		return s.errorEnvelope(err)
	}
	return s.envelope(plan)
}

func (s *Server) portsTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Selectors []string `json:"selectors"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	services, err := s.selectedServices(args.Selectors)
	if err != nil {
		return s.errorEnvelope(err)
	}
	ports, seen := []int{}, map[int]bool{}
	for _, service := range services {
		for _, port := range append(append([]int(nil), service.Config.Ports...), service.DetectedPorts...) {
			if !seen[port] {
				seen[port], ports = true, append(ports, port)
			}
		}
	}
	owners, err := s.api.InspectPorts(ports)
	if err != nil {
		return s.errorEnvelope(err)
	}
	entries := make([]serviceResourceEntry, 0, len(services))
	for _, service := range services {
		entry, entryErr := serviceEntry(service)
		if entryErr != nil {
			return s.errorEnvelope(entryErr)
		}
		entries = append(entries, entry)
	}
	return s.envelope(map[string]any{"services": entries, "owners": owners})
}

func (s *Server) logsTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Selectors   []string `json:"selectors"`
		Tail        int      `json:"tail"`
		Since       string   `json:"since"`
		Run         int      `json:"run"`
		Runs        int      `json:"runs"`
		Sources     []string `json:"sources"`
		WithActions bool     `json:"with_actions"`
		Cursor      string   `json:"cursor"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	if args.Tail < 0 || args.Tail > maxLogEvents {
		return s.argError(fmt.Errorf("tail must be between 0 and %d", maxLogEvents))
	}
	query := app.LogQuery{Selectors: args.Selectors, Tail: args.Tail, Run: args.Run, Runs: args.Runs, Sources: args.Sources, WithActions: args.WithActions, Cursor: args.Cursor}
	if query.Tail == 0 && args.Since == "" && args.Run == 0 && args.Runs == 0 {
		query.DefaultTail = 200
	}
	if args.Since != "" {
		if duration, err := time.ParseDuration(args.Since); err == nil {
			query.Since = time.Now().Add(-duration)
		} else if timestamp, parseErr := time.Parse(time.RFC3339, args.Since); parseErr == nil {
			query.Since = timestamp
		} else {
			return s.argError(fmt.Errorf("since must be RFC3339 or a Go duration: %w", parseErr))
		}
	}
	result, err := s.api.QueryLogs(query)
	if err != nil {
		return s.errorEnvelope(err)
	}
	return s.envelope(result)
}

func (s *Server) waitTool(ctx context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Selectors []string `json:"selectors"`
		Condition string   `json:"condition"`
		Timeout   string   `json:"timeout"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	request := app.WaitRequest{Selectors: args.Selectors, Condition: args.Condition}
	if args.Timeout != "" {
		duration, err := time.ParseDuration(args.Timeout)
		if err != nil || duration <= 0 {
			return s.argError(errors.New("timeout must be a positive duration such as 60s"))
		}
		// The runtime enforces the timeout, so the answer can say what it was
		// still waiting for. The request deadline is only a backstop for a
		// transport that stopped answering at all.
		request.Timeout = duration
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, duration+waitTransportGrace)
		defer cancel()
	}
	result, err := s.api.Wait(ctx, request)
	if err != nil {
		return s.errorEnvelope(err)
	}
	entries := make([]serviceResourceEntry, 0, len(result.Services))
	for _, service := range result.Services {
		entry, entryErr := serviceEntry(service)
		if entryErr != nil {
			return s.errorEnvelope(entryErr)
		}
		entries = append(entries, entry)
	}
	return s.envelope(map[string]any{"condition": result.Condition, "generation": result.Generation, "services": entries, "cursor": result.Cursor})
}

func (s *Server) actionListTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Owner string `json:"owner"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	return s.envelope(s.actionEntries(args.Owner))
}

func (s *Server) actionInfoTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Action string `json:"action"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	id, _, err := s.resolveAction(args.Action)
	if err != nil {
		return s.errorEnvelope(err)
	}
	for _, entry := range s.actionEntries(id.Owner) {
		if entry.ID == args.Action {
			return s.envelope(entry)
		}
	}
	return s.errorEnvelope(fmt.Errorf("%w: %s", app.ErrActionNotFound, args.Action))
}

func (s *Server) actionResultTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Action string `json:"action"`
		Run    int    `json:"run"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	id, _, err := s.resolveAction(args.Action)
	if err != nil {
		return s.errorEnvelope(err)
	}
	result, err := s.api.ActionResult(id, args.Run)
	if err != nil {
		return s.errorEnvelope(err)
	}
	return s.envelope(result)
}

type mutationArgs struct {
	Selectors           []string `json:"selectors"`
	IncludeDependencies *bool    `json:"include_dependencies"`
	ConfirmationToken   string   `json:"confirmation_token"`
}

func (s *Server) lifecycleTool(ctx context.Context, raw json.RawMessage, operation string) ResultEnvelope {
	var args mutationArgs
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	if len(args.Selectors) == 0 {
		return s.argError(errors.New("selectors must contain at least one explicit service or tag"))
	}
	requestArgs := planArgs{Operation: operation, Selectors: args.Selectors, IncludeDependencies: args.IncludeDependencies}
	request, err := s.planRequest(requestArgs)
	if err != nil {
		return s.argError(err)
	}
	result, err := s.api.ExecutePlan(ctx, request, args.ConfirmationToken)
	if err != nil {
		envelope := s.errorEnvelope(err)
		envelope.Data = result
		if result.ActionResult != nil {
			switch result.ActionResult.Status {
			case app.ActionTimedOut:
				envelope.Error.Code = "action_timed_out"
			case app.ActionCancelled:
				envelope.Error.Code = "action_cancelled"
			case app.ActionFailed:
				envelope.Error.Code = "action_failed"
			}
		}
		return envelope
	}
	return s.envelope(result)
}

func (s *Server) startTool(ctx context.Context, raw json.RawMessage) ResultEnvelope {
	return s.lifecycleTool(ctx, raw, "start")
}
func (s *Server) stopTool(ctx context.Context, raw json.RawMessage) ResultEnvelope {
	return s.lifecycleTool(ctx, raw, "stop")
}
func (s *Server) restartTool(ctx context.Context, raw json.RawMessage) ResultEnvelope {
	return s.lifecycleTool(ctx, raw, "restart")
}

func (s *Server) actionRunTool(ctx context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Action            string `json:"action"`
		ConfirmationToken string `json:"confirmation_token"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	id, definition, err := s.resolveAction(args.Action)
	if err != nil {
		return s.errorEnvelope(err)
	}
	if definition.InteractiveEnabled() {
		return s.errorEnvelope(fmt.Errorf("%w: %s", app.ErrInteractiveAction, args.Action))
	}
	type execution struct {
		result app.OperationResult
		err    error
	}
	done := make(chan execution, 1)
	go func() {
		result, err := s.api.ExecutePlan(context.WithoutCancel(ctx), app.PlanRequest{Operation: "action", Action: id}, args.ConfirmationToken)
		done <- execution{result: result, err: err}
	}()
	var result app.OperationResult
	select {
	case completed := <-done:
		result, err = completed.result, completed.err
	case <-ctx.Done():
		envelope := s.errorEnvelope(ctx.Err())
		envelope.Error.Hint = "The action continues. Use action_result to observe it or action_cancel to cancel it explicitly."
		envelope.Data = map[string]any{"action": args.Action, "continues": true}
		return envelope
	}
	if err != nil {
		envelope := s.errorEnvelope(err)
		envelope.Data = result
		if result.ActionResult != nil {
			switch result.ActionResult.Status {
			case app.ActionTimedOut:
				envelope.Error.Code = "action_timed_out"
			case app.ActionCancelled:
				envelope.Error.Code = "action_cancelled"
			case app.ActionFailed:
				envelope.Error.Code = "action_failed"
			}
		}
		return envelope
	}
	return s.envelope(result)
}

func (s *Server) actionCancelTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Action string `json:"action"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	id, definition, err := s.resolveAction(args.Action)
	if err != nil {
		return s.errorEnvelope(err)
	}
	if definition.InteractiveEnabled() {
		return s.errorEnvelope(fmt.Errorf("%w: %s", app.ErrInteractiveAction, args.Action))
	}
	return s.envelope(map[string]any{"action": args.Action, "cancelled": s.api.CancelAction(id)})
}

func (s *Server) logsClearTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Selectors   []string `json:"selectors"`
		WithActions bool     `json:"with_actions"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	if len(args.Selectors) == 0 {
		return s.argError(errors.New("logs_clear requires at least one explicit selector"))
	}
	cleared, err := s.api.ClearLogStreams(args.Selectors, args.WithActions)
	if err != nil {
		return s.errorEnvelope(err)
	}
	slices.Sort(cleared)
	return s.envelope(map[string]any{"cleared": cleared})
}

const maxChangeEvents = 500

func (s *Server) changesTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Since           uint64   `json:"since"`
		SinceGeneration uint64   `json:"since_generation"`
		Kinds           []string `json:"kinds"`
		Selectors       []string `json:"selectors"`
		Limit           int      `json:"limit"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	result, err := s.api.Changes(app.ChangeQuery{Since: args.Since, SinceGeneration: args.SinceGeneration, Kinds: args.Kinds, Selectors: args.Selectors, Limit: args.Limit})
	if err != nil {
		return s.errorEnvelope(err)
	}
	return s.envelope(result)
}

func (s *Server) graphTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	return s.envelope(s.api.Graph())
}

func (s *Server) portInspectTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Ports []int `json:"ports"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	if len(args.Ports) == 0 {
		return s.argError(errors.New("port_inspect requires at least one port"))
	}
	for _, port := range args.Ports {
		if port < 1 || port > 65535 {
			return s.argError(fmt.Errorf("port %d is outside 1-65535", port))
		}
	}
	owners, err := s.api.InspectPorts(args.Ports)
	if err != nil {
		return s.errorEnvelope(err)
	}
	// A listener Kranz manages is a different fact from a foreign one, and the
	// agent asking "who took my port" needs exactly that distinction.
	entries := make([]map[string]any, 0, len(args.Ports))
	for _, port := range args.Ports {
		entry := map[string]any{"port": port, "in_use": false}
		if info, ok := owners[port]; ok && info != nil {
			owner := s.api.ManagedServiceForPID(info.PID)
			entry["in_use"], entry["listener"], entry["service"], entry["external"] = true, info, owner, owner == ""
		}
		entries = append(entries, entry)
	}
	return s.envelope(map[string]any{"ports": entries})
}

func (s *Server) healthTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Selectors []string `json:"selectors"`
		History   bool     `json:"history"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	services, err := s.selectedServices(args.Selectors)
	if err != nil {
		return s.errorEnvelope(err)
	}
	entries := make([]map[string]any, 0, len(services))
	for _, service := range services {
		entry := map[string]any{"service": service.Name, "state": service.State.Status, "health": service.Health}
		if service.Config.HealthCheck == nil {
			entry["configured"] = false
		} else {
			entry["configured"] = true
		}
		if args.History {
			entry["history"] = s.api.HealthHistory(service.Name)
		}
		entries = append(entries, entry)
	}
	return s.envelope(entries)
}

func (s *Server) reloadTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct {
		Force bool `json:"force"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	result, err := s.api.Reload(args.Force)
	if err != nil {
		return s.errorEnvelope(err)
	}
	project := s.api.Project()
	return s.envelope(map[string]any{"generation": project.Generation, "loaded_at": project.LoadedAt, "added": result.Added, "removed": result.Removed, "updated": result.Updated, "restarted": result.Restarted})
}

func (s *Server) doctorTool(_ context.Context, raw json.RawMessage) ResultEnvelope {
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return s.argError(err)
	}
	return s.envelope(s.api.Preflight())
}
