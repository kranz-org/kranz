# Log tools playground

Try a custom log processor with synthetic live output and repeatable action
reports. The default colorizer needs only standard shell tools. No network
ports, credentials or external requests are involved.

From the repository root:

```sh
make dev
./bin/kranz-dev -C examples/log-tools
```

Select `api` and press `s`. Its initial burst fills the panel for immediate
scrolling, then a new record arrives every second. `Ctrl+G` switches between
processed and original output. Try `/` with `WARN|ERROR`, Enter and Escape;
Escape again clears the filter. `w` toggles wrapping, `i` capture timestamps,
and `f` pauses or resumes following.

Restart `api` with `r` and confirm to create another run. Use `x`, `[` / `]`,
`Shift+X` and `Shift+3` to compare individual, combined and pinned histories.
Expand `api`, select `report` and press `s` repeatedly for finite action runs.

## Try tailspin

Install tailspin separately, then stop the first example runtime before
changing profiles:

```sh
brew install tailspin
./bin/kranz-dev -C examples/log-tools --override tailspin.yaml
```

Start `api`. Its selected history passes through `tspin --print` inside the log
panel. Use `Ctrl+G` to compare. Replace `command` in the override to try another
stdin/stdout tool or pipeline; disable its pager and enable flushing for live
output. A missing or failed command falls back to original logs.

## Try less

After stopping the previous example runtime:

```sh
./bin/kranz-dev -C examples/log-tools --override viewer.yaml
```

Start `api`, choose runs and optionally apply a regex filter, then press
`Ctrl+G`. The viewer receives a frozen snapshot. Press `q` in less to return to
the same Kranz context.

## Cleanup

Press `q` in Kranz and choose to stop the example runtime. If you detached:

```sh
./bin/kranz-dev -C examples/log-tools down
```

Full source and profiles: [examples/log-tools](https://github.com/kranz-org/kranz/tree/main/examples/log-tools).
