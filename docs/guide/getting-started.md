# Five-minute quickstart

This walkthrough starts two harmless local processes and shows the main Kranz
workflow. You need macOS or Linux, Python 3, and a terminal. No Docker, YAML, or
existing project is required.

## Install

```bash
brew install kranz-org/tap/kranz
```

Or install with Go 1.24 or newer:

```bash
go install github.com/kranz-org/kranz/cmd/kranz@latest
```

## 1. Create a small playground

```bash
mkdir kranz-hello
cd kranz-hello
```

Create a file named `Procfile` with these two lines:

```text
web: python3 -u -m http.server 8000 --bind 127.0.0.1
worker: while true; do date; sleep 2; done
```

The first command serves the directory over HTTP. The second prints a heartbeat
every two seconds. Both keep running until stopped.

## 2. Open Kranz

```bash
kranz
```

You should see `web` and `worker` in panel `1`. Nothing starts automatically.

<div class="demo-frame">

![Creating a Procfile and operating it in Kranz](../assets/procfile-quickstart.gif)

</div>

## 3. Start and inspect

Try this sequence:

1. Press `a` to select every service.
2. Press `s` to start the selection.
3. Use `↑` and `↓` to focus `web` or `worker`.
4. Press `2` to focus Details. For `web`, Kranz discovers port `8000` from the
   process group even though Procfile has no port syntax.
5. Press `3` to focus Logs and watch each service separately.
6. Open [http://127.0.0.1:8000](http://127.0.0.1:8000) in a browser.

The header should report two active services. A green dot means running; a gray
dot means stopped; yellow means a start, stop, or dependency wait is in flight.

## 4. Stop safely

Return to panel `1`, keep both services selected, and press `s`. Kranz asks for
confirmation before every stop. Confirm it and watch both process groups exit.

Press `q` to leave. If process-supervised services are still running, quitting
also asks for confirmation and stops them.

## 5. What to try next

- Follow the full [Procfile example](../examples/procfile) to add `.env` values.
- Use [native YAML](../examples/native) for dependencies and readiness.
- Read [core concepts](./core-concepts) before modeling a real project.

## Choosing a configuration format

- **Procfile** — the smallest path for `name: command` processes.
- **Process Compose** — run `kranz` beside a supported
  `process-compose.yaml` without maintaining a second config.
- **Native `kranz.yaml`** — dependencies, probes, recovery, actions, explicit
  endpoints, appearance, and detached resources.

Continue with [Configuration](./configuration) or try the
[runnable examples](../examples).
