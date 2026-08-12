# Runnable examples

Every example uses only standard shell tools and Python 3, and keeps its traffic
on localhost. Run Kranz from the example directory so conventional config-file
discovery and relative commands work together.

`app/examples` is the single canonical example location. The examples do not
use Docker, SSH, credentials, external hosts, or destructive system commands.
They only open documented localhost ports; the lifecycle playground additionally
creates ignored marker files inside its own directory.

## Procfile: zero configuration

```bash
cp examples/procfile/.env.example examples/procfile/.env
cd examples/procfile
kranz
```

Press `a`, then `s`. The `web` service has no declared port; select it and open
Details to see the actual listener discovered from its process group. The
adjacent `.env` supplies the greeting and port, while existing shell variables
would take precedence.

## Native Kranz YAML: lifecycle features

```bash
cd examples/native
kranz
```

This example combines a one-shot migration, HTTP readiness, a dependent worker,
and a web server whose port is learned at runtime. Start the full stack with
`a`, then `s`.

## Prerequisites: work that must finish before a service starts

```bash
cd examples/prerequisites
kranz
```

Press `a`, then `s`. A project-level check runs before every start, a migration
runs once per session, and a second service that references the same migration
waits for that run instead of repeating it. The `gated-demo` service is
disabled on purpose: edit its `preflight` action to `exit 1`, reload with
`Ctrl+L`, and start it to see a prerequisite block a start.

## Detached lifecycle playground

```bash
cd examples/lifecycle
kranz
```

This self-contained example exercises both `command` shorthand and explicit
`lifecycle.start`, configurable start confirmation, always-confirmed detached
stops, status reconciliation (`running`, `stopped`, and `unknown`), detached log
following, readiness distinct from lifecycle status, observe-only resources,
service actions, action groups, and `process_healthy` dependencies. It only
creates marker files under `examples/lifecycle/.kranz-demo`.

Try this sequence:

1. Start `guarded-worker` and accept its configured start confirmation.
2. Start `remote-stack`; its detached start does not ask for confirmation.
3. Run `mark-ready`, then start `dependent-app` and watch the health gate pass.
4. Run `mark-unready`: the stack stays running but becomes unhealthy.
5. Run `status-unknown`, wait for two failed probes, then `status-recover`.
6. Expand `observed-resource` and use its actions to simulate external state.
7. Stop `remote-stack`; detached stop always asks for confirmation.

Run the confirmed `playground / reset` action before or after another session
to clear all demo markers.

## Process Compose compatibility

```bash
cd examples/process-compose
kranz
```

This is a small compatible Process Compose project with a readiness-gated
worker. It demonstrates trying Kranz without maintaining a second config.

## Full dependency graph

```bash
cd examples/full-stack
kranz -f kranz.yaml
```

This larger localhost-only example has a completed migration, two parallel APIs,
a gateway waiting on both readiness probes, and a worker with recovery policy.
The same directory includes `process-compose.yaml` for a side-by-side format
comparison. See its README for ports and invocation.

## Runtime port discovery lab

```bash
cd examples/runtime-ports
kranz
```

The lab shows detected-only, matching, stale, dynamic, child-process, cycling,
and multi-port scenarios, plus safe service and project actions. See its README
for the fixed and dynamically selected localhost port ranges.

## Reference configuration

`examples/reference/kranz.yaml` is not meant to be run: it is the annotated
file behind the [configuration reference](https://kranz-org.github.io/kranz/reference/kranz-yaml),
using every part of the format with a comment on each field. A test loads and
validates it, so it cannot drift from what Kranz actually accepts.
