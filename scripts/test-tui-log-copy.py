#!/usr/bin/env python3
"""Real tmux OSC 52 -> paste smoke. Never reads the desktop clipboard.

Run after make dev: python3 scripts/test-tui-log-copy.py [bin/kranz-dev]
Requires tmux and a POSIX shell. Uses only a disposable synthetic runtime and
an isolated tmux server with set-clipboard on; prints no host paths or IDs.
"""
import os
import re
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import uuid


def run_smoke(binary):
    if not shutil.which("tmux"):
        print("SKIP: tmux unavailable")
        return 77
    socket = "kranz-copy-smoke-" + uuid.uuid4().hex[:8]
    with tempfile.TemporaryDirectory(prefix="kranz-copy-", dir="/tmp") as directory:
        root = Path(directory)
        state = root / "state"
        state.mkdir()
        terminal = os.environ.get("KRANZ_COPY_SMOKE_TERM", "xterm-256color")
        if terminal not in {"xterm-256color", "linux"}:
            raise ValueError("unsupported synthetic terminal profile")
        # The parent shell may disable colors or mark itself as noninteractive CI.
        # The disposable PTY must exercise real attributes, not ASCII fallback.
        env = dict(os.environ, TMPDIR=str(state), TERM=terminal,
                   NO_COLOR="", CLICOLOR="1", CLICOLOR_FORCE="", CI="")
        config = r'''project: selection-smoke
services:
  sample:
    shell: sh
    command: 'printf "prefix https://example.test/path suffix\n雪é👩‍💻\tend\n"; while :; do sleep 1; printf "incoming sample\n"; done'
'''
        background = os.environ.get("KRANZ_COPY_SMOKE_BACKGROUND", "")
        if background:
            if not re.fullmatch(r"#[0-9a-fA-F]{6}", background):
                raise ValueError("synthetic canvas must be hex")
            config += 'ui:\n  theme: kranz\n  accent: "#7AA2F7"\n  background: "' + background + '"\n'
        (root / "kranz.yml").write_text(config)
        (root / "paste.py").write_text('''import os,sys,tty
tty.setraw(0)
data=b""
while True:
    chunk=os.read(0,65536)
    if b"\\x04" in chunk:
        data+=chunk.split(b"\\x04")[0]
        break
    data+=chunk
open(sys.argv[1],"wb").write(data)
''')

        def command(args, check=True):
            return subprocess.run(args, env=env, capture_output=True, check=check)

        def tm(*args, check=True):
            return command(["tmux", "-L", socket, *args], check)

        def key(value):
            tm("send-keys", "-t", "smoke:tui.0", value)
            time.sleep(0.18)

        def frame():
            return tm("capture-pane", "-p", "-t", "smoke:tui.0").stdout.decode()

        def wait_for(predicate):
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if predicate():
                    return
                time.sleep(0.1)
            raise RuntimeError("terminal step timed out")

        def paste(expected, label):
            destination = root / (label + ".txt")
            shell = shlex.join([sys.executable, str(root / "paste.py"), str(destination)])
            tm("new-window", "-d", "-t", "smoke", "-n", "paste", "-c", directory, shell)
            time.sleep(0.2)
            key("Enter")
            # Preserve LF bytes: tmux otherwise turns them into Enter (CR).
            tm("paste-buffer", "-r", "-t", "smoke:paste.0")
            time.sleep(0.15)
            tm("send-keys", "-t", "smoke:paste.0", "C-d")
            wait_for(destination.exists)
            actual = destination.read_bytes()
            if actual != expected.encode():
                raise RuntimeError(f"{label}: pasted text differs ({len(actual)} bytes, expected {len(expected.encode())})")
            print("PASS: " + label)

        started = False
        try:
            command([binary, "-C", directory, "up", "--start", "-d"])
            started = True
            attach = shlex.join(["env", "TMPDIR=" + str(state), "TERM=" + terminal, "COLORTERM=", binary, "-C", directory, "attach"])
            tm("-f", "/dev/null", "new-session", "-d", "-x", "120", "-y", "30",
               "-s", "smoke", "-n", "tui", "-c", directory, attach)
            tm("set-option", "-g", "set-clipboard", "on")
            wait_for(lambda: "incoming sample" in frame())
            for value in ["3", "y", "Home", "Down", "Down"]:
                key(value)
            if terminal == "linux":
                colored = tm("capture-pane", "-p", "-e", "-t", "smoke:tui.0").stdout.decode()
                if not re.search(r"\x1b\[(?:[0-9]+;)*7(?:;[0-9]+)*m", colored):
                    raise RuntimeError("16-color selection omitted reverse video")
                print("PASS: 16-color reverse-video selection")
            paste("prefix https://example.test/path suffix", "keyboard-line-paste")
            if "[Enter] Copy" not in frame() or "With context" in frame():
                raise RuntimeError("selection footer did not simplify copying")
            for cycle, reset in enumerate(["Escape", "click", "Escape"]):
                if reset == "Escape":
                    key("Escape")
                else:
                    tm("send-keys", "-t", "smoke:tui.0", "-l", "\x1b[<0;45;5M\x1b[<0;45;5m")
                wait_for(lambda: "SELECT" not in frame())
                if "[c] Clear" not in frame():
                    raise RuntimeError("log clear button missing after reset")
                # Start in padding immediately after reset, without entering
                # keyboard selection or waiting out the double-click timeout.
                tm("send-keys", "-t", "smoke:tui.0", "-l",
                   "\x1b[<0;119;6M\x1b[<32;45;5M\x1b[<0;45;5m")
                wait_for(lambda: "SELECT" in frame())
                paste("prefix https://example.test/path suffix\n雪é👩‍💻\tend",
                      f"reselect-from-padding-{cycle + 1}")
            # Real presses/releases must also clear over non-text cells, where
            # hit-testing previously swallowed clicks and kept the snapshot.
            for label, x, y in [("text", 53, 5), ("padding", 119, 5),
                                ("empty-row", 80, 25), ("header", 80, 2),
                                ("border", 120, 5), ("footer", 120, 30)]:
                for sequence in [f"\x1b[<0;{x};{y}M", f"\x1b[<0;{x};{y}m"]:
                    tm("send-keys", "-t", "smoke:tui.0", "-l", sequence)
                    time.sleep(0.15)
                wait_for(lambda: "SELECT" not in frame())
                print("PASS: click-clears-" + label)
                for value in ["3", "y", "Home", "Down", "Down"]:
                    key(value)
            for sequence in ["\x1b[<0;45;5M", "\x1b[<32;119;6M", "\x1b[<0;119;6m"]:
                tm("send-keys", "-t", "smoke:tui.0", "-l", sequence)
                time.sleep(0.15)
            paste("prefix https://example.test/path suffix\n雪é👩‍💻\tend", "drag-through-short-row-padding")
            # SGR mouse events go to the real application's PTY, not a model mock.
            for sequence in ["\x1b[<0;52;5M", "\x1b[<32;76;5M", "\x1b[<0;76;5m"]:
                tm("send-keys", "-t", "smoke:tui.0", "-l", sequence)
                time.sleep(0.15)
            time.sleep(1.2)
            paste("https://example.test/path", "mouse-substring-after-incoming")
            tm("resize-window", "-t", "smoke:tui", "-x", "80", "-y", "24")
            time.sleep(0.3)
            key("w")
            paste("https://example.test/path", "resize-wrap-retry")
            key("Escape")
            if "SELECT" in frame():
                raise RuntimeError("Done did not leave selection")
            print("PASS: done-live-recovery")
            for value in ["#", "3", "y", "Home", "Down", "Down", "Down"]:
                key(value)
            paste("雪é👩‍💻\tend", "pinned-unicode-tab-paste")
            key("Escape")
            # Close the disposable runtime while a captured fragment is active.
            key("y")
            command([binary, "-C", directory, "down"])
            started = False
            wait_for(lambda: "disconnected" in frame())
            # The captured text remains copyable without a live supervisor.
            key("Home")
            key("Down")
            key("Down")
            paste("prefix https://example.test/path suffix", "disconnect-copy")
            key("Escape")
            print("PASS: terminal smoke (tmux clipboard on; desktop/SSH unverified)")
            return 0
        finally:
            tm("kill-server", check=False)
            if started:
                command([binary, "-C", directory, "down"], check=False)


if __name__ == "__main__":
    try:
        sys.exit(run_smoke(str(Path(sys.argv[1] if len(sys.argv) > 1 else "bin/kranz-dev").resolve())))
    except Exception as error:
        # Errors may contain temporary paths or runtime IDs; never print them.
        detail = str(error) if isinstance(error, RuntimeError) else type(error).__name__
        print("FAIL: terminal smoke (" + detail + "); no private output retained")
        sys.exit(1)
