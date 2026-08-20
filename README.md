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

It runs in the foreground without a daemon or control plane. Use it for the
processes you would otherwise spread across terminal tabs, alongside Docker
Compose when containers remain the right home for infrastructure.

<p align="center">
  <img src="docs/assets/kranz-demo.gif" alt="Kranz terminal interface">
</p>

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

## Command line

The TUI is optional. Kranz 0.8 adds a complete CLI for starting a project in
the background, inspecting it from another terminal, acting on services, and
returning stable JSON to scripts:

```bash
kranz init --from Procfile
kranz config check
kranz up -d
kranz status
kranz logs api --tail 20
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
- Procfile, native YAML, and conservative Process Compose loading
- Live configuration reload with last-known-good fallback

## Documentation

- [Getting started](https://kranz-org.github.io/kranz/guide/getting-started)
- [Working from the command line](https://kranz-org.github.io/kranz/guide/cli-workflow)
- [Configuration and lifecycle](https://kranz-org.github.io/kranz/guide/configuration)
- [Configuration reference](https://kranz-org.github.io/kranz/reference/configuration)
- [Annotated kranz.yaml](https://kranz-org.github.io/kranz/reference/kranz-yaml)
- [CLI reference](https://kranz-org.github.io/kranz/reference/cli)
- [Keyboard controls](https://kranz-org.github.io/kranz/reference/controls)
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
