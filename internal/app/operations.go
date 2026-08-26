package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

const (
	OperationSchemaVersion = 1
	maxConfirmationTokens  = 256
)

type PlanRequest struct {
	Operation           string          `json:"operation"`
	Selectors           []string        `json:"selectors,omitempty"`
	IncludeDependencies bool            `json:"include_dependencies,omitempty"`
	Action              config.ActionID `json:"action,omitempty"`
}

type OperationWave struct {
	Wave     int      `json:"wave"`
	Services []string `json:"services"`
}

type OperationPlan struct {
	SchemaVersion        int             `json:"schema_version"`
	SessionID            string          `json:"session_id"`
	Generation           uint64          `json:"generation"`
	Operation            string          `json:"operation"`
	Selectors            []string        `json:"selectors,omitempty"`
	Targets              []string        `json:"targets"`
	Waves                []OperationWave `json:"waves,omitempty"`
	Action               string          `json:"action,omitempty"`
	IncludeDependencies  bool            `json:"include_dependencies,omitempty"`
	RequiresConfirmation bool            `json:"requires_confirmation"`
	Fingerprint          string          `json:"fingerprint"`
	ConfirmationToken    string          `json:"confirmation_token,omitempty"`
}

type OperationResult struct {
	Plan         OperationPlan `json:"plan"`
	ActionResult *ActionResult `json:"action_result,omitempty"`
}

// OperationExecutionError preserves the resolved result when an operation
// reached execution but failed. IPC clients can therefore return an action's
// stable run identity and terminal snapshot together with its causal error.
type OperationExecutionError struct {
	Result OperationResult
	Cause  error
}

func (e *OperationExecutionError) Error() string { return e.Cause.Error() }
func (e *OperationExecutionError) Unwrap() error { return e.Cause }

type confirmationRecord struct {
	generation  uint64
	sessionID   string
	fingerprint string
}

type ConfirmationRequiredError struct{ Plan OperationPlan }

func (e *ConfirmationRequiredError) Error() string { return "operation requires confirmation" }

type ConfirmationError struct{ Code, Message string }

func (e *ConfirmationError) Error() string { return e.Message }

// ResolveServiceSelectors is the one application-level meaning of a service
// selector: exact service name first, then a case-insensitive tag.
func ResolveServiceSelectors(cfg *config.Config, selectors []string) ([]string, error) {
	selected := map[string]bool{}
	for _, selector := range selectors {
		if _, ok := cfg.Services[selector]; ok {
			selected[selector] = true
			continue
		}
		matched := false
		for _, name := range cfg.ServiceOrder {
			if slices.ContainsFunc(cfg.Services[name].Tags, func(tag string) bool { return strings.EqualFold(tag, selector) }) {
				selected[name], matched = true, true
			}
		}
		if !matched {
			return nil, &LogQueryError{Code: "selector_not_found", Selector: selector, Message: fmt.Sprintf("service or tag %q was not found", selector), Hint: "List services and tags before retrying."}
		}
	}
	names := make([]string, 0, len(selected))
	for _, name := range cfg.ServiceOrder {
		if selected[name] {
			names = append(names, name)
		}
	}
	return names, nil
}

func (l *Local) Plan(request PlanRequest) (OperationPlan, error) {
	project := l.Project()
	plan := OperationPlan{SchemaVersion: OperationSchemaVersion, SessionID: project.SessionID, Generation: project.Generation, Operation: request.Operation, Selectors: append([]string(nil), request.Selectors...), IncludeDependencies: request.IncludeDependencies, Targets: []string{}}
	switch request.Operation {
	case "start", "stop", "restart":
		selectors := request.Selectors
		if len(selectors) == 0 {
			selectors = append([]string(nil), l.Config().ServiceOrder...)
		}
		names, err := ResolveServiceSelectors(l.Config(), selectors)
		if err != nil {
			return plan, err
		}
		if request.Operation == "start" && request.IncludeDependencies {
			closure, err := l.manager.StartDependencyClosure(names)
			if err != nil {
				return plan, err
			}
			order, err := service.TopologicalOrder(l.Config())
			if err != nil {
				return plan, err
			}
			for _, name := range order {
				if closure[name] {
					plan.Targets = append(plan.Targets, name)
				}
			}
			// A plan with no targets has no waves: DependencyLevels answers a
			// request for the levels of nothing with one empty level, and a
			// reader counting waves would take that for work to do.
			if len(plan.Targets) > 0 {
				for index, wave := range service.DependencyLevels(l.Config(), plan.Targets) {
					plan.Waves = append(plan.Waves, OperationWave{Wave: index + 1, Services: wave})
				}
			}
		} else if request.Operation == "start" {
			plan.Targets = names
		} else {
			seen := map[string]bool{}
			for _, name := range names {
				for _, affected := range l.AffectedServices(name) {
					if !seen[affected] {
						seen[affected] = true
						plan.Targets = append(plan.Targets, affected)
					}
				}
			}
		}
		if request.Operation == "start" {
			plan.RequiresConfirmation = len(l.StartConfirmationNames(names, request.IncludeDependencies)) > 0
		} else {
			plan.RequiresConfirmation = l.RequiresStopConfirmation(plan.Targets)
		}
	case "action":
		action, ok := l.Config().ResolveAction(request.Action)
		if !ok {
			return plan, fmt.Errorf("%w: %s/%s", ErrActionNotFound, request.Action.Owner, request.Action.Name)
		}
		plan.Action = request.Action.Owner + "/" + request.Action.Name
		plan.Targets = []string{plan.Action}
		plan.RequiresConfirmation = action.ConfirmationRequired()
	default:
		return plan, &ConfirmationError{Code: "invalid_operation", Message: fmt.Sprintf("unsupported operation %q", request.Operation)}
	}
	plan.Fingerprint = operationFingerprint(plan)
	if plan.RequiresConfirmation {
		plan.ConfirmationToken = l.issueConfirmation(plan)
	}
	return plan, nil
}

