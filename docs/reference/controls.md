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

### Action parameters

An action whose command is assembled from values ends in `◆`, and `c` opens
those values as a form. `Enter` opens its
parameters, then opens one parameter: a checkbox toggles, a list of options
opens for `Enter` or `Space` on the chosen value, and a text or number parameter becomes
editable in place, where `Enter` keeps what was typed and `Esc` restores the
previous value. `Space` marks a value without opening anything, `←` and `→` walk a list of
values or count a number up and down (by ten with `Shift`), and `d` switches a
parameter off entirely, so it reads `DISABLED` and contributes nothing — which
is different from holding its default. A parameter that takes a set of
values draws its options as `▣`/`▢` boxes and keeps every one that is checked; a
parameter that takes one draws them as `◉`/`○`. A typed value is underlined, the
way an input field is.

The first row under an open action is the exact invocation, marked with `$` the
way run output is. When the current values cannot build one, that row turns red
and shows the template instead, the value that blocks it is red, and its name
carries the accent; what is actually wrong is said in the details panel. `s` runs
the action from its own row or from the command row — a parameter row offers
neither run nor start, only the form — and a value that asks for confirmation
opens the usual modal showing that same invocation. While a parameter row is
focused, the details panel describes what that parameter accepts, what it holds,
which of its values ask before they run, and, while one is being typed, how to
keep or discard it.

### The parameter form

`c` opens the same values as a form, with the command they build pinned above
the fields. It is the better place for an action with more than a couple of
settings, or when the tree has scrolled the command out of sight. Under each
field is the line that says what it accepts, and that same line turns red with
the reason when the value stops satisfying it.

| Key | Action |
| --- | --- |
| `↑` `↓` or `k` `j` | Move between fields |
| `←` `→` or `h` `l` | Move through a field's values, choosing as it goes for a single choice |
| `Space` | Check a box, or add and remove one value of a set |
| `Shift` + `←` `→` | Count a number by ten |
| `d` | Switch the parameter off, so it is left out of the command |
| `Enter` | Type into a text or number field; `Enter` keeps it and `Esc` discards it |
| `s` | Run what the command line shows |
| `Esc` | Close the form |

Outside a parameterized action `c` keeps its usual meaning of clearing logs.

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
| `Ctrl+L` | Apply detected configuration changes and reload terminal appearance |
| `Ctrl+O` | Hand terminal to a shell; press again to return |
| `m` | Open the configuration map |
| `p` | Switch to another local Kranz runtime |
| `?` | Help |
| `q` | Always open the quit confirmation |
| `Ctrl+C` | Immediate shutdown |

The quit confirmation shows the complete shutdown plan. `Enter` or `y` stops
the runtime and quits, `d` detaches the TUI while keeping the runtime running,
and `c` stops the current runtime and opens the live runtime chooser without
leaving the TUI. `Esc` or `n` stays on the current dashboard. Shutdown stops
process-owned services and only detached services with `stop_on_exit: true`;
other external resources are listed and remain active.

In the runtime list opened with `p`, `Enter` attaches to another compatible
runtime or closes the list when the current runtime is selected. Press `s`
on any other highlighted runtime to review and confirm its shutdown without
attaching; the TUI stays open. The current runtime has no stop shortcut in
this list. Use `q` from its dashboard for its full exit choices. If the
selected runtime has an incompatible protocol, the confirmation lists verified
managed processes and warns that detached resources may remain running.

## Configuration map

`m` opens a read-only map of where the effective configuration came from. It
never reads or writes a file: every row is derived from the configuration the
runtime already loaded. The default **by source** view lists the resolved files
in deterministic order, drawn as the include tree.
How each file joined the merge follows its name in parentheses: `(explicit)`,
`(via glob)`, `(via discover)`, `(override)`, or `(virtual root)`, with an exact
include left untagged, plus `(truncated)` when an include budget cut a source's
children. Under each file it names the services that file defined, the fields
it overrode grouped under `overrides FILE:` for the earlier file whose value
they replaced, and `sets:` for fields no earlier file had set. `Tab` switches to the **by service**
view: one row per service with the file that defined it, and every later
override layer with its fields beneath, so "which file changed this service?" is
one glance away. `↑`/`↓` (or `j`/`k`) scroll a long map line by line,
`PgUp`/`PgDn` page through it, `Home`/`End` (or `g`/`G`) jump to either end, and
`Esc` closes it. `kranz config sources` and `kranz config sources --by-service`
print both views without colour from the same layout, so the dashboard and the
CLI cannot disagree about merge order or attribution.

## Switching runtimes

`p` opens a list of every Kranz runtime registered on the machine, current
runtime first, then the rest ordered by how recently they started. It
refreshes on its own while open — a runtime that starts or stops elsewhere
appears or disappears without reopening the modal.

| Key | Action |
| --- | --- |
| `↑` / `↓`, `j` / `k` | Move the selection |
| `Enter` | Connect to another compatible runtime, or close the list on the current one |
| `s` | Review and stop the selected other runtime |
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
reach, cannot be attached to; the row explains why and `s` offers a confirmed
stop. In a narrow terminal the directory is dropped
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

The same chooser opens immediately after **Close & choose**. If no runtime
remains, the empty list is valid; press `q` to leave the TUI or `Esc` to return
to the recovery actions.

A restart reuses the project directory and configuration paths Kranz already
had for that runtime; it does not repeat any command a user typed. If it
fails, the reason stays on screen and either action can be tried again. Both
options are also available by clicking their label.
