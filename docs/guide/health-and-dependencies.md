# Health and dependencies

## Readiness and liveness

Each probe declares its own type: `http`, `tcp`, or `command`.

```yaml
healthcheck:
  readiness:
    type: http
    url: http://127.0.0.1:3000/ready
    interval: 2s
    timeout: 1s
    failure_threshold: 10
  liveness:
    type: tcp
    port: 3000
    initial_delay: 10s
    interval: 15s
    timeout: 2s
    failure_threshold: 3
```

Readiness gates dependent startup. Liveness describes a running service's
ongoing health. Lifecycle status for a detached resource is separate: a
resource may exist while readiness is still failing.

## Dependency conditions

```yaml
services:
  api:
    command: npm run dev
    depends_on: [database, migrate]
    dependency_conditions:
      database:
        condition: process_healthy
      migrate:
        condition: process_completed_successfully
```

Supported conditions:

| Condition | Satisfied when |
| --- | --- |
| `process_started` | The process started, or detached status observed running |
| `process_healthy` | Readiness passed; lifecycle status alone is insufficient |
| `process_completed` | The command exited |
| `process_completed_successfully` | The command exited with a successful code |
| `process_log_ready` | `ready_log_line` matched captured output |

Starting a target includes its transitive dependencies. Stopping a target
includes transitive dependents in reverse order. `Shift+S` deliberately bypasses
graph expansion and operates on the selected targets only.

## Recovery

Process-supervised services can configure restart behavior:

```yaml
availability:
  restart: on_failure
  backoff: 2s
  max_restarts: 5
```

Detached status changes do not trigger automatic restart. An external resource
may have been intentionally stopped outside Kranz, so observation remains
read-only until the user explicitly starts it.
