# Dependency viewer playground

Use this project to try the TUI dependency graph and operation preview. The
project opens no ports. Its services run short shell commands or sleep, and the
health actions write only an ignored marker under `.kranz-demo/`.

## Open the graph

From the repository root, build and open the example.

```bash
make dev
./bin/kranz-dev -C examples/dependency-viewer
```

1. Focus `gateway` and press `g`. Above it, `catalog` and `search` share
   `datastore`. Below it, `dashboard` and `reports` depend on `gateway`, and
   `export` depends on `reports`.
2. Use `d` and `u` to hide or show the two directions independently.
3. Press `Esc`, start `gateway` with `s`, and open the graph again. During the
   six-second readiness delay, watch `datastore` check readiness and
   `gateway` queue. After the gate passes, the graph shows the running
   services, the exit code of `schema`, and hollow circles on stopped services.
4. With the graph open, run the following from a second shell at the
   repository root. The graph updates as the actual probe fails and recovers.

```bash
./bin/kranz-dev -C examples/dependency-viewer actions run datastore/fail-liveness
./bin/kranz-dev -C examples/dependency-viewer actions run datastore/restore-liveness
```

Wait until the graph shows the liveness failure before running the second
command.

## Preview a batch

After `Esc`, check `catalog` and `search` with `Space`, then press `g` to open
their plan. Press `r` to preview restart, then `f` to compare it with restarting
only the checked services. Switch to the graph with `Tab` to see both checked
services in the center and their shared dependencies above them.

Press `Esc`, uncheck `search`, and check `gateway` so `gateway` and `catalog`
are the two targets. Press `g` to see the dependency between them. The plan
draws that relationship among the requested services or in the operation
steps, depending on their current states.

Restore liveness if you changed it, then quit the TUI with `q` and confirm
shutdown.
