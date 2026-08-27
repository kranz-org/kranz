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
| `?` | Help |
| `q` | Confirm quit when services are active |
| `Ctrl+C` | Immediate shutdown |

Shutdown stops process-owned services and only detached services with
`stop_on_exit: true`.
