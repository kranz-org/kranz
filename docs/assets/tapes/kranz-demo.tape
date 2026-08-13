# Regenerate with: vhs docs/assets/tapes/kranz-demo.tape
# The site hero. Run from the repository root with a built binary in ./bin.
Output docs/assets/kranz-demo.gif

Set Shell "bash"
Set FontSize 13
Set Width 1180
Set Height 620
Set Padding 16

Hide
Type "cd examples/full-stack && export PATH=$PWD/../../bin:$PATH && clear"
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 3s

# Start the whole graph and let the dependency order play out.
Type "a"
Sleep 1s
Type "s"
Sleep 10s

# Details, then logs.
Type "2"
Sleep 4s
Down
Sleep 3s
Type "3"
Sleep 4s

Hide
Type "1"
Type "q"
Sleep 1s
Type "y"
Sleep 4s
