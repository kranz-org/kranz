# CLI reference

Kranz has one binary and no daemon. Running `kranz` with no subcommand opens the
terminal UI; every other command either describes a project or talks to one
running project runtime.

## Synopsis

```bash
kranz                                # open the TUI for this directory
kranz [GLOBAL OPTIONS] COMMAND [ARGS]
```

The first positional argument after the global options is always a subcommand.

## Global options

| Option | Description |
| --- | --- |
| `-f`, `--config PATH` | Load a configuration layer. Repeatable, merged left to right. |
| `-C`, `--directory DIR` | Work in `DIR` instead of the current directory. |
| `-p`, `--project VALUE` | Address a runtime by name, ID, or unique ID prefix. |
| `--output text\|json` | Choose human output or the machine-readable envelope. |
| `-h`, `--help` | Print usage for the command and exit. |
| `-v`, `--version` | Print version, commit, and build time, then exit. |

## Choosing the runtime a command acts on

`-p` is optional everywhere. Without it, the target is the runtime named by the
configuration in the working directory, which is what makes this work:

```bash
cd ~/projects/shop
kranz up -d
kranz status
kranz stop api
kranz down
```

With `-p`, the explicit value always wins — including from a directory that has
a project of its own, so you can drive another project without leaving this
one:

```bash
cd ~/projects/shop
kranz -p billing status
```

A directory that is not a project says so rather than reporting a missing
runtime:

```console
$ cd /tmp && kranz status
Kranz: no Kranz configuration was found in this directory.
Run from a project directory, pass -f PATH, or name a runtime with -p NAME_OR_ID.
```

## Runtime names and IDs

Each `up` creates one runtime session. Its NAME comes from `runtime.name`, or
from a lowercase slug of `project` when that field is absent. Its ID is
generated per run and never reused, so a project restarted after `down` keeps
its NAME and gets a new ID. `-p` accepts a full NAME, a full ID, or a unique ID
prefix.

## Commands

### Creating a configuration

```bash
kranz init                                   # wizard, or flags when there is no terminal
kranz init --from Procfile                   # convert an existing source
kranz init --from process-compose.yaml
kranz init --service api --command "npm run dev" --yes
kranz init -o kranz.local.yaml
```

`init` discovers a Kranz, Process Compose, or Procfile source and offers to
convert it, reads `package.json` scripts and offers them as actions without
running them, previews the file it is about to write, and refuses to replace an
existing file without `--yes` or a confirmation. It reloads what it wrote before
reporting success.

### Inspecting a project

These read the configuration only. They work before the first `up` and never
disturb a running runtime.

```bash
kranz config                        # same as config show
kranz config check                  # load, merge, and validate
kranz config show [--provenance]    # effective configuration, secrets redacted
kranz config explain [SERVICE] [--all]  # which layer set each field
kranz doctor                        # preflight checks
kranz list [services|actions|tags]
kranz info [SERVICE]
kranz plan [SELECTOR ...]           # the waves a start would use
kranz graph [--format text|json|dot]
kranz ports [SELECTOR ...]
kranz port inspect PORT
```

`ports` reports both the ports a service declares and the ports a running
runtime saw it open, labelled by origin, because a service that picks its port
at runtime is exactly the case where the configuration cannot answer.

`info SERVICE` describes the configuration, and adds what the service is doing
right now when a runtime is up.

`config show` redacts environment values whose name looks like a credential and
keeps services, action groups, and actions in the order the configuration
declares them. `config explain` on a single-layer project says so instead of
repeating the same filename on every field; `--all` lists them anyway.

A group runs its obvious subcommand when invoked bare: `kranz config` is
`config show`, `kranz action` is `action list`, and `kranz port 8080` is
`port inspect 8080`.

`plan` prints the dependency waves the supervisor itself gates readiness on, and
pulls in the dependencies of whatever you selected:

```console
$ kranz plan gateway
Wave 1:
  shared-infra
Wave 2:
  migrate  (after shared-infra)
Wave 3:
  catalog-api  (after migrate)
  billing-api  (after migrate)
Wave 4:
  gateway  (after billing-api, catalog-api)
```

`doctor` reports every finding rather than stopping at the first, and exits `3`
when any check fails.

### Runtimes

```bash
kranz ps                            # every runtime this user has running
kranz up [SELECTOR ...]             # foreground runtime with multiplexed logs
kranz up -d [SELECTOR ...]          # background runtime, returns the prompt
kranz up --no-start                 # an empty foreground runtime
kranz attach                        # open the TUI on a running runtime
kranz status [SELECTOR ...]
kranz start SELECTOR ...
kranz stop SELECTOR ...
kranz restart SELECTOR ...
kranz reload                        # re-read the configuration
kranz down                          # stop the project and end the runtime
kranz down --force                  # recover a runtime that stopped answering
```

