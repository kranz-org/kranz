# Coding agents and one live runtime

Kranz MCP lets a coding agent join the local runtime already open in your TUI.
The agent sees the same services, actions, readiness, ports, action-run numbers,
and bounded logs. It does not start a second development stack.

TUI, CLI, and MCP are three views of the same application API:

![TUI, CLI, and MCP clients converge on one Kranz session, application API, and supervisor](../assets/diagrams/mcp-runtime.svg)

The TUI is the interactive operator view, the CLI is the terminal and scripting
view, and MCP is the coding-agent view. None of them shells out to another.

<div class="demo-frame">

![Kranz TUI and Codex share one live runtime while the agent plans a restart, restarts the API and its dependents, and waits for readiness through MCP](../assets/mcp-shared-runtime.gif)

</div>

The recording is a real Codex session, not a scripted imitation. The Kranz TUI
on the left and the coding agent on the right are attached to one runtime. Codex
asks Kranz to resolve the restart, confirms the exact affected set, performs the
operation, and waits for readiness through MCP. The recording runs from an
isolated temporary project; setup and cleanup are hidden, and no account,
credential, home-directory path, real project, or terminal history is shown.
Its recording driver accepts only the `plan`, `restart`, and `wait` prompts for
that temporary Codex session and does not persist approval rules.

## Install and verify Kranz

The MCP bridge is part of the ordinary `kranz` binary; there is no separate
server package to install. Follow the [installation guide](./installation),
then verify that your build includes the command:

```bash
kranz version
kranz mcp --help
```

The bridge uses foreground stdio. Register it in an MCP client with an absolute
project directory and `--attach-only`, then let the client start and supervise
the command. This makes a missing TUI/background runtime a loud error rather
than creating an empty project that looks successfully connected.

## Register the server

### Codex

```bash
codex mcp add kranz -- kranz mcp -C /path/to/project --attach-only
codex mcp list
```

Codex also supports a project-scoped `.codex/config.toml`:

```toml
[mcp_servers.kranz]
command = "kranz"
args = ["mcp", "-C", "/path/to/project", "--attach-only"]
```

The Codex CLI, IDE extension, and ChatGPT desktop app on the same host share
this configuration. In an interactive Codex session, use `/mcp` to inspect the
connected server. See the [official Codex MCP setup](https://learn.chatgpt.com/docs/extend/mcp)
for client-side configuration and approval options.

### Claude Code

Run this from the project to keep the registration local to that checkout:

```bash
claude mcp add --scope local kranz -- kranz mcp -C /path/to/project --attach-only
claude mcp get kranz
```

### OpenCode

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "kranz": {
      "type": "local",
      "command": ["kranz", "mcp", "-C", "/path/to/project", "--attach-only"],
      "enabled": true
    }
  }
}
```

Check it with `opencode mcp list`. Use absolute paths when the client does not
inherit the same `PATH` as your shell.

## Check the first connection

Open the configured client in that project and ask:

> Which Kranz services are running, and which are not ready?

The client should discover Kranz resources and tools without you naming
protocol calls. While the connection is active, `kranz ps` shows the selected
runtime. If the TUI already owned it, the MCP session reports
`connection_mode: attached`; closing the client leaves the TUI and services
running.

## Server, project, and runtime names

The same setup exposes three related names, each with a different job:

- `kranz` in `mcp_servers.kranz`, `mcp add kranz`, or the OpenCode key is the
  MCP client's local alias for this connection;
- `project: "Harness"` is the display title from `kranz.yaml`;
- `harness` in `kranz ps` is the runtime name. It comes from `runtime.name`
  when that field is set, otherwise from a stable lower-case slug of `project`.

The `mcp` value in the `MODE` column is not another project name. It says that
the process which owns this runtime is an MCP bridge. Another user's runtime is
named from that user's selected project configuration, not from their account
name and not automatically `kranz`.

Each MCP connection has one fixed runtime binding. Use the `runtimes` tool (or
`kranz://runtimes`) before interpreting an empty status or a missing selector:
it lists other sessions and marks the current one. A cross-runtime
`selector_not_found` also reports `available_in` when, for example, `im-core`
exists in `myclass` while this connection is bound to `harness`.

