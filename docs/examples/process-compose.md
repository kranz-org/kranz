# Process Compose compatibility

If a project already has `process-compose.yaml`, try Kranz before maintaining a
second configuration. Kranz imports the supported process, dependency,
environment, namespace, shutdown, and probe fields.

## Run it

```bash
cd examples/process-compose
kranz
```

Kranz discovers `process-compose.yaml` automatically. Start `worker`; Kranz
also starts `api`, imports its HTTP readiness probe, and waits for it before
starting the worker.

## Source configuration

```yaml
version: "0.5"
name: Example Process Compose Stack

processes:
  api:
    command: python3 -u -m http.server 18211 --bind 127.0.0.1
    namespace: backend
    readiness_probe:
      http_get:
        host: 127.0.0.1
        scheme: http
        path: /
        port: 18211
      period_seconds: 1
      timeout_seconds: 1

  worker:
    command: while true; do echo "processed compatible job"; sleep 3; done
    namespace: worker
    depends_on:
      api:
        condition: process_healthy
```

## What to inspect

- Tags come from Process Compose namespaces.
- The API shows readiness in Details.
- Starting the worker includes the API automatically.
- Stopping the API includes the worker in reverse order.

## Know the boundary

Kranz supports a deliberate subset rather than silently guessing every Process
Compose feature. Unsupported structures produce validation errors with their
configuration path. Use native YAML when you need actions, appearance,
detached lifecycle, or Kranz-specific port behavior.

See the [compatibility reference](../reference/process-compose) for the field
matrix.

## Cleanup

Select both services, press `s`, and confirm. The only listener is localhost
port `18211`.
