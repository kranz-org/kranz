package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
	kranzlog "github.com/kranz-org/kranz/internal/log"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

type logOptions struct {
	selectors []string
	tail      int
	tailSet   bool
	follow    bool
	since     time.Duration
	sinceSet  bool
}

type cliLogEvent struct {
	Service   string    `json:"service"`
	Source    string    `json:"source"`
	Sequence  uint64    `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Text      string    `json:"text"`
	Raw       string    `json:"raw"`
}

func runLogs(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	parsed, err := parseLogOptions(args)
	if err != nil {
		return err
	}
	record, err := resolveSession(options)
	if err != nil {
		return err
	}
	client, err := kranzruntime.DialContext(context.Background(), record.Socket, version)
	if err != nil {
		return classifyRuntimeError(err)
	}
	defer func() { _ = client.Close() }()
	names, err := resolveLogSelectors(client.Config(), parsed.selectors)
	if err != nil {
		return err
	}
	allEvents := collectLogEvents(client, names)
	cursors := make(map[string]uint64, len(names))
	for _, event := range allEvents {
		cursors[event.Service] = max(cursors[event.Service], event.Sequence)
	}
	// --since narrows the window and --tail caps what that window returns, so
	// the two compose: "the last 50 lines, from the past five minutes".
	events := allEvents
	if parsed.sinceSet {
		events = filterLogEventsSince(events, time.Now().Add(-parsed.since))
	}
	if parsed.tailSet && parsed.tail < len(events) {
		events = events[len(events)-parsed.tail:]
	}
	if err := writeLogEvents(stdout, options.Output, events, parsed.follow); err != nil {
		return err
	}
	if !parsed.follow {
		return nil
	}
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
			fresh := collectLogEventsAfter(client, names, cursors)
			if err := writeLogEvents(stdout, options.Output, fresh, true); err != nil {
				return err
			}
			for _, event := range fresh {
				cursors[event.Service] = max(cursors[event.Service], event.Sequence)
			}
		}
	}
}

func parseLogOptions(args []string) (logOptions, error) {
	var options logOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--follow" || arg == "-f":
			options.follow = true
		case arg == "--tail":
			if index+1 >= len(args) {
				return logOptions{}, &kranzcli.Error{Code: "missing_option_value", Message: "--tail requires a value", ExitCode: kranzcli.ExitUsage}
			}
			index++
			value, err := strconv.Atoi(args[index])
			if err != nil || value < 0 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_tail", Message: "--tail requires a non-negative integer", ExitCode: kranzcli.ExitUsage}
			}
			options.tail, options.tailSet = value, true
		case strings.HasPrefix(arg, "--tail="):
			value, err := strconv.Atoi(strings.TrimPrefix(arg, "--tail="))
			if err != nil || value < 0 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_tail", Message: "--tail requires a non-negative integer", ExitCode: kranzcli.ExitUsage}
			}
			options.tail, options.tailSet = value, true
		case arg == "--since":
			if index+1 >= len(args) {
				return logOptions{}, &kranzcli.Error{Code: "missing_option_value", Message: "--since requires a value", ExitCode: kranzcli.ExitUsage}
			}
			index++
			duration, err := time.ParseDuration(args[index])
			if err != nil || duration < 0 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_since", Message: "--since requires a non-negative duration such as 5m", ExitCode: kranzcli.ExitUsage}
			}
			options.since, options.sinceSet = duration, true
		case strings.HasPrefix(arg, "--since="):
			duration, err := time.ParseDuration(strings.TrimPrefix(arg, "--since="))
			if err != nil || duration < 0 {
				return logOptions{}, &kranzcli.Error{Code: "invalid_since", Message: "--since requires a non-negative duration such as 5m", ExitCode: kranzcli.ExitUsage}
			}
			options.since, options.sinceSet = duration, true
		case strings.HasPrefix(arg, "-"):
			return logOptions{}, &kranzcli.Error{Code: "unknown_option", Message: "unknown logs option " + arg, ExitCode: kranzcli.ExitUsage}
		default:
			options.selectors = append(options.selectors, arg)
		}
	}
	return options, nil
}

func resolveLogSelectors(cfg *config.Config, selectors []string) ([]string, error) {
	if len(selectors) != 0 {
		return resolveServiceSelectors(cfg, selectors)
	}
	return append([]string(nil), cfg.ServiceOrder...), nil
}

func collectLogEvents(client *kranzruntime.Client, names []string) []cliLogEvent {
	events := make([]cliLogEvent, 0)
	for _, name := range names {
		for _, entry := range client.Logs(name) {
			parsed := kranzlog.ParseLine(entry.Raw)
			source := entry.Source
			if source == "" {
				source = "unknown"
			}
			events = append(events, cliLogEvent{
				Service:   name,
				Source:    source,
				Sequence:  entry.Sequence,
				Timestamp: entry.Timestamp,
				Level:     logLevelName(parsed.Level),
				// The line terminator is how the line arrived, not part of
				// what it says, so it does not belong in a JSON field a
				// consumer will compare or print.
				Text: trimLineEnding(parsed.Text),
				Raw:  trimLineEnding(entry.Raw),
			})
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Timestamp.Equal(events[j].Timestamp) {
			if events[i].Service == events[j].Service {
				return events[i].Sequence < events[j].Sequence
			}
			return events[i].Service < events[j].Service
		}
		return events[i].Timestamp.Before(events[j].Timestamp)
	})
	return events
}

const logTimestampLayout = "2006-01-02T15:04:05.000Z07:00"

func trimLineEnding(line string) string { return strings.TrimRight(line, "\r\n") }

func logLevelName(level config.LogLevel) string {
	switch level {
	case config.LogError:
		return "error"
	case config.LogWarn:
		return "warn"
	case config.LogDebug:
		return "debug"
	default:
		return "info"
	}
}

func collectLogEventsAfter(client *kranzruntime.Client, names []string, cursors map[string]uint64) []cliLogEvent {
	all := collectLogEvents(client, names)
	fresh := all[:0]
	for _, event := range all {
		if event.Sequence > cursors[event.Service] {
			fresh = append(fresh, event)
		}
	}
	return fresh
}

func filterLogEventsSince(events []cliLogEvent, cutoff time.Time) []cliLogEvent {
	filtered := events[:0]
	for _, event := range events {
		if !event.Timestamp.Before(cutoff) {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func writeLogEvents(stdout io.Writer, format kranzcli.OutputFormat, events []cliLogEvent, stream bool) error {
	if format == kranzcli.OutputJSON {
		if stream {
			for _, event := range events {
				if err := kranzcli.WriteJSON(stdout, event); err != nil {
					return err
				}
			}
			return nil
		}
		return kranzcli.WriteJSON(stdout, events)
	}
	for _, event := range events {
		// A fixed-width timestamp keeps the service column aligned; RFC3339Nano
		// drops trailing zeros and leaves the output ragged.
		prefix := fmt.Sprintf("%s [%s/%s] ", event.Timestamp.Local().Format(logTimestampLayout), event.Service, event.Source)
		lines := strings.Split(event.Raw, "\n")
		for _, line := range lines {
			if _, err := fmt.Fprintln(stdout, prefix+strings.TrimSuffix(line, "\r")); err != nil {
				return err
			}
		}
	}
	return nil
}
