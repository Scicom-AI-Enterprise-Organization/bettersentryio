#!/usr/bin/env python3
"""Tiny webhook receiver for local development.

Prints every alert bettersentryio delivers, so you can watch the transitions
without wiring up a real Slack or Teams endpoint.

    python3 scripts/webhook-sink.py 9099
"""
import json
import sys
from datetime import datetime
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length)
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"ok":true}')

        stamp = datetime.now().strftime("%H:%M:%S")
        try:
            body = json.loads(raw)
        except json.JSONDecodeError:
            print(f"[{stamp}] non-JSON payload: {raw!r}", flush=True)
            return

        text = body.get("text") or body.get("summary") or json.dumps(body)
        print(f"[{stamp}] ALERT  {text}", flush=True)
        fields = body.get("fields") or {}
        if fields:
            detail = "  ".join(f"{k}={v}" for k, v in sorted(fields.items()))
            print(f"           {detail}", flush=True)

    def log_message(self, *args):
        pass  # the alert lines above are the only output we want


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 9099
    print(f"webhook sink listening on http://localhost:{port}/hook", flush=True)
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()
