package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
)

// --since narrows the window and --tail caps what that window returns, so the
// two have to compose rather than exclude each other.
func TestLogOptionsAcceptTailAndSinceTogether(t *testing.T) {
	options, err := parseLogOptions([]string{"--since", "5m", "--tail", "50", "api"})
	if err != nil {
		t.Fatalf("parse = %v", err)
	}
	if !options.sinceSet || options.since != 5*time.Minute {
		t.Errorf("since = %v", options.since)
	}
	if !options.tailSet || options.tail != 50 {
		t.Errorf("tail = %d", options.tail)
	}
	if len(options.selectors) != 1 || options.selectors[0] != "api" {
		t.Errorf("selectors = %v", options.selectors)
	}
}

func TestLogOptionsRejectMalformedValues(t *testing.T) {
	for _, args := range [][]string{
		{"--tail"},
		{"--tail", "-3"},
		{"--tail", "many"},
		{"--since"},
		{"--since", "yesterday"},
		{"--nope"},
	} {
		if _, err := parseLogOptions(args); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

// A log line's terminator is how it arrived, not part of what it says, so it
// must not reach a JSON field a consumer will compare or print.
func TestTrimLineEndingRemovesOnlyTheTerminator(t *testing.T) {
	for input, want := range map[string]string{
		"hello\n":      "hello",
		"hello\r\n":    "hello",
		"hello":        "hello",
		"  spaced  \n": "  spaced  ",
		"two\nlines\n": "two\nlines",
	} {
		if got := trimLineEnding(input); got != want {
			t.Errorf("trimLineEnding(%q) = %q, want %q", input, got, want)
		}
	}
}

// A ragged timestamp column makes multi-service output hard to scan, and
// RFC3339Nano drops trailing zeros, so the layout is fixed width by design.
func TestLogTimestampLayoutIsFixedWidth(t *testing.T) {
	widths := make(map[int]bool)
	for _, moment := range []time.Time{
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 20, 9, 0, 0, 100000000, time.UTC),
		time.Date(2026, 8, 20, 9, 0, 0, 123456789, time.UTC),
	} {
		widths[len(moment.Format(logTimestampLayout))] = true
	}
	if len(widths) != 1 {
		t.Errorf("timestamp width varies: %v", widths)
	}
}

func TestLogLevelNamesCoverEveryLevel(t *testing.T) {
	for level, want := range map[config.LogLevel]string{
		config.LogError: "error",
		config.LogWarn:  "warn",
		config.LogInfo:  "info",
		config.LogDebug: "debug",
	} {
		if got := logLevelName(level); got != want {
			t.Errorf("logLevelName(%v) = %q, want %q", level, got, want)
		}
	}
}

// A bare `kranz logs` used to print every line in every service's buffer —
// thousands of lines on a project with a few services — when what was asked for
// was "what happened recently".
func TestLogOptionsCapAnUnqualifiedRequest(t *testing.T) {
	options, err := parseLogOptions(nil)
	if err != nil {
		t.Fatalf("parse = %v", err)
	}
	options = applyLogDefaults(options, []logTarget{serviceLogTarget("api")})
	if !options.tailSet || options.tail != defaultLogTail {
		t.Errorf("bare logs tail = %d (set %t), want %d", options.tail, options.tailSet, defaultLogTail)
	}
}

// An action run is finite and self-contained, so capping it at the last lines
// cuts off its head: the part that says what the run was doing. A bare action
// selector therefore means the whole latest run, not the last 50 lines of it.
func TestLogDefaultsGiveAnActionItsWholeLatestRun(t *testing.T) {
	action := actionLogTarget(config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "analytics", Name: "stats"})
	options := applyLogDefaults(logOptions{}, []logTarget{action})
	if options.tailSet {
		t.Errorf("an action run was capped at %d lines", options.tail)
	}
	if !options.runSet || options.run != -1 {
		t.Errorf("run = %d (set %t), want the latest", options.run, options.runSet)
	}
	// A selection that still contains an endless stream keeps the line cap:
	// "everything the service ever printed" is not what was asked for.
	mixed := applyLogDefaults(logOptions{}, []logTarget{serviceLogTarget("api"), action})
	if !mixed.tailSet || mixed.runSet {
		t.Errorf("mixed selection defaults = %+v", mixed)
	}
}

