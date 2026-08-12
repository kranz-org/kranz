# Procfile quickstart

Use this example if you have a handful of development commands and want Kranz
without learning its YAML format first.

## What it demonstrates

- automatic `Procfile` discovery;
- one service per `name: command` line;
- adjacent `.env` loading;
- separate logs for concurrent processes;
- automatic listener discovery for a child process.

## Run it

From the repository root:

```bash
cp examples/procfile/.env.example examples/procfile/.env
cd examples/procfile
kranz
```

Kranz discovers `Procfile` because there is no native configuration in this
directory. You should see three stopped services: `clock`, `environment`, and
`web`.

<div class="demo-frame">

![From a Procfile to running services in Kranz](../assets/procfile-quickstart.gif)

</div>

## Walk through the UI

1. Press `a`, then `s`, to start all three.
2. Focus `clock`: its log prints the current time every two seconds.
3. Focus `environment`: its log shows `MESSAGE` from the adjacent `.env` file.
4. Focus `web` and inspect Details. The command contains a port, but Kranz does
   not parse shell text to guess it. Instead it observes the real listener from
   the process group and reports it as detected.
5. Open the displayed localhost URL in a browser.
6. Press `s`, confirm the stop, then `q`.

## The complete Procfile

```text
clock: while true; do date; sleep 2; done
environment: while true; do echo "MESSAGE=${MESSAGE:-hello from Procfile}"; sleep 5; done
web: python3 -u -m http.server ${PORT:-8123} --bind 127.0.0.1
```

There is intentionally no Kranz-specific syntax here. Procfile is the entry
point; native YAML is the upgrade path when you need dependencies or actions.

## Try changing it

Edit `.env`:

```dotenv
MESSAGE=hello from my project
PORT=8124
```

Press `Ctrl+L` in Kranz to reload. Restart the affected services and inspect the
new output and listener.

## Cleanup

Stop the services from Kranz, then optionally remove the copied file:

```bash
rm .env
```

Next: [add dependencies and readiness with native YAML](./native).
