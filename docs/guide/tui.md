# TUI: the terminal interface

Running `kranz` opens the keyboard-first TUI. It is an operator view over the
same runtime used by the CLI and MCP, so a command or coding agent can inspect
the stack without opening a second copy.

<div class="demo-frame">

![Kranz terminal interface showing the service tree, focused service details, and live logs](../assets/kranz-demo.gif)

</div>

## What is on screen

| Area | What it shows | Focus key |
| --- | --- | --- |
| Services / Tags | Services, dependencies, actions, groups, tags, selection, and lifecycle state | `1` |
| Details | The focused item's command, health, dependencies, ports, and current operation | `2` |
| Logs | Captured service, action, and lifecycle output with follow, wrap, timestamps, and regex search | `3` |

The header summarizes the runtime and active work. The footer shows the most
relevant keys for the current focus. Press `?` at any time for in-application
help.

## Read state at a glance

The dot beside a service is its lifecycle state, not a health verdict:

| Indicator | Meaning |
| --- | --- |
| Green | The process or detached resource is running |
| Gray | It is stopped |
| Yellow | A start, stop, dependency wait, or lifecycle operation is in progress |
| Red | It failed or exited unsuccessfully |

Readiness and liveness are separate. Details names the configured probes and
their latest result; a missing probe is shown as not configured, never as a
successful health check. Runtime-discovered ports and the PID that owns them
appear in the same panel.

The focused row and selected rows are also different. Focus chooses what
Details and Logs show. Selection chooses the targets for the next lifecycle
operation. This lets you inspect one service while preparing an operation on
several others.

## A first session

1. Use `↑` / `↓` or `j` / `k` to focus a service.
2. Press `Space` to select it, or `a` to select all services.
3. Press `s` to start the selection and its dependencies.
4. Press `2` to inspect readiness, liveness, dependencies, and ports.
5. Press `3` to follow logs; press `/` to search them.
6. Return to panel `1` and press `s` again to review and confirm the stop plan.

Actions appear below their service or project group. Focus an action and press
`s` to run it. Its output is multiplexed into the Logs panel and labeled with
the exact `OWNER/ACTION#run` identity.

## Review lifecycle plans

Starting a service includes its transitive dependencies. Stopping it includes
running dependents in reverse order. Restarting follows the same resolved graph.
Before a destructive operation Kranz opens a confirmation view with the exact
targets and execution waves; the TUI does not hide graph expansion behind a
single service name.

Use `Shift+S` only when you deliberately want the selected targets without graph
expansion. The confirmation view calls out that override so it cannot be
mistaken for the safe default.

## Work with logs

Panel `3` follows the focused service by default. `Shift+3` pins it while you
move elsewhere. Search with `/`, then use `Tab` to switch between filtering and
highlighting; `n` and `Shift+N` move between highlighted matches. Toggle wrap
with `w`, captured-at timestamps with `i`, and following with `f`.

Service output survives a crash or stop for as long as the runtime exists.
Every service start and action execution has a run number, so logs remain
attributable across restarts instead of becoming one ambiguous stream.

## Actions, history, and notifications

Actions live below their service or project action group. Run a focused action
with `s`; confirmation is shown when its configuration requests it. Completed
actions remain available by their `OWNER/ACTION#run` identity, including their
captured output and exit status.

Press `n` outside an active log-highlight search to open notifications. Press
`h` from Logs for recorded health transitions. Both views explain what changed
without requiring you to reconstruct it from raw output.

## Attach to a background runtime

`kranz up -d` starts a runtime and returns the shell. Open the same runtime in
the TUI later with:

```bash
kranz attach
```

Quitting an attached TUI disconnects only that view; the background runtime and
its services continue. By contrast, the TUI created by running bare `kranz`
owns its foreground runtime. Quitting it confirms cleanup for process-owned
services and for detached services whose `stop_on_exit` is enabled.

The [CLI workflow](./cli-workflow) and [MCP guide](./mcp) operate the same live
runtime. A restart from either is reflected immediately in this TUI.

## Common keys

| Key | Meaning |
| --- | --- |
| `1`, `2`, `3` | Focus Services/Tags, Details, or Logs |
| `Tab`, `Shift+Tab` | Move between panels |
| `Enter` | Expand or collapse the focused row |
| `Space` | Select or unselect a service or tag |
| `s` | Start, stop with confirmation, or run a focused action |
| `r` | Review and confirm a restart |
| `/` | Search logs with a regular expression |
| `x` | Toggle Combined/Single run logs |
| `[` / `]` | Move to the previous/next run |
| `v` | Open run history; filter and select with keyboard or mouse |
| `e` / `Shift+E` | Export the selected run to clipboard / a chosen file |
| `Ctrl+T` | Open the theme and appearance picker |
| `Ctrl+L` | Reload configuration |
| `?` | Open help |
| `q` | Quit, with cleanup confirmation when required |

The [complete controls reference](../reference/controls) covers selection
overrides, log navigation, health history, notifications, shell handoff, mouse
interaction, and shutdown behavior. The guides for [actions](./actions),
[lifecycle](./lifecycle), [logs and ports](./logs-and-ports), and
[appearance](./appearance) include focused recordings of those workflows.
