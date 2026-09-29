# Kranz for Claude

Kranz lets Claude Code work with the development services already running in your local Kranz session. This plugin connects Claude to the local `kranz mcp` server and includes a skill that explains how to find the right runtime, inspect services and logs, and handle service changes safely.

Install Kranz on the same machine as Claude Code first. On macOS, run `brew install kranz-org/tap/kranz`; on systems with Go, run `go install github.com/kranz-org/kranz/cmd/kranz@latest`. Verify that `kranz mcp --help` works and that `kranz` is on Claude Code's `PATH`.

The plugin launches the installed `kranz mcp` command over local stdio. The server reads the local Kranz runtime registry and connects to the runtime that matches the current project, or asks the agent to select one. It can expose service state, logs, ports, actions, and lifecycle operations. It does not start a runtime when Claude connects. Service changes still require an explicit user request, as described in the bundled skill.

The local MCP server works in Claude Code and other supported local Claude surfaces. In Claude chat, where local MCP servers are unavailable, the skill cannot inspect or change your machine's services. See the [MCP guide](https://kranz-org.github.io/kranz/guide/mcp) for setup and behavior.
