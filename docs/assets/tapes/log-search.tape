# Regenerate with: vhs docs/assets/tapes/log-search.tape
# Run from the repository root with a release-stamped binary in ./bin:
#   make build VERSION=v0.7.0
Output docs/assets/log-search.gif

Set Shell "bash"
Set FontSize 13
Set Width 1180
Set Height 560
Set Padding 16

Hide
Type "cd examples/moonflight && export PATH=$PWD/../../bin:$PATH && rm -rf .kranz-demo && clear"
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 2s
Type "a"
Sleep 500ms
Type "s"
Sleep 16s

# Focus the edge service, whose log mixes several routes.
Down 2
Sleep 1500ms

# Filter its logs with a regular expression.
Type "3"
Sleep 2s
Type "/"
Sleep 1500ms
Type "catalog|featured"
Sleep 2s
Enter
Sleep 5s

# Escape keeps the highlight and returns to the logs.
Escape
Sleep 3s

Hide
Type "1"
Type "q"
Sleep 1s
Type "y"
Sleep 3s
