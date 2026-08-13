# Logs and ports

## Logs

<div class="demo-frame">

![Filtering a service log with a regular expression](../assets/log-search.gif)

</div>

Kranz captures stdout and stderr for process services and actions. Detached
services can provide `lifecycle.logs.command`, usually a following command such
as `docker compose logs -f`. Its process is managed independently from the
short-lived lifecycle start and stop commands.

The log panels support:

- regex filter and highlight modes;
- next and previous match navigation;
- wrapping and captured-at timestamps;
- pause/follow mode and unread counters;
- a pinned service above the currently focused log panel.

## Declared ports

```yaml
services:
  web:
    command: npm run dev
    ports: [3000]
```

Declared ports are checked before start. When a listener is already occupied,
Kranz identifies whether it belongs to another managed service or an external
process. An external process is only signalled after an explicit action and a
fresh ownership check.

## Runtime discovery

Without `ports`, discovery defaults on. Kranz scans listeners owned by the
service process group, including child processes. With declared ports,
discovery defaults off unless `detect_ports: true` is set.

```yaml
services:
  web:
    command: npm run dev
    detect_ports: true
    ports: [3000]
```

Details distinguishes `declared`, `detected`, and `declared · listening`.
Discovery uses `lsof` on macOS and `ss` on Linux.

## Dynamic health targets

A TCP check without a static port can use a detected listener. An HTTP URL
without an explicit port can do the same:

```yaml
healthcheck:
  readiness:
    type: http
    url: http://127.0.0.1/ready
    detected_port_index: 0
```

When multiple listeners exist, set `detected_port_index` explicitly. The index
addresses the sorted, deduplicated runtime port list.
