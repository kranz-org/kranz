#!/usr/bin/env python3
"""Exercise log processors and a real less handoff in an isolated tmux PTY.

Uses disposable synthetic inputs and never prints host paths or runtime IDs.
"""
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import uuid


def smoke(binary):
    if not shutil.which("tmux") or not shutil.which("less"):
        print("SKIP: tmux or less unavailable")
        return 77
    socket = "kranz-log-tool-" + uuid.uuid4().hex[:8]
    with tempfile.TemporaryDirectory(prefix="kranz-log-tool-", dir="/tmp") as directory:
        root = Path(directory)
        state = root / "state"
        state.mkdir()
        env = dict(os.environ, TMPDIR=str(state), TERM="xterm-256color",
                   COLORTERM="truecolor", NO_COLOR="", CLICOLOR="1",
                   CLICOLOR_FORCE="", CI="", LESS="")
        (root / "source.py").write_text('''from pathlib import Path
p=Path("counter.txt")
run=int(p.read_text())+1 if p.exists() else 1
p.write_text(str(run))
for i in range(36): print(f"raw r{run} record {i:02d}", flush=True)
''')
        (root / "processor.py").write_text('''import sys
for line in sys.stdin:
    print("\\x1b[31m"+line.rstrip("\\n").replace("raw ", "tool ")+"\\x1b[0m", flush=True)
''')

        def write_config(terminal=False):
            tool = ('cp "$$KRANZ_LOG_FILE" viewer-input.txt; less "$$KRANZ_LOG_FILE"'
                    if terminal else shlex.join(["python3", "processor.py"]))
            command = shlex.join(["python3", "source.py"])
            (root / "kranz.yaml").write_text(
                "project: log-tool-smoke\nservices:\n  sample:\n"
                "    command: '" + command.replace("'", "''") + "'\n"
                "    ui:\n      logs:\n        command: '" + tool.replace("'", "''") + "'\n"
                "        mode: " + ("terminal" if terminal else "stream") + "\n")

        def command(args, check=True):
            return subprocess.run(args, env=env, capture_output=True, check=check)

        def tm(*args, check=True):
            return command(["tmux", "-L", socket, *args], check)

        def key(value):
            tm("send-keys", "-t", "smoke:tui.0", value)
            time.sleep(0.15)

        def frame(colored=False):
            args = ["capture-pane", "-p"]
            if colored:
                args.append("-e")
            return tm(*args, "-t", "smoke:tui.0").stdout.decode()

        def wait_for(predicate, label):
            deadline = time.monotonic() + 12
            while time.monotonic() < deadline:
                if predicate():
                    return
                time.sleep(0.1)
            raise RuntimeError(label)

        def search(pattern):
            key("/")
            key("C-u")
            tm("send-keys", "-t", "smoke:tui.0", "-l", pattern)
            key("Enter")
            key("Escape")

        started = False
        try:
            write_config()
            command([binary, "-C", directory, "up", "--start", "-d"])
            started = True
            wait_for(lambda: (root / "counter.txt").exists(), "first source run")
            # Wait for the finite source to be observed as stopped before start.
            wait_for(lambda: '"stopped"' in command([binary, "-C", directory, "status", "--output", "json"]).stdout.decode(), "source completion")
            command([binary, "-C", directory, "start", "sample"])
            wait_for(lambda: (root / "counter.txt").read_text() == "2", "second source run")
            attach = shlex.join(["env", "TMPDIR=" + str(state), "TERM=xterm-256color",
                                 "COLORTERM=truecolor", binary, "-C", directory, "attach"])
            tm("-f", "/dev/null", "new-session", "-d", "-x", "140", "-y", "30",
               "-s", "smoke", "-n", "tui", "-c", directory, attach)
            wait_for(lambda: "tool r2 record 35" in frame(), "stream command output")
            if any("LOGS" in line and "Ctrl+G" in line for line in frame().splitlines()):
                raise RuntimeError("shortcut hint in log header")
            if "\x1b[31m" not in frame(True):
                raise RuntimeError("processor colors")
            key("3")
            key("Up")
            wait_for(lambda: "BROWSING" in frame(), "processed scrolling")
            key("w")
            key("x")
            wait_for(lambda: "RUN #2/2" in frame(), "latest run scope")
            key("[")
            wait_for(lambda: "tool r1 record 35" in frame() and "tool r2 record" not in frame(), "historical run isolation")
            key("#")
            key("l")
            wait_for(lambda: "tool r1 record" in frame() and "tool r2 record" in frame(), "independent pinned scope")
            key("#")
            search("^tool r2 record 00$")
            wait_for(lambda: "tool r2 record 00" in frame() and "tool r2 record 35" not in frame(), "processed regex filter")
            key("Escape")
            key("C-g")
            wait_for(lambda: "raw r2 record 35" in frame(), "original output toggle")
            key("C-g")
            wait_for(lambda: "tool r2 record 35" in frame(), "processor resume")
            key("X")
            key("X")
            search("^tool r1 record 00$")
            wait_for(lambda: "tool r1 record 00" in frame(), "global all-runs switch")
            key("Escape")
            key("l")
            print("PASS: processor colors, runs, pin, regex, scrolling, wrapping, original toggle")

            write_config(terminal=True)
            key("C-l")
            wait_for(lambda: "VIEWER" in frame(), "terminal mode reload")
            search("^raw r2 record 00$")
            key("C-g")
            wait_for(lambda: (root / "viewer-input.txt").exists() and "(END)" in frame(), "interactive less handoff")
            if (root / "viewer-input.txt").read_text() != "raw r2 record 00\n":
                raise RuntimeError("viewer snapshot scope and regex")
            key("q")
            wait_for(lambda: "KRANZ" in frame() and "RUN #2/2" in frame() and "FILTER" in frame(), "viewer return context")
            print("PASS: real less viewer, filtered snapshot, return to run and search context")
            return 0
        finally:
            tm("kill-server", check=False)
            if started:
                command([binary, "-C", directory, "down"], check=False)


if __name__ == "__main__":
    try:
        executable = Path(sys.argv[1] if len(sys.argv) > 1 else "bin/kranz-dev").resolve()
        sys.exit(smoke(str(executable)))
    except Exception as error:
        detail = str(error) if isinstance(error, RuntimeError) else type(error).__name__
        print("FAIL: log tool terminal check (" + detail + "); private output omitted")
        sys.exit(1)
