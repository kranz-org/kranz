# Composing configurations

A Kranz project can be assembled from several configuration files. Every file
is a complete, independently runnable configuration: a repository keeps its own
`kranz.yaml`, and a workspace combines those files without copying them.
Override layers change values on top of the result, and `protected` values
enforce the ones that must not change.

![Color TUI configuration map switching between source and service views](../assets/config-composition.gif)

## Combine projects from the command line

Repeated `-f` composes several autonomous configurations into one project:

```bash
kranz -f repositories/catalog/kranz.yaml -f repositories/checkout/kranz.yaml
kranz -f 'repositories/*/kranz.yaml'
```

Each file keeps its own `project`, `ui`, `defaults`, adjacent `.env`, and
relative paths. Only its services and action groups join the project.

## Include other configurations

A root file lists what it composes under `include`, by exact path, sorted glob,
or bounded discovery:

```yaml
project: Workspace
include:
  - path: platform/kranz.yaml
  - glob: services/*/kranz.yaml
  - discover:
      root: repositories
      max_depth: 3
```

Included files can include further files. A file reached twice loads once, a
cycle is an error, and `max_depth` on an include entry limits how deep the
include graph goes below it. When the working directory has no root file, Kranz
discovers the configurations below it and composes them into a virtual project
named after the directory. The [configuration reference](../reference/configuration#composition)
lists every rule.

Native `kranz.yaml` files are the recursive nodes of the tree. `Procfile` and
supported `process-compose.yaml` files can appear anywhere as terminal leaves:
their services join the same effective graph and keep paths relative to their
own directories, but those formats cannot declare Kranz `include` or
`overrides` sections. A composition-wide `--override` can still patch their
effective services after the graph is assembled. Process Compose's conventional
`process-compose.override.yaml` remains supported as part of that leaf.

## Change values with override layers

An override layer is a partial file that patches services. Where the layer is
declared decides what it can reach:

- **Inside a file.** The file's own `overrides:` list patches only that file's
  services, by their original names, before the file joins the project.

  ```yaml
  # repositories/catalog/kranz.yaml
  overrides:
    - overrides/local.yaml
  ```

- **On the command line.** `--override PATH`, repeatable and applied in order,
  patches the composed project, so it can reach any service by its display
  name. `KRANZ_OVERRIDE` sets the same list from the environment.

  ```bash
  kranz -f kranz.yaml --override kranz.local.yaml
  ```

```yaml
# kranz.local.yaml — only the differences
services:
  catalog/api:
    ports: [18111]
    env:
      LOG_LEVEL: debug
```

Mappings merge, sequences replace, and `null` removes a value. A layer is a
pure patch: it cannot declare `defaults`, `include`, `overrides`, or
`protected`.

## Enforce values with protected

`protected` values apply after composition and every override layer. When
several files protect the same field, the outermost file wins:

```yaml
protected:
  services:
    checkout/api:
      env:
        TLS_MODE: required
```

## Service names

A service keeps its short name while it is unique. When two files declare the
same name, both are qualified by the smallest useful directory prefix, for
example `catalog/api` and `checkout/api`, and `kranz config check` reports a
`display_name_qualified` diagnostic.

Dependencies are resolved after those display names have been allocated. The
source that declares the reference is part of the lookup:

```yaml
# repositories/checkout/kranz.yaml
services:
  api:
    command: ./serve
  worker:
    command: ./work
    depends_on: [api]
```

If another included file also declares `api`, the two services may be displayed
as `catalog/api` and `checkout/api`. The `worker` dependency above still means
the `api` from the checkout file. Kranz rewrites it to `checkout/api` in the
effective graph; the author does not need to predict the name allocator's
result.

If the declaring source has no local match, a bare reference may cross source
boundaries only when the original name is globally unique. For example, a
service in one file may use `depends_on: [log-collector]` when exactly one
composed source declares `log-collector`. If two external sources declare that
name, composition fails as ambiguous and lists their qualified display names.
Kranz never chooses one based on include or discovery order.

An outer source can disambiguate an intentional cross-source dependency with
the effective qualified name:

```yaml
services:
  root-task:
    command: ./run-task
    depends_on: [checkout/api]
```

Qualified display names are derived from the current composition. Adding or
removing a colliding source can change them, so prefer a same-source short name
when the dependency belongs to that autonomous configuration. After every
reload Kranz allocates names and resolves references again; an obsolete or
newly ambiguous reference rejects the new configuration instead of silently
retargeting the dependency.

The same rules apply to keys in `dependency_conditions` and to service
references in `before_start`. Internally, each service also has a stable ID
derived from its defining source and original key. That ID lets reload
reconciliation recognize the same running service across a display-name
change; display names remain the user-facing keys of the resolved dependency
graph.

## Check the result

1. `kranz config check` validates the project and lists its sources and
   diagnostics, such as `source_deduplicated` or `include_depth_truncated`.
2. `kranz config sources` draws every file as an include tree with the services
   each defined and the fields each overrode. `--by-service` lists each service
   with its defining file and every later override. In the dashboard, `m` opens
   the same map.
3. `kranz config explain SERVICE` shows the ordered chain for every field.

## Try it

`examples/composition` in the repository is a runnable catalog of every rule on
this page, including the cases that must fail. Its README lists a command and
the expected result for each scenario.
