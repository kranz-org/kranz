# Migrating layered configuration

Repeated `-f` arguments now compose independently runnable configurations.
They preserve file-local defaults, relative paths, metadata boundaries, and
stable service identities. Use ordered override layers when one file is only a
partial patch for another.

## Replace legacy layering

Before:

```bash
kranz -f kranz.yaml -f kranz.local.yaml
```

After, with the same patch intent:

```bash
kranz -f kranz.yaml --override kranz.local.yaml
```

The equivalent checked-in root declaration is:

```yaml
project: Shop
overrides:
  - kranz.local.yaml
services:
  api:
    command: ./bin/api
```

Keep repeated `-f` when both files are complete projects that should run on
their own:

```bash
kranz -f repositories/catalog/kranz.yaml -f repositories/checkout/kranz.yaml
```

If both sources contain `api`, Kranz reports a `display_name_qualified`
diagnostic and shows the new effective names. Stable IDs remain tied to each
source and original service key.

## Composition examples

Exact include:

```yaml
include:
  - path: repositories/catalog/kranz.yaml
```

Sorted glob include:

```yaml
include:
  - glob: repositories/*/kranz.yaml
```

Bounded discovery:

```yaml
include:
  - discover:
      root: repositories
      max_depth: 3
      follow_symlinks: false
```

Ordered override and final protected policy:

```yaml
overrides:
  - local/ports.yaml
  - local/debug.yaml
protected:
  services:
    database:
      env:
        TLS_MODE: required
```

Run `kranz config check` to inspect sources and migration diagnostics, then
`kranz config show --provenance` or `kranz config explain` to inspect each
field's explicit, default, override, and protected chain.
