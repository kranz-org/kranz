#!/usr/bin/env python3
import json
import os
import signal
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


SERVICE_NAME = os.environ.get("SERVICE_NAME", "api")
PORT = int(os.environ.get("PORT", "8080"))


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path in {"/health/ready", "/health/live", "/api/status"}:
            body = (json.dumps({"status": "ok", "service": SERVICE_NAME}) + "\n").encode()
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_error(404)

    def log_message(self, format, *args):
        if args and "/health/" not in str(args[0]):
            print(f"INFO http {format % args}", flush=True)


server = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)


def stop(signum, _frame):
    print(f"INFO shutdown signal={signum}", flush=True)
    raise SystemExit(0)


signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)
print(f"INFO {SERVICE_NAME} listening on http://127.0.0.1:{PORT}", flush=True)
try:
    server.serve_forever()
finally:
    server.server_close()
