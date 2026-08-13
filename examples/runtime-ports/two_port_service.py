#!/usr/bin/env python3

import select
import signal
import socket


listeners = []
running = True


def stop(_signum, _frame):
    global running
    running = False


def listen_on_first_free(candidates):
    for port in candidates:
        listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            listener.bind(("127.0.0.1", port))
        except OSError:
            listener.close()
            continue
        listener.listen()
        return listener
    raise RuntimeError("no free port in the example range")


listeners.append(listen_on_first_free(range(3800, 3900)))
listeners.append(listen_on_first_free(range(48000, 48100)))

signal.signal(signal.SIGINT, stop)
signal.signal(signal.SIGTERM, stop)

ports = sorted(listener.getsockname()[1] for listener in listeners)
print(f"two runtime listeners: {ports[0]}, {ports[1]}", flush=True)

while running:
    readable, _, _ = select.select(listeners, [], [], 0.5)
    for listener in readable:
        connection, _ = listener.accept()
        connection.close()

for listener in listeners:
    listener.close()
