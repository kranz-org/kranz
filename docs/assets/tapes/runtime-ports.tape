# Regenerate with: vhs docs/assets/tapes/runtime-ports.tape
# Run from the repository root with a release-stamped binary in ./bin:
#   make build VERSION=v0.7.0
Output docs/assets/runtime-ports.gif

Set Shell "bash"
Set FontSize 13
Set Width 1180
Set Height 620
Set Padding 16

Hide
Type "cd examples/runtime-ports && export PATH=$PWD/../../bin:$PATH && clear"
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 3s

# Start everything, then watch Details fill in the discovered listeners.
Type "a"
Sleep 1s
Type "s"
Sleep 7s
Type "2"
Sleep 5s
Down
Sleep 4s
Down
Sleep 4s

Hide
Type "1"
Type "q"
Sleep 1s
Type "y"
Sleep 3s
