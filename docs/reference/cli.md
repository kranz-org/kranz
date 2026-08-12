# CLI reference

Kranz is a single foreground binary with no daemon, no background service, and
no state directory of its own beyond your personal appearance settings.

## Synopsis

```bash
kranz                            # auto-discover a configuration
kranz CONFIG [CONFIG ...]        # load specific files, merged left to right
kranz -f CONFIG [-f OVERRIDE]    # the same, with explicit flags
```

## Options

| Option | Description |
| --- | --- |
| `-f`, `--config PATH` | Load a configuration layer. Repeatable. |
| `--config=PATH` | Same, in one argument. |
| `-h`, `--help` | Print usage and exit. |
| `-v`, `--version` | Print version, commit, and build time, then exit. |

`--help` and `--version` accept no other arguments.

```console
$ kranz --version
kranz 0.6.1 (commit 4505b4d1c0f2, built 2026-08-10T12:04:11+02:00)
```

## Configuration discovery

With no arguments, Kranz loads the first file that exists in the current
directory, in this order:

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

Use this to keep a shared configuration in version control and personal
overrides out of it. Because `command` is normalized to `lifecycle.start`
before merging, a later layer can override only a start timeout or a
confirmation without repeating the command. Merge rules per field are listed in
the [configuration reference](./configuration).

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Kranz exited normally |
| `1` | Startup failed — configuration could not be loaded or validated |
| *other* | A service requested the exit through `availability.exit_on_end` or `exit_on_failure` |

Configuration errors print to stderr and exit before anything starts:

```console
$ kranz
Kranz error: load config: service "api": dependency "database" was not found
```

## Signals

`SIGINT`, `SIGTERM`, and `SIGHUP` begin an orderly shutdown: the interface
closes, then every process group Kranz owns is stopped synchronously before the
command returns. Detached resources follow their
[`stop_on_exit`](./configuration#stop-on-exit) setting.

## Files Kranz reads and writes

| Path | Purpose |
| --- | --- |
| `./kranz.yaml` and the other discovered names | Project configuration |
| `.env` beside the first configuration file | Environment, if present |
| Every file named in `env_files` | Environment |
| `$XDG_CONFIG_HOME/kranz/settings.yaml` (Linux) | Personal appearance |
| `~/Library/Application Support/kranz/settings.yaml` (macOS) | Personal appearance |

Kranz writes only the settings file, and only when you confirm a save in the
theme picker. Project appearance is written back to your `kranz.yaml` when you
explicitly choose to save it there.

## Live reload

Configuration files and every referenced dotenv file are watched. A valid
change applies without restarting running services; an invalid change is
reported and leaves the running configuration untouched. `Ctrl+L` reloads
immediately.

See the [controls reference](./controls) for every key binding.
