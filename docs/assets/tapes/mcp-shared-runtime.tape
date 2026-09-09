# Privacy-safe MCP walkthrough. The right pane is a deterministic client that
# performs real MCP resource and tool calls while the left pane shows the same
# live runtime in the TUI. No fixture JSON, personal paths, or runtime IDs are
# rendered.
# Regenerate from the repository root:
#   make build VERSION=v0.14.0
#   vhs docs/assets/tapes/mcp-shared-runtime.tape
Output docs/assets/mcp-shared-runtime.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 620
Set Padding 12
Set LineHeight 1.2
Set Framerate 12

Hide
Type "DEMO_DIR=/private/tmp/kranz-mcp-demo; DEMO_SESSION=kranz-mcp-demo"
Enter
Type "make build VERSION=v0.14.0"
Enter
Type `mkdir -p "$DEMO_DIR"`
Enter
Type `cp examples/mcp-shared-runtime/kranz.yaml examples/mcp-shared-runtime/api.py examples/mcp-shared-runtime/mcp_client.py bin/kranz "$DEMO_DIR/"`
Enter
Type `"$DEMO_DIR/kranz" -C "$DEMO_DIR" down >/dev/null 2>&1 || true`
Enter
Type `"$DEMO_DIR/kranz" -C "$DEMO_DIR" up --start -d`
Enter
Sleep 2s
Type `tmux kill-session -t "$DEMO_SESSION" >/dev/null 2>&1 || true`
Enter
Type `tmux new-session -d -s "$DEMO_SESSION" -x 140 -y 38 "env -u NO_COLOR TERM=xterm-256color COLORTERM=truecolor FORCE_COLOR=1 $DEMO_DIR/kranz -C $DEMO_DIR attach"`
Enter
Type `tmux set-option -t "$DEMO_SESSION" status off`
Enter
Type `tmux set-option -t "$DEMO_SESSION" default-terminal tmux-256color`
Enter
Type `tmux set-option -t "$DEMO_SESSION" pane-border-status top`
Enter
Type `tmux set-window-option -t "$DEMO_SESSION:0" allow-rename off`
Enter
Type `tmux select-pane -t "$DEMO_SESSION:0.0" -T 'KRANZ · LIVE RUNTIME'`
Enter
Type `tmux split-window -h -p 44 -t "$DEMO_SESSION:0" "cd $DEMO_DIR && env -u NO_COLOR TERM=xterm-256color COLORTERM=truecolor FORCE_COLOR=1 PS1='' bash --noprofile --norc"`
Enter
Type `tmux select-pane -t "$DEMO_SESSION:0.1" -T 'AGENT WORKFLOW · KRANZ MCP'`
Enter
Type "clear"
Enter
Type `tmux attach-session -t "$DEMO_SESSION"`
Enter
Wait+Screen@15s /READINESS ready/
Sleep 1s
Show
Type "KRANZ_BIN=./kranz KRANZ_PROJECT_DIR=. python3 mcp_client.py walkthrough"
Enter
Wait+Screen@30s /no re-execution/
Sleep 3s

Hide
Ctrl+b
Type "d"
Sleep 1s
Type `tmux kill-session -t "$DEMO_SESSION" >/dev/null 2>&1 || true`
Enter
Type `"$DEMO_DIR/kranz" -C "$DEMO_DIR" down >/dev/null 2>&1 || true`
Enter
Type `rm -rf "$DEMO_DIR"`
Enter
Sleep 2s
