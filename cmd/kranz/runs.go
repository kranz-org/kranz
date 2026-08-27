package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

func runRunsDelete(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	confirmed := false
	identities := make([]string, 0, 1)
	for _, arg := range args {
		switch {
		case arg == "--confirm":
			confirmed = true
		case strings.HasPrefix(arg, "-"):
			return &kranzcli.Error{Code: "unknown_option", Message: fmt.Sprintf("unknown runs delete option %q", arg), Hint: "runs delete accepts one TARGET#N and --confirm.", ExitCode: kranzcli.ExitUsage}
		default:
			identities = append(identities, arg)
		}
	}
	if len(identities) != 1 {
		return &kranzcli.Error{Code: "missing_run_identity", Message: "runs delete requires exactly one TARGET#N", Hint: "Use kranz runs to inspect absolute run identities.", ExitCode: kranzcli.ExitUsage}
	}
	if !confirmed {
		return &kranzcli.Error{Code: "confirmation_required", Message: "deleting a retained run requires --confirm", Hint: "Review the absolute TARGET#N, then repeat with --confirm.", ExitCode: kranzcli.ExitUsage}
	}
	targetName, run, err := parseRunIdentity(identities[0])
	if err != nil {
		return err
	}
	client, closeClient, err := dialProjectRuntime(options)
	if err != nil {
		return err
	}
	defer closeClient()
	var target *app.RunTarget
	for _, candidate := range client.Runs() {
		if candidate.Run == run && strings.EqualFold(runTargetName(candidate.Target), targetName) {
			copy := candidate.Target
			target = &copy
			break
		}
	}
	if target == nil {
		return &kranzcli.Error{Code: "run_not_found", Message: fmt.Sprintf("%s#%d is not retained", targetName, run), Hint: "Use kranz runs to inspect retained absolute run identities.", ExitCode: kranzcli.ExitNotFound}
	}
	deleted, err := client.DeleteRun(*target, run)
	if err != nil {
		var deleteErr *app.RunDeleteError
		if errors.As(err, &deleteErr) {
			exit := kranzcli.ExitConflict
			if deleteErr.Code == "run_not_found" {
				exit = kranzcli.ExitNotFound
			}
			return &kranzcli.Error{Code: deleteErr.Code, Message: deleteErr.Error(), ExitCode: exit}
		}
		return err
	}
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, struct {
			Deleted app.RunSummary `json:"deleted"`
		}{Deleted: deleted})
	}
	_, err = fmt.Fprintf(stdout, "Deleted %s#%d from retained history.\n", runTargetName(deleted.Target), deleted.Run)
	return err
}

func parseRunIdentity(value string) (string, uint32, error) {
	separator := strings.LastIndex(value, "#")
	if separator <= 0 || separator == len(value)-1 {
		return "", 0, &kranzcli.Error{Code: "invalid_run_identity", Message: fmt.Sprintf("%q is not an absolute TARGET#N run identity", value), Hint: "Use kranz runs to inspect retained absolute run identities.", ExitCode: kranzcli.ExitUsage}
	}
	number, err := strconv.ParseUint(value[separator+1:], 10, 32)
	if err != nil || number == 0 {
		return "", 0, &kranzcli.Error{Code: "invalid_run_identity", Message: fmt.Sprintf("%q is not an absolute TARGET#N run identity", value), Hint: "Run numbers are positive integers.", ExitCode: kranzcli.ExitUsage}
	}
	return value[:separator], uint32(number), nil
}

func runRuns(options kranzcli.GlobalOptions, targets []string, stdout io.Writer) error {
	client, closeClient, err := dialProjectRuntime(options)
	if err != nil {
		return err
	}
	defer closeClient()
	runs := filterRuns(client.Runs(), targets)
	retention := filterRunRetention(client.RunRetention(), targets)
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, struct {
			Runs      []app.RunSummary           `json:"runs"`
			Retention []app.RunRetentionBoundary `json:"retention"`
		}{Runs: runs, Retention: retention})
	}
	// The two tables describe different things and share no columns. One
	// tabwriter aligned them against each other, so a long budget string
	// stretched the run table's DURATION column across half the terminal.
	if len(retention) > 0 {
		rw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(rw, "TARGET\tOLDEST\tBUDGETS\tEVICTED")
		for _, boundary := range retention {
			oldest := "-"
			if boundary.OldestRetainedRun > 0 {
				oldest = fmt.Sprintf("#%d", boundary.OldestRetainedRun)
			}
			_, _ = fmt.Fprintf(rw, "%s\t%s\t%d runs / %d entries / %s\t%d runs\n",
				runTargetName(boundary.Target), oldest, boundary.MaxRuns, boundary.MaxEntries, formatRunBytes(boundary.MaxBytes), boundary.EvictedRuns)
		}
		if err := rw.Flush(); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(stdout); err != nil {
			return err
		}
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "RUN\tSTATUS\tSTARTED\tDURATION\tEXIT\tREASON\tINITIATOR\tOUTPUT")
	for _, run := range runs {
		exit := "-"
		if run.ExitCode != nil {
			exit = fmt.Sprint(*run.ExitCode)
		}
		duration := time.Since(run.StartedAt)
		if !run.FinishedAt.IsZero() {
			duration = run.FinishedAt.Sub(run.StartedAt)
		}
		_, _ = fmt.Fprintf(w, "%s#%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			runTargetName(run.Target), run.Run, run.Status, run.StartedAt.Format(time.RFC3339), duration.Round(time.Millisecond), exit,
			run.StartReason, runInitiator(run), run.Output.State)
	}
	return w.Flush()
}

func filterRunRetention(boundaries []app.RunRetentionBoundary, targets []string) []app.RunRetentionBoundary {
	if len(targets) == 0 {
		return boundaries
	}
	selected := make(map[string]bool, len(targets))
	for _, target := range targets {
		selected[strings.ToLower(target)] = true
	}
	result := make([]app.RunRetentionBoundary, 0, len(boundaries))
	for _, boundary := range boundaries {
		if selected[strings.ToLower(runTargetName(boundary.Target))] {
			result = append(result, boundary)
		}
	}
	return result
}

// formatRunBytes keeps a budget readable in the text table. JSON output keeps
// the exact byte count, which is what a machine reader needs.
func formatRunBytes(bytes uint64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.3g GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.3g MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.3g KB", float64(bytes)/(1<<10))
	}
	return fmt.Sprintf("%d B", bytes)
}

func runTargetName(target app.RunTarget) string {
	if target.Kind == app.RunKindService {
		return target.Name
	}
	return target.Action.Owner + "/" + target.Action.Name
}

func runInitiator(run app.RunSummary) string {
	if run.ClientLabel == "" {
		return run.Surface
	}
	return run.Surface + ":" + run.ClientLabel
}

func filterRuns(runs []app.RunSummary, targets []string) []app.RunSummary {
	if len(targets) == 0 {
		return runs
	}
	selected := make(map[string]bool, len(targets))
	for _, target := range targets {
		selected[strings.ToLower(target)] = true
	}
	result := make([]app.RunSummary, 0, len(runs))
	for _, run := range runs {
		if selected[strings.ToLower(runTargetName(run.Target))] {
			result = append(result, run)
		}
	}
	return result
}
