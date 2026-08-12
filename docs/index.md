---
layout: home

hero:
  name: Kranz
  text: Your local services, under control
  tagline: Start dependency graphs, observe health and ports, run one-shot actions, and manage detached infrastructure from one keyboard-first terminal UI.
  image:
    light: /logo-light.svg
    dark: /logo.svg
    alt: Kranz service network logo
  actions:
    - theme: brand
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: Configuration
      link: /guide/configuration
    - theme: alt
      text: View on GitHub
      link: https://github.com/kranz-org/kranz

features:
  - title: Lifecycle, not terminal tabs
    details: Dependency-aware start and reverse-order stop for local processes and detached external resources.
  - title: State you can trust
    details: Separate readiness, liveness, lifecycle status, runtime port discovery, and explicit unknown state.
  - title: Operations beside services
    details: Run migrations, builds, checks, and project actions with captured output, timeout, and confirmation.
  - title: Bring your existing config
    details: Start with a Procfile, a supported Process Compose file, or native Kranz YAML when you need the full model.
---

<div class="demo-frame">

![Kranz terminal interface](./assets/kranz-demo.gif)

</div>

## Kranz in one minute

Your project probably has a web app, an API, a worker, a database, and a few
commands you run by hand. Kranz turns those pieces into one local workspace:

1. open Kranz in the project directory;
2. select the service you need;
3. press `s`;
4. watch dependencies start in order and logs arrive in one place;
5. press `s` again to stop, with confirmation.

Kranz runs in the foreground. It is not a container runtime, a deployment
platform, or a daemon that keeps changing your machine after you leave.

::: tip New here?
Start with [What is Kranz?](./guide/what-is-kranz), then follow the
[five-minute quickstart](./guide/getting-started). No YAML is required.
:::

## Pick the path that matches your project

| You already have | Start here |
| --- | --- |
| A few shell commands | [Procfile quickstart](./examples/procfile) |
| Services with dependencies and health checks | [Native YAML example](./examples/native) |
| A `process-compose.yaml` | [Process Compose example](./examples/process-compose) |
| Docker Compose or remote infrastructure | [Detached lifecycle example](./examples/lifecycle) |
| A larger API/worker graph | [Full-stack example](./examples/full-stack) |
| Migrations or setup that must run first | [Prerequisites example](./examples/prerequisites) |
| Processes that choose ports at runtime | [Runtime-port laboratory](./examples/runtime-ports) |

## What Kranz keeps together

- **Services** are long-running things such as an API, Vite server, or worker.
- **Dependencies** describe what must start first and when it is actually ready.
- **Actions** are one-shot jobs such as lint, test, build, migrate, or inspect.
- **Health checks** distinguish “the process exists” from “the service is ready.”
- **Detached lifecycle** lets Kranz operate external resources without owning
  their PID, while an optional status probe reconnects state across sessions.

See the [core concepts](./guide/core-concepts) for a picture of how these pieces
fit together, or jump directly to the [runnable examples](./examples).
