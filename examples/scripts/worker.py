#!/usr/bin/env python3
import itertools
import os
import signal
import time
import urllib.request


WORKER_NAME = os.environ.get("WORKER_NAME", "worker")
API_URL = os.environ.get("API_URL", "http://127.0.0.1:8080")
INTERVAL = float(os.environ.get("INTERVAL", "3"))
running = True


def stop(signum, _frame):
    global running
    print(f"received signal {signum}; draining", flush=True)
    running = False


signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)
for sequence in itertools.count(1):
    if not running:
        break
    try:
        with urllib.request.urlopen(f"{API_URL}/api/status", timeout=1) as response:
            response.read()
        print(f"{WORKER_NAME} completed job-{sequence}", flush=True)
    except Exception as error:
        print(f"{WORKER_NAME} retrying: {error}", flush=True)
    deadline = time.monotonic() + INTERVAL
    while running and time.monotonic() < deadline:
        time.sleep(min(0.2, deadline - time.monotonic()))
print("drain complete", flush=True)
