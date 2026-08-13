# Actions

Actions are explicit one-shot operations: migrations, builds, tests, data
seeding, or inspection commands. They are not represented as continuously
running services.

<div class="demo-frame">

![Running a service action, declining a destructive one, and running a project action](../assets/actions.gif)

</div>

## Service actions

```yaml
services:
  api:
    command: npm run dev
    dir: apps/api
    env_files: [.env]
    actions:
      migrate:
        command: npm run db:migrate
        description: Apply pending database migrations
        timeout: 2m
        confirm: true
      test:
        command: npm test
        timeout: 5m
```

Service actions inherit `dir`, `shell`, `env`, and `env_files`. Expand a service
with `Enter`, focus an action, and press `s`. Output, exit code, and duration are
retained separately from service logs.

## Project action groups

Use an action group when the operation does not belong to one service:

```yaml
action_groups:
  infrastructure:
    description: Shared development infrastructure
    dir: infra
    actions:
      inspect:
        command: docker compose ps
      reset:
        command: docker compose down --volumes
        confirm: true
```

Groups can provide shared execution context. An action's own values override
the inherited group values.

## Running an action before a service starts

Some actions are not something you remember to run — they are a precondition.
`before_start` declares that relationship:

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

Each entry references an action that already exists, so the command keeps one
definition and stays runnable on demand. Reference an action of the declaring
service with `action:` alone, another service's action with `service:`, or a
group's with `group:`.

Prerequisites run in declared order, after dependencies are ready and before
the service starts:

```text
dependencies ready → before_start actions → service starts → readiness probe
```

`run: once` is the default and means one successful run per Kranz session,
including across restarts — a migration is not re-applied every time you
restart the service. `run: always` runs before every start, for checks that are
cheap and whose answer can change.

If a prerequisite fails, the service stays stopped and its log names the action
and the reason. Only success is remembered, so fixing the cause and starting
again retries it. When two services reference the same prerequisite, whichever
reaches it first runs it and the other waits for that same run.

Interactive actions cannot be prerequisites: they run unattended while a start
is already in flight, so they cannot own the terminal.

See the [prerequisites example](../examples/prerequisites) for a runnable
walkthrough.

## Confirmation and cancellation

`confirm: true` requires explicit approval before starting an action. Stopping
a running action always asks for confirmation regardless of its start setting.
`timeout` covers the whole process group; cancellation sends a graceful signal
and escalates when necessary.

## Actions that ask a question

Some commands have to be answered: a migration that confirms before it writes,
a REPL, a scaffolding wizard. `interactive: true` hands the real terminal to
the command:

```yaml
actions:
  migrate:
    command: npm run db:migrate
    interactive: true
```

Running one always asks first, whether or not it also sets `confirm`. The
confirmation says plainly that Kranz is about to leave the screen, because
handing the terminal over is not something that should happen to someone who
just pressed a key in a list:

```text
Run action "migrate"?

⚠ KRANZ WILL LEAVE THE SCREEN
OWNER  api

This action takes over your terminal so you can answer it.
Kranz returns as soon as the command exits.

[Enter/y] Hand over the terminal  [Esc/n] Cancel
```

<div class="demo-frame">

![Confirming the handoff, answering the command in the terminal, and returning to Kranz](../assets/interactive-action.gif)

</div>

After you accept, the command owns the terminal until it exits. Kranz then
resumes and records the result — running, succeeded or failed, with the exit
code and duration — exactly like a captured action. Because the output
went to your terminal rather than into a buffer, the action's log pane says so
instead of showing an empty capture.

Two limits follow from what handoff means:

- lifecycle `start`, `stop`, and `logs` commands cannot be interactive; they run
  unattended, sometimes while Kranz is shutting down;
- an interactive action cannot be a [prerequisite](#running-an-action-before-a-service-starts),
  because prerequisites run while a start is already in flight.

`confirm` and `interactive` answer different questions. `confirm` is about
whether the command should run at all — use it for anything destructive.
`interactive` is about who owns the terminal while it runs. An interactive
action confirms regardless, so setting both only changes the wording.
