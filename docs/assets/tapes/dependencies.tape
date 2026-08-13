# Regenerate with: vhs docs/assets/tapes/dependencies.tape
# Run from the repository root with a release-stamped binary in ./bin:
#   make build VERSION=v0.7.0
Output docs/assets/dependencies.gif

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

# Start one service at the end of the chain. Kranz pulls in what it needs.
Down 5
Sleep 1500ms
Type "s"
Sleep 3s

# Everything it depends on is queued and started in order.
Sleep 12s

# Details shows what each gate actually waited for.
Type "2"
Sleep 5s

Hide
Type "1"
Type "q"
Sleep 1s
Type "y"
Sleep 3s
