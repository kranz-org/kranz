# Privacy-safe, real-agent MCP walkthrough. The recorded panes run from a
# neutral temporary directory. Setup, authentication, commands, cleanup, and
# the user's home directory stay hidden.
# Regenerate from the repository root:
#   make build VERSION=v0.11.1
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
Type "DEMO_DIR=/private/tmp/kranz-mcp-demo; DEMO_CODEX_HOME=/private/tmp/kranz-codex-demo; DEMO_SESSION=kranz-mcp-demo"
Enter
Type "make build VERSION=v0.11.1"
Enter
Type `mkdir -p "$DEMO_DIR" "$DEMO_CODEX_HOME"`
Enter
Type `cp examples/mcp-shared-runtime/kranz.yaml examples/mcp-shared-runtime/api.py examples/mcp-shared-runtime/demo_approvals.py bin/kranz "$DEMO_DIR/"`
Enter
Type `ln -sf "$HOME/.codex/auth.json" "$DEMO_CODEX_HOME/auth.json"`
Enter
Type `printf '[projects."/private/tmp/kranz-mcp-demo"]\ntrust_level = "trusted"\n' > "$DEMO_CODEX_HOME/config.toml"`
Enter
Type `"$DEMO_DIR/kranz" -C "$DEMO_DIR" down >/dev/null 2>&1 || true`
Enter
Type `"$DEMO_DIR/kranz" -C "$DEMO_DIR" up -d`
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
Type `tmux split-window -h -p 44 -t "$DEMO_SESSION:0" "env -u NO_COLOR TERM=xterm-256color COLORTERM=truecolor FORCE_COLOR=1 CODEX_HOME=$DEMO_CODEX_HOME codex -s read-only -C $DEMO_DIR -c 'mcp_servers={kranz={command=\"$DEMO_DIR/kranz\",args=[\"mcp\"]}}' 'Restart api and anything Kranz says depends on it. Wait until everything is ready again. Use only Kranz MCP—no shell commands. Begin the one-sentence final answer with Done:'"`
Enter
Type `tmux select-pane -t "$DEMO_SESSION:0.1" -T 'CODEX · KRANZ MCP'`
Enter
Type `python3 "$DEMO_DIR/demo_approvals.py" "$DEMO_SESSION:0.1" >/dev/null 2>&1 &`
Enter
Type `until tmux capture-pane -p -t "$DEMO_SESSION:0.1" | grep -q 'kranz.restart'; do sleep 1; done`
Enter
Type `while tmux capture-pane -p -t "$DEMO_SESSION:0.1" | grep -q 'usage limit'; do sleep 1; done`
Enter
Type "clear"
Enter
Type `tmux attach-session -t "$DEMO_SESSION"`
Enter
Wait+Screen@15s /READINESS ready/
Sleep 1s
Show
Wait+Screen@60s /kranz.wait/
Sleep 12s

Hide
Ctrl+b
Type "d"
Sleep 1s
Type `tmux kill-session -t "$DEMO_SESSION" >/dev/null 2>&1 || true`
Enter
Type `"$DEMO_DIR/kranz" -C "$DEMO_DIR" down >/dev/null 2>&1 || true`
Enter
Sleep 2s
