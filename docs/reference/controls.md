# Controls

Mouse interaction is available for panels, rows, checkboxes, modal actions,
search controls, and the theme picker. Keyboard shortcuts remain the primary
workflow.

## Navigation

| Key | Action |
| --- | --- |
| `1`, `2`, `3` | Focus Services/Tags, Details, or Logs |
| `t`, `←` / `→`, or `1` again | Switch Services and Tags |
| `Tab` / `Shift+Tab` | Focus next or previous panel |
| `↑` / `↓`, `j` / `k` | Navigate or scroll |
| `Shift+3` | Pin or unpin focused service logs |
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
