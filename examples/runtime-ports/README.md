# Runtime port discovery lab

Run `kranz` in this directory, select all services with `a`, and start with `s`.
Everything binds only to `127.0.0.1`.

The services demonstrate detected-only ports, matching and stale declarations,
explicit discovery opt-out, a dynamically selected HTTP port, a child listener,
an opening/closing listener, and two independently selected health ports. The
safe actions only fetch localhost, print context, list example files, or open a
Python REPL after an explicit interactive handoff.

Fixed ports are in the `18301`–`18307` range. Kranz preflight checks declared
ports; Python reports a clear bind error if an undeclared lab port is already in
use. The two-port scenario chooses free ports from `3800`–`3899` and
`48000`–`48099`.
