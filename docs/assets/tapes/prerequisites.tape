# Regenerate with: vhs docs/assets/tapes/prerequisites.tape
# Run from the repository root with a built binary in ./bin.
Output docs/assets/prerequisites.gif

Set Shell "bash"
Set FontSize 13
Set Width 1180
Set Height 620
Set Padding 12
Set PlaybackSpeed 1.0

Hide
Type "cd examples/prerequisites && export PATH=$PWD/../../bin:$PATH && clear"
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 3s

# Select every service, then start them.
Type "a"
Sleep 1s
Type "s"
Sleep 8s

# Inspect the service that owns the shared migration.
Type "2"
Sleep 3s
Type "3"
Sleep 4s

Hide
Type "q"
Sleep 1s
Type "y"
Sleep 3s
