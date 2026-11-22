# Run from the repository root:
#   make dev VERSION=0.19.0
#   vhs docs/assets/tapes/dependency-viewer.tape
Output docs/assets/dependency-viewer.gif

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 620
Set Padding 16
Set LineHeight 1.2
Set Framerate 15
Set TypingSpeed 5ms

# Use a fresh project, registry, and user settings for every capture.
Hide
Type "set +o history; unset PROMPT_COMMAND; PS1='demo$ '"
Enter
Type "demo_root=$(mktemp -d /tmp/kranz-demo.XXXXXX) && trap 'find $demo_root -depth -delete' EXIT"
Enter
Type "mkdir -p $demo_root/work $demo_root/user $demo_root/tmp"
Enter
Type "cp examples/dependency-viewer/kranz.yaml $demo_root/work/"
Enter
Type "clear; env -i PATH=/usr/bin:/bin TERM=xterm-256color COLORTERM=truecolor HOME=$demo_root/user XDG_CONFIG_HOME=$demo_root/user TMPDIR=$demo_root/tmp ${KRANZ_DEMO_BIN:-./bin/kranz-dev} -C $demo_root/work"
Enter
Sleep 2s
Type "g"
Sleep 500ms
Show

# Open with both directions visible around gateway.
Sleep 3s
Type "u"
Sleep 1500ms
Type "u"
Sleep 1500ms

# Check gateway and its direct dependency catalog, then open their plan.
Escape
Sleep 300ms
Space
Down
Space
Sleep 1s
Type "g"
Sleep 4500ms

Hide
Escape
Type "q"
Sleep 500ms
Type "y"
Sleep 1500ms
Type "exit"
Enter
