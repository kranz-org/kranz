# MoonFlight: the whole model in one project

MoonFlight is the project in the recording on the front page. It is a complete
imitation of a small product, so every part of the Kranz model appears in one
place instead of one feature at a time.

<div class="demo-frame">

![Starting MoonFlight: infrastructure, a migration, two APIs, a gateway, a web front end, and two workers](../assets/kranz-demo.gif)

</div>

## Run it

```bash
cd examples/moonflight
kranz
```

Press `a`, then `s`. Nothing leaves localhost, and the "infrastructure" is
marker files under `.kranz-demo`.

## What is in it

| Service | What it demonstrates |
| --- | --- |
| `shared-infra` | A [detached](../guide/lifecycle) resource that outlives the session |
| `migrate` | A one-shot service others wait to finish successfully |
| `catalog-api`, `billing-api` | Readiness, liveness, restart policy, and an action |
| `gateway` | Waiting for two dependencies to become healthy, not merely to exist |
| `moonflight-web` | A [runtime-discovered port](../guide/logs-and-ports) and a [prerequisite](../guide/actions#running-an-action-before-a-service-starts) |
| `orders-worker`, `reconciliation-worker` | Workers gated on health, with recovery |
| `toolbox` | A project-level [action group](../guide/actions#project-action-groups) |

## The order it starts in

Selecting everything and pressing `s` does not start eight things at once. The
graph decides:

1. `shared-infra` comes up first — its status probe reports it exists.
2. `migrate` runs and exits successfully.
3. `catalog-api` and `billing-api` start together, because both only needed the
   migration to finish, and become ready.
4. `gateway` waits for **both** to be healthy, not merely running.
5. `moonflight-web` builds its assets through `before_start`, then starts.
6. Both workers start once the service each one talks to is healthy.

Watch the `queued` labels in the list: they show what a service is waiting for
while it waits.

## Things worth trying

**Stop `gateway`.** Kranz includes everything that depends on it and stops the
dependents first, in reverse order.

**Quit while `shared-infra` runs.** The exit plan separates the processes that
will stop from the detached resource that will keep running, because it
declares `stop_on_exit: false`. Start Kranz again: the status probe finds the
resource and reconnects to it without running the start command.

**Open `moonflight-web` in Details.** It declares no port; the listener shown
was discovered from the running process.

**Expand `catalog-api` with `Enter`.** Its `reindex` action runs on demand and
keeps its own output, exit code, and duration.

**Run `toolbox / reset`.** It asks first, because it deletes the marker files.

## Cleanup

Press `a`, then `s`, and confirm. `shared-infra` keeps running by design; stop
it explicitly, or run `toolbox / reset`.

Full source:
[`examples/moonflight/kranz.yaml`](https://github.com/kranz-org/kranz/blob/main/examples/moonflight/kranz.yaml).
