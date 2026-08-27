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

The log header names the history position explicitly: `ALL RUNS · INCLUDES NEW`
keeps accepting new runs, `LATEST RUN · #N` shows the newest run in isolation,
and `HISTORY · RUN #N` means a newer run exists. A pinned panel is frozen and
shows `Shift+3 UNPIN` in its title; while any pin exists the footer starts with
`[Shift+3] unpin`, and the shortcut works from every panel.

`LATEST RUN` follows future starts automatically. Choosing an older run with
`[`, the catalog, or previous-failed navigation enters `HISTORY`; that explicit
selection remains stable until `l` returns to the latest run. The all-runs view
scrolls across the complete retained output of service and action runs.

The in-app `?` reference is a single scrollable column split into Navigation,
Services & Actions, Logs & Run History, Log Search, Appearance, and Application
sections.

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
