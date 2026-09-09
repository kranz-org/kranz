# Focused v0.14.0 CLI recording: narrow status with a filter and a row template,
# preview what a restart touches, run it, then inspect the numbered runs and read
# only the newest run's retained logs.
# Regenerate from the repository root:
#   make build VERSION=v0.14.0
#   vhs docs/assets/tapes/cli-workflow.tape
Output docs/assets/cli-workflow.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 560
Set Padding 16
Set LineHeight 1.2
Set Framerate 12
Set TypingSpeed 18ms

Hide
Type "DEMO_DIR=/private/tmp/kranz-cli-demo"
Enter
Type `rm -rf "$DEMO_DIR" && mkdir -p "$DEMO_DIR"`
Enter
Type `cp examples/mcp-shared-runtime/kranz.yaml examples/mcp-shared-runtime/api.py bin/kranz "$DEMO_DIR/"`
Enter
Type `"$DEMO_DIR/kranz" -C "$DEMO_DIR" down >/dev/null 2>&1 || true`
Enter
Type `"$DEMO_DIR/kranz" -C "$DEMO_DIR" up --start -d`
Enter
Sleep 3s
Type `"$DEMO_DIR/kranz" -C "$DEMO_DIR" stop worker >/dev/null`
Enter
Sleep 1s
Type `cd "$DEMO_DIR" && unset NO_COLOR && export TERM=xterm-256color COLORTERM=truecolor FORCE_COLOR=1 PATH="$DEMO_DIR:$PATH" && PS1='$ ' && clear`
Enter
Show

# One service is stopped, so the filter and the row template both have work to do.
Type "kranz status"
Enter
Sleep 1500ms
Type "kranz status --filter state=running --format 'table {{.Name}}\t{{.State}}\t{{.Health}}\t{{.Uptime}}'"
Enter
Sleep 1800ms

# Preview the blast radius before acting: web depends on api, so it restarts too.
Type "kranz plan api --operation restart"
Enter
Sleep 1800ms
Type "kranz restart api"
Enter
Sleep 2s

# The restart is a second numbered run; logs address it as the newest one.
Type "kranz runs api --limit 2"
Enter
Sleep 2s
Type "kranz logs api --run -1 --tail 3"
Enter
Sleep 3s

Hide
Type `"$DEMO_DIR/kranz" -C "$DEMO_DIR" down >/dev/null 2>&1 || true`
Enter
Type `rm -rf "$DEMO_DIR"`
Enter
Sleep 2s
