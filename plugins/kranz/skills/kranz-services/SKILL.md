---
name: kranz-services
description: Use existing Kranz runtimes via MCP to inspect services, logs, ports, actions, and plans, or to start, stop, restart, and handle confirmation tokens. Also use before starting a dev server, worker, Docker stack, build, test, or migration. If no matching runtime exists, use the project's normal workflow.
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
KRANZ_PROJECT=NAME kranz services info SERVICE
KRANZ_PROJECT=NAME kranz actions --output json
kranz ports inspect 3303 --output json
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

- `confirm: true` means the action requires a plan-bound confirmation token.
  Check that the user has authorized the action and its affected scope, then
  follow the MCP confirmation flow below. Do not ask for a second approval when
  the request already authorizes it.
- `interactive: true` requires a real terminal and should be run by the user in
  the TUI or terminal CLI.

Stopping or restarting a service can include its dependents. Read the resolved
plan or mutation result and report every affected service.

MCP `up` creates a background runtime with no services and requires explicit
authorization. It does not happen when the MCP server connects. MCP `down`
only stops a runtime that the same MCP session created with `up`.

## Confirm the exact MCP operation

`start`, `stop`, and `restart` require a non-empty `selectors` list of explicit
services or tags. Use the same `runtime` and `selectors` for a preview with
`plan` and for the mutation. An unscoped `plan` previews every service, but an
unscoped lifecycle mutation is rejected. For `start`, also keep
`include_dependencies` identical between preview and execution.

If an authorized mutation returns `confirmation_required`, read its resolved
plan and one-shot `confirmation_token`, check the affected targets, and repeat
that same MCP call with the token. A token from an explicit `plan` call can also
be used with the matching mutation. This protocol confirmation is not a new
request for user permission when the user has already authorized that scope.

Tokens have no time-based expiry. `confirmation_expired` means the token is
unknown or already used, or the runtime session or configuration generation
changed; `confirmation_plan_changed` means the resolved plan differs. In either
case, get a fresh plan and token, compare the affected targets with the user's
request, and retry only within the authorized scope. Do not describe either
error as a token expiring instantly without evidence.

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
