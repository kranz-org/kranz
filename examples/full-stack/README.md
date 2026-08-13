# Full dependency graph

This safe localhost-only stack demonstrates a completed one-shot migration,
parallel APIs, readiness-gated fan-in, a dependent worker, and recovery policy.

Use native Kranz YAML:

```bash
kranz -f kranz.yaml
```

Or load the equivalent Process Compose subset explicitly:

```bash
kranz -f process-compose.yaml
```

The native stack uses ports `18881`–`18883`; the Process Compose variant uses
`18901`. It never connects outside `127.0.0.1` and writes no persistent data.
