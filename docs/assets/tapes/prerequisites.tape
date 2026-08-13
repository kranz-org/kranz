# Regenerate with: vhs docs/assets/tapes/prerequisites.tape
# Run from the repository root with a release-stamped binary in ./bin:
#   make build VERSION=v0.7.0
Output docs/assets/prerequisites.gif

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 620
Set Padding 16
Set LineHeight 1.2
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
