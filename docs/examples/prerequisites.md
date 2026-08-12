# Prerequisites: work that must finish before a service starts

Some work has to happen before a service is worth starting: apply migrations,
build an asset bundle, bring up shared infrastructure. This example shows how
`before_start` expresses that without turning one-shot work into a fake
service.

<div class="demo-frame">

![A shared migration running once before two services start](../assets/prerequisites.gif)

</div>

## Run it

```bash
cd examples/prerequisites
kranz
```

Press `a`, then `s`, and read the order in the logs:

1. `check-tools` runs — a project-level action shared by the whole workspace.
2. `migrate` runs **once**, printing three lines.
3. `catalog-api` starts and becomes ready on port `18891`.
4. `reporting-worker` waits for the same migration, does **not** run it a
   second time, and starts after the API is healthy.

Everything is safe: the "migration" only prints messages, and both services are
localhost HTTP servers on high ports.

## Read the important configuration

```yaml
action_groups:
  environment:
    actions:
      check-tools:
        command: python3 --version

services:
  catalog-api:
    command: python3 -u ../scripts/http_service.py
    actions:
      migrate:
        command: /bin/sh ../scripts/migrate.sh
    before_start:
      - group: environment
        action: check-tools
        run: always
      - action: migrate

  reporting-worker:
    command: python3 -u ../scripts/worker.py
    depends_on: [catalog-api]
    before_start:
      - service: catalog-api
        action: migrate
```

Three things are worth noticing.

**A prerequisite is a reference, not an inline command.** `migrate` stays an
ordinary action: you can expand `catalog-api` with `Enter` and run it by hand
whenever you want. There is one definition of the command and one place to fix
it.

**`once` is per session, not per start.** The migration runs the first time
something needs it and is then considered satisfied — restarting a service does
not re-apply it. `run: always` is for checks that are cheap and whose answer
can change, like `check-tools` above.

**Two services sharing a prerequisite share one run.** `reporting-worker`
references the same action as `catalog-api`. Whichever one gets there first
runs it; the other waits for that same run instead of starting a second copy.

## Where prerequisites sit in the order

```text
dependencies ready → before_start actions → service starts → readiness probe
```

A prerequisite runs after everything the service depends on is ready, so a
migration can rely on the database being up. It runs before the service itself,
so a failed prerequisite means the service never starts.

## See a blocked start

The `gated-demo` service exists for this. It is `disabled`, so batch starts
skip it.

1. Edit its `preflight` action command to `exit 1`.
2. Press `Ctrl+L` to reload.
3. Focus `gated-demo` and press `s`.

The service stays stopped and its log explains why:

```text
[Kranz] Running prerequisite: action "preflight"
[Kranz] Prerequisite failed: action "preflight" · exited with code 1
```

Change the command back, reload, and start it again. Because that prerequisite
uses `run: always`, it is retried on every attempt; a failed `once`
prerequisite is retried too, since only success is remembered.

## Compare with dependency conditions

The [native example](./native) gates `api` on a `migrate` **service** using
`process_completed_successfully`. Both approaches work, and they answer
different questions:

| Use | When |
| --- | --- |
| `before_start` | The work is an operation. You want it out of the service list, runnable on demand, and shared between services. |
| A one-shot service with `process_completed_successfully` | The work is a node in the graph that other services should visibly wait on, with its own state in the list. |

## Cleanup

Press `a`, then `s`, and confirm. Both services are local processes owned by
Kranz, and the example writes nothing outside its own directory.

Full source:
[`examples/prerequisites/kranz.yaml`](https://github.com/kranz-org/kranz/blob/main/examples/prerequisites/kranz.yaml).