func (l *Local) ExecutePlan(ctx context.Context, request PlanRequest, token string) (OperationResult, error) {
	plan, err := l.Plan(request)
	if err != nil {
		return OperationResult{}, err
	}
	if token != "" {
		// Plan issued a fresh token while recomputing the exact current plan;
		// an execution presenting an older token must not leak that unused one.
		l.discardConfirmation(plan.ConfirmationToken)
		plan.ConfirmationToken = ""
		// A presented token is always validated against the plan that would
		// run, including when that plan no longer requires confirming: the
		// caller confirmed a specific resolved plan, and a plan that changed
		// under it is exactly what confirmation exists to catch.
		if err := l.consumeConfirmation(token, plan); err != nil {
			return OperationResult{Plan: plan}, err
		}
	} else if plan.RequiresConfirmation {
		return OperationResult{Plan: plan}, &ConfirmationRequiredError{Plan: plan}
	}
	result := OperationResult{Plan: plan}
	switch request.Operation {
	case "start":
		if request.IncludeDependencies {
			err = l.StartServicesContext(ctx, plan.Targets)
		} else {
			err = l.ForceStartServicesContext(ctx, plan.Targets)
		}
	case "stop":
		err = l.StopServices(plan.Targets)
	case "restart":
		err = l.RestartServicesContext(ctx, plan.Targets)
	case "action":
		var actionResult ActionResult
		// A delivery request is only waiting for an action result; it does not
		// own the action lifetime. Cancellation is the separate CancelAction
		// application operation, so an MCP/IPC disconnect cannot kill a job.
		actionResult, err = l.RunAction(context.WithoutCancel(ctx), request.Action)
		result.ActionResult = &actionResult
	}
	return result, err
}

func (l *Local) discardConfirmation(token string) {
	if token == "" {
		return
	}
	l.confirmMu.Lock()
	delete(l.confirmations, token)
	l.confirmMu.Unlock()
}

func operationFingerprint(plan OperationPlan) string {
	copy := plan
	copy.Fingerprint, copy.ConfirmationToken = "", ""
	payload, _ := json.Marshal(copy)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (l *Local) issueConfirmation(plan OperationPlan) string {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		panic(fmt.Sprintf("generate confirmation token: %v", err))
	}
	token := hex.EncodeToString(bytes)
	l.confirmMu.Lock()
	if len(l.confirmations) >= maxConfirmationTokens {
		for oldest := range l.confirmations {
			delete(l.confirmations, oldest)
			break
		}
	}
	l.confirmations[token] = confirmationRecord{generation: plan.Generation, sessionID: plan.SessionID, fingerprint: plan.Fingerprint}
	l.confirmMu.Unlock()
	return token
}

func (l *Local) consumeConfirmation(token string, plan OperationPlan) error {
	l.confirmMu.Lock()
	record, ok := l.confirmations[token]
	delete(l.confirmations, token)
	l.confirmMu.Unlock()
	if !ok {
		return &ConfirmationError{Code: "confirmation_expired", Message: "confirmation token is unknown, expired, or already used"}
	}
	if record.sessionID != plan.SessionID || record.generation != plan.Generation {
		return &ConfirmationError{Code: "confirmation_expired", Message: "confirmation token belongs to another session or config generation"}
	}
	if record.fingerprint != plan.Fingerprint {
		return &ConfirmationError{Code: "confirmation_plan_changed", Message: "resolved operation plan changed after confirmation"}
	}
	return nil
}

