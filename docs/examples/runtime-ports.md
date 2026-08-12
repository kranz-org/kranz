# Runtime-port laboratory

Port numbers in configuration are useful, but the listener a process actually
opens is the stronger fact. This laboratory puts all port cases side by side.

<div class="demo-frame">

![Details filling in listeners discovered from running processes](../assets/runtime-ports.gif)

</div>

## Run it

```bash
cd examples/runtime-ports
kranz
```

Start one service at a time at first; the names describe the scenario.

## Scenarios

| Service | What to look for in Details |
| --- | --- |
| `auto-detected` | Listener `18301` appears without a `ports` field |
| `configured-and-detected` | `18302 declared · listening` appears once |
| `different-runtime-port` | Stale declaration `18399` stays distinct from listener `18303` |
| `dynamic-http-health` | Python chooses a port and readiness follows it |
| `configured-only` | `18304` remains a declaration; no process listens there |
| `subprocess-listener` | A child process listener maps back to its Kranz service |
| `cycling-listener` | Port `18307` appears and disappears while the process stays alive |
| `two-dynamic-health-ports` | Readiness and liveness select different detected ports |
| `detection-disabled` | Listener exists, but explicit opt-out keeps it hidden |

## Start with automatic discovery

Focus `auto-detected`, press `s`, then open Details. Kranz scans listeners owned
by the service process group and its children. On macOS it uses `lsof`; on Linux
it uses `ss`.

Expand the service and run `check-http`. This is a service action: it fetches
the endpoint once and records `HTTP 200` separately from the service logs.

## Dynamic health target

This service asks Python to bind any free port:

```yaml
dynamic-http-health:
  command: python3 -u -m http.server 0 --bind 127.0.0.1
  healthcheck:
    readiness:
      type: http
      url: http://127.0.0.1/
```

Because the URL omits a port, the readiness checker waits for listener
discovery and inserts the detected port. This is useful for test servers and
tools that allocate ports dynamically.

## Project actions

Expand `example-tools` beneath the service list:

- `describe` demonstrates inherited directory and environment;
- `list-files` captures a bounded command result;
- `confirm-demo` demonstrates confirmation without changing project state.

## Cleanup

Press `a`, then `s`, and confirm. The example opens only localhost ports in the
`18301–18307` range plus dynamically allocated loopback ports.

Full source and expected cases are also documented in
[`examples/runtime-ports/README.md`](https://github.com/kranz-org/kranz/blob/main/examples/runtime-ports/README.md).
