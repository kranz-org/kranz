# Regenerate with: vhs docs/assets/tapes/lifecycle.tape
# Run from the repository root with a release-stamped binary in ./bin:
#   make build VERSION=v0.7.0
Output docs/assets/lifecycle.gif

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 620
Set Padding 16
Set LineHeight 1.2

Hide
Type "cd examples/lifecycle && export PATH=$PWD/../../bin:$PATH && rm -rf .kranz-demo && clear"
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 3s

# Focus remote-stack, the fully managed detached resource, and start it.
Down
Down
Down
Sleep 1s
Type "s"
Sleep 5s

# Details: the resource exists, but readiness has not passed yet. Lifecycle
# status and health answer different questions.
Type "2"
Sleep 6s

# Back to the list, expand the service, and run the action that makes its
# readiness probe pass.
Type "1"
Sleep 1s
Enter
Sleep 1500ms
Down
Sleep 1s
Type "s"
Sleep 4s

# Move back onto the service itself and scroll to the health rows: readiness
# is passing now, so the dependent service is free to start.
Up
Sleep 1s
Type "2"
Sleep 2s
Down 6
Sleep 6s

# Stopping a detached resource always asks first.
Type "1"
Sleep 1500ms
Type "s"
Sleep 3s
Type "y"
Sleep 4s

Hide
Type "q"
Sleep 1s
Type "y"
Sleep 2s
