# Logs, runs, and ports

Kranz keeps output attributable even after a service restarts or an action
finishes. The useful mental model has three levels:

| Level | What it identifies | Examples |
| --- | --- | --- |
| Target | The service or action whose work you asked about | `api`, `api/migrate` |
| Run | One numbered execution of that target | `api#4`, `api/migrate#3` |
| Log event | One captured line from that run and source | `stdout`, `stderr`, `kranz` |

Service and action numbers are independent. `api#4` and `api/migrate#4` are not
the same run; the complete target before `#` is part of the identity.

![A target keeps numbered run summaries separately from the bounded output events for those runs](../assets/diagrams/run-history-retention.svg)

## Logs

<div class="demo-frame">

![Filtering a service log with a regular expression](../assets/log-search.gif)

</div>

Kranz captures stdout and stderr for process services and actions. Detached
services can provide `lifecycle.logs.command`, usually a following command such
as `docker compose logs -f`. Its process is managed independently from the
short-lived lifecycle start and stop commands.

Without run options, `kranz logs api` shows a recent tail because a service may
stream forever. `kranz logs api/migrate` shows the latest action run in full
because an action is a finite report. Use `--run`, `--runs`, `--tail`, or
`--all` when you want a different boundary.

The log panels support:

- regex filter and highlight modes;
- next and previous match navigation;
- wrapping and captured-at timestamps;
- pause/follow mode and unread counters;
- a pinned service above the currently focused log panel.

Long, wrapped, filtered, and pinned output stays anchored as new lines arrive.
Clearing logs or deleting a retained run refreshes the visible window. Managed
service restarts retain the default log history for that service. Output split
across multiple process writes is reconstructed into complete lines, with a
bounded buffer for an unfinished line.

## Your own log tools

Connect any command that reads logs on stdin and writes text or ANSI-colored
text to stdout. Commands run through a shell, so arguments and pipelines work:

```yaml
ui:
  logs:
    command: tspin --print
    mode: stream
```

Other examples include `ccze -A`, `hl -P`, `jq --unbuffered -C .`, or your own
script. Choose options that disable the tool's pager and flush live output.
Tools that buffer until EOF show their result when the selected run finishes.
Kranz closes stdin for completed history; a command that has not exited five
seconds later is stopped and the original output is shown.

The selected service/action and **all runs / run #N** determine the input
*before* the command runs. Regex filter/highlight, next/previous match,
wrapping, pause, scrolling, and text selection operate on its output. A pinned
panel has an independent command and history scope. Changing runs, clearing
logs, or changing the retained input rebuilds the corresponding presentation;
late output from the old command is discarded. Browsing and paused views stay
anchored while new command output appends.

`Ctrl+G` switches the active panel between processed and original output.
Switching representations starts at the tail; run selections and search
patterns remain. The header identifies the active representation. A command
failure falls back to original output with a notification. Displayed command
output retains the latest 4 MiB / 20000 lines; individual output lines are
limited to 1 MiB. These limits do not change runtime log retention.

Commands may change, remove, expand, or reorder lines. Such output belongs to
the selected history scope, but has no invented per-line capture timestamp or
stdout/stderr attribution. For a tool that only adds colors, enable
`preserve_lines: true`: Kranz verifies the unchanged text and order and keeps
capture metadata. A violation falls back to the original logs. Only ANSI text
styles are rendered; terminal control sequences cannot move or clear the TUI.

Source logs, run summaries, run export (`e` / `Shift+E`), CLI, and MCP remain
original. Text selection copies the displayed text without ANSI codes.

### Personal and service preferences

