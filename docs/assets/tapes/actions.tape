# Regenerate with: vhs docs/assets/tapes/actions.tape
# Run from the repository root with a release-stamped binary in ./bin:
#   make build VERSION=v0.12.1
Output docs/assets/actions.gif

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 620
Set Padding 16
Set LineHeight 1.2

Hide
Type "cd examples/moonflight && unset NO_COLOR && export TERM=xterm-256color COLORTERM=truecolor FORCE_COLOR=1 PATH=$PWD/../../bin:$PATH && rm -rf .kranz-demo && clear"
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 1200ms

# Expand catalog-api and focus its reindex action.
Down
Down
Enter
Down
Sleep 500ms

# Run the same action twice. Kranz keeps two distinct numbered results.
Type "s"
Sleep 1500ms
Type "s"
Sleep 1500ms

# Move back to run #1, forward to run #2, then inspect the retained history.
Type "["
Sleep 2s
Type "]"
Sleep 2s
Type "v"
Sleep 2500ms
Up
Sleep 1s
Down
Sleep 1s

Hide
Escape
Type "q"
Sleep 2s
