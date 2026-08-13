# Runnable examples

These examples are small projects you can operate, not isolated configuration
fragments. They live under `examples/` and use only a shell and Python 3. They
do not contact external hosts, require credentials, or modify anything outside
their own directory.

Start from the repository root after building or installing Kranz.

<div class="example-grid">
  <a class="example-card" href="./examples/moonflight"><strong>MoonFlight showcase</strong>The whole model in one project: infrastructure, migration, APIs, gateway, workers.</a>
  <a class="example-card" href="./examples/procfile"><strong>Procfile quickstart</strong>Three commands, no YAML. Learn the interface and environment loading.</a>
  <a class="example-card" href="./examples/native"><strong>Native YAML</strong>Dependencies, a one-shot setup, readiness, and runtime ports.</a>
  <a class="example-card" href="./examples/lifecycle"><strong>Detached lifecycle</strong>Start, stop, observe, and reconnect external resources safely.</a>
  <a class="example-card" href="./examples/prerequisites"><strong>Prerequisites</strong>Migrations and setup that must finish before a service starts.</a>
  <a class="example-card" href="./examples/process-compose"><strong>Process Compose</strong>Open an existing compatible configuration directly.</a>
  <a class="example-card" href="./examples/full-stack"><strong>Full dependency graph</strong>Two APIs, a gateway, a worker, health gates, and recovery.</a>
  <a class="example-card" href="./examples/runtime-ports"><strong>Runtime ports</strong>See how Kranz discovers listeners from processes and children.</a>
</div>

## Which example should I choose?

| If you want to learn… | Use |
| --- | --- |
| How everything fits together in one project | [MoonFlight](./examples/moonflight) |
| The keyboard and basic service lifecycle | [Procfile](./examples/procfile) |
| How a real `kranz.yaml` is structured | [Native YAML](./examples/native) |
| Docker/SSH-style resources that survive Kranz | [Detached lifecycle](./examples/lifecycle) |
| Work that must succeed before a service starts | [Prerequisites](./examples/prerequisites) |
| Whether your existing Process Compose file works | [Process Compose](./examples/process-compose) |
| Dependency fan-out, fan-in, health, and recovery | [Full stack](./examples/full-stack) |
| Declared, detected, stale, and dynamic ports | [Runtime ports](./examples/runtime-ports) |

::: tip Safe by construction
The lifecycle playground simulates external resources with ignored marker files.
The other examples open documented localhost ports only. Each page includes its
cleanup instructions.
:::