`down` stops the whole runtime and takes no service selectors; use `stop` for a
single service. `down --force` is emergency recovery for an unreachable
session, not the ordinary way to stop a project.

Leaving an attached TUI does not stop a background runtime. An external `down`
closes attached clients cleanly.

`ps` reports the lifecycle owner in its `MODE` column. The possible owner modes
are `tui`, `foreground`, `background`, and `mcp`. Different runtime rows can use
different modes at the same time, and the same project can have multiple rows
when each was given a distinct runtime name with `-p`. One session ID has only
one owner mode: an attached TUI, CLI command, or additional MCP bridge remains
a client of that session and does not add another mode or another `ps` row.

### Serving a runtime to a coding agent

```bash
kranz mcp --attach-only             # attach-only stdio MCP server (recommended)
```

`mcp` speaks the Model Context Protocol on stdin and stdout, so stdout carries
JSON-RPC framing and nothing else; `--output` is refused and diagnostics go to
stderr. One connection stays bound to one runtime. `--attach-only` refuses a
missing runtime and lists other live sessions; without it, MCP can create and
own the selected project when none exists. See the [MCP reference](./mcp.md).

### Logs

```bash
kranz logs [SELECTOR ...]          # the last 50 lines
kranz logs --tail 200
kranz logs --all                   # everything still buffered
kranz logs --since 5m
kranz logs api --follow
kranz logs api --with-actions      # the service and the actions it has run
kranz logs api --source stderr     # stdout, stderr, or kranz; comma-separated
kranz logs api --plain             # no time column, no label column
kranz logs clear api               # discard one buffer
kranz logs clear --force           # discard every buffer in the project
```

A bare `kranz logs` returns the last fifty lines, because every service keeps a
thousand and a few services make that thousands. `--all` returns everything.
`--tail` and `--since` compose: `--since 5m --tail 50` is the last fifty lines
from the past five minutes. A stopped service keeps its buffer, so logs still
answer for a service that has already died. `--follow` resumes from a cursor
rather than reprinting, and stops on `Ctrl+C`.

`--plain` is `--no-timestamps` and `--no-labels` together: both columns exist to
tell interleaved streams apart, which reading one stream back does not need.
`logs clear` narrows the same way `logs` does, and an unqualified clear needs
`--force`, because it is the one shape that cannot be narrowed afterwards.

Every start of a service and every execution of an action is a numbered run,
so a single buffer stays addressable after the thing that filled it restarted:

```bash
kranz logs analytics/stats                # the latest run, whole
kranz logs analytics/stats --run 7        # run number 7
kranz logs analytics/stats --run -1       # the latest run
kranz logs analytics/stats --run -2       # the run before it
kranz logs analytics/stats --runs 3       # the last three runs
kranz logs api --run -1                   # only the newest start of a service
kranz runs                                # bounded catalog and retention limits
kranz runs api analytics/stats            # narrow catalog by target
kranz runs delete api#4 --confirm         # delete one completed run and retained output
```

Every line carries its stable absolute identity (`api#7` or
`analytics/stats#3`). Relative values are query syntax only and are never
printed as identities.

A positive `--run` is the absolute run number Kranz assigned. A negative one is
an offset from the newest run in the independent run catalog, so `-1` keeps
meaning "the latest" even after its output has aged out. When a retained run
has lost its output prefix, text output prints the exact missing lines/bytes
marker before the available tail. `kranz runs` publishes the oldest retained
run, catalog/output budgets, evicted summary count, provenance, and output
state (`complete`, `partial`, or `unavailable`). The catalog belongs to the
live runtime session and is not restored after `down`.

### Actions

```bash
kranz action list [OWNER]
kranz action info OWNER/ACTION
kranz action run OWNER/ACTION
```

An action is identified by owner and name together, so a service action and an
action-group action may share a name. Running one goes through the runtime,
which owns the execution slot. A failed action fails the command. Interactive
actions need the real terminal and are run from the TUI.

### Shell completion

```bash
kranz completion bash > /usr/share/bash-completion/completions/kranz
kranz completion zsh  > "${fpath[1]}/_kranz"
kranz completion fish > ~/.config/fish/completions/kranz.fish
```

The Linux packages install these already.

The scripts are generated from the same command tree as `--help`, so they offer
what the binary actually has: commands, the subcommands of a group, and the
options of whichever command is being typed. `kranz logs --<TAB>` offers the log
flags, `kranz logs clear --<TAB>` offers only the two `clear` takes, and an
option with a fixed set of values completes those too — `--source` to a stream
name, `--format` to a graph format. A flag that takes a path completes
filenames.

