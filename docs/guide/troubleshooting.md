# Troubleshooting

Symptoms you are likely to hit, and what each one actually means.

## A service is stuck on "starting"

A yellow `starting` state means Kranz is waiting for evidence, not that
something is broken. Focus the service and press `2` to see what it waits for.

**Waiting for a dependency.** Details shows which one. The dependency's
readiness must pass before this service starts. Check the dependency's own
readiness probe — if the probe is wrong, the gate never opens.

**Waiting for its own readiness.** The process started, but the probe has not
passed yet. Common causes:

- the probe URL points at a port the service does not actually use;
- the service binds `localhost` only, while the probe uses a different host;
- `initial_delay` is shorter than the real startup time and the failures have
  not yet accumulated to `failure_threshold`.

To see whether the probe or the service is at fault, run the probe by hand:

```bash
curl -v http://127.0.0.1:3001/ready
```

If that works while Kranz still waits, the probe configuration and the real
endpoint disagree — compare them in Details.

## A port is already taken

Kranz checks declared [`ports`](../reference/configuration#ports) before
starting and names the owner rather than failing obscurely. The dialog
distinguishes a process Kranz manages from an external one, and terminating an
external process always requires your explicit confirmation.

If the port belongs to a leftover process from a previous session, stop it from
the dialog. If it belongs to something you want to keep, change the service's
port or start the other tool instead.

## A service shows "unknown"

`unknown` only applies to [detached](./lifecycle) resources, and it means the
status probe could not tell Kranz what the resource is doing. It is a deliberate
"no evidence" state, not a failure.

Causes, in order of likelihood:

1. **The probe host is unreachable.** An SSH probe against a sleeping laptop
   cannot answer. Kranz shows `unknown` instead of claiming `stopped`.
2. **The probe timed out.** Raise `timeout`; a remote probe often needs more
   than the default `2s`.
3. **The probe returned an unclassified exit code.** This happens only if you
   declared `stopped_exit_codes` — a code in neither set is unclassified by
   design. Check the code your script actually returns:

   ```bash
   ssh host ./stack-status.sh; echo "exit: $?"
   ```

The service's log pane records the reason as `[Kranz] Status unavailable: …`.
An `unknown` resource is polled on the slower `stopped_interval`, so recovery
can take up to 30 seconds by default.

## A service did not start and mentions a prerequisite

A [`before_start`](../reference/configuration#before-start) entry failed, so
Kranz did not start the service against an unprepared environment. The logs
name the action and what happened:

```text
[Kranz] Prerequisite failed: action "migrate" · exited with code 1
```

Run that action on its own — expand the service with `Enter`, select the
action, press `s` — and read its output. Prerequisites with `run: once` are
remembered only after they succeed, so fixing the cause and starting again
retries it.

## Detached logs are empty

A detached resource has no process for Kranz to read output from. It shows logs
only when you provide `lifecycle.logs`, and only while it is running:

```yaml
lifecycle:
  logs:
    command: docker compose logs -f --tail=100
```

The command must stream and stay attached. A command that prints once and exits
produces one burst of output and then nothing.

## Mouse clicks stop working

Some integrated terminals silently reset mouse reporting when focus moves.
Kranz re-enables tracking periodically and when the window regains focus, so
clicking again usually recovers within a second. Keyboard control is never
affected, and every mouse action has a key binding — see
[controls](../reference/controls).

## Colors look wrong or unreadable

Kranz derives its palette from the terminal background by default. If your
terminal reports its background inaccurately, pin the mode explicitly:

```yaml
ui:
  color_mode: dark        # or light
  background: theme       # stop deriving from the terminal
```

`Ctrl+T` opens the live picker to try values before saving them. See
[appearance](./appearance).

## Configuration changes have no effect

Kranz watches the configuration file and every referenced dotenv file, and
reloads valid changes without restarting running services.

- **The change was invalid.** The previous configuration keeps running and the
  error is reported. Fix the error and save again.
- **The file is not one Kranz loaded.** Auto-discovery uses the first matching
  name only, so a `kranz.yaml` shadows a `process-compose.yaml` in the same
  directory. Check which files are in use, and pass them explicitly with `-f`
  if needed.
- **The value is overridden by a later layer.** With `-f a.yaml -f b.yaml`, the
  right-hand file wins.

`Ctrl+L` forces an immediate reload.

## An environment variable is not what I expect

Precedence runs from lowest to highest:

1. `.env` beside the first configuration file
2. `defaults.env`
3. `defaults.env_files`, in order
4. service `env_files`, in order
5. service `env`

A variable already exported in your shell wins over the adjacent `.env`, but
never over an explicit configuration value. Details shows the resolved
environment file list for the selected service.

## Quitting leaves something running

That is deliberate for detached resources: they default to
[`stop_on_exit: false`](../reference/configuration#stop-on-exit) so a shared
Docker stack is not torn down because you closed a terminal. The quit dialog
lists exactly what will stop and what will keep running before you confirm.

Set `stop_on_exit: true` if closing Kranz should also run the external stop
command. Process-supervised services always stop with Kranz; they cannot be
left behind.

## A restart policy is being ignored

`availability.restart` applies to process-supervised services only. Kranz does
not restart a detached resource it does not own — the configuration is rejected
rather than silently ignored.

Also check `max_restarts`: once the limit is reached the service stays stopped
until you start it again.

## Still stuck

Open the [lifecycle playground](../examples/lifecycle), which simulates
managed, observe-only, healthy, unhealthy, and unknown resources locally with
marker files. Reproducing a symptom there separates a Kranz problem from a
configuration problem in your own project.

If it is a Kranz problem, please
[open an issue](https://github.com/kranz-org/kranz/issues) with your
configuration and the version from `kranz --version`.
