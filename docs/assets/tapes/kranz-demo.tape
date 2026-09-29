# Kranz v0.16.4: typed action parameters across two dark runtimes.
# Re-record with VHS 0.11.0: vhs docs/assets/tapes/kranz-demo.tape
# VHS 0.12.0 has an upstream cancelled-render regression.

Output docs/assets/kranz-demo.gif

Set Shell "bash"
Set FontSize 20
Set Width 1000
Set Height 620
Set Padding 16
Set TypingSpeed 35ms
Set Framerate 20

Hide
Type "DEMO_ROOT=/tmp/kranz-v016-hero"
Enter
Type `rm -rf "$DEMO_ROOT" && mkdir -p "$DEMO_ROOT/params" "$DEMO_ROOT/stack/scripts" "$DEMO_ROOT/tmp"`
Enter
Type `cp examples/action-parameters/kranz.yaml examples/action-parameters/seed.sh "$DEMO_ROOT/params/" && chmod +x "$DEMO_ROOT/params/seed.sh"`
Enter
Type `cp examples/moonflight/kranz.yaml "$DEMO_ROOT/stack/kranz.yaml" && cp examples/scripts/migrate.sh examples/moonflight/moonflight_service.py examples/moonflight/worker.py "$DEMO_ROOT/stack/scripts/" && chmod +x "$DEMO_ROOT/stack/scripts/"*`
Enter
Type `printf '\nui:\n  theme: tokyo-night\n  accent: "#7DCFFF"\n  background: theme\n' >> "$DEMO_ROOT/params/kranz.yaml"`
Enter
Type `printf '\nui:\n  theme: dracula\n  accent: "#FF9E64"\n  background: theme\n' >> "$DEMO_ROOT/stack/kranz.yaml"`
Enter
Type `cp bin/kranz "$DEMO_ROOT/kranz" && export TMPDIR="$DEMO_ROOT/tmp" PATH="$DEMO_ROOT:$PATH" TERM=xterm-256color COLORTERM=truecolor && unset NO_COLOR`
Enter
Type `kranz -C "$DEMO_ROOT/stack" up -d >/dev/null`
Enter
Type `kranz -C "$DEMO_ROOT/params" up -d >/dev/null`
Enter
Type `cd "$DEMO_ROOT/params" && clear`
Enter
Show

Type "kranz"
Enter
Sleep 2s
Down
Enter
Sleep 700ms
Down
Enter
Sleep 700ms
Down
Down
Enter
Sleep 700ms
Down
Down
Space
Sleep 700ms
Down
Down
Down
Enter
Sleep 1200ms
Type "p"
Sleep 1s
Down
Enter
Sleep 2s
Type "q"
Type "c"
Sleep 700ms
Enter
Sleep 2s

Hide
Type "q"
Enter
Type `kranz -C "$DEMO_ROOT/params" down >/dev/null 2>&1 || true`
Enter
Type `kranz -C "$DEMO_ROOT/stack" down >/dev/null 2>&1 || true`
Enter
Type `rm -rf "$DEMO_ROOT"`
Enter
