#!/usr/bin/env python3
"""A stand-in web service for the MoonFlight showcase.

It serves readiness on a real localhost port and prints the kind of output a
small HTTP service produces, so the example looks like a running application
instead of a loop echoing its own name. It talks to nothing outside localhost.
"""
import json
import os
import random
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SERVICE_NAME = os.environ.get("SERVICE_NAME", "service")
PORT = int(os.environ.get("PORT", "8080"))
ROUTES = os.environ.get("ROUTES", "/api/status").split(",")
BOOT_DELAY = float(os.environ.get("BOOT_DELAY", "0"))


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path in {"/health/ready", "/health/live"}:
            body = (json.dumps({"status": "ok", "service": SERVICE_NAME}) + "\n").encode()
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_error(404)

    def log_message(self, format, *args):
        # Requests are reported by the traffic loop below in a friendlier shape.
        pass


def serve():
    # Signal handlers can only be installed from the main thread, and the
    # default SIGTERM disposition already ends the process, so this thread just
    # serves until Kranz stops the process group.
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()


print(f"INFO bootstrap starting {SERVICE_NAME}", flush=True)
time.sleep(BOOT_DELAY)
for route in ROUTES:
    print(f"INFO routes mapped GET {route}", flush=True)
threading.Thread(target=serve, daemon=True).start()
print(f"INFO server listening on http://127.0.0.1:{PORT}", flush=True)

while True:
    time.sleep(random.uniform(0.6, 1.6))
    route = random.choice(ROUTES)
    print(f"INFO http GET {route} 200 {random.randint(4, 40)}ms", flush=True)
