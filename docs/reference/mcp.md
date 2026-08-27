# MCP reference

`kranz mcp` is a foreground stdio MCP server in the main Kranz binary. stdout
is reserved for JSON-RPC framing; server diagnostics go to stderr. It supports
MCP protocol versions `2025-11-25`, `2025-06-18`, `2025-03-26`, and
`2024-11-05`.

Every resource and tool result contains `schema_version`, config `generation`,
and session identity. Failures use `{code, message, hint?, details?}` rather
than CLI text. Log results also contain `truncated`, `next_cursor`, and actual
window boundaries.

## Command

```bash
kranz mcp [-C DIR] [-f FILE] [-p NAME_OR_ID] [--attach-only]
kranz mcp --help
```

Install the ordinary Kranz binary, then register this command as a local stdio
server in the coding-agent client. Use `--attach-only` for agent registrations:
it refuses to create a missing runtime and names the runtimes that are already
running. Use an absolute `-C` path for a project-scoped client, or `-p NAME|ID`
to bind a connection to a known live runtime from any directory. The [MCP
guide](../guide/mcp) has ready-to-copy configurations and a first-connection
check.

Do not run `kranz mcp` directly in an interactive terminal to test it: stdout
is the protocol transport, not a human-readable prompt. Use the configured MCP
client or the repository's [minimal example client](../examples/mcp-shared-runtime).

## Resources

| URI | Contents |
| --- | --- |
| `kranz://session` | Runtime/session identity, protocol, ownership, generation |
| `kranz://runtimes` | Registry sessions, service counts, and the current fixed binding |
| `kranz://config` | Effective config with shared secret redaction, loader diagnostics, and provenance |
| `kranz://services` | Definitions, snapshots, and computed `primary_action` |
| `kranz://actions` | Service/group actions and current state |
| `kranz://graph` | Dependency, prerequisite, and ownership edges with live service state |
| `kranz://tags` | Shared service/tag selector index |
| `kranz://capabilities` | Exact allow-list and unavailable unsafe operations |

## Tools

Observation tools are `runtimes`, `status`, `changes`, `plan`, `graph`, `ports`,
`port_inspect`, `logs`, `wait`, `health`, `doctor`, `action_list`,
`action_info`, and `action_result`. Mutations are explicit: `start`, `stop`,
`restart`, `action_run`, `action_cancel`, `run_delete`, `logs_clear`, and `reload`. `run_delete`
accepts one absolute target and run number, refuses active runs, and requires
`confirm: true` before removing the completed summary and retained output. There is
no toggle or generic application-method dispatcher.

Every tool declares an `outputSchema` for the result envelope, and returns the
envelope as `structuredContent` as well as JSON text.

Selectors resolve an exact service first and then a case-insensitive tag.
Actions always use `OWNER/ACTION`. `start` defaults
`include_dependencies` to true; false keeps the resolved exact targets.
Lifecycle mutations require at least one explicit selector; an omitted or empty
list cannot turn an agent request into a project-wide start, stop, or restart.

One MCP connection remains bound to one runtime. The `runtimes` tool and
`kranz://runtimes` resource show every registry session visible to the user and
flag the current binding. If a service/tag misses here but matches another
running runtime, `selector_not_found` names that runtime in `available_in`
instead of letting the caller conclude the service is globally unavailable.

`logs` accepts `tail` (maximum 1000), `since` as RFC3339 or a duration,
`sources`, `run`, `runs`, `with_actions`, and an opaque cursor. The server
default is a bounded 200-event service tail; an action defaults to its latest
retained run. Source filtering happens before tail. Every start of a service is
a numbered run as well, so `run: -1` on a service reads only its newest start.

`changes` answers what happened rather than where things ended up, which the
difference between two `status` results cannot: a service that restarted and
came back looks unchanged in a diff. It returns service state transitions,
detected-port changes, action runs, and configuration reloads, oldest first,
after a `since` cursor — or after `since_generation`, for a caller that knows
only which configuration generation it last saw. Every result carries the
`cursor` to pass next time, and `truncated` reports that the bounded journal
had already dropped part of the answer. `wait` returns the same cursor, so
"what happened while I waited" is one follow-up call.

`status` includes the structured `cause` of a state whose reason is not the
state itself: `prerequisite_failed` (with the action and its run number),
`port_conflict`, `dependency_failed`, `start_failed`, or `exited`. `health`
adds the probe target and the last error each probe returned, with the recorded
history behind `history: true`.

`graph` and `kranz://graph` return the same structure: nodes for services,
action groups, and actions, and edges typed `dependency` (with its condition),
`prerequisite` (with its run policy), and `owns`.

`port_inspect` identifies the listener on explicit port numbers and says
whether it is a Kranz-managed service or a foreign process. `doctor` runs the
same preflight checks as `kranz doctor`, and `reload` re-reads the
configuration after an agent has edited it.

`wait` accepts one or more selectors, one of `ready`, `running`, `stopped`,
`healthy`, or `unhealthy`, and an optional duration such as `"60s"`. The
runtime enforces that duration itself, so a timeout is reported as
`wait_timeout` with the services it was still waiting for, never as a
cancellation. Cancellation stops only the wait. It does not stop a service or
action.

## Confirmation and errors

A mutation whose resolved plan requires confirmation returns
`confirmation_required` with the exact plan and a one-shot token. Repeat the
same call with `confirmation_token`. The supervisor rejects a used token or a
token whose session, generation, or resolved plan changed.

Stable causal codes include `selector_not_found`, `service_unavailable`,
`action_not_found`, `action_run_not_found`, `action_run_evicted`,
`interactive_action`, `confirmation_required`, `confirmation_expired`,
`confirmation_plan_changed`, `action_busy`, `action_failed`,
`action_timed_out`, `action_cancelled`, `wait_timeout`, `wait_cancelled`,
`run_not_found`, `run_active`, `run_not_retained`,
`dependency_blocked`, `terminal_failure`, `port_conflict`, `prerequisite_failed`,
`no_run_streams`,
`invalid_cursor`, `invalid_change_query`, and `invalid_change_kind`.

`service_unavailable` is the runtime declining to answer for a service the
configuration still declares — a reload race, or an attached runtime that went
away mid-call.

`owner_reason: created_missing_runtime` means this MCP process used the
non-attach fallback because its selected runtime did not exist. The session
resource also names other runtimes that were already running. Agent
registrations should normally use `--attach-only`; omit it only when the MCP
process is deliberately allowed to own and clean up a new stack.

The allow-list cannot reach project-wide down/force-down, `StopAll`, raw force
start/stop, shutdown, external PID/port release, arbitrary shell execution,
interactive leases, or test-only application methods.

## What is deliberately not here

Creating or destroying runtimes (`up`, `down`, `attach`), writing a
configuration (`init`), interactive actions and their terminal leases, and
per-field configuration provenance (`kranz config explain`) have no tool. The
first three change what a runtime is rather than what it is doing, the fourth
needs a terminal, and the last is a rendering of the layered files that
`kranz://config` already delivers whole.
