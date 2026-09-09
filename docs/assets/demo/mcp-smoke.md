# MCP demo smoke evidence

Recorded: 2026-09-09 on macOS arm64 with the v0.14.0 release candidate.

The deterministic client in `examples/mcp-shared-runtime/mcp_client.py` started
`kranz mcp`, negotiated MCP `2025-11-25`, and used only resource/tool calls.
Against one live background runtime it observed:

```text
MCP  status api                 -> running, ready
MCP  plan restart api           -> api, web, worker
MCP  restart api                -> accepted
MCP  wait ready                 -> api, web, worker
MCP  action_run api/migrate     -> run #1 succeeded
MCP  logs api/migrate#1         -> migration complete; schema=42
MCP  action_result same run     -> #1, no re-execution
```

The session was then stopped through the ordinary terminal CLI. The tape uses
the same client and example, so regenerated frames cannot substitute fixture
JSON for a Kranz result. It deliberately renders only stable product names and
results: no runtime ID, process ID, socket, personal path, or raw envelope is
shown in the published recording.
