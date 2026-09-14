# Virtual root

There is deliberately no `kranz.yaml` in this directory. When the working
directory has no conventional root file, Kranz discovers the autonomous configs
below it and composes them into a virtual project:

- the project and runtime are named after the directory (`virtual-root`);
- the UI uses built-in defaults, because no root file declares any;
- each discovered config (`alpha/kranz.yaml`, `beta/kranz.yaml`) becomes a
  direct child of the virtual root.

```bash
./bin/kranz config check -C examples/composition/virtual-root
./bin/kranz config sources -C examples/composition/virtual-root
```

`alpha` and `beta` stay complete, independently runnable configs; each also
works alone via `-C examples/composition/virtual-root/alpha`.

The runtime picker is a different fallback: a bare `kranz` shows it only when
discovery finds no configuration at all and a local runtime is already running.
With neither, Kranz fails with `config_not_found`. This directory always has
configs, so it always loads as a virtual project.
