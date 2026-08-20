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

### Logs

```bash
kranz logs [SELECTOR ...]          # the last 50 lines
kranz logs --tail 200
kranz logs --all                   # everything still buffered
kranz logs --since 5m
kranz logs api --follow
```

A bare `kranz logs` returns the last fifty lines, because every service keeps a
thousand and a few services make that thousands. `--all` returns everything.
`--tail` and `--since` compose: `--since 5m --tail 50` is the last fifty lines
from the past five minutes. A stopped service keeps its buffer, so logs still
answer for a service that has already died. `--follow` resumes from a cursor
rather than reprinting, and stops on `Ctrl+C`.

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

## Machine-readable output

`--output json` wraps every successful result in a versioned envelope:

```console
$ kranz ps --output json
{"schema_version":1,"data":[]}
```

Failures use the same envelope with an `error` object, and stdout stays valid
JSON so a script never has to parse prose:

```console
$ kranz list --output json
{"schema_version":1,"error":{"code":"no_project","message":"no Kranz configuration was found in this directory","hint":"Run from a project directory or pass -f PATH."}}
```

`kranz logs --follow --output json` emits one envelope per event as it arrives.

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
