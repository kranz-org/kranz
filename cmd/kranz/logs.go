package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	kranzcli "github.com/kranz-org/kranz/internal/cli"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

const defaultLogTail = 50

type logOptions struct {
	selectors   []string
	tail        int
	tailSet     bool
	all         bool
	follow      bool
	since       time.Duration
	sinceSet    bool
	withActions bool
	run         int
	runSet      bool
	runs        int
	runsSet     bool
	noTimes     bool
	noLabels    bool
	sources     []string
}

func runLogs(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	parsed, err := parseLogOptions(args)
	if err != nil {
		return err
	}
	client, closeClient, err := dialProjectRuntime(options)
	if err != nil {
		return err
	}
	defer closeClient()
	query := app.LogQuery{Selectors: parsed.selectors, Tail: parsed.tail, Run: parsed.run, Runs: parsed.runs, Sources: parsed.sources, WithActions: parsed.withActions}
	if parsed.sinceSet {
		query.Since = time.Now().Add(-parsed.since)
	}
	if !parsed.tailSet && !parsed.all {
		query.DefaultTail = defaultLogTail
	}
	result, err := client.QueryLogs(query)
	if err != nil {
		return classifyLogQueryError(err)
	}
	if err := writeLogRetentionNotices(stdout, options.Output, result.Retention, parsed); err != nil {
		return err
	}
	if err := writeLogEvents(stdout, options.Output, result.Events, parsed); err != nil {
		return err
	}
	if !parsed.follow {
		return nil
	}
	query.Cursor, query.Tail, query.DefaultTail = result.NextCursor, 0, 0
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupts)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-interrupts:
			return nil
		case <-client.Done():
			return nil
		case <-ticker.C:
			fresh, queryErr := client.QueryLogs(query)
			if queryErr != nil {
				return classifyLogQueryError(queryErr)
			}
			query.Cursor = fresh.NextCursor
			if err := writeLogEvents(stdout, options.Output, fresh.Events, parsed); err != nil {
				return err
			}
		}
	}
}

func writeLogRetentionNotices(stdout io.Writer, format kranzcli.OutputFormat, notices []app.RunOutputNotice, options logOptions) error {
	if format == kranzcli.OutputJSON {
		return nil
	}
	for _, notice := range notices {
		marker := fmt.Sprintf("[Kranz] Output truncated · %s#%d · missing %d lines / %d bytes · retained %d lines / %d bytes", notice.Stream, notice.Run, notice.Output.MissingLines, notice.Output.MissingBytes, notice.Output.RetainedLines, notice.Output.RetainedBytes)
		if _, err := fmt.Fprintln(stdout, logEventPrefix(app.LogEvent{Timestamp: time.Now(), Stream: notice.Stream, Run: notice.Run, Source: "kranz"}, options, true)+marker); err != nil {
			return err
		}
	}
	return nil
}

func runLogsClear(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	selectors := make([]string, 0, len(args))
	force, withActions := false, false
	for _, arg := range args {
		switch {
		case arg == "--force":
			force = true
		case arg == "--with-actions":
			withActions = true
		case strings.HasPrefix(arg, "-"):
			return &kranzcli.Error{Code: "unknown_option", Message: fmt.Sprintf("unknown logs clear option %q", arg), Hint: "logs clear accepts --with-actions and --force.", ExitCode: kranzcli.ExitUsage}
		default:
			selectors = append(selectors, arg)
		}
	}
	if len(selectors) == 0 && !force {
		return &kranzcli.Error{Code: "confirmation_required", Message: "clearing every log buffer needs --force", Hint: "Name what to clear, or repeat with --force.", ExitCode: kranzcli.ExitUsage}
	}
	client, closeClient, err := dialProjectRuntime(options)
	if err != nil {
		return err
	}
	defer closeClient()
	cleared, err := client.ClearLogStreams(selectors, withActions || len(selectors) == 0)
	if err != nil {
		return classifyLogQueryError(err)
	}
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, struct {
			Cleared []string `json:"cleared"`
		}{cleared})
	}
	_, _ = fmt.Fprintf(stdout, "Cleared %s.\n", pluralizeStreams(cleared))
	return nil
}