// Narrowing by time is already a deliberate limit, so it must not be silently
// narrowed further by a line cap the user did not ask for.
func TestLogOptionsDoNotCapAnExplicitWindow(t *testing.T) {
	for _, args := range [][]string{{"--since", "5m"}, {"--all"}, {"--tail", "500"}} {
		options, err := parseLogOptions(args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if applied := applyLogDefaults(options, []logTarget{serviceLogTarget("api")}); applied.tailSet && applied.tail == defaultLogTail {
			t.Errorf("%v was capped at the default tail", args)
		}
	}
}

func TestLogOptionsRejectAllWithTail(t *testing.T) {
	if _, err := parseLogOptions([]string{"--all", "--tail", "5"}); err == nil {
		t.Error("--all --tail was accepted")
	}
}

// logTargetConfig mirrors the shape this project actually uses: a service that
// owns actions, and an action group that owns actions but no command.
func logTargetConfig() *config.Config {
	return &config.Config{
		Services: map[string]config.Service{
			"api":  {Tags: []string{"backend"}, Actions: map[string]config.Action{"migrate": {}}, ActionOrder: []string{"migrate"}},
			"docs": {Tags: []string{"frontend"}},
		},
		ServiceOrder: []string{"api", "docs"},
		ActionGroups: map[string]config.ActionGroup{
			"analytics": {Actions: map[string]config.Action{"stats": {}}, ActionOrder: []string{"stats"}},
		},
		ActionGroupOrder: []string{"analytics"},
	}
}

func targetAddresses(targets []logTarget) []string {
	addresses := make([]string, 0, len(targets))
	for _, target := range targets {
		addresses = append(addresses, target.address)
	}
	return addresses
}

// The reason this work exists: an action is addressed OWNER/ACTION exactly the
// way `kranz action run` addresses it.
func TestResolveLogTargetsAddressesActionsByOwnerAndName(t *testing.T) {
	targets, err := resolveLogTargets(logTargetConfig(), []string{"analytics/stats"}, false)
	if err != nil {
		t.Fatalf("resolve = %v", err)
	}
	if got := targetAddresses(targets); len(got) != 1 || got[0] != "analytics/stats" {
		t.Fatalf("targets = %v", got)
	}
	if !targets[0].isAction || targets[0].action.OwnerKind != config.ActionOwnerGroup {
		t.Errorf("target = %+v, want a group action", targets[0])
	}
}

// A service name means the service's own command. Its actions are separate
// streams that only --with-actions folds in.
func TestResolveLogTargetsSeparatesServiceFromItsActions(t *testing.T) {
	cfg := logTargetConfig()
	plain, err := resolveLogTargets(cfg, []string{"api"}, false)
	if err != nil {
		t.Fatalf("resolve = %v", err)
	}
	if got := targetAddresses(plain); len(got) != 1 || got[0] != "api" {
		t.Fatalf("without --with-actions = %v", got)
	}
	merged, err := resolveLogTargets(cfg, []string{"api"}, true)
	if err != nil {
		t.Fatalf("resolve = %v", err)
	}
	if got := targetAddresses(merged); len(got) != 2 || got[0] != "api" || got[1] != "api/migrate" {
		t.Fatalf("with --with-actions = %v", got)
	}
}

// A group has no command, so its bare name addresses nothing. The answer has to
// say which two ways forward exist rather than claim the name is unknown.
func TestResolveLogTargetsExplainsAGroupHasNoStreamOfItsOwn(t *testing.T) {
	_, err := resolveLogTargets(logTargetConfig(), []string{"analytics"}, false)
	if err == nil {
		t.Fatal("a bare group name was accepted")
	}
	cliErr, ok := err.(*kranzcli.Error)
	if !ok || cliErr.Code != "group_has_no_stream" {
		t.Fatalf("err = %#v", err)
	}
	if !strings.Contains(cliErr.Hint, "analytics/stats") || !strings.Contains(cliErr.Hint, "--with-actions") {
		t.Errorf("hint does not offer both ways forward: %s", cliErr.Hint)
	}
	expanded, err := resolveLogTargets(logTargetConfig(), []string{"analytics"}, true)
	if err != nil {
		t.Fatalf("resolve with --with-actions = %v", err)
	}
	if got := targetAddresses(expanded); len(got) != 1 || got[0] != "analytics/stats" {
		t.Fatalf("expanded = %v", got)
	}
}

func TestResolveLogTargetsResolvesTagsAndRejectsUnknownNames(t *testing.T) {
	tagged, err := resolveLogTargets(logTargetConfig(), []string{"backend"}, false)
	if err != nil {
		t.Fatalf("resolve = %v", err)
	}
	if got := targetAddresses(tagged); len(got) != 1 || got[0] != "api" {
		t.Fatalf("tag targets = %v", got)
	}
	if _, err := resolveLogTargets(logTargetConfig(), []string{"nope"}, false); err == nil {
		t.Error("an unknown selector was accepted")
	}
}

// Selectors that overlap must not print the same line twice.
func TestResolveLogTargetsDeduplicatesOverlappingSelectors(t *testing.T) {
	targets, err := resolveLogTargets(logTargetConfig(), []string{"api", "backend", "api/migrate"}, true)
	if err != nil {
		t.Fatalf("resolve = %v", err)
	}
	if got := targetAddresses(targets); len(got) != 2 {
		t.Fatalf("targets = %v", got)
	}
}

func TestLogOptionsParseRunSelection(t *testing.T) {
	options, err := parseLogOptions([]string{"analytics/stats", "--run=-1"})
	if err != nil {
		t.Fatalf("parse = %v", err)
	}
	if !options.runSet || options.run != -1 {
		t.Errorf("run = %d set=%t", options.run, options.runSet)
	}
	// Naming a run is already a deliberate limit, so the default tail must not
	// silently trim the run the user asked to see in full.
	if options.tailSet {
		t.Errorf("run selection still received the default tail of %d", options.tail)
	}
	if _, err := parseLogOptions([]string{"--run", "2", "--runs", "3"}); err == nil {
		t.Error("--run and --runs were accepted together")
	}
	for _, args := range [][]string{{"--run"}, {"--run", "0"}, {"--run", "x"}, {"--runs", "0"}, {"--runs", "-2"}} {
		if _, err := parseLogOptions(args); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

func runEntries(runs ...uint32) []config.LogEntry {
	entries := make([]config.LogEntry, 0, len(runs))
	for index, run := range runs {
		entries = append(entries, config.LogEntry{Sequence: uint64(index + 1), Run: run, Raw: fmt.Sprintf("line %d", index+1)})
	}
	return entries
}

func entryRuns(entries []config.LogEntry) []uint32 {
	runs := make([]uint32, 0, len(entries))
	for _, entry := range entries {
		runs = append(runs, entry.Run)
	}
	return runs
}

// A negative --run counts back from the newest run still buffered, so -1 keeps
// meaning "the latest" however many runs have aged out.
func TestFilterLogEntriesByRunAddressesAbsoluteAndRelativeRuns(t *testing.T) {
	entries := runEntries(1, 1, 2, 3, 3)
	for _, testCase := range []struct {
		name    string
		options logOptions
		want    []uint32
	}{
		{"absolute", logOptions{run: 2, runSet: true}, []uint32{2}},
		{"latest", logOptions{run: -1, runSet: true}, []uint32{3, 3}},
		{"previous", logOptions{run: -2, runSet: true}, []uint32{2}},
		{"last two", logOptions{runs: 2, runsSet: true}, []uint32{2, 3, 3}},
		{"more runs than exist", logOptions{runs: 9, runsSet: true}, []uint32{1, 1, 2, 3, 3}},
		{"beyond the buffer", logOptions{run: -9, runSet: true}, nil},
		{"never ran", logOptions{run: -1, runSet: true}, nil},
	} {
		input := entries
		if testCase.name == "never ran" {
			input = runEntries(0, 0)
		}
		got := entryRuns(filterLogEntriesByRun(input, testCase.options))
		if len(got) != len(testCase.want) {
			t.Errorf("%s: runs = %v, want %v", testCase.name, got, testCase.want)
			continue
		}
		for index := range got {
			if got[index] != testCase.want[index] {
				t.Errorf("%s: runs = %v, want %v", testCase.name, got, testCase.want)
				break
			}
		}
	}
}

// --run addresses an execution, and services do not execute; asking for one
// against services alone is a mistake worth naming rather than an empty answer.
func TestActionTargetsOnlyRejectsAServiceOnlySelection(t *testing.T) {
	if _, err := actionTargetsOnly([]logTarget{serviceLogTarget("api")}); err == nil {
		t.Error("a service-only selection was accepted for --run")
	}
	kept, err := actionTargetsOnly([]logTarget{
		serviceLogTarget("api"),
		actionLogTarget(config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "api", Name: "migrate"}),
	})
	if err != nil {
		t.Fatalf("mixed selection = %v", err)
	}
	if got := targetAddresses(kept); len(got) != 1 || got[0] != "api/migrate" {
		t.Fatalf("kept = %v", got)
	}
}

// The address already contains a slash for an action, so the source is split
// off by a space instead of a second slash that would read as another name.
func TestLogEventLabelKeepsTheAddressReadableBack(t *testing.T) {
	for _, testCase := range []struct {
		event cliLogEvent
		want  string
	}{
		{cliLogEvent{Stream: "docs", Source: "stdout"}, "docs stdout"},
		{cliLogEvent{Stream: "analytics/stats", Run: 3, Source: "stderr"}, "analytics/stats#3 stderr"},
	} {
		if got := logEventLabel(testCase.event); got != testCase.want {
			t.Errorf("logEventLabel = %q, want %q", got, testCase.want)
		}
	}
}

// A pipe hands Kranz whatever chunk it read, so one captured entry can hold
// many lines. Counting those as one event would make --tail print an
// unpredictable number of lines and cut in the middle of one.
func TestLogTargetEventsSplitACapturedChunkIntoLines(t *testing.T) {
	target := serviceLogTarget("api")
	events := target.events(config.LogEntry{Sequence: 7, Source: "stdout", Raw: "first\r\nsecond\nthird\n"})
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	for index, want := range []string{"first", "second", "third"} {
		if events[index].Raw != want {
			t.Errorf("line %d = %q, want %q", index, events[index].Raw, want)
		}
		// The lines came from one captured entry and stay addressable as one:
		// the follow cursor advances per entry, not per printed line.
		if events[index].Sequence != 7 {
			t.Errorf("line %d lost its cursor: %d", index, events[index].Sequence)
		}
	}
}

// Both added columns exist to tell interleaved streams apart, which is exactly
// what reading a single stream back does not need.
func TestLogEventPrefixCanBeSwitchedOff(t *testing.T) {
	event := cliLogEvent{Stream: "analytics/stats", Run: 2, Source: "stdout", Timestamp: time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)}
	full := logEventPrefix(event, logOptions{})
	if !strings.Contains(full, "analytics/stats#2 stdout") || !strings.Contains(full, "2026-08-23") {
		t.Fatalf("default prefix = %q", full)
	}
	if got := logEventPrefix(event, logOptions{noTimes: true}); got != "[analytics/stats#2 stdout] " {
		t.Errorf("--no-timestamps prefix = %q", got)
	}
	if got := logEventPrefix(event, logOptions{noLabels: true}); strings.Contains(got, "[") {
		t.Errorf("--no-labels prefix = %q", got)
	}
	if got := logEventPrefix(event, logOptions{noTimes: true, noLabels: true}); got != "" {
		t.Errorf("--plain prefix = %q, want nothing", got)
	}
}

func TestLogOptionsParseDisplayFlags(t *testing.T) {
	plain, err := parseLogOptions([]string{"--plain"})
	if err != nil || !plain.noTimes || !plain.noLabels {
		t.Fatalf("--plain = %+v, %v", plain, err)
	}
	separate, err := parseLogOptions([]string{"--no-timestamps", "--no-labels"})
	if err != nil || !separate.noTimes || !separate.noLabels {
		t.Fatalf("separate flags = %+v, %v", separate, err)
	}
}

// The contract --tail states: N is the number of lines the user sees. It broke
// once because a captured entry could hold many lines while counting as one, so
// this walks the whole chain — capture, split, tail, print — and counts the
// lines that actually come out.
func TestTailPrintsExactlyTheRequestedNumberOfLines(t *testing.T) {
	target := serviceLogTarget("api")
	// Chunks as a pipe really delivers them: one read can carry many lines.
	entries := []config.LogEntry{
		{Sequence: 1, Source: "stdout", Raw: "one\ntwo\nthree\nfour\n"},
		{Sequence: 2, Source: "stdout", Raw: "five\n"},
		{Sequence: 3, Source: "stderr", Raw: "six\nseven\n"},
	}
	var events []cliLogEvent
	for _, entry := range entries {
		events = append(events, target.events(entry)...)
	}
	if len(events) != 7 {
		t.Fatalf("chunks expanded to %d events, want 7 lines", len(events))
	}
	for _, tail := range []int{1, 3, 7, 50} {
		var out bytes.Buffer
		options := logOptions{tail: tail, tailSet: true}
		if err := writeLogEvents(&out, kranzcli.OutputText, tailLogEvents(events, options), options); err != nil {
			t.Fatal(err)
		}
		printed := strings.Count(out.String(), "\n")
		want := min(tail, len(events))
		if printed != want {
			t.Errorf("--tail %d printed %d lines, want %d:\n%s", tail, printed, want, out.String())
		}
	}
	// The tail is the *last* lines, not any N of them.
	var out bytes.Buffer
	options := logOptions{tail: 2, tailSet: true, noTimes: true, noLabels: true}
	if err := writeLogEvents(&out, kranzcli.OutputText, tailLogEvents(events, options), options); err != nil {
		t.Fatal(err)
	}
	if out.String() != "six\nseven\n" {
		t.Errorf("tail kept the wrong lines: %q", out.String())
	}
}

// Every event is one line, so a JSON consumer counting records and a human
// counting lines must arrive at the same number.
func TestTextAndJSONAgreeOnHowManyLinesThereAre(t *testing.T) {
	target := actionLogTarget(config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "analytics", Name: "stats"})
	events := target.events(config.LogEntry{Sequence: 1, Run: 2, Source: "stdout", Raw: "alpha\nbeta\ngamma\n"})
	var text, encoded bytes.Buffer
	if err := writeLogEvents(&text, kranzcli.OutputText, events, logOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := writeLogEvents(&encoded, kranzcli.OutputJSON, events, logOptions{}); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data []cliLogEvent `json:"data"`
	}
	if err := json.Unmarshal(encoded.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(text.String(), "\n"); lines != len(envelope.Data) {
		t.Errorf("text printed %d lines but JSON carried %d records", lines, len(envelope.Data))
	}
	for _, record := range envelope.Data {
		if strings.Contains(record.Raw, "\n") {
			t.Errorf("a JSON record holds more than one line: %q", record.Raw)
		}
	}
}

func TestLogSourceParsingAcceptsListsAndRejectsUnknownNames(t *testing.T) {
	options, err := parseLogOptions([]string{"--source", "stdout,stderr", "--source=kranz"})
	if err != nil {
		t.Fatalf("parse = %v", err)
	}
	if len(options.sources) != 3 {
		t.Fatalf("sources = %v", options.sources)
	}
	for _, args := range [][]string{{"--source"}, {"--source", "stdin"}, {"--source", ""}, {"--source", "stdout,nope"}} {
		if _, err := parseLogOptions(args); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

// --source narrows before the tail, so `--source stderr --tail 2` means two
// error lines, not the errors that survive in the last two lines of everything.
func TestSourceFilterNarrowsBeforeTheTail(t *testing.T) {
	target := serviceLogTarget("api")
	events := target.events(config.LogEntry{Sequence: 1, Source: "stderr", Raw: "boom\nbang\n"})
	events = append(events, target.events(config.LogEntry{Sequence: 2, Source: "stdout", Raw: "fine\nalso fine\nstill fine\n"})...)

	options := logOptions{sources: []string{"stderr"}, tail: 2, tailSet: true, noTimes: true, noLabels: true}
	var out bytes.Buffer
	if err := writeLogEvents(&out, kranzcli.OutputText, tailLogEvents(filterLogEventsBySource(events, options.sources), options), options); err != nil {
		t.Fatal(err)
	}
	if out.String() != "boom\nbang\n" {
		t.Errorf("--source stderr --tail 2 printed %q", out.String())
	}
	if got := filterLogEventsBySource(events, nil); len(got) != 5 {
		t.Errorf("no --source dropped lines: %d of 5 kept", len(got))
	}
}