Put `logs:` at the root of your [personal settings file](./appearance#two-places-settings-can-live)
to use the same tool across projects:

```yaml
logs:
  command: hl -P
```

Precedence is service `ui.logs`, personal `logs`, then project `ui.logs`.
An explicit empty mapping disables an inherited command for a service:

```yaml
services:
  api:
    command: ./server
    ui:
      logs: {}
```

The command runs in the active runtime's project directory and inherits the
TUI's environment. `shell` selects a shell executable (default `/bin/sh`).
`KRANZ_LOG_TARGET`, `KRANZ_LOG_SCOPE`, and `KRANZ_LOG_RUN` describe the selected
history. In project YAML, write `$$` for a dollar sign that should reach the
shell; project configuration expands environment variables while loading.
Personal settings are not expanded, so use a single `$` there.

### Interactive viewers

Use terminal mode for a program that owns the screen and keyboard:

```yaml
ui:
  logs:
    command: 'lnav "$$KRANZ_LOG_FILE"'
    mode: terminal
```

`Ctrl+G` hands the terminal to the viewer. `KRANZ_LOG_FILE` names a private
temporary plain-text snapshot of the active panel's selected runs and regex
filter. Highlight mode includes all selected records. The snapshot is frozen;
it does not follow later output. Viewer navigation and search use the viewer's
own keys. Exit it normally to return to Kranz with the same run selection,
search, pause, and scroll position. Managed services continue running, and the
temporary file is removed when the viewer closes.

## Run history

Every start of a service and every invocation of an action is a numbered *run*.
Run numbers are assigned once and never reused, so `api#4` keeps addressing the
same execution after later runs age out of the buffer.

Kranz retains a bounded catalog of run summaries per target, independently of
the log buffer. A noisy service can only evict its own history, never another
target's. Think of the catalog as the index of executions and the log buffer as
the retained evidence for what those executions printed.

```bash
kranz runs                          # every retained run
kranz runs api analytics/stats      # narrow to one or more targets
kranz runs --status failed --since 2h --limit 10
kranz runs retention                # per-target catalog and output budgets
kranz logs api --run 4              # only run #4
kranz logs api --run -1             # the newest run
kranz logs api --runs 3             # the last three runs
```

A run summary records how the run ended and who started it: exit code,
duration, the structured cause of a state, the surface that initiated it
(`tui`, `cli`, `mcp`, or `runtime`), and why (`first_start`, `manual_start`,
`invoked`, and so on). Client labels are short product names, never socket,
process, or user identifiers.

Addressing a run that is not retained is an error rather than empty output, and
the message names the range each selected stream can still answer for:

```console
$ kranz logs api --run 99
Kranz: run #99 is not retained by anything this query selected.
Retained runs: api #7-#12.
```

The catalog and the log buffer have separate budgets, so a run's summary can
outlive its output:

- `complete` — all retained output for the run is available;
- `partial` — the beginning was evicted, and Kranz reports the missing line and
  byte counts before the available tail;
- `unavailable` — the summary remains, but none of that run's output does.

Kranz never presents a shortened log as if it were complete.

In the TUI, `v` opens the run history for the focused service or action. `x`
switches between all runs and a single run, `[` and `]` step through history,
`Shift+F` jumps to the previous failed run, and `l` returns to the latest.
`Shift+3` pins a run so it stays frozen while the live panel keeps moving.
`Shift+X` first switches every service to its latest run, regardless of individual
selections. The next press restores combined logs for every service; using `x`
on one service does not change that sequence. This toggle is separate for each
runtime. Actions and pinned snapshots keep their own selections.
If the target has never run, `v` opens a dismissible explanation instead of an
empty table.
See [Controls](../reference/controls) for the complete list.

Retention is per session. A run summary and its output can also be dropped
explicitly:

```bash
kranz runs delete api#4 --confirm
```

Deleting a run never reuses its number and never touches the transition
journal, so history stays honest about what happened.

## Declared ports

```yaml
services:
  web:
    command: npm run dev
    ports: [3000]
```

Declared ports are checked before start. When a listener is already occupied,
Kranz identifies whether it belongs to another managed service or an external
process. An external process is only signalled after an explicit action and a
fresh ownership check.

## Runtime discovery

Without `ports`, discovery defaults on. Kranz scans listeners owned by the
service process group, including child processes. With declared ports,
discovery defaults off unless `detect_ports: true` is set.

```yaml
services:
  web:
    command: npm run dev
    detect_ports: true
    ports: [3000]
```

Details distinguishes `declared`, `detected`, and `declared · listening`.
Discovery uses `lsof` on macOS and `ss` on Linux.

## Dynamic health targets

A TCP check without a static port can use a detected listener. An HTTP URL
without an explicit port can do the same:

```yaml
healthcheck:
  readiness:
    type: http
    url: http://127.0.0.1/ready
    detected_port_index: 0
```

When multiple listeners exist, set `detected_port_index` explicitly. The index
addresses the sorted, deduplicated runtime port list.
