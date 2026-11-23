# Dependency viewer playground

Use this project to try the TUI dependency graph and run lifecycle operations. The
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
3. Start is selected when the viewer opens. Press `Tab` to review the Steps.
   Press `f` to see what changes when Start applies only to `gateway`. Press
   `f` again to restore the readiness waves.
4. Press `Enter` to start, then press `Tab` to return to Graph. During the
   six-second readiness delay, watch `datastore` check readiness and
   `gateway` queue. When the viewer shows **Start complete**, the graph
   shows the running services, the exit code of `schema`, and hollow
   circles on stopped services.
5. With the graph open, run the following from a second shell at the
   repository root. The graph updates as the actual probe fails and recovers.

```bash
./bin/kranz-dev -C examples/dependency-viewer actions run datastore/fail-liveness
./bin/kranz-dev -C examples/dependency-viewer actions run datastore/restore-liveness
```

Wait until the graph shows the liveness failure before running the second
command.

## Run a batch

After `Esc`, check `catalog` and `search` with `Space`, then press `g` to open
Steps. Press `r` to choose Restart, then `f` to limit the operation to the
checked services. Restart skips stopped targets. Switch to Graph with `Tab`
to see both targets in the center and their shared dependencies above them.
Press `Enter` in Graph to restart the two running services and wait for
**Restart complete**.

Press `Esc`, uncheck `search`, and check `gateway` so `gateway` and `catalog`
are the two targets. Press `g`, then `Tab` to see the dependency between them.
The plan draws that relationship among the requested services or in the
operation steps, depending on their current states. To add dependencies of both targets
to the dashboard selection, close the viewer and press `Shift+E`.

Restore liveness if you changed it, then quit the TUI with `q` and confirm
shutdown.
