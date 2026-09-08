package main

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

type watchQuery struct {
	formatter *rowTemplate
	args      []string
	filters   map[string]map[string]bool
	watch     bool
	interval  time.Duration
	count     int
}

func parseWatchQuery(command string, output kranzcli.OutputFormat, args []string, allowedFilters ...string) (watchQuery, error) {
	formatter, args, err := extractRowFormat(command, output, args)
	if err != nil {
		return watchQuery{}, err
	}
	allowed := make(map[string]bool, len(allowedFilters))
	for _, key := range allowedFilters {
		allowed[key] = true
	}
	query := watchQuery{formatter: formatter, filters: make(map[string]map[string]bool), interval: time.Second}
	intervalSet := false
	for index := 0; index < len(args); index++ {
		arg, value := args[index], ""
		switch {
		case arg == "--watch":
			query.watch = true
			continue
		case arg == "--filter" || arg == "--interval" || arg == "--count":
			if index+1 >= len(args) {
				return watchQuery{}, watchUsageError(command, arg+" requires a value")
			}
			index++
			value = args[index]
		case strings.HasPrefix(arg, "--filter="):
			arg, value = "--filter", strings.TrimPrefix(arg, "--filter=")
		case strings.HasPrefix(arg, "--interval="):
			arg, value = "--interval", strings.TrimPrefix(arg, "--interval=")
		case strings.HasPrefix(arg, "--count="):
			arg, value = "--count", strings.TrimPrefix(arg, "--count=")
		case strings.HasPrefix(arg, "-"):
			return watchQuery{}, watchUsageError(command, fmt.Sprintf("unknown %s option %q", command, arg))
		default:
			query.args = append(query.args, arg)
			continue
		}
		switch arg {
		case "--filter":
			key, filterValue, ok := strings.Cut(value, "=")
			key = strings.ToLower(strings.TrimSpace(key))
			if !ok || !allowed[key] {
				return watchQuery{}, watchUsageError(command, fmt.Sprintf("unsupported filter %q; use one of: %s", key, strings.Join(allowedFilters, ", ")))
			}
			for _, candidate := range strings.Split(filterValue, ",") {
				candidate = strings.ToLower(strings.TrimSpace(candidate))
				if candidate == "" {
					return watchQuery{}, watchUsageError(command, "--filter values cannot be empty")
				}
				if query.filters[key] == nil {
					query.filters[key] = make(map[string]bool)
				}
				query.filters[key][candidate] = true
			}
		case "--interval":
			interval, parseErr := time.ParseDuration(value)
			if parseErr != nil || interval <= 0 {
				return watchQuery{}, watchUsageError(command, "--interval must be a positive duration")
			}
			query.interval = interval
			intervalSet = true
		case "--count":
			count, parseErr := strconv.Atoi(value)
			if parseErr != nil || count <= 0 {
				return watchQuery{}, watchUsageError(command, "--count must be a positive integer")
			}
			query.count = count
		}
	}
	if !query.watch && (query.count > 0 || intervalSet) {
		return watchQuery{}, watchUsageError(command, "--count and --interval require --watch")
	}
	if query.watch && output == kranzcli.OutputJSON {
		return watchQuery{}, watchUsageError(command, "--watch cannot be combined with --output=json; use --format for streaming rows")
	}
	return query, nil
}

func watchUsageError(command, message string) error {
	return &kranzcli.Error{Code: "invalid_watch_query", Message: message, Hint: fmt.Sprintf("Use `kranz %s --help` to inspect filters and watch options.", command), ExitCode: kranzcli.ExitUsage}
}

func matchesWatchFilters(filters map[string]map[string]bool, values map[string][]string) bool {
	for key, accepted := range filters {
		matched := false
		for _, candidate := range values[key] {
			if accepted[strings.ToLower(candidate)] {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// runWatch executes immediately, then waits between bounded refreshes. Signals
// interrupt the wait and return success; each refresh function owns its own
// I/O timeout so an unavailable runtime cannot trap a watch indefinitely.
func runWatch(query watchQuery, output io.Writer, refresh func() error) error {
	if !query.watch {
		return refresh()
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	for iteration := 1; ; iteration++ {
		if iteration > 1 && query.formatter == nil {
			if _, err := fmt.Fprintln(output); err != nil {
				return err
			}
		}
		if err := refresh(); err != nil {
			return err
		}
		if query.count > 0 && iteration >= query.count {
			return nil
		}
		timer := time.NewTimer(query.interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}
