# Regenerate from the repository root with a release-stamped binary in ./bin:
#   make build VERSION=v0.15.0 && vhs docs/assets/tapes/config-composition.tape
Output docs/assets/config-composition.gif

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 620
Set Padding 16
Set LineHeight 1.2

Hide
Type "unset NO_COLOR && export TERM=xterm-256color COLORTERM=truecolor FORCE_COLOR=1 && clear"
Enter
Sleep 1s
Show

Type "bin/kranz -C examples/composition/workspace"
Enter
Sleep 2500ms

# Open the source-to-service composition map from the dashboard.
Type "m"
Sleep 3500ms

# Switch to the inverse service-to-source view, then return to the include tree.
Tab
Sleep 3500ms
Down 3
Sleep 1500ms
Tab
Sleep 3000ms

Hide
Escape
Type "q"
Sleep 2s
