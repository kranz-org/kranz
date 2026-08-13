# Regenerate with: vhs docs/assets/tapes/appearance.tape
# Run from the repository root with a release-stamped binary in ./bin:
#   make build VERSION=v0.7.0
Output docs/assets/appearance.gif

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

# Ctrl+T opens the picker; every change previews live.
Ctrl+T
Sleep 3s
Down
Sleep 1500ms
Down
Sleep 1500ms
Down
Sleep 2s
Right
Sleep 2s
Right
Sleep 2s

# Escape leaves without saving anything.
Escape
Sleep 3s

Hide
Type "q"
Sleep 1s
Sleep 2s
