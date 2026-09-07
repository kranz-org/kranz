# Keyboard shortcuts

This is the complete keyboard shortcut reference for the Kranz terminal UI.
Press `?` inside Kranz to open the same context-aware help without leaving the
terminal. Mouse interaction is also available for panels, rows, checkboxes,
modal actions, search controls, and the theme picker.

## Navigation

| Key | Action |
| --- | --- |
| `1`, `2`, `3` | Focus Services/Tags, Details, or Logs |
| `t`, `←` / `→`, or `1` again | Switch Services and Tags |
| `Tab` / `Shift+Tab` | Focus next or previous panel |
| `↑` / `↓`, `j` / `k` | Navigate or scroll |
| `Shift+3` | Pin the current run, or unpin the existing view from any panel |
| `d` | Delete the selected completed run from Run history after confirmation |
| `Enter` | Expand/collapse tags, services, actions, or action groups |

## Lifecycle and actions

| Key | Action |
| --- | --- |
| `Space` | Select/unselect a service or tag |
| `s` | Start dependencies and targets, confirm stop, or run focused action |
| `Shift+S` | Start/stop only targets without graph expansion |
| `r` | Confirm restart of the focused service |
| `Shift+R` | Confirm restart of running services |
| `a` | Select or clear all services |
| `Shift+A` | Confirm stop all |
| `Shift+T` | Clear tag selection |

Starting includes transitive dependencies. Stopping includes transitive
dependents in reverse order. `Shift+S` is the explicit override in both
directions.

## Logs

| Key | Action |
| --- | --- |
| `/` | Open regex search |
| `Enter` in search | Apply without closing |
| `Tab` in search | Toggle filter/highlight |
| `Esc` | Close search or clear an active filter |
| `n` / `Shift+N` | Next/previous highlighted match |
| `w` | Toggle wrapping |
| `i` | Toggle captured-at time |
| `f` | Pause/resume following |
| `c` | Confirm clear focused logs |
| `h` | Health history |
| `n` | Notifications when highlight search is inactive |
| `x` | Toggle Combined/Single run |
| `[` / `]` | Previous/next run |
| `Shift+F` / `l` | Previous failed run / latest run |
| `v` | Open the filterable run list |
| `Shift+3` | Pin the selected `{target, run, view mode}` snapshot |
| `e` / `Shift+E` | Export selected run to clipboard / chosen file |

The log header names the history position as a fraction. `ALL RUNS` accepts new
output, `RUN #3/3` is the newest run in isolation, and `RUN #2/3 · [L] LATEST`
is an older one — the numerator is the run's absolute identity and the
denominator is the newest run, so the position, the distance from the end, and
the existence of newer runs are one reading rather than three phrases. The
panel title names the target once; it does not repeat the run number the label
already carries. A pinned panel is frozen and shows `Shift+3 UNPIN` in its
title; while any pin exists the footer starts with `[Shift+3] unpin`, and the
shortcut works from every panel.

An action's outcome is the status indicator. The word appears after the run
label, and only where the glyph cannot carry the difference: `×` covers both
`FAILED` and `TIMED_OUT`, while `✓` and "succeeded" would say the same thing
twice. Keeping the word behind the label matters because it exists only while a
run is live — in front of the label it moved the run position on every start
and every finish.

The run list windows itself to the terminal and shows a `position/total`
indicator when the history is longer than the window, so the selected row and
the shortcut footer stay visible however far back the selection is. Its
`[Tab]` shortcut carries the active filter and appears only when a filter would
actually divide the list: a candidate is offered only if it selects some but
not all retained runs, so three successful actions have nothing to filter and
one failure among them makes `failed` worth offering. Retention budgets are
shown only alongside a gap they explain — dropped summaries, or runs whose
output the buffer no longer holds.

`RUN #N/N` follows future starts automatically. Choosing an older run with `[`,
the catalog, or previous-failed navigation pins the selection; it remains
stable until `l` returns to the latest run. The all-runs view scrolls across
the complete retained output of service and action runs.

