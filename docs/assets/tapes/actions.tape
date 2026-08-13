# Regenerate with: vhs docs/assets/tapes/actions.tape
# Run from the repository root with a release-stamped binary in ./bin:
#   make build VERSION=v0.7.0
Output docs/assets/actions.gif

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 620
Set Padding 16
Set LineHeight 1.2

Hide
Type "cd examples/moonflight && export PATH=$PWD/../../bin:$PATH && rm -rf .kranz-demo && clear"
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 2s

# A service action: expand the service, run it, watch its own output.
Down
Sleep 1s
Enter
Sleep 1500ms
Down
Sleep 1s
Type "s"
Sleep 5s

# A project action group owns operations that belong to no single service.
# It sits at the end of the list, after every service.
Down 12
Sleep 1500ms
Enter
Sleep 1500ms
Down
Sleep 1s

# A destructive action asks before it runs. Decline it.
Type "s"
Sleep 4s
Escape
Sleep 1500ms

# The harmless one beside it just runs.
Down
Sleep 1s
Type "s"
Sleep 5s

Hide
Type "q"
Sleep 1s
Type "y"
Sleep 2s
