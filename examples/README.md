# Runnable examples

Every example uses only standard shell tools and Python 3, and keeps its traffic
on localhost. Run Kranz from the example directory so conventional config-file
discovery and relative commands work together.

## Procfile: zero configuration

```bash
cp examples/procfile/.env.example examples/procfile/.env
cd examples/procfile
kranz
```

Press `a`, then `s`. The `web` service has no declared port; select it and open
Details to see the actual listener discovered from its process group. The
adjacent `.env` supplies the greeting and port, while existing shell variables
would take precedence.

## Native Kranz YAML: lifecycle features

```bash
cd examples/native
kranz
```

This example combines a one-shot migration, HTTP readiness, a dependent worker,
and a web server whose port is learned at runtime. Start the full stack with
`a`, then `s`.

## Process Compose compatibility

```bash
cd examples/process-compose
kranz
```

This is a small compatible Process Compose project with a readiness-gated
worker. It demonstrates trying Kranz without maintaining a second config.

## Runtime port discovery lab

```bash
cd examples/runtime-ports
kranz
```

The four services show detected-only, declared-and-listening, deliberately
different declared/detected, and explicit opt-out behavior side by side. The
example uses ports `18301`–`18303` and a deliberately unused hint `18399`.
