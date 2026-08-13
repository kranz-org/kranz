# The site hero, recorded from the MoonFlight showcase project.
# Regenerate from the repository root with a 0.7.0 binary in ./bin:
#   make build VERSION=v0.7.0 && vhs docs/assets/tapes/kranz-demo.tape
Output docs/assets/kranz-demo.gif

Set Shell "bash"
Set FontSize 13
Set Width 1180
Set Height 600
Set Padding 16

Hide
Type "cd examples/moonflight && export PATH=$PWD/../../bin:$PATH && rm -rf .kranz-demo && clear"
Enter
Sleep 1s
Show

Type "kranz"
Enter
Sleep 1500ms

# Start everything and let the dependency order play out: infrastructure, the
# migration, the APIs, then the gateway and everything waiting on it.
Type "a"
Sleep 1s
Type "s"
Sleep 22s

# Focus the edge service, pin its logs beside the live ones, and read Details.
Down
Down
Sleep 2s
Type "#"
Sleep 5s
Type "2"
Sleep 5s

Hide
Type "1"
Type "q"
Sleep 1s
Type "y"
Sleep 4s