## Machine-readable output

`--output json` wraps every successful non-interactive result in a versioned
envelope:

```console
$ kranz ps --output json
{"schema_version":1,"data":[]}
```

Commands report the result they produced rather than an empty success marker:

```console
$ kranz restart api --output json
{"schema_version":1,"data":{"command":"restart","services":["api","web"]}}

$ kranz reload --output json
{"schema_version":1,"data":{"command":"reload","runtime":"shop-dev","changed":false,"added":[],"removed":[],"restarted":[],"updated":[]}}
```

`init --output json` omits the human preview and reports the absolute path it
wrote, the project, services, action count, and suggested next commands.
`up -d --output json` reports the new runtime's full ID, name, PID, and mode.
Foreground `up` and `attach` require a terminal; help and completion output
are text artifacts rather than data envelopes.

Failures use the same envelope with an `error` object, and stdout stays valid
JSON so a script never has to parse prose:

```console
$ kranz list --output json
{"schema_version":1,"error":{"code":"no_project","message":"no Kranz configuration was found in this directory","hint":"Run from a project directory or pass -f PATH."}}
```

Commands that completed their work but found an unsuccessful outcome keep the
useful result in `data` and signal failure with the exit code. In particular, a
failed `doctor` returns `findings`, `services_checked`, `problems`, and
`warnings` in one envelope with exit `3`; a failed action returns its captured
output and exits `1`. They never append a second envelope.

`kranz logs --follow --output json` emits one envelope per event as it arrives.
For `status`, services are under `.data.services[]`; `.data.session` carries
the runtime identity. Unconfigured readiness and liveness probes are `null`,
not successful booleans.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Internal error, or an action that ran and failed |
| `2` | Usage error — unknown command, missing or malformed argument |
| `3` | Configuration error, including a failed `doctor` |
| `4` | Not found — no such runtime, service, action, or selector |
| `5` | Conflict — a runtime with that name is already active, or a file exists |
| `6` | Runtime unavailable — unreachable, incompatible, or a refused force-down |

A foreground `up` instead exits with whatever the project asked for through
`availability.exit_on_end` or `exit_on_failure`, and dies by signal when it is
signalled, so a supervisor above Kranz sees the truth.

## Configuration discovery

With no `-f`, Kranz loads the first file that exists in the working directory,
in this order:

1. `kranz.yaml`
2. `kranz.yml`
3. `process-compose.yaml`
4. `process-compose.yml`
5. `Procfile.dev`
6. `Procfile`

Native configuration wins over a Process Compose file in the same directory, so
adding `kranz.yaml` to a project takes effect without deleting anything.

## Layering

Several files merge left to right; later files override earlier ones:

```bash
kranz -f kranz.yaml -f kranz.local.yaml
```

Keep the shared configuration in version control and personal overrides out of
it. Because `command` is normalized to `lifecycle.start` before merging, a later
layer can override a start timeout or a confirmation without repeating the
command. `kranz config explain` shows which layer set each field. Merge rules
per field are listed in the [configuration reference](./configuration).

## Signals

`SIGINT`, `SIGTERM`, and `SIGHUP` begin an orderly shutdown: attached clients
close, then every process group the runtime owns is stopped synchronously.
Detached resources follow their [`stop_on_exit`](./configuration#stop-on-exit)
setting.

## Files Kranz reads and writes

| Path | Purpose |
| --- | --- |
| `./kranz.yaml` and the other discovered names | Project configuration |
| `.env` beside the first configuration file | Environment, if present |
| Every file named in `env_files` | Environment |
| `$XDG_RUNTIME_DIR` or the user's temporary directory | Runtime registry, locks, and sockets |
| `$XDG_CONFIG_HOME/kranz/settings.yaml` (Linux) | Personal appearance |
| `~/Library/Application Support/kranz/settings.yaml` (macOS) | Personal appearance |

Runtime state belongs to the invoking user. Kranz never manages another user's
runtime and starts no system-wide daemon.

## Changes from 0.7

The positional configuration form is gone:

```bash
kranz prod.yaml     # 0.7
kranz -f prod.yaml  # 0.8
```

Kranz recognises the old shape and says what to do instead:

```console
$ kranz prod.yaml
Kranz: unknown command "prod.yaml".
Did you mean `kranz -f prod.yaml`?
```

Bare `kranz` still opens the TUI, and every 0.7 configuration file loads
unchanged.

See the [controls reference](./controls) for every key binding.
