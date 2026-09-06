# The site hero, recorded from two synthetic local projects.
# Regenerate from the repository root with the release binary in ./bin:
#   make build VERSION=v0.13.0 && vhs docs/assets/tapes/kranz-demo.tape
Output docs/assets/kranz-demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 620
Set Padding 16
Set LineHeight 1.2

Hide
Type "DEMO_ROOT=/private/tmp/kranz-runtime-switching-demo"
Enter
Type `export TMPDIR="$DEMO_ROOT/tmp" && mkdir -p "$TMPDIR"`
Enter
Type `bin/kranz -C "$DEMO_ROOT/moonflight" down >/dev/null 2>&1 || true`
Enter
Type `bin/kranz -C "$DEMO_ROOT/native" down >/dev/null 2>&1 || true`
Enter
Type `rm -rf "$DEMO_ROOT/moonflight" "$DEMO_ROOT/native" "$DEMO_ROOT/scripts" && mkdir -p "$DEMO_ROOT/moonflight" "$DEMO_ROOT/native" "$DEMO_ROOT/scripts"`
Enter
Type `cp examples/moonflight/kranz.yaml "$DEMO_ROOT/moonflight/"`
Enter
Type `cp examples/native/kranz.yaml "$DEMO_ROOT/native/"`
Enter
Type `cp examples/scripts/migrate.sh examples/scripts/moonflight_service.py examples/scripts/worker.py "$DEMO_ROOT/scripts/"`
Enter
Type `cp bin/kranz "$DEMO_ROOT/"`
Enter
Type `env -u NO_COLOR TERM=xterm-256color COLORTERM=truecolor FORCE_COLOR=1 "$DEMO_ROOT/kranz" -C "$DEMO_ROOT/native" up -d`
Enter
Sleep 2s
Type `cd "$DEMO_ROOT/moonflight" && unset NO_COLOR && export TERM=xterm-256color COLORTERM=truecolor FORCE_COLOR=1 PATH="$DEMO_ROOT:$PATH" && clear`
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 1200ms

# Start everything and let the dependency order play out: infrastructure, the
# migration, the APIs, then the gateway and everything waiting on it.
Type "a"
Sleep 500ms
Type "s"
Sleep 5s

# Open the live runtime list and switch to the second project in place.
Type "p"
Sleep 2s
Down
Enter
Sleep 2500ms

# Return to the first runtime. Its running services and UI state are intact.
Type "p"
Sleep 1500ms
Down
Enter
Sleep 2500ms

Hide
Type "q"
Sleep 1s
Type "d"
Sleep 1s
Type `"$DEMO_ROOT/kranz" -C "$DEMO_ROOT/moonflight" down >/dev/null 2>&1 || true`
Enter
Type `"$DEMO_ROOT/kranz" -C "$DEMO_ROOT/native" down >/dev/null 2>&1 || true`
Enter
Type `rm -rf "$DEMO_ROOT"`
Enter
Sleep 2s
