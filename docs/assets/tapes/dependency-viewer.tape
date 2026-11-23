# Run from the repository root:
#   make build VERSION=v0.19.1 COMMIT=demo BUILD_TIME=demo
#   vhs docs/assets/tapes/dependency-viewer.tape
# Crop out the dashboard margins, leaving only the viewer.
#   ffmpeg -y -i /tmp/kranz-dependency-viewer.gif -filter_complex_threads 1 \
#     -filter_complex "crop=918:724:102:18,split[a][b];[a]palettegen[p];[b][p]paletteuse" \
#     docs/assets/dependency-viewer.gif
# Keep the uncropped recording outside the repository.
Output /tmp/kranz-dependency-viewer.gif

Set Shell "bash"
Set FontSize 15
Set Width 1120
Set Height 760
Set Padding 16
Set LineHeight 1.2
Set Framerate 15
Set TypingSpeed 5ms

# Use a fresh project, registry, and user settings for every capture.
Hide
Type "set +o history; unset PROMPT_COMMAND; PS1='demo$ '"
Enter
Type "demo_root=$(mktemp -d /tmp/kranz-demo.XXXXXX) && trap 'find $demo_root -depth -delete' EXIT"
Enter
Type "mkdir -p $demo_root/work $demo_root/user $demo_root/tmp"
Enter
Type "cp examples/dependency-viewer/kranz.yaml $demo_root/work/"
Enter
Type "clear; env -i PATH=/usr/bin:/bin TERM=xterm-256color COLORTERM=truecolor HOME=$demo_root/user XDG_CONFIG_HOME=$demo_root/user TMPDIR=$demo_root/tmp ./bin/kranz-dev -C $demo_root/work"
Enter
Sleep 2s
Type "g"
Sleep 500ms
Show

# Follow the graph around gateway and open the Start steps.
Sleep 2500ms
Tab
Sleep 3s

# Compare a selected-only Start and the default readiness waves.
Type "f"
Sleep 2s
Type "f"
Sleep 1500ms

# Execute the Start from Steps and see the live graph during readiness.
Enter
Sleep 1800ms
Tab
Sleep 6s
Tab
Sleep 3s

Hide
Escape
Type "q"
Sleep 500ms
Type "y"
Sleep 1500ms
Type "exit"
Enter
