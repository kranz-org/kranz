# Process Compose compatibility

Kranz opens a supported `process-compose.yaml` directly, so you can try it on
an existing project without maintaining a second configuration.

```bash
cd your-project
kranz                       # finds process-compose.yaml automatically
kranz -f process-compose.yaml
```

A native `kranz.yaml` in the same directory takes priority, so adding one later
is not a breaking change.

Compatibility is deliberately conservative: a feature is either translated
faithfully, ignored with a visible diagnostic, or rejected before anything
starts. Kranz never accepts a file by quietly dropping a field that changes
what your processes do.

## Top level

| Field | Support | Notes |
| --- | --- | --- |
| `version` | ✅ Translated | Kept as the project version |
| `name` | ✅ Translated | Project name; defaults to the directory name |
| `environment` | ✅ Translated | Mapping or `NAME=value` list |
| `processes` | ✅ Translated | See below |

## Process fields

| Field | Support | Maps to |
| --- | --- | --- |
| `command` | ✅ Translated | [`command`](./configuration#command) |
| `description` | ✅ Translated | [`description`](./configuration#description) |
| `working_dir` | ✅ Translated | [`dir`](./configuration#dir-shell) |
| `environment` | ✅ Translated | [`env`](./configuration#env-env-files) |
| `env_file` | ✅ Translated | [`env_files`](./configuration#env-env-files) |
| `depends_on` | ✅ Translated | [`depends_on`](./configuration#depends-on) with conditions |
| `readiness_probe` | ✅ Translated | `healthcheck.readiness` |
| `liveness_probe` | ✅ Translated | `healthcheck.liveness` |
| `ready_log_line` | ✅ Translated | [`ready_log_line`](./configuration#ready-log-line) |
| `availability` | ✅ Translated | [`availability`](./configuration#availability) |
| `shutdown` | ✅ Translated | [`shutdown`](./configuration#shutdown) |
| `success_exit_codes` | ✅ Translated | [`success_exit_codes`](./configuration#success-exit-codes) |
| `disabled`, `is_disabled` | ✅ Translated | [`disabled`](./configuration#disabled) |
| `is_dotenv_disabled` | ✅ Translated | Skips the adjacent `.env` |
| `namespace` | ⚠️ Approximated | Becomes a [tag](./configuration#tags), unless it is `default` |
| `log_location` | ⚠️ Ignored | Reported as a diagnostic; Kranz keeps logs in the interface |
| `replicas` | ❌ Rejected | Values above 1 are not supported |
| `is_daemon` | ❌ Rejected | Kranz supervises process groups it starts |
| `is_tty`, `is_interactive`, `is_foreground` | ❌ Rejected | Use a Kranz [interactive action](../guide/actions) instead |
| `schedule` | ❌ Rejected | Kranz is not a scheduler |

A rejected field fails the load with a message naming the process and the
field. An ignored field loads successfully and reports a diagnostic in the
interface.

## Dependency conditions

Both the list form and the mapping form are supported:

```yaml
processes:
  web:
    depends_on:
      api:
        condition: process_healthy
      seed:
        condition: process_completed_successfully
```

| Condition | Support |
| --- | --- |
| `process_started` | ✅ (default for the list form) |
| `process_healthy` | ✅ |
| `process_completed` | ✅ |
| `process_completed_successfully` | ✅ |
| `process_log_ready` | ✅ |

Any other condition is rejected by name.

## Probes

| Probe field | Support | Notes |
| --- | --- | --- |
| `exec.command` | ✅ | Becomes a `command` check |
| `http_get` | ✅ | Becomes an `http` check; its port is also declared |
| `http_get.host` | ✅ | Defaults to `127.0.0.1` |
| `http_get.scheme` | ✅ | Defaults to `http` |
| `http_get.path` | ✅ | Defaults to `/` |
| `http_get.headers` | ✅ | Passed through |
| `http_get.status_code` | ✅ | Defaults to `200` |
| `initial_delay_seconds` | ✅ | → `initial_delay` |
| `period_seconds` | ✅ | → `interval`, default `10s` |
| `timeout_seconds` | ✅ | → `timeout`, default `1s` |
| `failure_threshold` | ✅ | Default `3` |
| `success_threshold` | ⚠️ Approximated | Values above 1 are accepted as 1, with a diagnostic |

Exactly one of `exec` or `http_get` is required per probe.

## What you gain by converting

Compatibility mode gives you the Kranz interface over your existing file.
Moving to a native `kranz.yaml` additionally gives you:

- [actions](../guide/actions) and [`before_start`](./configuration#before-start)
  prerequisites;
- [detached lifecycle](../guide/lifecycle) for Docker and remote resources;
- runtime [port discovery](../guide/logs-and-ports) and dynamic probe targets;
- layered configuration with `-f`;
- project [appearance](../guide/appearance).

The [full-stack example](../examples/full-stack) ships the same project in both
formats, so you can compare them field by field.
