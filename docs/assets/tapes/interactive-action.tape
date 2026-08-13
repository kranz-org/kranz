# Regenerate with: vhs docs/assets/tapes/interactive-action.tape
# Run from the repository root with a release-stamped binary in ./bin:
#   make build VERSION=v0.7.0
Output docs/assets/interactive-action.gif

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 620
Set Padding 16
Set LineHeight 1.2

Hide
Type "cd examples/prerequisites && export PATH=$PWD/../../bin:$PATH && clear"
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 2s

# Expand the service and focus the action that has to be answered.
Enter
Sleep 1500ms
Down
Down
Sleep 1500ms

# Kranz warns before it leaves the screen.
Type "s"
Sleep 4s
Enter
Sleep 3s

# The command owns the terminal; answer it.
Type "y"
Enter
Sleep 4s

# Kranz is back, with the result recorded.
Sleep 4s

Hide
Type "q"
Sleep 1s
Type "y"
Sleep 2s
