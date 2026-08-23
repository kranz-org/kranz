# Working from the command line

The terminal UI is one way to drive Kranz. The other is the command line, which
matters when you want a project running in the background, when you are in a
second terminal, or when a script has to make a decision from what Kranz
reports.

Everything below works on the project in the current directory. You never have
to name it.

## Start a project and leave it running

```console
$ kranz up -d
Started shop-dev (7fa21c8d), PID 18421.
```

`up -d` creates a background runtime for this project and gives you the prompt
back. The processes belong to that runtime, not to your shell, so closing the
terminal leaves them alone.

Without `-d`, `up` stays in the foreground and streams every service's output
with a prefix. That is the shape you want inside a container or under another
supervisor.

## See what is running

`ps` lists every runtime you have, across projects:

```console
$ kranz ps
ID        NAME      PROJECT  MODE        SERVICES  STATE    UPTIME
7fa21c8d  shop-dev  Shop     background  4/4       running  18m
91bc430a  billing   Billing  tui         3/3       running  6m
```

`status` describes the services of one runtime:

```console
$ kranz status
NAME     STATE    HEALTH  UPTIME  PID    PORTS
migrate  stopped  -       -       -      -
api      running  ready   18m     26078  3000
worker   running  -       18m     26085  -
```

`HEALTH` is `-` when no readiness or liveness probe is configured. Kranz does
not turn the internal assumption that a missing probe permits startup into a
false claim that a probe passed.

## Act on services

```console
$ kranz stop worker
$ kranz start worker
$ kranz restart api
```

Selectors are service names or tags, the same ones the TUI uses. `stop` expands
to dependents exactly as the TUI does, so stopping something nothing else can
survive without stops those too.

`down` ends the whole runtime:

```console
$ kranz down
```

It takes no service selectors. If you name one, Kranz says which command you
wanted:

```console
$ kranz down worker
Kranz: down stops the whole runtime and does not take service selectors.
Stop one service with `kranz stop worker`, or stop everything with `kranz down`.
```

## Read the logs

```console
$ kranz logs --tail 20
$ kranz logs api --follow
$ kranz logs --since 5m
```

Logs survive the service. A worker that crashed two minutes ago still answers
`kranz logs worker`, which is when you actually need it.

Actions keep their own history under the same name `kranz action run` uses, so
an action that has already finished can be read again without running it twice:

```console
$ kranz logs api/migrate
$ kranz logs analytics/stats --run -1    # the latest execution
$ kranz logs analytics/stats --run -2    # the one before it
$ kranz logs analytics/stats --run 7     # run number 7
$ kranz logs analytics/stats --runs 3    # the last three
```

A positive `--run` is the run number Kranz assigned; a negative one counts back
from the newest run still buffered, which is what keeps `-1` meaning "the
latest" as older runs age out of the buffer.

A bare service name means "recent lines", because a service streams without
end. A bare action name means its whole latest run: an action produces a
finite, self-contained report, and capping that at the last lines would cut off
the part explaining what the run did. `--tail` and `--all` still override both.

The timestamp and label columns exist to tell interleaved streams apart, which
is exactly what reading one stream back does not need:

```console
$ kranz logs analytics/stats --plain            # the output as the command printed it
$ kranz logs analytics/stats --no-timestamps    # keep the labels, drop the clock
$ kranz logs api --with-actions --no-labels
```

`--source` narrows to where a line came from — `stdout`, `stderr`, or `kranz`
for the lifecycle notes Kranz writes into the buffer itself. It narrows before
`--tail`, so `--source stderr --tail 20` means twenty error lines rather than
whichever errors survive in the last twenty lines of everything:

```console
$ kranz logs api --source stderr --tail 20
$ kranz logs analytics/stats --source stdout --plain > report.txt
```

## What a selector means

One rule everywhere. A name a service answers to means that service; a name no
service answers to is tried as a tag. `kranz plan api`, `kranz status api` and
`kranz stop api` therefore always cover the same services.

Actions extend the rule rather than change it: `OWNER/ACTION` addresses one
action, using the same name `kranz action run` uses. Because of that, a service
and an action group may not share a name — the actions under the second one
would be unreachable — and `kranz config check` rejects a project that tries.

A service name means the service's own command; a group name has no command of
its own and so no stream. `--with-actions` folds an owner's actions into one
timeline, labelled so every line says where it came from:

```console
$ kranz logs api --with-actions
2026-08-21T09:12:04.118+03:00 [api stdout] listening on :3000
2026-08-21T09:12:31.902+03:00 [api/migrate#2 stdout] applied 3 migrations
```

Lifecycle hooks are not addressed separately: their output is part of the life
of the service they act on, and `kranz logs api` already carries it.

Buffers live in the runtime and are gone after `kranz down`. To reclaim one
sooner:

```console
$ kranz logs clear api
$ kranz logs clear analytics/stats
$ kranz logs clear --force          # every stream in the project
```

## Open the interface on a running project

```console
$ kranz attach
```

The TUI connects to the runtime that is already there. Quitting the TUI leaves
it running; `down` stops it.

## Work on another project without leaving this one

`-p` names a runtime explicitly and always wins over the current directory:

```console
$ kranz -p billing status
$ kranz -p billing logs api --tail 20
```

You can use the runtime's NAME, its ID, or a unique ID prefix.

## Look before you start

None of these need a runtime, so they answer on a project that has never been
started:

```console
$ kranz config check
$ kranz doctor
$ kranz plan
$ kranz list services
$ kranz ports
```

`plan` is the useful one when a start is not doing what you expected: it prints
the waves the runtime will actually use.

```console
$ kranz plan
Wave 1:
  shared-infra
Wave 2:
  migrate  (after shared-infra)
Wave 3:
  catalog-api  (after migrate)
  billing-api  (after migrate)
```

## Scripting

Non-interactive commands that return a result take `--output json` and answer
with a versioned envelope, so a script never parses prose. The interactive TUI,
help text, and generated completion scripts remain text:

```bash
if ! kranz doctor --output json > report.json; then
  echo "preflight failed"
  exit 1
fi

kranz status --output json |
  jq -r '.data.services[] | select(.state != "running") | .name'
```

Mutation results carry what changed. For example,
`restart api --output json` returns the full service expansion, and
`reload --output json` returns the added, removed, restarted, and updated
sets. `up -d --output json` returns the runtime's full ID, name, PID, and mode.

Exit codes distinguish the failures worth branching on: `2` is your command
being wrong, `3` is the configuration being wrong, `4` is something not
existing, `6` is a runtime that is not answering. The full table is in the
[CLI reference](../reference/cli#exit-codes).

## A whole session

```bash
kranz init --from Procfile   # convert what the project already has
kranz config check           # confirm it loads
kranz up -d                  # start it in the background
kranz ps                     # confirm the runtime exists
kranz logs --tail 20         # see what happened
kranz restart api            # act on one service
kranz attach                 # open the interface on it
kranz down                   # stop everything
```