func pluralizeStreams(cleared []string) string {
	if len(cleared) == 1 {
		return "1 log stream: " + cleared[0]
	}
	return fmt.Sprintf("%d log streams: %s", len(cleared), strings.Join(cleared, ", "))
}

func dialProjectRuntime(options kranzcli.GlobalOptions) (*kranzruntime.Client, func(), error) {
	record, err := resolveSession(options)
	if err != nil {
		return nil, nil, err
	}
	client, err := kranzruntime.DialContext(context.Background(), record.Socket, version)
	if err != nil {
		return nil, nil, classifyRuntimeError(err)
	}
	return client, func() { _ = client.Close() }, nil
}

func classifyLogQueryError(err error) error {
	var queryErr *app.LogQueryError
	if errors.As(err, &queryErr) {
		exit := kranzcli.ExitUsage
		// run_not_retained is an address that resolved to nothing, the same
		// class of failure as naming a service that does not exist.
		if queryErr.Code == "selector_not_found" || queryErr.Code == "action_not_found" ||
			queryErr.Code == "group_has_no_stream" || queryErr.Code == "run_not_retained" {
			exit = kranzcli.ExitNotFound
		}
		return &kranzcli.Error{Code: queryErr.Code, Message: queryErr.Message, Hint: queryErr.Hint, ExitCode: exit}
	}
	return err
}

func parseLogOptions(args []string) (logOptions, error) {
	var options logOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--follow":
			options.follow = true
		case arg == "--all":
			options.all = true
		case arg == "--with-actions":
			options.withActions = true
		case arg == "--no-timestamps":
			options.noTimes = true
		case arg == "--no-labels":
			options.noLabels = true
		case arg == "--plain":
			options.noTimes, options.noLabels = true, true
		case arg == "--source" || strings.HasPrefix(arg, "--source="):
			value, consumed, valueErr := logOptionValue(args, index, "--source")
			if valueErr != nil {
				return logOptions{}, valueErr
			}
			index += consumed
			selected, parseErr := parseLogSources(value)
			if parseErr != nil {
				return logOptions{}, parseErr
			}
			options.sources = append(options.sources, selected...)
		case arg == "--tail" || strings.HasPrefix(arg, "--tail="):
			value, consumed, valueErr := logOptionValue(args, index, "--tail")
			if valueErr != nil {
				return logOptions{}, valueErr
			}
			index += consumed
			count, convErr := strconv.Atoi(value)
			if convErr != nil || count < 0 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_tail", Message: "--tail requires a non-negative integer", ExitCode: kranzcli.ExitUsage}
			}
			options.tail, options.tailSet = count, true
		case arg == "--since" || strings.HasPrefix(arg, "--since="):
			value, consumed, valueErr := logOptionValue(args, index, "--since")
			if valueErr != nil {
				return logOptions{}, valueErr
			}
			index += consumed
			duration, convErr := time.ParseDuration(value)
			if convErr != nil || duration < 0 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_since", Message: "--since requires a non-negative duration such as 5m", ExitCode: kranzcli.ExitUsage}
			}
			options.since, options.sinceSet = duration, true
		case arg == "--run" || strings.HasPrefix(arg, "--run="):
			value, consumed, valueErr := logOptionValue(args, index, "--run")
			if valueErr != nil {
				return logOptions{}, valueErr
			}
			index += consumed
			number, convErr := strconv.Atoi(value)
			if convErr != nil || number == 0 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_run", Message: "--run requires a run number, or a negative offset such as -1 for the latest", ExitCode: kranzcli.ExitUsage}
			}
			options.run, options.runSet = number, true
		case arg == "--runs" || strings.HasPrefix(arg, "--runs="):
			value, consumed, valueErr := logOptionValue(args, index, "--runs")
			if valueErr != nil {
				return logOptions{}, valueErr
			}
			index += consumed
			count, convErr := strconv.Atoi(value)
			if convErr != nil || count < 1 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_runs", Message: "--runs requires a positive count", ExitCode: kranzcli.ExitUsage}
			}
			options.runs, options.runsSet = count, true
		case strings.HasPrefix(arg, "-"):
			return logOptions{}, &kranzcli.Error{Code: "unknown_option", Message: fmt.Sprintf("unknown logs option %q", arg), Hint: "logs accepts --tail N, --since D, --run N, --runs N, --source S, --all, --with-actions, --plain, --no-timestamps, --no-labels, and --follow.", ExitCode: kranzcli.ExitUsage}
		default:
			options.selectors = append(options.selectors, arg)
		}
	}
	if options.all && options.tailSet {
		return logOptions{}, &kranzcli.Error{Code: "invalid_arguments", Message: "--all and --tail contradict each other", ExitCode: kranzcli.ExitUsage}
	}
	if options.runSet && options.runsSet {
		return logOptions{}, &kranzcli.Error{Code: "invalid_arguments", Message: "--run and --runs contradict each other", ExitCode: kranzcli.ExitUsage}
	}
	return options, nil
}

