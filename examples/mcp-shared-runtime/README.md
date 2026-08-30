# MCP shared-runtime example

This local-only project proves that Kranz CLI and MCP calls reach one runtime.
It uses Python 3, `/bin/sh`, and localhost ports `18931` and `18932`; it needs no
credentials or network access.

The commands below use the `kranz` on your `PATH`. From the repository
root:

```bash
cd examples/mcp-shared-runtime
kranz up -d
kranz status
python3 mcp_client.py status
python3 mcp_client.py migrate
kranz down
```

The Python client starts an unbound MCP server, negotiates over stdio, and calls
real resources and tools. Set `KRANZ_RUNTIME` to a name returned by `runtimes`
to address another live project without restarting the server, and `KRANZ_BIN`
to run an uninstalled build such as `../../bin/kranz`.
It is deliberately small enough to audit; it is demo code, not a general MCP
SDK. See the [published walkthrough](https://kranz-org.github.io/kranz/examples/mcp-shared-runtime)
for the three-terminal TUI/CLI/MCP flow and cleanup notes.
