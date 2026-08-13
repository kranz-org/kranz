# Regenerate with: vhs docs/assets/tapes/lifecycle.tape
# Run from the repository root with a built binary in ./bin.
Output docs/assets/lifecycle.gif

Set Shell "bash"
Set FontSize 13
Set Width 1180
Set Height 620
Set Padding 16

Hide
Type "cd examples/lifecycle && export PATH=$PWD/../../bin:$PATH && rm -rf .kranz-demo && clear"
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 3s

# Focus remote-stack, the fully managed detached resource.
Down
Down
Down
Sleep 1s
Type "s"
Sleep 5s

# Details separate lifecycle status from readiness.
Type "2"
Sleep 4s

# Stopping a detached resource always asks first.
Type "1"
Sleep 1s
Type "s"
Sleep 3s
Type "y"
Sleep 4s

Hide
Type "q"
Sleep 1s
Type "y"
Sleep 2s
