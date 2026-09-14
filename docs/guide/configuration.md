# Configuration

Kranz reads three formats. The same two-process project looks like this in each
of them:

::: code-group

```yaml [kranz.yaml]
project: MyProject

services:
  api:
    command: npm run dev
    dir: apps/api
    ports: [3001]
    healthcheck:
      readiness:
        type: http
        url: http://127.0.0.1:3001/ready
  web:
    command: npm run dev
    dir: apps/web
    depends_on: [api]
    dependency_conditions:
      api: {condition: process_healthy}
```

```text [Procfile]
api: npm run dev --prefix apps/api
web: npm run dev --prefix apps/web
```

```yaml [process-compose.yaml]
version: "0.5"

processes:
  api:
    command: npm run dev
    working_dir: apps/api
    readiness_probe:
      http_get: {host: 127.0.0.1, port: 3001, path: /ready}
  web:
    command: npm run dev
    working_dir: apps/web
    depends_on:
      api: {condition: process_healthy}
```

:::

A `Procfile` gets you the interface, separate logs, process-group shutdown, and
port discovery — but it cannot express dependencies or probes. A supported
`process-compose.yaml` runs as-is; see the
[compatibility matrix](../reference/process-compose). Native `kranz.yaml` is
the only format with actions, prerequisites, and detached lifecycle.

## Native configuration

Native configuration uses `kranz.yaml`:

```yaml
project: MyProject
version: "1.0"

defaults:
  dir: .
  shell: /bin/sh
  env_files: [.env.shared]

services:
  api:
    command: npm run dev
    dir: apps/api
    ports: [3001]
    tags: [backend]
    healthcheck:
      readiness:
        type: http
        url: http://127.0.0.1:3001/ready
        interval: 2s

  web:
    command: npm run dev
    dir: apps/web
    ports: [3000]
    tags: [frontend]
    depends_on: [api]
    dependency_conditions:
      api:
        condition: process_healthy
```

## Loading files

Run with auto-discovery or explicit files:

```bash
kranz
kranz -f path/to/kranz.yaml
kranz -f kranz.yaml --override kranz.local.yaml
```

Bare `kranz` searches the current directory. Use `-f` when the file has a
different name or lives elsewhere. Repeated `-f` values compose autonomous
projects into one; `--override` applies partial files on top.

Auto-discovery uses the first existing file in this order:

1. `kranz.yaml`
2. `kranz.yml`
3. `process-compose.yaml`
4. `process-compose.yml`
5. `Procfile.dev`
6. `Procfile`

Explicit `-f` sources compose independently runnable configurations, and the
repeatable `--override` option applies ordered override layers. A root file can
use `include` with exact paths, globs, or bounded discovery; without a root file
the working directory becomes a virtual project over discovered configs. See
[composing configurations](./composition).

The root and every recursive composition node use native `kranz.yaml` syntax.
`Procfile` and supported `process-compose.yaml` files are valid terminal leaves:
they contribute services but cannot declare `include` or `overrides` themselves.

Valid file changes hot-reload. New services and stopped-service changes apply
immediately. Running services keep their accepted snapshot when changed,
renamed, or removed; the pending reason is visible until an explicit restart.
Invalid changes leave the last known good runtime untouched. Press `Ctrl+L` to
reload immediately.

## Environment precedence

From lowest to highest precedence:

1. `.env` beside the configuration file that declares the service
2. `defaults.env`
3. `defaults.env_files`, in order
4. service `env_files`, in order
5. service `env`

An existing host environment value wins over the adjacent `.env`. Explicit
configuration values remain explicit overrides. References such as `$HOME` are
expanded after the layers merge.

In a composed project every file keeps its own `.env` and `defaults`: a
repository's `.env` reaches only the services that repository declares, never a
sibling's.

## Where to go next

- Every field, with types and defaults:
  [configuration reference](../reference/configuration)
- One complete annotated file:
  [annotated kranz.yaml](../reference/kranz-yaml)
- Flags, discovery, and exit codes: [CLI reference](../reference/cli)
- Several files in one project: [composing configurations](./composition)
