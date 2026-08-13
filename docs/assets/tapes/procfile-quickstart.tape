# Regenerate with: vhs docs/assets/tapes/procfile-quickstart.tape
# Run from the repository root with a built binary in ./bin.
Output docs/assets/procfile-quickstart.gif

Set Shell "bash"
Set FontSize 13
Set Width 1180
Set Height 620
Set Padding 16

Hide
Type "cd examples/procfile && export PATH=$PWD/../../bin:$PATH && cp -f .env.example .env && clear"
Enter
Sleep 1s
Show

# The whole configuration is three lines of Procfile.
Type "cat Procfile"
Enter
Sleep 3s

Type "kranz"
Enter
Sleep 3s

Type "a"
Sleep 1s
Type "s"
Sleep 6s

# Details shows the listener discovered from the running process.
Type "2"
Sleep 5s

Hide
Type "1"
Type "q"
Sleep 1s
Type "y"
Sleep 3s