func logOptionValue(args []string, index int, name string) (string, int, error) {
	if strings.HasPrefix(args[index], name+"=") {
		return strings.TrimPrefix(args[index], name+"="), 0, nil
	}
	if index+1 >= len(args) {
		return "", 0, &kranzcli.Error{Code: "missing_option_value", Message: fmt.Sprintf("%s requires a value", name), ExitCode: kranzcli.ExitUsage}
	}
	return args[index+1], 1, nil
}

func parseLogSources(value string) ([]string, error) {
	known := map[string]bool{"stdout": true, "stderr": true, "kranz": true}
	var selected []string
	for _, part := range strings.Split(value, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if !known[part] {
			return nil, &kranzcli.Error{Code: "invalid_source", Message: fmt.Sprintf("unknown log source %q", part), Hint: "Sources are stdout, stderr, kranz.", ExitCode: kranzcli.ExitUsage}
		}
		selected = append(selected, part)
	}
	if len(selected) == 0 {
		return nil, &kranzcli.Error{Code: "invalid_source", Message: "--source requires a source name", Hint: "Sources are stdout, stderr, kranz.", ExitCode: kranzcli.ExitUsage}
	}
	return selected, nil
}

const logTimestampLayout = "2006-01-02T15:04:05.000Z07:00"

// logEventLabel always publishes the stable absolute run identity. Relative
// selectors remain query syntax only; output never invents a second identity.
func logEventLabel(event app.LogEvent, _ bool) string {
	if event.Run > 0 {
		return fmt.Sprintf("%s#%d %s", event.Stream, event.Run, event.Source)
	}
	return event.Stream + " " + event.Source
}

// streamsSpanningRuns reports which streams show output from more than one run.
func streamsSpanningRuns(events []app.LogEvent) map[string]bool {
	runs := map[string]map[uint32]bool{}
	for _, event := range events {
		if runs[event.Stream] == nil {
			runs[event.Stream] = map[uint32]bool{}
		}
		runs[event.Stream][event.Run] = true
	}
	spanning := make(map[string]bool, len(runs))
	for stream, seen := range runs {
		spanning[stream] = len(seen) > 1
	}
	return spanning
}

func logEventPrefix(event app.LogEvent, options logOptions, showRun bool) string {
	var prefix strings.Builder
	if !options.noTimes {
		prefix.WriteString(event.Timestamp.Local().Format(logTimestampLayout))
		prefix.WriteByte(' ')
	}
	if !options.noLabels {
		prefix.WriteString("[" + logEventLabel(event, showRun) + "] ")
	}
	return prefix.String()
}

func writeLogEvents(stdout io.Writer, format kranzcli.OutputFormat, events []app.LogEvent, options logOptions) error {
	if format == kranzcli.OutputJSON {
		if options.follow {
			for _, event := range events {
				if err := kranzcli.WriteJSON(stdout, event); err != nil {
					return err
				}
			}
			return nil
		}
		return kranzcli.WriteJSON(stdout, events)
	}
	spanning := streamsSpanningRuns(events)
	for _, event := range events {
		if _, err := fmt.Fprintln(stdout, logEventPrefix(event, options, spanning[event.Stream])+event.Raw); err != nil {
			return err
		}
	}
	return nil
}
