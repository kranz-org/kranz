package service

import (
	"sort"
	"sync"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

const defaultRunCatalogSize = 100

// RunKind identifies the runtime subject that owns a numbered execution.
type RunKind string

const (
	RunKindService RunKind = "service"
	RunKindAction  RunKind = "action"
)

// RunTarget is the stable, comparable address of one run-producing subject.
// Service targets fill Name. Action targets retain the complete ActionID so a
// group action cannot collide with a service action that has the same spelling.
type RunTarget struct {
	Kind   RunKind         `json:"kind"`
	Name   string          `json:"name,omitempty"`
	Action config.ActionID `json:"action,omitempty"`
}

func ServiceRunTarget(name string) RunTarget {
	return RunTarget{Kind: RunKindService, Name: name}
}

func ActionRunTarget(id config.ActionID) RunTarget {
	return RunTarget{Kind: RunKindAction, Action: id}
}

// RunOutputState distinguishes intact output from known retention loss. An
// empty run with no captured output is complete; unavailable means output did
// exist but none of it remains.
type RunOutputState string

const (
	RunOutputComplete    RunOutputState = "complete"
	RunOutputPartial     RunOutputState = "partial"
	RunOutputUnavailable RunOutputState = "unavailable"
)

// RunOutputSummary makes retention loss explicit and exactly measurable.
type RunOutputSummary struct {
	State         RunOutputState `json:"state"`
	CapturedLines uint64         `json:"captured_lines"`
	CapturedBytes uint64         `json:"captured_bytes"`
	RetainedLines uint64         `json:"retained_lines"`
	RetainedBytes uint64         `json:"retained_bytes"`
	MissingLines  uint64         `json:"missing_lines"`
	MissingBytes  uint64         `json:"missing_bytes"`
}

// RunSummary is the bounded catalog record kept independently from log lines
// and the transition journal.
type RunSummary struct {
	Target      RunTarget          `json:"target"`
	Run         uint32             `json:"run"`
	Status      string             `json:"status"`
	StartedAt   time.Time          `json:"started_at"`
	FinishedAt  time.Time          `json:"finished_at,omitempty"`
	PID         int                `json:"pid,omitempty"`
	ExitCode    *int               `json:"exit_code,omitempty"`
	Cause       *config.StateCause `json:"cause,omitempty"`
	Surface     string             `json:"surface"`
	ClientLabel string             `json:"client_label,omitempty"`
	StartReason string             `json:"start_reason,omitempty"`
	Live        bool               `json:"live"`
	Output      RunOutputSummary   `json:"output"`
}

// RunRetentionBoundary describes the independent catalog and output budgets
// for one target and the oldest summary still addressable in this session.
type RunRetentionBoundary struct {
	Target            RunTarget `json:"target"`
	MaxRuns           int       `json:"max_runs"`
	MaxEntries        int       `json:"max_entries"`
	MaxBytes          uint64    `json:"max_bytes"`
	OldestRetainedRun uint32    `json:"oldest_retained_run,omitempty"`
	EvictedRuns       uint64    `json:"evicted_runs"`
}

// RunCatalog retains a fair, per-target bounded history. A noisy target can
// evict only its own old summaries, never another service or action's history.
type RunCatalog struct {
	mu               sync.RWMutex
	maxRunsPerTarget int
	runs             map[RunTarget][]RunSummary
	boundaries       map[RunTarget]RunRetentionBoundary
}

func NewRunCatalog(maxRunsPerTarget int) *RunCatalog {
	if maxRunsPerTarget <= 0 {
		maxRunsPerTarget = defaultRunCatalogSize
	}
	return &RunCatalog{maxRunsPerTarget: maxRunsPerTarget, runs: make(map[RunTarget][]RunSummary), boundaries: make(map[RunTarget]RunRetentionBoundary)}
}

func (c *RunCatalog) SetOutputLimits(target RunTarget, maxEntries int, maxBytes uint64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	boundary := c.boundaries[target]
	boundary.Target, boundary.MaxRuns, boundary.MaxEntries, boundary.MaxBytes = target, c.maxRunsPerTarget, maxEntries, maxBytes
	c.boundaries[target] = boundary
	c.mu.Unlock()
}

func (c *RunCatalog) Begin(summary RunSummary) {
	if c == nil || summary.Run == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	summary.Live = true
	summary.FinishedAt = time.Time{}
	summary.ExitCode = nil
	summary.Output.State = RunOutputComplete
	history := c.runs[summary.Target]
	for index := range history {
		if history[index].Run == summary.Run {
			history[index] = cloneRunSummary(summary)
			c.runs[summary.Target] = history
			return
		}
	}
	history = append(history, cloneRunSummary(summary))
	if len(history) > c.maxRunsPerTarget {
		boundary := c.boundaries[summary.Target]
		boundary.Target, boundary.MaxRuns = summary.Target, c.maxRunsPerTarget
		boundary.EvictedRuns += uint64(len(history) - c.maxRunsPerTarget)
		c.boundaries[summary.Target] = boundary
		history = append([]RunSummary(nil), history[len(history)-c.maxRunsPerTarget:]...)
	}
	c.runs[summary.Target] = history
}

func (c *RunCatalog) Boundaries() []RunRetentionBoundary {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	result := make([]RunRetentionBoundary, 0, len(c.boundaries))
	for target, boundary := range c.boundaries {
		boundary.Target = target
		boundary.MaxRuns = c.maxRunsPerTarget
		if history := c.runs[target]; len(history) > 0 {
			boundary.OldestRetainedRun = history[0].Run
		}
		result = append(result, boundary)
	}
	c.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return runTargetLess(result[i].Target, result[j].Target) })
	return result
}

