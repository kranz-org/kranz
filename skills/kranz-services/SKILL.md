---
name: kranz-services
description: Use an existing Kranz project runtime before starting a dev server, worker, Docker stack, migration, build, or test; also use it to inspect service state, logs, ports, actions, or an already-running command. If no matching Kranz runtime exists, leave quietly and use the project's normal workflow.
---

# Work with services through Kranz

Kranz is a local service orchestrator. A developer may already have the project
running before the task starts, and that runtime should remain intact after the
task finishes. Starting another `npm run dev`, worker, or Docker stack can create
a duplicate process with unrelated logs and ports.

Prefer Kranz MCP tools over shell commands when they are available. MCP is
already connected to the runtime API and does not require direct access to its
Unix socket.

## Discover the runtime first

Inspect the complete tool registry, including lazy or deferred tools, for tools
named like `mcp__kranz__runtimes`, `mcp__kranz__status`, and
`mcp__kranz__logs`.

If `mcp__kranz__runtimes` is available:

1. Call it.
2. Select the runtime whose `directory` is equal to, or a parent of, the current
   working directory.
3. Pass its `name` as `runtime` to every runtime-scoped MCP call.

If Kranz MCP is unavailable or its transport cannot start, run exactly one
fallback discovery command:

```bash
kranz ps --output json
```

Match the current directory against `.data[].directory`, including parent
directories. If there is no matching runtime, stop applying this skill and use
the project's ordinary workflow. Do not infer Kranz usage from a configuration
file: personal Kranz configuration may live above the repository or outside
version control.

When a runtime is found, tell the user briefly that subsequent service work
will use it.

## Read without disturbing processes

Use the matching MCP tool with `runtime: NAME`:

| Need | MCP tool |
| --- | --- |
| Service state | `status` |
| Bounded logs | `logs` |
| Declared and detected ports | `ports` |
| Owner of a local port | `port_inspect` |
| Available actions | `action_list` |
| Operation plan | `plan` |
| Dependency graph | `graph` |
| Preflight checks | `doctor` |

Use CLI only as a fallback or for a surface MCP does not expose:

```bash
KRANZ_PROJECT=NAME kranz status --output json
KRANZ_PROJECT=NAME kranz logs SERVICE --tail 200
KRANZ_PROJECT=NAME kranz ports --output json
KRANZ_PROJECT=NAME kranz info SERVICE
KRANZ_PROJECT=NAME kranz action list --output json
kranz port inspect 3303 --output json
KRANZ_PROJECT=NAME kranz clients --output json
```

`-p NAME` is equivalent to the one-shot `KRANZ_PROJECT=NAME` prefix and is
appropriate when explicit command text is easier to audit.

Treat `ready: null` and `alive: null` as “no probe configured”, never as a
successful health check. Logs remain available after a process exits, so read
them before considering a restart.

## Mutate only when requested

`start`, `stop`, `restart`, `up`, `down`, `reload`, and `action_run` change the
developer's live environment. Use them only when the user explicitly asks for
that outcome.

Before a build, test, migration, or other project command:

1. Read `action_list`.
2. If Kranz declares the operation, use its action so the configured working
   directory, environment, dependencies, output retention, and ownership stay
   correct.
3. If no matching action exists, use the repository's normal command.

Before `action_run`, inspect the action metadata:

- `confirm: true` means the operation is destructive; obtain confirmation.
- `interactive: true` requires a real terminal and should be run by the user in
  the TUI or terminal CLI.

Stopping or restarting a service can include its dependents. Read the resolved
plan or mutation result and report every affected service.

MCP `up` creates a background runtime with no services and requires explicit
authorization. It does not happen when the MCP server connects. MCP `down`
only stops a runtime that the same MCP session created with `up`.

## Avoid duplicate and destructive operations

- Do not run `kranz attach`, foreground `kranz up`, or `logs --follow` from an
  agent session; they require an interactive or unbounded terminal.
- Do not start a second copy of a service that is already running, even on a
  different port.
- Do not treat an occupied port as an obstacle before checking `port_inspect`.
- Do not run `down`, `down --force`, or clear retained logs without a direct
  request.
- Do not edit Kranz configuration on the user's behalf unless the task is
  specifically about that configuration.
- Do not add Kranz instructions to an unrelated repository's README,
  `AGENTS.md`, or similar shared files. Kranz may be the developer's personal
  tool rather than a project dependency.

## Handle addressing errors

Kranz errors are structured as `error.code`, `error.message`, `error.hint`, and
`error.details`.

- On `runtime_required` or `runtime_not_found`, use a candidate from
  `details.candidates` only when it matches the task.
- Do not bypass `runtime_pinned` by dialing another address.
- If the CLI cannot open the Unix socket, check lazy MCP tools again before
  concluding the runtime is unavailable.