func (l *Local) invalidateConfirmations() {
	l.confirmMu.Lock()
	l.confirmations = map[string]confirmationRecord{}
	l.confirmMu.Unlock()
}

type WaitRequest struct {
	Selectors []string `json:"selectors"`
	Condition string   `json:"condition"`
	// Timeout bounds the wait inside the runtime. A delivery adapter that
	// instead relied only on its own request deadline reported a timeout as a
	// cancellation, because the transport gave up before the runtime could say
	// what it had still been waiting for.
	Timeout time.Duration `json:"timeout,omitempty"`
}

type WaitResult struct {
	Condition  string             `json:"condition"`
	Generation uint64             `json:"generation"`
	Services   []*ServiceSnapshot `json:"services"`
	// Cursor is the journal sequence at which the wait finished. A caller that
	// passes the cursor it held before the wait to Changes reads exactly what
	// happened while it was waiting, rather than only where things ended up.
	Cursor uint64 `json:"cursor"`
}

type WaitError struct {
	Code, Message string
	Services      []*ServiceSnapshot
}

func (e *WaitError) Error() string { return e.Message }

func (l *Local) Wait(ctx context.Context, request WaitRequest) (WaitResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !slices.Contains([]string{"ready", "running", "stopped", "healthy", "unhealthy"}, request.Condition) {
		return WaitResult{}, &WaitError{Code: "invalid_condition", Message: fmt.Sprintf("unsupported wait condition %q", request.Condition)}
	}
	names, err := ResolveServiceSelectors(l.Config(), request.Selectors)
	if err != nil {
		return WaitResult{}, err
	}
	if len(names) == 0 {
		return WaitResult{}, &WaitError{Code: "selector_required", Message: "wait requires at least one service or tag selector"}
	}
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshots := make([]*ServiceSnapshot, 0, len(names))
		matched := true
		terminal := false
		blocked := false
		for _, name := range names {
			snapshot, ok := l.Service(name)
			if !ok {
				return WaitResult{}, &WaitError{Code: "selector_not_found", Message: fmt.Sprintf("service %q disappeared while waiting", name)}
			}
			snapshots = append(snapshots, snapshot)
			matched = matched && serviceCondition(snapshot, request.Condition)
			terminal = terminal || snapshot.State.Completed && snapshot.State.ExitCode != 0 && snapshot.State.Status == config.StatusStopped
			// One meaning of "blocked": the same derived cause status reports,
			// so a wait cannot disagree with what the snapshot says.
			blocked = blocked || snapshot.State.Cause != nil && snapshot.State.Cause.Type == "dependency_failed"
		}
		result := WaitResult{Condition: request.Condition, Generation: l.Project().Generation, Services: snapshots, Cursor: l.manager.Journal().Latest()}
		if matched {
			return result, nil
		}
		if blocked {
			return result, &WaitError{Code: "dependency_blocked", Message: "a selected service is blocked by a dependency that terminated unsuccessfully", Services: snapshots}
		}
		if terminal && request.Condition != "stopped" {
			return result, &WaitError{Code: "terminal_failure", Message: "a selected service terminated before satisfying the wait condition", Services: snapshots}
		}
		select {
		case <-ctx.Done():
			code := "wait_cancelled"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				code = "wait_timeout"
			}
			return result, &WaitError{Code: code, Message: ctx.Err().Error(), Services: snapshots}
		case <-ticker.C:
		}
	}
}

func serviceCondition(snapshot *ServiceSnapshot, condition string) bool {
	status := snapshot.State.Status
	running := status == config.StatusRunning || status == config.StatusUnhealthy
	readinessConfigured := snapshot.Config.HealthCheck != nil && snapshot.Config.HealthCheck.Readiness != nil
	livenessConfigured := snapshot.Config.HealthCheck != nil && snapshot.Config.HealthCheck.Liveness != nil
	readinessOK := !readinessConfigured || snapshot.Health.Observed && snapshot.Health.Ready
	livenessOK := !livenessConfigured || snapshot.Health.Observed && snapshot.Health.Alive
	switch condition {
	case "running":
		return running
	case "stopped":
		return status == config.StatusStopped
	case "ready":
		return running && readinessOK
	case "healthy":
		return running && readinessOK && livenessOK
	case "unhealthy":
		return status == config.StatusUnhealthy || snapshot.Health.Observed && (!readinessOK || !livenessOK)
	default:
		return false
	}
}
