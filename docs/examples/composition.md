# Configuration composition

This catalog combines autonomous native Kranz files, a Procfile, and a supported
Process Compose file into one effective project. It is safe to explore: the
commands below only load and inspect configuration; they do not start services.

![Color TUI configuration map switching between source and service views](../assets/config-composition.gif)

## Inspect the mixed workspace

Run from the repository root after building or installing Kranz:

```bash
kranz config check -C examples/composition/workspace
kranz config sources -C examples/composition/workspace
kranz config sources -C examples/composition/workspace --by-service
```

The workspace resolves 12 services. Its native files form the recursive tree;
the Procfile and Process Compose sources are terminal leaves. Local defaults,
dotenv files, relative directories, overrides, protected values, name
qualification, and diamond deduplication remain attributable to their sources.

Press `m` in the TUI to open the same Config Map and `Tab` to switch between the
source tree and the reverse service-to-source view.

## Try each boundary

| Scenario | What it demonstrates |
| --- | --- |
| `workspace` | Exact, glob, discovery, recursive native files, mixed-format leaves, local and explicitly qualified dependencies, overrides, protected values, and collisions |
| `graph-depth` | A bounded include graph and the `include_depth_truncated` diagnostic |
| `symlinks` | Default symlink refusal and canonical deduplication with `--follow-symlinks` |
| `virtual-root` | A stable project discovered without a conventional root file |
| `negatives/cycle` | The complete include-cycle chain |
| `negatives/empty-glob` | A glob that matched no configuration |
| `negatives/missing-explicit` | A missing exact source |
| `negatives/remote` | Rejection of non-local sources |
| `negatives/type-mismatch` | An override value with an incompatible type |
| `negatives/forbidden-override` | Composition directives rejected inside an override layer |
| `negatives/ambiguous-dependency` | A cross-source bare dependency with two equally valid targets |

For example:

```bash
kranz config check -C examples/composition/graph-depth
kranz config check -C examples/composition/virtual-root
kranz config check -C examples/composition/symlinks --follow-symlinks
```

The negative scenarios are expected to fail with their named diagnostic. The
checked-in catalog is loaded by automated tests, so examples cannot silently
drift away from the configuration loader.

In `workspace`, both repository configs declare `api`. The checkout `worker`
uses the local short form `depends_on: [api]`, which resolves to
`checkout/api`; `root-task` lives outside both repositories and uses the
qualified `depends_on: [checkout/api]` form. The separate
`negatives/ambiguous-dependency` scenario shows why a root-level bare `api`
cannot be guessed safely.

See [Composing configurations](../guide/composition) for the full contract and
the repository's `examples/composition/README.md` for additional commands.