Global project selection is unchanged. `kranz -C DIR mcp`, `kranz -f FILE mcp`,
and `kranz -p NAME_OR_ID mcp` use the same config discovery and runtime registry
as the CLI. Global flags can appear before or after `mcp`.
Run `kranz mcp --help` for the selected build's project-selection options.

`-C DIR` discovers the project at that directory; without `--attach-only` it
may create and own the runtime when none exists. `-p NAME|ID` binds by runtime
identity and never creates one. For an agent that moves between projects, use
separate named registrations pinned with `-p`, rather than relying on the
client's startup working directory to follow the conversation.

Try prompts that describe an outcome rather than protocol calls:

- Which services are not ready, and why?
- Show the latest failed `api/migrate` run.
- Restart `api` and wait until it is ready; do not stop the rest of the stack.
- What changed in the environment since I edited the handler?
- I added a service to kranz.yaml — reload and start it.

## Attach-first ownership

When a compatible runtime exists, MCP reports `connection_mode: attached`.
Closing that MCP process closes only its IPC connection: the TUI and services
keep running. If no runtime exists for the configured project, MCP creates one
foreground owner runtime; closing that owner performs normal managed-process
cleanup. Ambiguous, incompatible, stale, or unreachable registry entries fail
instead of creating a competing supervisor.

An owner fallback is explicit in `kranz://session` as
`owner_reason: created_missing_runtime`, together with a hint when other
runtimes were already live. `--attach-only` disables that fallback and prints
the available runtimes instead.

`kranz down` against an MCP-owned runtime waits until the old session has left
the registry before reporting `Stopped`. It also disconnects every attached MCP
bridge cleanly. An MCP client may supervise its configured child process and
start a new bridge after that intentional EOF. If it does, `kranz ps` shows a
new session ID: that is the client applying its desired configuration, not
Kranz resurrecting the stopped session. Disable or remove the MCP connection in
that client when the project must stay down. Seeing the same session ID after a
successful `down` would be a lifecycle failure.

The path is the same as the CLI:

```text
MCP tools → registry → IPC client → application API → supervisor
```

## Actions and exact runs

Actions keep monotonic run numbers. `action_result` accepts an absolute run or
a negative offset such as `-1`; `logs` uses the same identity. Interactive
actions return `interactive_action` and must be run in the TUI or terminal CLI.

Services are numbered the same way: every start opens a run, so `logs` with
`run: -1` on a service reads the newest start on its own, without the agent
reconstructing a time range around a restart.

## What changed since I last looked

The question a coding agent asks after editing code is not "what is the state"
but "what did my change do to the environment". `status` cannot answer it: a
service that crashed and was restarted looks exactly like one that never
moved. `changes` answers it directly.

```text
1. changes {}                    → cursor 41
2. edit code, or run an action
3. changes {"since": 41}         → api restarted · api ports 8080 -> 8081
                                   · api/migrate #3 failed · exit 1
```

Anything that carries a cursor hands one back, so the loop closes: `wait`
returns the cursor it finished at, and passing the cursor you held *before* the
wait to `changes` replays what happened during it. When the bounded journal has
already dropped part of the answer, the result says `truncated: true` rather
than quietly returning a shorter story.

## Why a service is not running

A stopped service is not a reason. `status` carries a structured `cause` when
the reason is not the state itself:

```json
{ "name": "api", "state": {"status": "stopped", "cause": {
    "type": "prerequisite_failed", "action": "api/migrate", "action_run": 3 }}}
```

`dependency_failed` names the dependency and its exit code, `port_conflict`
names the port and the process holding it, and `exited` carries the exit code.
For probes, `health` reports the target that was contacted and the error it
returned, so a readiness failure reads as `connection_refused` at a named URL
rather than as a boolean. `port_inspect` answers "who took my port" for any
port, including processes Kranz does not manage.

## Reproduce the shared-runtime proof

The [MCP shared-runtime example](../examples/mcp-shared-runtime) performs live
stdio calls and includes the exact commands behind the recording. It does not
print fixture results. The CLI and MCP client report one session identity, and
reading an action result twice does not execute it again.

For protocol schemas, resources, tools, cursors, confirmation, and error codes,
see the [MCP reference](../reference/mcp).
