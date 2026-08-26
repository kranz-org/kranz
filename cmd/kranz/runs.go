package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

func runRuns(options kranzcli.GlobalOptions, targets []string, stdout io.Writer) error {
	client, closeClient, err := dialProjectRuntime(options)
	if err != nil {
		return err
	}
	defer closeClient()
	runs := filterRuns(client.Runs(), targets)
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, runs)
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
