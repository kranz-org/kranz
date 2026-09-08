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
	query, err := parseRunsListArgs(options.Output, targets)
	if err != nil {
		return err
	}
	client, closeClient, err := dialProjectRuntime(options)
	if err != nil {
		return err
	}
	defer closeClient()
	runs := filterRuns(client.Runs(), query.targets, query.statuses, query.since, time.Now())
	if query.limit > 0 && len(runs) > query.limit {
		runs = runs[len(runs)-query.limit:]
	}
	if options.Output == kranzcli.OutputJSON {
		// Preserve the established machine-readable envelope. Text output has
		// one record type per command; JSON consumers can migrate retention at
		// their own pace to `runs retention --output=json`.
		return kranzcli.WriteJSON(stdout, struct {
			Runs      []app.RunSummary           `json:"runs"`
			Retention []app.RunRetentionBoundary `json:"retention"`
		}{Runs: runs, Retention: filterRunRetention(client.RunRetention(), query.targets)})
	}
	if query.formatter != nil {
		rows := make([]map[string]any, 0, len(runs))
		for _, run := range runs {
			rows = append(rows, runFormatRow(run))
		}
		return query.formatter.write(stdout, runFormatHeaders(), rows)
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

type runsListQuery struct {
	targets   []string
	statuses  map[string]bool
	limit     int
	since     time.Duration
	formatter *rowTemplate
}

func parseRunsListArgs(output kranzcli.OutputFormat, args []string) (runsListQuery, error) {
	formatter, args, err := extractRowFormat("runs", output, args)
	if err != nil {
		return runsListQuery{}, err
	}
	query := runsListQuery{formatter: formatter, statuses: make(map[string]bool)}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		value := ""
		switch {
		case arg == "--limit" || arg == "--since" || arg == "--status":
			if index+1 >= len(args) {
				return runsListQuery{}, runsUsageError(arg + " requires a value")
			}
			index++
			value = args[index]
		case strings.HasPrefix(arg, "--limit="):
			arg, value = "--limit", strings.TrimPrefix(arg, "--limit=")
		case strings.HasPrefix(arg, "--since="):
			arg, value = "--since", strings.TrimPrefix(arg, "--since=")
		case strings.HasPrefix(arg, "--status="):
			arg, value = "--status", strings.TrimPrefix(arg, "--status=")
		case strings.HasPrefix(arg, "-"):
			return runsListQuery{}, runsUsageError(fmt.Sprintf("unknown runs option %q", arg))
		default:
			query.targets = append(query.targets, arg)
			continue
		}
		switch arg {
		case "--limit":
			limit, parseErr := strconv.Atoi(value)
			if parseErr != nil || limit <= 0 {
				return runsListQuery{}, runsUsageError("--limit must be a positive integer")
			}
			query.limit = limit
		case "--since":
			since, parseErr := time.ParseDuration(value)
			if parseErr != nil || since <= 0 {
				return runsListQuery{}, runsUsageError("--since must be a positive duration such as 30m or 2h")
			}
			query.since = since
		case "--status":
			for _, status := range strings.Split(value, ",") {
				status = strings.ToLower(strings.TrimSpace(status))
				if status == "" {
					return runsListQuery{}, runsUsageError("--status requires one or more statuses")
				}
				query.statuses[status] = true
			}
		}
	}
	return query, nil
}

func runsUsageError(message string) error {
	return &kranzcli.Error{Code: "invalid_runs_query", Message: message, Hint: "Use `kranz runs --help` to inspect filters.", ExitCode: kranzcli.ExitUsage}
}

