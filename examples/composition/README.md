# Configuration composition examples

Runnable scenarios for composing independent Kranz configs. Nothing here starts
a service; every command only loads and inspects. Build once (`make build`) and
run from `app/`.

Running `kranz` from this directory loads its own root config, which composes
the `workspace` scenario; each scenario still works alone via `-C`.

| Scenario | Command | Expect |
| --- | --- | --- |
| `workspace` | `./bin/kranz config check -C examples/composition/workspace` | project `Aurora`, 12 services, including Procfile and process-compose leaves, local and qualified dependencies, plus diagnostics for qualified names, local overrides, and a deduplicated shared source |
| `graph-depth` | `... -C examples/composition/graph-depth` | 3 services, `level3-service` absent, `include_depth_truncated` on `level1/level2/kranz.yaml` |
| `symlinks` | `... -C examples/composition/symlinks` (add `--follow-symlinks`) | 2 services either way; the flag adds `source_deduplicated` for `real/kranz.yaml` |
| `virtual-root` | `... -C examples/composition/virtual-root` | project `virtual-root`, `alpha-api` and `beta-worker`; see [its README](virtual-root/README.md) |
| `negatives/*` | `.../negatives/<name>` | the matching error, see below |

## workspace

The main scenario covers all three include selectors, recursive native includes,
Procfile and process-compose terminal leaves, diamond dedupe, local defaults,
overrides, protected, name collisions, and dependency resolution across those
collisions:

```bash
./bin/kranz config explain -C examples/composition/workspace catalog/api
```

`overrides:` in a file overrides only that file's own services. To override the
composed graph, pass layers in order, relative to the root:

```bash
./bin/kranz config show -C examples/composition/workspace \
  --override overrides/10-ports.yaml \
  --override overrides/20-debug.yaml --output json
```

Each included file is also a complete config on its own, e.g.
`./bin/kranz config check -C examples/composition/workspace/repositories/catalog`.
Repeated `-f` composes rather than merges.

### Dependencies when names collide

Both repository configs export a service whose original name is `api`. In the
effective project they appear as `catalog/api` and `checkout/api`.

The checkout file's `worker` declares `depends_on: [api]`. Because that file
also declares its own `api`, the short reference stays local and is rewritten
to `checkout/api`. The workspace root has no local `api`, so its `root-task`
uses `depends_on: [checkout/api]` to select a cross-source dependency
explicitly. The service under `services/worker` demonstrates the third case: a
bare `log-collector` resolves across sources because there is exactly one
service with that original name.

Run `./bin/kranz graph -C examples/composition/workspace --format json` to see
the resolved names. For the rejected fourth case, run:

```bash
./bin/kranz config check \
  -C examples/composition/negatives/ambiguous-dependency
```

That root-level `depends_on: [api]` has no local match and two global matches,
so composition fails and lists both qualified choices instead of using
discovery order.

### See the composed result

`config sources` draws the include tree with what each file contributed, and
`--by-service` answers the reverse question, which file defined a service and
who overrode it:

```bash
./bin/kranz config sources -C examples/composition/workspace
./bin/kranz config sources -C examples/composition/workspace --by-service \
  --override overrides/10-ports.yaml --override overrides/20-debug.yaml
```

With both layers, `catalog/api.env.FEATURE_FLAG` shows three writers in order:
the catalog's local override, then `10-ports.yaml`, then `20-debug.yaml`, whose
`beta` wins. `config explain catalog/api` has the per-field chain, and the
dashboard's `m` key opens the same map.

## negatives

```bash
for d in cycle empty-glob remote missing-explicit type-mismatch forbidden-override ambiguous-dependency; do
  ./bin/kranz config check -C "examples/composition/negatives/$d"
done
```

Each fails with its specific error: cycle chain, empty glob, remote source,
missing explicit file, incompatible override type, forbidden override section.

The catalog is loaded by `internal/config/composition_examples_test.go`, so it
cannot drift from what Kranz accepts.
