# The site hero, recorded from the MoonFlight showcase project.
# Regenerate from the repository root with the release binary in ./bin:
#   make build VERSION=v0.12.0 && vhs docs/assets/tapes/kranz-demo.tape
Output docs/assets/kranz-demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 620
Set Padding 16
Set LineHeight 1.2

Hide
Type "cd examples/moonflight && unset NO_COLOR && export TERM=xterm-256color COLORTERM=truecolor FORCE_COLOR=1 PATH=$PWD/../../bin:$PATH && rm -rf .kranz-demo && clear"
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 1200ms

# Start everything and let the dependency order play out: infrastructure, the
# migration, the APIs, then the gateway and everything waiting on it.
Type "a"
Sleep 500ms
Type "s"
Sleep 5s

# Expand catalog-api and run its reindex action twice. Each execution keeps an
# independent result even though the action has the same name.
Down
Down
Enter
Down
Sleep 500ms
Type "s"
Sleep 1500ms
Type "s"
Sleep 1500ms

# Browse the older and newer retained runs, then open their history.
Type "["
Sleep 1500ms
Type "]"
Sleep 1500ms
Type "v"
Sleep 2s

Hide
Escape
Type "q"
Sleep 1s
Type "y"
Sleep 2s