func (c *RunCatalog) Update(target RunTarget, run uint32, status string, pid int, cause *config.StateCause) {
	c.update(target, run, func(summary *RunSummary) {
		if status != "" {
			summary.Status = status
		}
		if pid > 0 {
			summary.PID = pid
		}
		if cause != nil {
			summary.Cause = cloneStateCause(cause)
		}
	})
}

func (c *RunCatalog) Finish(target RunTarget, run uint32, status string, finishedAt time.Time, exitCode int, cause *config.StateCause) {
	c.update(target, run, func(summary *RunSummary) {
		summary.Status = status
		summary.Live = false
		summary.FinishedAt = finishedAt
		exit := exitCode
		summary.ExitCode = &exit
		summary.Cause = cloneStateCause(cause)
	})
}

func (c *RunCatalog) RecordOutput(target RunTarget, run uint32, bytes uint64) {
	if run == 0 {
		return
	}
	c.update(target, run, func(summary *RunSummary) {
		summary.Output.CapturedLines++
		summary.Output.CapturedBytes += bytes
		summary.Output.RetainedLines++
		summary.Output.RetainedBytes += bytes
		summary.Output.State = outputState(summary.Output)
	})
}

func (c *RunCatalog) EvictOutput(target RunTarget, run uint32, bytes uint64) {
	if run == 0 {
		return
	}
	c.update(target, run, func(summary *RunSummary) {
		if summary.Output.RetainedLines > 0 {
			summary.Output.RetainedLines--
		}
		if bytes <= summary.Output.RetainedBytes {
			summary.Output.RetainedBytes -= bytes
		} else {
			summary.Output.RetainedBytes = 0
		}
		summary.Output.MissingLines++
		summary.Output.MissingBytes += bytes
		summary.Output.State = outputState(summary.Output)
	})
}

func (c *RunCatalog) ClearOutput(target RunTarget) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	history := c.runs[target]
	for index := range history {
		history[index].Output.MissingLines += history[index].Output.RetainedLines
		history[index].Output.MissingBytes += history[index].Output.RetainedBytes
		history[index].Output.RetainedLines = 0
		history[index].Output.RetainedBytes = 0
		history[index].Output.State = outputState(history[index].Output)
	}
	c.runs[target] = history
}

func outputState(output RunOutputSummary) RunOutputState {
	if output.MissingLines == 0 && output.MissingBytes == 0 {
		return RunOutputComplete
	}
	if output.RetainedLines == 0 {
		return RunOutputUnavailable
	}
	return RunOutputPartial
}

func (c *RunCatalog) List(target RunTarget) []RunSummary {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	history := c.runs[target]
	result := make([]RunSummary, len(history))
	for index := range history {
		result[index] = cloneRunSummary(history[index])
	}
	return result
}

func (c *RunCatalog) All() []RunSummary {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]RunSummary, 0)
	for _, history := range c.runs {
		for _, summary := range history {
			result = append(result, cloneRunSummary(summary))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartedAt.Equal(result[j].StartedAt) {
			if result[i].Target != result[j].Target {
				return runTargetLess(result[i].Target, result[j].Target)
			}
			return result[i].Run < result[j].Run
		}
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	return result
}

func runTargetLess(left, right RunTarget) bool {
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	if left.Action.OwnerKind != right.Action.OwnerKind {
		return left.Action.OwnerKind < right.Action.OwnerKind
	}
	if left.Action.Owner != right.Action.Owner {
		return left.Action.Owner < right.Action.Owner
	}
	return left.Action.Name < right.Action.Name
}

func (c *RunCatalog) update(target RunTarget, run uint32, update func(*RunSummary)) {
	if c == nil || run == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	history := c.runs[target]
	for index := range history {
		if history[index].Run == run {
			update(&history[index])
			c.runs[target] = history
			return
		}
	}
}

func cloneRunSummary(summary RunSummary) RunSummary {
	summary.Cause = cloneStateCause(summary.Cause)
	return summary
}

func cloneStateCause(cause *config.StateCause) *config.StateCause {
	if cause == nil {
		return nil
	}
	copy := *cause
	return &copy
}