The in-app `?` reference is a compact, scrollable single column split into
Navigation, Services & Actions, Logs & Run History, Log Search, Appearance, and
Application sections. Neutral section headings are visually distinct from the
accent-coloured shortcuts.

## Application

| Key | Action |
| --- | --- |
| `Ctrl+T` | Theme and appearance picker |
| `Ctrl+L` | Reload configuration and terminal appearance |
| `Ctrl+O` | Hand terminal to a shell; press again to return |
| `p` | Switch to another local Kranz runtime |
| `?` | Help |
| `q` | Always open the quit confirmation |
| `Ctrl+C` | Immediate shutdown |

The quit confirmation shows the complete shutdown plan. `Enter` or `y` stops
the runtime and quits, `d` detaches the TUI while keeping the runtime running,
and `Esc` or `n` stays in the TUI. Shutdown stops process-owned services and
only detached services with `stop_on_exit: true`; other external resources are
listed and remain active.

## Switching runtimes

`p` opens a list of every Kranz runtime registered on the machine, current
runtime first, then the rest ordered by how recently they started. It
refreshes on its own while open — a runtime that starts or stops elsewhere
appears or disappears without reopening the modal.

| Key | Action |
| --- | --- |
| `↑` / `↓`, `j` / `k` | Move the selection |
| `Enter` | Connect to the selected runtime |
| `Esc` or `p` | Close the modal and stay on the current runtime |

The table has separate `RUNTIME`, `STATUS`, `CLIENTS`, `SERVICES`, `UPTIME`,
and `DIRECTORY` columns. `STATUS` is `current` for the runtime already open
and `started` for another available runtime. `CLIENTS` lists the unique client
surfaces connected to it (for example `CLI MCP TUI`) without counts. The
current row includes the dashboard's own `TUI` connection, so another `MCP` or
`CLI` client remains visible beside it. The compact labels fit three standard
surfaces in the 11-cell column, while
`SERVICES` shows running/total in the same `3/5` form as `kranz ps`. A runtime
running an incompatible protocol version, or one the switcher cannot currently
reach, is shown greyed out and cannot be selected; selecting it spells the
reason out under the list. In a narrow terminal the directory is dropped
first, then the client surfaces shorten, then the runtime name; `UPTIME` and
`CLIENTS` disappear only after that, and `STATUS` and `SERVICES` are the last
to give up any width. Mouse clicks move the selection the same way arrow keys
do, and a second click on the same row within the usual double-click window
connects to it.

The runtime and Run history modals keep a small dashboard gutter instead of
growing to the full terminal width. When space is limited, their tables remove
secondary columns and shorten cells before any horizontal clipping occurs.
Shortcut groups wrap onto complete additional rows, and list windowing reserves
those rows so the cursor and close action remain visible.

Connecting is all-or-nothing: Kranz only switches once the new runtime has
answered a handshake and handed over its configuration and current state. A
failed connection leaves the previous runtime's dashboard exactly as it was,
with an error explaining why. Switching never stops, restarts, or otherwise
disturbs the runtime being left — a start, stop, or restart it already
accepted keeps running, and its result is shown if you switch back before it
finishes. Each visited runtime keeps its own selection, filters, pinned log,
and scroll position for the life of the TUI process; none of it is written to
disk, and it is cleared on exit.

If `kranz` is started outside a project directory but at least one local
runtime is already running, the switcher opens immediately instead of
failing. If neither a configuration nor a runtime is found, Kranz prints an
error to the terminal and exits without opening a screen at all.

### If the current runtime stops

Losing the connection to the runtime the dashboard is showing does not close
the TUI. Instead it shows a recovery screen:

| Key | Action |
| --- | --- |
| `r` or `Enter` | Restart the same project's runtime and reattach |
| `c` | Choose a different, already-running runtime |
| `q` or `Ctrl+C` | Close only the TUI |

A restart reuses the project directory and configuration paths Kranz already
had for that runtime; it does not repeat any command a user typed. If it
fails, the reason stays on screen and either action can be tried again. Both
options are also available by clicking their label.
