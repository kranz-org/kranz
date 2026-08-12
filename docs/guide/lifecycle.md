# Service lifecycle

Kranz supports two sources of lifecycle truth.

## Process supervision

`supervision: process` is the default. Kranz owns the command's process group,
tracks its PID, captures output, and stops it when Kranz exits.

```yaml
services:
  api:
    command: npm run dev
```

`command` is shorthand for an explicit start definition:

```yaml
services:
  guarded-worker:
    lifecycle:
      start:
        command: npm run expensive-worker
        confirm: true
```

`confirm` controls confirmation before start. Every TUI operation that stops a
service asks for confirmation, including stop, restart, and all-service
variants.

## Detached supervision

Use `supervision: detached` when the start command finishes before the external
resource stops—for example `docker compose up -d` or an SSH operation.

```yaml
services:
  remote-stack:
    supervision: detached
    stop_on_exit: false
    detect_ports: false
    lifecycle:
      start:
        command: ssh host 'cd app && docker compose up -d'
        timeout: 2m
      stop:
        command: ssh host 'cd app && docker compose stop api db redis'
        timeout: 2m
      status:
        type: command
        command: ssh host 'cd app && ./stack-status.sh'
        interval: 5s
        stopped_interval: 30s
        timeout: 15s
        failure_threshold: 3
        running_exit_codes: [0]
        stopped_exit_codes: [3]   # opt-in; see below
      logs:
        command: ssh host 'cd app && docker compose logs -f --tail=100'
```

The status command observes **existence**, not health.

By default it follows the convention every shell command already follows: exit
`0` means the resource is running, and any other exit code means it is stopped.
Most probes need nothing more than this:

```yaml
status:
  type: command
  command: docker compose ps --status running --quiet api | grep -q .
```

Declare `stopped_exit_codes` only when your probe can distinguish "not running"
from "I could not find out". That opts into a three-way contract:

- an exit code in `running_exit_codes` means `running`;
- an exit code in `stopped_exit_codes` means `stopped`;
- any other exit code is unclassified, and `failure_threshold` consecutive
  unclassified results produce `unknown`.

A probe that never produced an exit code — it could not start, timed out, or
was killed — is always unclassified. Kranz reports `unknown` rather than
claiming a resource is stopped on no evidence.

Status results cannot overwrite an in-flight `starting` or `stopping`
transition. Stopped and unknown resources are polled on the slower
`stopped_interval`, which defaults to `30s` because such a resource only
changes when something outside Kranz acts on it. Status observation never
invokes a process restart policy.

## Optional capabilities

A detached service can omit capabilities it does not own. A service with only
`lifecycle.status` is observe-only: Kranz displays its state, while start and
stop remain unavailable.

Detached services default to `stop_on_exit: false`. Set it to `true` only when
closing Kranz should execute the external stop command. Declared `ports` remain
valid endpoint documentation, but local PID discovery is unavailable and
`detect_ports: true` is rejected.

See the [lifecycle playground](../examples#detached-lifecycle-playground) for a
safe local simulation of managed and observe-only external resources.
