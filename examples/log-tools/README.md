# Try your own log tool

This example prints synthetic logs only. It opens no network ports, sends no
requests and writes no project data. The default processor uses just `sh` and
standard shell tools; tailspin is optional.

From the repository root, build the development binary:

```sh
make dev
./bin/kranz-dev -C examples/log-tools
```

Select `api` and press `s`. Logs immediately fill the panel, then continue once
per second. `Ctrl+G` switches between the processor's colors and original
Kranz presentation. Press `/`, type `WARN|ERROR`, then Enter and Escape to
filter. Press Escape again to clear the filter. Try `w`, `i`, scrolling and `f`.

Restart `api` with `r` and confirm to create another service run. `x` switches
all runs / one run; `[` and `]` select runs, `Shift+X` switches every service,
and `Shift+3` pins the selected history. Expand `api`, select `report` and press
`s` twice to compare finite action runs too.

## Use tailspin

Yes: to try the real tailspin command, install it separately:

```sh
brew install tailspin
```

Quit the first example with `q` and choose to stop its runtime before changing
profiles. Then run from the repository root:

```sh
./bin/kranz-dev -C examples/log-tools --override tailspin.yaml
```

Start `api` with `s`. Kranz passes its selected logs to `tspin --print`; its
timestamps, numbers, URLs, IP addresses and severity words receive tailspin's
formatting inside the Kranz log panel. `Ctrl+G` lets you compare with the
original presentation. A missing `tspin` produces a notification and falls back
to the original logs.

To use a different tool, change `command` in the override. The command must read
stdin and write stdout without opening its own pager for stream mode. It can
also be a shell pipeline. Use `preserve_lines: true` only for a processor that
leaves text, line count and order unchanged.

## Use an interactive viewer

After stopping the previous example runtime:

```sh
./bin/kranz-dev -C examples/log-tools --override viewer.yaml
```

Start `api`, choose a run and optionally apply a regex filter, then press
`Ctrl+G`. `less` receives a frozen snapshot of that history. Press `q` in less
to return to the same Kranz run and viewport. This profile requires `less`.

## Cleanup

Press `q` and choose to stop the example runtime. If you detached instead, run
this from the repository root:

```sh
./bin/kranz-dev -C examples/log-tools down
```

