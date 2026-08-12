# What is Kranz?

Kranz is a terminal workspace for the processes and commands you run while
developing a project.

Without Kranz, a typical morning might look like this:

```text
terminal 1: docker compose up -d postgres redis
terminal 2: npm run api
terminal 3: npm run web
terminal 4: npm run worker
terminal 5: npm run migrate
```

You remember which command must run first, inspect five terminals when
something fails, and manually stop the right processes at the end. Kranz gives
that workflow one model and one interface.

## What you get

Kranz can:

- start one service or a complete dependency chain;
- wait until a dependency is ready, not merely until its process exists;
- collect service logs and one-shot command output;
- discover ports opened by a process and its children;
- run actions such as lint, test, build, migrate, or inspect;
- restart failed local processes according to an explicit policy;
- start and stop Docker Compose or remote infrastructure through lifecycle
  commands;
- reconnect to detached infrastructure on the next session through a status
  probe.

The interface stays in your terminal and is designed around the keyboard. The
three numbered panels are always the same:

| Panel | Purpose |
| --- | --- |
| `1` Services / Actions | Choose what to operate on |
| `2` Details | Inspect command, dependencies, health, ports, and lifecycle |
| `3` Logs | Read the selected service or action output |

## What Kranz is not

Kranz does not replace Docker, systemd, Kubernetes, or your production
deployment platform. It calls the tools your project already uses and provides
coordination and visibility for local development.

It also does not need a background daemon. Ordinary process-supervised services
stop when Kranz exits. Detached resources can deliberately survive the session
with `stop_on_exit: false`.

## The smallest useful setup

If these are the commands you already run:

```bash
python3 -m http.server 8000
npm run worker
```

put them in a `Procfile`:

```text
web: python3 -u -m http.server 8000 --bind 127.0.0.1
worker: npm run worker
```

Run `kranz` beside that file. That is enough to get one service list, separate
logs, process-group shutdown, and runtime port discovery.

When the project needs dependencies, probes, actions, or detached resources,
move to native `kranz.yaml`. You do not need to adopt every feature at once.

Next: follow the [five-minute quickstart](./getting-started).
