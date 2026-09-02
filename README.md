<p align="center">
  <img src="docs/assets/logo.svg" width="190" alt="Kranz logo">
</p>

<h1 align="center">Kranz</h1>

<p align="center">
  <strong>A keyboard-first local service orchestrator with a focused terminal UI.</strong>
</p>

<p align="center">
  <a href="https://github.com/kranz-org/kranz/actions/workflows/ci.yml"><img src="https://github.com/kranz-org/kranz/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/kranz-org/kranz/actions/workflows/docs.yml"><img src="https://github.com/kranz-org/kranz/actions/workflows/docs.yml/badge.svg" alt="Documentation"></a>
  <a href="https://github.com/kranz-org/kranz/releases"><img src="https://img.shields.io/github/v/release/kranz-org/kranz?display_name=tag&amp;sort=semver" alt="GitHub release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/kranz-org/kranz" alt="MIT license"></a>
</p>

Kranz starts, observes, and stops a local development stack from one terminal.
It understands dependency order, readiness and liveness, process groups,
runtime ports, logs, one-shot actions, and detached infrastructure whose life is
not tied to a local PID.

Each project runtime is owned by a separate user-level supervisor; the TUI,
CLI, and MCP connect to it as clients. There is no system service or shared
control plane. Use it for the processes you would otherwise spread across
terminal tabs, alongside Docker Compose when containers remain the right home
for infrastructure.

<p align="center">
  <img src="docs/assets/kranz-demo.gif" alt="Kranz v0.12.0 terminal interface starting a project and browsing retained action runs">
</p>

## TUI, CLI, and MCP

Kranz exposes each runtime through three views:

- **TUI** — the keyboard-first operator view for services, details, logs,
  lifecycle plans, actions, health history, and notifications;
- **CLI** — the terminal and scripting view, including stable JSON results;
- **MCP** — the coding-agent view over the same application operations.

They use the same supervisor. A service restarted by a command or coding agent
changes immediately in the open TUI; none of these views starts a second stack.

### Coding agents join your live runtimes

Kranz MCP gives a coding agent the same services, actions, readiness, ports,
bounded logs, and numbered run history visible in the TUI. It attaches to the
existing runtimes instead of starting a second development stack.

- **Shared runtimes** — TUI, CLI, and MCP converge on the supervisor of the
  project addressed by each call.
- **One view of the project** — the same selectors, action runs, and logs.
- **Clear ownership** — disconnecting an attached agent does not stop the stack.

Register the existing binary once, globally, as a stdio MCP server running
`kranz mcp`. It takes no project: the runtime is chosen per call, so one
registration covers every project, and connecting creates nothing.

See [Coding agents and your live runtimes](https://kranz-org.github.io/kranz/guide/mcp)
for ownership behavior and examples, and the [MCP reference](https://kranz-org.github.io/kranz/reference/mcp)
for the exact resource, tool, cursor, confirmation, and error contracts.

## Quick start

Install on macOS or Linux:

```bash
brew install kranz-org/tap/kranz
```

Or with Go 1.24 or newer:

```bash
go install github.com/kranz-org/kranz/cmd/kranz@latest
```

Create a `Procfile`:

```text
web: python3 -u -m http.server 8000 --bind 127.0.0.1
worker: while true; do date; sleep 2; done
```

Run `kranz`, press `a` to select everything, then `s` to start. Kranz discovers
the web listener automatically and shows both services' state and logs.

Already using a supported `process-compose.yaml`? Run `kranz` beside it. Use
native `kranz.yaml` when you need the complete lifecycle model.

### Command line

The TUI is optional. Kranz includes a complete CLI for starting a project in
the background, inspecting it from another terminal, acting on services, and
returning stable JSON to scripts:

```bash
kranz init --from Procfile
kranz config check
kranz up -d
kranz status
kranz logs api --tail 20
kranz runs api
kranz logs api --run -1
kranz restart api
kranz down
```

Use `kranz --help` to discover commands, `kranz COMMAND --help` for command
options, and `--output json` for the versioned machine-readable envelope. See
[Working from the command line](https://kranz-org.github.io/kranz/guide/cli-workflow)
for a complete session and the [CLI reference](https://kranz-org.github.io/kranz/reference/cli)
for every command, option, output contract, and exit code.

## What it handles

- Dependency-aware startup and reverse-order shutdown
- HTTP, TCP, and command readiness/liveness checks
- Process recovery with backoff and restart limits
- Managed and observe-only detached resources with start/stop/status/logs
- Service actions and project action groups with timeout and confirmation
- Prerequisites that must succeed before a service starts
- Runtime port discovery and ownership-aware conflict handling
- Searchable, pinnable, timestamped logs in a keyboard and mouse TUI
- Bounded per-service and per-action run history with provenance, exact output
  retention state, navigation, deletion, and export
- Procfile, native YAML, and conservative Process Compose loading
- Live configuration reload with last-known-good fallback

## Documentation

- [Getting started](https://kranz-org.github.io/kranz/guide/getting-started)
- [TUI · Terminal interface](https://kranz-org.github.io/kranz/guide/tui)
- [CLI · Command line](https://kranz-org.github.io/kranz/guide/cli-workflow)
- [MCP · Coding agents](https://kranz-org.github.io/kranz/guide/mcp)
- [Configuration and lifecycle](https://kranz-org.github.io/kranz/guide/configuration)
- [Configuration reference](https://kranz-org.github.io/kranz/reference/configuration)
- [Annotated kranz.yaml](https://kranz-org.github.io/kranz/reference/kranz-yaml)
- [CLI reference](https://kranz-org.github.io/kranz/reference/cli)
- [MCP server reference](https://kranz-org.github.io/kranz/reference/mcp)
- [Keyboard shortcuts](https://kranz-org.github.io/kranz/reference/controls)
- [Troubleshooting](https://kranz-org.github.io/kranz/guide/troubleshooting)
- [Runnable examples](examples/)

The documentation source lives in [`docs/`](docs/) and is deployed to
[`kranz-org.github.io/kranz/`](https://kranz-org.github.io/kranz/) from `main`.

## Development

```bash
make verify
make lint
npm install
npm run docs:dev
```

Release instructions are in [docs/RELEASING.md](docs/RELEASING.md).

## License

[MIT](LICENSE)