func runRunsRetention(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	formatter, targets, err := extractRowFormat("runs retention", options.Output, args)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if strings.HasPrefix(target, "-") {
			return runsUsageError(fmt.Sprintf("unknown runs retention option %q", target))
		}
	}
	client, closeClient, err := dialProjectRuntime(options)
	if err != nil {
		return err
	}
	defer closeClient()
	boundaries := filterRunRetention(client.RunRetention(), targets)
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, boundaries)
	}
	if formatter != nil {
		rows := make([]map[string]any, 0, len(boundaries))
		for _, boundary := range boundaries {
			rows = append(rows, retentionFormatRow(boundary))
		}
		return formatter.write(stdout, retentionFormatHeaders(), rows)
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "TARGET\tOLDEST\tBUDGETS\tEVICTED")
	for _, boundary := range boundaries {
		row := retentionFormatRow(boundary)
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d runs / %d entries / %s\t%d runs\n",
			row["Target"], row["Oldest"], boundary.MaxRuns, boundary.MaxEntries, row["MaxBytes"], boundary.EvictedRuns)
	}
	return w.Flush()
}

func runFormatHeaders() map[string]any {
	return map[string]any{"Run": "RUN", "Target": "TARGET", "Number": "NUMBER", "Kind": "KIND", "Status": "STATUS", "Started": "STARTED", "StartedAt": "STARTED AT", "FinishedAt": "FINISHED AT", "Duration": "DURATION", "PID": "PID", "Exit": "EXIT", "Reason": "REASON", "Initiator": "INITIATOR", "Surface": "SURFACE", "Client": "CLIENT", "Live": "LIVE", "Output": "OUTPUT"}
}

func runFormatRow(run app.RunSummary) map[string]any {
	exit, finished := "-", "-"
	if run.ExitCode != nil {
		exit = fmt.Sprint(*run.ExitCode)
	}
	duration := time.Since(run.StartedAt)
	if !run.FinishedAt.IsZero() {
		duration, finished = run.FinishedAt.Sub(run.StartedAt), run.FinishedAt.Format(time.RFC3339)
	}
	return map[string]any{"Run": fmt.Sprintf("%s#%d", runTargetName(run.Target), run.Run), "Target": runTargetName(run.Target), "Number": run.Run, "Kind": string(run.Target.Kind), "Status": run.Status, "Started": shortDuration(time.Since(run.StartedAt)), "StartedAt": run.StartedAt.Format(time.RFC3339), "FinishedAt": finished, "Duration": duration.Round(time.Millisecond), "PID": run.PID, "Exit": exit, "Reason": run.StartReason, "Initiator": runInitiator(run), "Surface": run.Surface, "Client": run.ClientLabel, "Live": run.Live, "Output": run.Output.State}
}

func retentionFormatHeaders() map[string]any {
	return map[string]any{"Target": "TARGET", "Oldest": "OLDEST", "MaxRuns": "MAX RUNS", "MaxEntries": "MAX ENTRIES", "MaxBytes": "MAX BYTES", "MaxBytesRaw": "MAX BYTES RAW", "Evicted": "EVICTED"}
}

func retentionFormatRow(boundary app.RunRetentionBoundary) map[string]any {
	oldest := "-"
	if boundary.OldestRetainedRun > 0 {
		oldest = fmt.Sprintf("#%d", boundary.OldestRetainedRun)
	}
	return map[string]any{"Target": runTargetName(boundary.Target), "Oldest": oldest, "MaxRuns": boundary.MaxRuns, "MaxEntries": boundary.MaxEntries, "MaxBytes": formatRunBytes(boundary.MaxBytes), "MaxBytesRaw": boundary.MaxBytes, "Evicted": boundary.EvictedRuns}
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

func filterRuns(runs []app.RunSummary, targets []string, statuses map[string]bool, since time.Duration, now time.Time) []app.RunSummary {
	selected := make(map[string]bool, len(targets))
	for _, target := range targets {
		selected[strings.ToLower(target)] = true
	}
	result := make([]app.RunSummary, 0, len(runs))
	for _, run := range runs {
		if len(selected) > 0 && !selected[strings.ToLower(runTargetName(run.Target))] {
			continue
		}
		if len(statuses) > 0 && !statuses[strings.ToLower(run.Status)] {
			continue
		}
		if since > 0 && run.StartedAt.Before(now.Add(-since)) {
			continue
		}
		result = append(result, run)
	}
	return result
}
