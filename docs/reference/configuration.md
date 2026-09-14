# Configuration reference

Every field of the native `kranz.yaml` format, with its type, default, and a
usable example. For one complete file with all of it in context, see the
[annotated configuration](./kranz-yaml).

Durations are Go duration strings: `500ms`, `5s`, `2m`, `1h30m`. A bare number
is not a valid duration.

[[toc]]

## Root

```yaml
project: Northstar
version: "1.0"
runtime: {}
defaults: {}
include: []
overrides: []
protected: {}
services: {}
action_groups: {}
ui: {}
```

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| [`project`](#project) | string | — | **Required.** Project name shown in the header |
| [`version`](#version) | string | — | Free-form version label for your own use |
| [`runtime`](#runtime) | map | `{}` | Stable runtime addressing options |
| [`defaults`](#defaults) | map | `{}` | Execution context inherited by every service |
| [`include`](#composition) | list | `[]` | Autonomous local configurations to compose |
| [`overrides`](#override-layers) | string list | `[]` | Ordered partial files applied to this autonomous config |
| [`protected`](#protected-values) | map | `{}` | Final values enforced over this config and its descendants |
| [`services`](#services) | map | `{}` | Long-running processes and detached resources |
| [`action_groups`](#action-groups) | map | `{}` | Project-level one-shot commands |
| [`ui`](#ui) | map | `{}` | Appearance for this project |

A configuration needs at least one service or one action group.

### project

**Type:** string · **Required**

Displayed in the header and used to identify the project.

```yaml
project: Northstar
```

### version

**Type:** string · **Default:** none

A label for your own bookkeeping. Kranz neither validates nor interprets it.
Quote it, or YAML reads `1.0` as a number.

```yaml
version: "1.0"
```

### runtime

**Type:** map · **Default:** `{}`

Controls the stable name used to address a live runtime from another terminal,
an MCP client, or a script. When omitted, Kranz derives a lowercase slug from
`project`.

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `name` | string | project slug | Runtime name shown by `kranz ps` and accepted by `-p` or `KRANZ_PROJECT` |

```yaml
project: Northstar
runtime:
  name: northstar-dev
```

### defaults

**Type:** map · **Default:** `{}`

Execution context inherited by every service that does not set its own value.
Only these four fields exist; `defaults` cannot set commands, probes, or ports.

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `dir` | string | config file directory | Working directory for commands |
| `shell` | string | `/bin/bash` | Shell used to run commands |
| `env` | map | `{}` | Environment variables |
| `env_files` | string list | `[]` | Dotenv files, applied in order |

```yaml
defaults:
  dir: .
  shell: /bin/sh
  env:
    NODE_ENV: development
  env_files: [.env.shared]
```

Relative `dir` values resolve against the directory of the configuration file,
not the directory you started Kranz in.

## Composition

Every included file is a complete, independently runnable configuration. Its
`project`, `ui`, and `defaults` remain local; only its services and action groups
are exported. Relative paths and the adjacent `.env` are resolved from that
file's directory before it joins the effective graph.

```yaml
project: Workspace
include:
  - path: repositories/catalog/kranz.yaml
  - glob: repositories/*/kranz.yaml
  - discover:
      root: tools
      max_depth: 3
      follow_symlinks: false
    max_depth: 2
```

An include entry sets exactly one of `path`, `glob`, or `discover`. Exact paths
must exist. A glob that matches nothing fails with its own `config_glob_empty`
error rather than the missing-file error, so a typo is easy to tell apart.
Remote sources (URLs) are rejected. Glob and discovery results are sorted by
normalized path, then canonical paths are deduplicated: a file reached twice,
for example through two includes of a shared stack, loads once and is reported
as `source_deduplicated`. An include cycle fails with `config_include_cycle` and
the chain of relative paths.

Two different limits apply. `discover.max_depth` bounds the filesystem walk.
The entry-level `max_depth` bounds the include graph below the files that entry
selects: `max_depth: N` lets N further include levels load, and a file with no
budget left still loads, but its own includes are truncated and reported as
`include_depth_truncated`. `max_depth: 0` therefore loads the selected files and
none of their includes. Without `max_depth`, include depth is unlimited.

The selector can resolve a native Kranz file, a `Procfile`, or a supported
`process-compose.yaml`. Native files may recursively include more sources.
Procfile and Process Compose sources are terminal leaves: they contribute their
services with paths resolved beside the leaf, but cannot declare Kranz
`include` or `overrides`. A later composition-wide `--override` may still patch
their effective services. The conventional `process-compose.override.yaml` is
loaded with its Process Compose base for compatibility.

Discovery does not follow file or directory symlinks by default. Explicitly
including a symlink is allowed. Opt in per discovery block or globally with
`--follow-symlinks`. Hidden and dependency directories are not implicitly
ignored; choose a narrow root or depth when that matters.

If the working directory has no conventional root file, Kranz discovers
autonomous configs below it and creates a virtual project named after the
directory. If discovery finds nothing either, commands that need a project fail
with `config_not_found`; a bare `kranz` opens the runtime picker instead when a
local runtime is already running.

Service names remain short when unique. Collisions receive the smallest useful
directory prefix. The canonical source path plus original service key forms a
stable internal ID, so later qualification of a display name cannot transfer a
live process to another service. An ambiguous short selector reports the
qualified display names; the display name and stable ID are always unambiguous.

Service references are resolved only after display-name allocation. A bare
`depends_on` name first resolves within the file that declares it, so an
autonomous file can keep `depends_on: [api]` even when another discovered file
also exports `api`. With no local match, a bare name resolves to the one global
service with that original name. Multiple global matches are an error that
lists the qualified choices; zero matches are reported by dependency
validation. Kranz does not use source discovery order to break a tie.

The resolver rewrites `depends_on`, `dependency_conditions` keys, and
`before_start[].service` to effective display names together. A qualified
display name can explicitly select a cross-source service, but it is derived
from the current set of sources and can change when a new collision appears.
Reload therefore resolves the authored references again and rejects an
ambiguous or missing new graph while stable service IDs preserve process
identity across display-name changes.

### Override layers

`overrides` is distinct from composition. Layers apply left to right to the
logical config that declares them; CLI layers use repeatable `--override PATH`.

```yaml
overrides:
  - kranz.local.yaml
  - kranz.secrets.yaml
```

Where a layer is declared decides what it can see:

- A file's own `overrides:` list applies to that file before it joins the
  composition. It patches only that file's services and names them by their
  original keys (`api`), even if the service is later shown as `catalog/api`.
- `--override PATH` (or `KRANZ_OVERRIDE`) applies after the whole graph is
  composed, so it can patch any service and names it by display name
  (`catalog/api`). Relative CLI paths resolve against the working directory
  after `-C`.

Mappings merge by key, sequences replace, an empty mapping clears a mapping,
and `null` removes a value so its normal default can apply. Structural type
changes are errors. Override files cannot declare `defaults`, `include`,
`overrides`, or `protected`; this prevents defaults from leaking between files.
Relative `dir` values in a layer resolve beside that layer, and `${VAR}`
references expand from the process environment, then the `.env` beside the
layer.

`command` and `lifecycle.start` are one field. A layer can change only
`lifecycle.start.confirm` or a start timeout and keep the base command, and a
layer that sets `command` keeps the base start options. Setting `command: null`
removes the start command.

### Protected values

`protected` uses the ordinary typed configuration tree and applies after local
defaults and overrides. Descendant protected values apply first; each parent is
applied afterwards, so the root policy wins.

```yaml
protected:
  services:
    database:
      env:
        TLS_MODE: required
```

`config show --provenance`, `config explain`, and `kranz://config` expose the
winning source and protected rejection without revealing secret environment
values. `config sources` (or `m` in the dashboard) shows the same result per
file: the include tree, the services each file defined, and every field it
overrode, including protected values.

## Services

Each key under `services` is a service name. Names are shown verbatim in the
list, so keep them short and stable.

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| [`command`](#command) | string | — | Shorthand for `lifecycle.start.command` |
| [`description`](#description) | string | — | One line shown in Details |
| [`supervision`](#supervision) | enum | `process` | `process` or `detached` |
| [`lifecycle`](#lifecycle) | map | `{}` | Explicit start, stop, status, and logs |
| [`stop_on_exit`](#stop-on-exit) | bool | `true` / `false` | Whether runtime shutdown stops this service |
| [`dir`](#dir-shell) | string | `defaults.dir` | Working directory |
| [`shell`](#dir-shell) | string | `defaults.shell` | Shell for commands |
| [`env`](#env-env-files) | map | `{}` | Environment variables |
| [`env_files`](#env-env-files) | string list | `[]` | Dotenv files, applied in order |
| [`is_dotenv_disabled`](#is-dotenv-disabled) | bool | `false` | Skip the adjacent automatic `.env` for this service |
| [`ports`](#ports) | int list | `[]` | Declared ports, checked before start |
| [`detect_ports`](#detect-ports) | bool | see below | Discover listeners at runtime |
| [`tags`](#tags) | string list | `[]` | Grouping and selection labels |
| [`depends_on`](#depends-on) | string list | `[]` | Services that must start first |
| [`dependency_conditions`](#dependency-conditions) | map | `{}` | What "ready" means per dependency |
| [`healthcheck`](#healthcheck) | map | none | Readiness and liveness probes |
| [`ready_log_line`](#ready-log-line) | regex | — | Readiness from a log line |
| [`availability`](#availability) | map | `{}` | Restart policy and project exit |
| [`shutdown`](#shutdown) | map | `{}` | How this service is stopped |
| [`actions`](#actions) | map | `{}` | One-shot commands owned by this service |
| [`before_start`](#before-start) | list | `[]` | Actions that must succeed before start |
| [`success_exit_codes`](#success-exit-codes) | int list | `[0]` | Additional successful exit codes |
| [`disabled`](#disabled) | bool | `false` | Exclude from batch start |

### command

**Type:** string · **Required** for process supervision

The command that runs the service. It is executed by [`shell`](#dir-shell), so
pipes, `&&`, and variable expansion work.

```yaml
services:
  api:
    command: npm run dev
```

`command` is exactly equivalent to `lifecycle.start.command`, and is normalized
into it before configuration layers merge. Use the explicit form when the start
needs its own timeout or confirmation:

```yaml
services:
  api:
    lifecycle:
      start:
        command: npm run dev
        confirm: true
```

A single file must use one form or the other, never both.

### description

**Type:** string · **Default:** none

One line explaining what the service is, shown in Details.

```yaml
description: Messenger API and WebSocket backend
```

### supervision

**Type:** `process` | `detached` · **Default:** `process`

Declares where lifecycle truth comes from.

- `process` — Kranz starts the command, owns its process group, and knows the
  service stopped when the process exits.
- `detached` — the start command finishes while the resource it created keeps
  running. There is no PID to supervise, so stop and status must be described
  explicitly.

```yaml
services:
  remote-stack:
    supervision: detached
```

See the [lifecycle guide](../guide/lifecycle) for the full model.

### lifecycle

**Type:** map · **Default:** `{}`

| Field | Type | Applies to | Description |
| --- | --- | --- | --- |
| `start` | [action shape](#lifecycle-action-shape) | both | How to start |
| `stop` | [action shape](#lifecycle-action-shape) | detached | How to stop |
| `status` | [status shape](#lifecycle-status) | detached | Whether the resource exists |
| `logs` | [action shape](#lifecycle-action-shape) | detached | A command that streams logs |

A process-supervised service may only declare `lifecycle.start`; Kranz already
knows how to stop, observe, and read the logs of a process it owns. A detached
service may declare any subset: one with only `status` is observe-only, and its
start and stop controls stay unavailable rather than pretending to work.

#### Lifecycle action shape

`start`, `stop`, and `logs` share this shape:

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `command` | string | — | **Required** |
| `description` | string | — | Human-readable intent |
| `dir` | string | service `dir` | Working directory |
| `shell` | string | service `shell` | Shell |
| `env` | map | service `env` | Added and overriding variables |
| `env_files` | string list | service `env_files` | Dotenv sources |
| `timeout` | duration | none | Deadline for the whole command |
| `confirm` | bool | `false` | Ask before running (`start` only) |

Lifecycle commands cannot be `interactive`. Stopping from the TUI always asks
for confirmation regardless of `confirm`.

```yaml
lifecycle:
  start:
    command: ssh host 'cd app && docker compose up -d'
    timeout: 2m
  stop:
    command: ssh host 'cd app && docker compose down'
    timeout: 2m
```

`timeout` covers the entire command including the remote side of an SSH call.

#### Lifecycle status

A status probe answers one question: **does the external resource exist and
run?** It is not a health check and never restarts anything.

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `type` | enum | — | **Required.** Currently only `command` |
| `command` | string | — | **Required.** Observation command |
| `initial_delay` | duration | `0s` | Wait before the first probe |
| `interval` | duration | `5s` | Poll period while running |
| `stopped_interval` | duration | `30s` | Poll period while stopped or unknown |
| `timeout` | duration | `2s` | Deadline for one probe |
| `failure_threshold` | int | `3` | Unclassified probes before `unknown` |
| `running_exit_codes` | int list | `[0]` | Codes meaning running |
| `stopped_exit_codes` | int list | *unset* | Codes meaning stopped |

**Exit code contract.** By default, exit `0` means running and every other exit
code means stopped — the same convention every shell command already follows:

```yaml
status:
  type: command
  command: docker compose ps --status running --quiet api | grep -q .
```

Declaring `stopped_exit_codes` opts into a three-way contract, for probes that
can also report "I could not tell". Codes in neither set are unclassified, and
after `failure_threshold` consecutive unclassified results the service is shown
as `unknown`:

```yaml
status:
  type: command
  # 0 = running, 3 = stopped, 4 = the host is unreachable
  command: ssh host ./stack-status.sh
  running_exit_codes: [0]
  stopped_exit_codes: [3]
  failure_threshold: 2
```

A probe that never produced an exit code at all — it could not start, timed
out, or was killed — is always unclassified, never "stopped". Running and
stopped sets must not overlap, and each code must be between 0 and 255.

### stop_on_exit

**Type:** bool · **Default:** `true` for process, `false` for detached

Whether stopping the runtime also stops this service. Detaching a TUI does not
stop the runtime and therefore does not apply this setting.

```yaml
services:
  remote-stack:
    supervision: detached
    stop_on_exit: false
```

Process-supervised services always stop with Kranz and cannot set `false`;
Kranz does not leave orphaned child processes behind. A detached resource
defaults to surviving runtime shutdown, which is usually what you want for
external infrastructure shared with other tools. The quit dialog lists exactly
what shutdown will stop and what it will leave running.

### dir, shell

**Type:** string · **Default:** `defaults.dir` (config file directory),
`defaults.shell` (`/bin/bash`)

```yaml
services:
  api:
    dir: apps/api
    shell: /bin/bash
```

Relative directories resolve against the configuration file. Details shows the
path relative to where Kranz is running.

### env, env_files

**Type:** map, string list · **Default:** `{}`, `[]`

```yaml
services:
  api:
    env_files: [.env, .env.local]
    env:
      PORT: "3001"
```

Precedence, lowest to highest:

1. `.env` beside the configuration file that declares the service
2. `defaults.env`
3. `defaults.env_files`, in order
4. service `env_files`, in order
5. service `env`

A value already present in your shell environment wins over the adjacent
`.env`, but explicit configuration values always win. `$HOME`-style references
expand after all layers merge. In a composed project each file's `.env` reaches
only the services that file declares. Every referenced dotenv file is watched, so
editing one reloads the configuration.

### is_dotenv_disabled

**Type:** bool · **Default:** `false`

Set this on a service when it must not inherit the automatically discovered
`.env` beside the configuration file that declares it. Explicit `defaults.env`,
`defaults.env_files`, service `env_files`, and service `env` still apply.

```yaml
services:
  worker:
    command: npm run worker
    is_dotenv_disabled: true
```

### ports

**Type:** int list · **Default:** `[]`

Ports this service is expected to listen on.

```yaml
services:
  api:
    ports: [3001]
```

Declared ports are checked before start. If another process holds one, Kranz
names the owner and asks what to do instead of failing obscurely. They are also
valid documentation for a detached resource whose ports live elsewhere.

### detect_ports

**Type:** bool · **Default:** `true` when `ports` is empty, otherwise `false`

Discover TCP listeners actually opened by the service and its children.

```yaml
services:
  vite:
    detect_ports: true
```

Useful when a dev server picks its own port. Details separates declared ports
from detected ones. Not available for detached services, which have no local
process group to inspect; setting `true` there is rejected. See
[logs, runs, and ports](../guide/logs-and-ports).

### tags

**Type:** string list · **Default:** `[]`

```yaml
tags: [backend, messenger]
```

Tags appear as expandable groups with their own summary Details. Selecting a
tag selects every service in it.

### depends_on

**Type:** string list · **Default:** `[]`

```yaml
services:
  web:
    depends_on: [api]
```

Starting a service starts its dependencies first. Stopping it stops its
dependents first, in reverse order. `Shift+S` overrides both and acts only on
the selection.

### dependency_conditions

**Type:** map · **Default:** `process_healthy` per dependency

What counts as "ready" for each dependency.

| Condition | Meaning |
| --- | --- |
| `process_started` | The process exists |
| `process_healthy` | Its readiness probe passes (default) |
| `process_completed` | It finished, with any exit code |
| `process_completed_successfully` | It finished successfully |
| `process_log_ready` | Its `ready_log_line` matched |

```yaml
services:
  web:
    depends_on: [api, seed]
    dependency_conditions:
      api: {condition: process_healthy}
      seed: {condition: process_completed_successfully}
```

`process_log_ready` requires the dependency to define
[`ready_log_line`](#ready-log-line).

### healthcheck

**Type:** map · **Default:** none

Two independent probes:

- **readiness** — may dependents start? Gates the dependency graph.
- **liveness** — is this running service still healthy? Surfaces `unhealthy`.

```yaml
healthcheck:
  readiness:
    type: http
    url: http://127.0.0.1:3001/ready
    interval: 2s
  liveness:
    type: tcp
    port: 3001
    initial_delay: 10s
    interval: 15s
```

Each probe accepts:

| Field | Type | Default | Applies to |
| --- | --- | --- | --- |
| `type` | `http` \| `tcp` \| `command` | — | **Required** |
| `url` | string | — | `http` |
| `headers` | map | `{}` | `http` |
| `status_code` | int | any `2xx` | `http` |
| `port` | int | — | `tcp` |
| `command` | string | — | `command` |
| `port_from` | `detected` | — | `http`, `tcp` |
| `detected_port_index` | int | `0` | `http`, `tcp` |
| `initial_delay` | duration | `0s` | all |
| `interval` | duration | `5s` | all |
| `timeout` | duration | `2s` | all |
| `failure_threshold` | int | `3` | all |

An empty `healthcheck` block is invalid — a probe with no `type` cannot be run,
and silently ignoring it would misreport the service as healthy.

To probe a port discovered at runtime instead of a fixed one, take the port
from discovery. A `tcp` probe with no `port`, or an `http` probe with
`port_from: detected`, targets the first detected listener;
`detected_port_index` selects a later one for a service that opens several. See
[health and dependencies](../guide/health-and-dependencies).

### ready_log_line

**Type:** regular expression · **Default:** none

Readiness from output, for services with no endpoint to probe.

```yaml
services:
  worker:
    command: npm run worker
    ready_log_line: "worker listening"
```

Cannot be combined with a readiness probe; declare one source of readiness.

### availability

**Type:** map · **Default:** `{}`

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `restart` | `no` \| `always` \| `on_failure` \| `exit_on_failure` | `no` | Recovery policy |
| `backoff` | duration | `0s` | Wait before restarting |
| `max_restarts` | int | `0` (unlimited) | Restart attempt limit |
| `exit_on_end` | bool | `false` | End the runtime when this service ends |
| `exit_on_skipped` | bool | `false` | End the runtime if a dependency gate skips it |

```yaml
availability:
  restart: on_failure
  backoff: 2s
  max_restarts: 3
```

Restart policies apply to process-supervised services only. Kranz does not
restart a detached resource it does not own.

### shutdown

**Type:** map · **Default:** `{}`

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `signal` | int | `15` (SIGTERM) | Signal sent first |
| `timeout` | duration | `3s` | Grace period before escalation |
| `command` | string | — | Custom graceful shutdown command |
| `parent_only` | bool | `false` | Signal only the leader, not the group |

```yaml
shutdown:
  signal: 15
  timeout: 10s
```

After `timeout` expires the process group is killed, so a service that ignores
SIGTERM still exits.

By default the whole process group is signaled, so child processes do not
survive their parent. Use `parent_only: true` only when a program manages its
own children and must handle shutdown itself.

### actions

**Type:** map · **Default:** `{}`

One-shot commands owned by this service. See [actions](#action-fields) below
and the [actions guide](../guide/actions).

```yaml
services:
  api:
    command: npm run dev
    actions:
      migrate:
        command: npm run db:migrate
        confirm: true
```

### before_start

**Type:** list · **Default:** `[]`

Actions that must succeed before this service starts. Each entry references an
existing action rather than inlining a command, so the same command stays
runnable and inspectable on its own.

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `action` | string | — | **Required.** Action name |
| `service` | string | the declaring service | Service owning the action |
| `group` | string | — | Action group owning the action |
| `run` | `once` \| `always` | `once` | How often it runs per session |

```yaml
services:
  api:
    command: npm run dev
    actions:
      migrate:
        command: npm run db:migrate
    before_start:
      - action: migrate
      - group: infrastructure
        action: up
        run: always
```

Set either `service` or `group`, not both. Prerequisites run in declared order,
after dependencies are ready and before the service starts. `once` means one
successful run per Kranz session, including across restarts; `always` runs
before every start. If a prerequisite fails, the service stays stopped and the
failure is reported in its logs. Interactive actions cannot be prerequisites.

### success_exit_codes

**Type:** int list · **Default:** `[0]`

Additional exit codes treated as success, for commands that report a meaningful
non-zero status.

```yaml
success_exit_codes: [0, 2]
```

### disabled

**Type:** bool · **Default:** `false`

The service stays visible and startable by hand. Pressing `a` does not select
it, and start-all skips it, so it is excluded from `a`-style batch starts.

```yaml
disabled: true
```

## Action groups

Project-level actions that belong to no single service.

```yaml
action_groups:
  infrastructure:
    description: Shared development infrastructure
    dir: infra
    env_files: [.env.infra]
    actions:
      up:
        command: docker compose up -d
      reset:
        command: docker compose down --volumes
        confirm: true
```

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `description` | string | — | Shown on the group row |
| `dir` | string | `defaults.dir` | Inherited working directory |
| `shell` | string | `defaults.shell` | Inherited shell |
| `env` | map | `{}` | Inherited environment |
| `env_files` | string list | `[]` | Inherited dotenv sources |
| `actions` | map | — | **Required.** The group's actions |

An action's own values override the group's.

### Action fields

The same shape for service actions and group actions:

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `command` | string | — | **Required** |
| `description` | string | — | What it does, shown beside the name |
| `dir` | string | owner's `dir` | Working directory |
| `shell` | string | owner's `shell` | Shell |
| `env` | map | owner's `env` | Added and overriding variables |
| `env_files` | string list | owner's `env_files` | Dotenv sources |
| `timeout` | duration | none | Deadline for the whole process group |
| `confirm` | bool | `false` | Ask before running |
| `interactive` | bool | `false` | Hand the terminal to the command |

The action's key is its name in the list; `description` explains it. Keep keys
short and stable — `migrate`, not `run-the-database-migrations`.

## UI

Appearance for this project. Personal settings live outside the repository; see
[appearance](../guide/appearance).

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `theme` | string | built-in default | Named theme |
| `accent` | `#RRGGBB` | theme accent | Accent color |
| `background` | `terminal` \| `theme` \| `#RRGGBB` | `terminal` | Canvas source |
| `color_mode` | `auto` \| `dark` \| `light` | `auto` | Palette mode |

```yaml
ui:
  theme: tokyo-night
  accent: "#7AA2F7"
  background: terminal
  color_mode: auto
```

`Ctrl+T` opens the live picker, which can write these values back to the
project or to your personal settings.

## Validation rules

Kranz rejects a configuration rather than starting with an ambiguous one:

- unknown fields are errors, so a typo never becomes silence;
- a process-supervised service needs `command` or `lifecycle.start`;
- `lifecycle.stop`, `status`, and `logs` require `supervision: detached`;
- `detect_ports: true` and restart policies are rejected for detached services;
- `running_exit_codes` and `stopped_exit_codes` must not overlap;
- every probe needs its own `type`;
- `ready_log_line` and a readiness probe are mutually exclusive;
- dependencies must exist, and the graph must be acyclic;
- `before_start` must reference an action that exists and is not interactive.

An invalid change during a live reload leaves the running configuration in
place. Additions and stopped-service changes apply immediately. A changed,
renamed, or removed running service keeps its accepted snapshot and is reported
as `pending_restart`; an explicit restart adopts the desired snapshot.
