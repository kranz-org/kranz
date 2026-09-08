# MCP reference

`kranz mcp` is a foreground stdio MCP server in the main Kranz binary. stdout
is reserved for JSON-RPC framing; server diagnostics go to stderr. It supports
MCP protocol versions `2025-11-25`, `2025-06-18`, `2025-03-26`, and
`2024-11-05`.

Every result contains `schema_version`, currently `2`. A result served by a
runtime also carries that runtime's identity in `session` and the configuration
`generation` it was read at; a global answer — `runtimes`, `up`, `down`,
`kranz://runtimes`, `kranz://capabilities` — carries neither, because no
runtime produced it. Failures use `{code, message, hint?, details?}` rather than
CLI text. Log results also contain `truncated`, `next_cursor`, and actual window
boundaries.

`session` names the runtime that **answered the call**. Before `schema_version`
2 it named what the connection was bound to; connections are no longer bound to
anything, so the old reading has no referent. `current` in the runtime listing
is gone for the same reason.

## Command

```bash
kranz mcp [-C DIR] [-f FILE] [-p NAME_OR_ID]
kranz mcp --help
```

Install the ordinary Kranz binary and register `kranz mcp` once, globally, with
no project arguments. The server starts anywhere, including a directory with no
Kranz configuration, writes nothing to the runtime registry, and supervises
nothing. Which runtime answers is decided per call.

`-C DIR`, `-f FILE`, and `-p NAME|ID` pin the server to one project for a
client that should reach exactly one. A pinned server rejects any other address
with `runtime_pinned`. `--attach-only` is a deprecated compatibility flag: it
has no effect, emits a warning, and will be removed in the next major release.

The [MCP guide](../guide/mcp) has ready-to-copy configurations and a
first-connection check.

Do not run `kranz mcp` directly in an interactive terminal to test it: stdout
is the protocol transport, not a human-readable prompt. Use the configured MCP
client or the repository's [minimal example client](../examples/mcp-shared-runtime).

## Resources

| URI | Contents |
| --- | --- |
| `kranz://session` | Identity, protocol, and generation of the runtime this read resolved to |
| `kranz://runtimes` | Registry sessions, service counts, and connected client counts (global) |
| `kranz://config` | Effective config with shared secret redaction, loader diagnostics, and provenance |
| `kranz://services` | Definitions, snapshots, and computed `primary_action` |
| `kranz://actions` | Service/group actions and current state |
| `kranz://graph` | Dependency, prerequisite, and ownership edges with live service state |
| `kranz://tags` | Shared service/tag selector index |
| `kranz://capabilities` | Exact allow-list, addressing mode, and unavailable unsafe operations (global) |

Every runtime-scoped resource has an addressed form:
`kranz://runtimes/{runtime}/config`, `.../services`, `.../session`, and so on,
published through `resources/templates/list`. The short URI reads whichever
runtime the standard resolution order picks.

## Tools

### Addressing

Every tool except `runtimes`, `up`, and `down` takes an optional `runtime`
argument naming a project by name or id. Resolution, first match wins:

1. the `runtime` argument;
2. the `-C`/`-p` pin, when the server was launched with one;
3. the working directory the MCP process was started in, resolved to a runtime
   name exactly as the CLI resolves it without `-p` — a registry lookup, never a
   creation;
4. otherwise `runtime_required`, carrying every running runtime as a candidate.

`runtimes` takes no `runtime`: listing what exists cannot be scoped to one of
the things it lists.

The working directory is fixed when the client spawns the server and does not
follow a user who moves to another project mid-session. That case lands on
`runtime_required`, whose candidates can be passed straight back as `runtime`.

### The catalogue

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

If a service or tag misses in the runtime that answered but matches another
running runtime, `selector_not_found` names that runtime in `available_in`.
Repeating the call with that `runtime` succeeds: the error carries the argument
that fixes it.

### Starting and stopping a runtime

`up` starts the runtime of a project that has none, and starts no service. It
takes `directory` (defaulting to the directory the server was started in) and
requires `confirm: true`, because the runtime it creates is a background
process that outlives the session and stays in `kranz ps` until someone stops
it. Nothing else creates a runtime: every other tool answers
`runtime_not_found` for a project that is not running, and names `up` in the
hint.

`down` stops a runtime **this MCP process started through `up`**, after
`confirm: true`. Any other runtime answers `not_owned`: a project someone is
working in is not an agent's to stop.

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

Addressing has its own codes. `runtime_required` means no call, pin, or working
directory named a runtime, and carries `candidates`. `runtime_not_found` means
the named project has no live runtime; its hint names `up`. `runtime_pinned`
means a `-C`/`-p` server was asked for a different project. `not_owned` means
`down` was asked to stop a runtime this session did not start.
`runtime_version_mismatch` fails the one call that named a runtime built from an
incompatible protocol version, and leaves every other runtime served.

The allow-list cannot reach project-wide down/force-down, `StopAll`, raw force
start/stop, shutdown, external PID/port release, arbitrary shell execution,
interactive leases, or test-only application methods.

## What is deliberately not here

`attach`, writing a configuration (`init`), interactive actions and their
terminal leases, and per-field configuration provenance (`kranz config
explain`) have no tool. Attaching is what every call already does, `init`
writes files a person should read first, interactive actions need a terminal,
and provenance is a rendering of the layered files that `kranz://config`
already delivers whole.

`up` and `down` are here, narrowly: `up` starts a runtime with no services, and
`down` reaches only what `up` created in this session. Neither can touch a
project someone else is running.
