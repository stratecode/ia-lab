#!/usr/bin/env python3
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    def _authorized(self):
        return self.headers.get("Authorization") == "Bearer test-localai-api-key-123456"

    def _json(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if not self._authorized():
            self._json(401, {"error": "unauthorized"})
            return
        if self.path == "/readyz":
            self._json(200, {"ready": True})
            return
        if self.path == "/v1/models":
            self._json(200, {"data": [{"id": "qwen3-4b"}]})
            return
        self._json(404, {"error": "not found"})

    def do_POST(self):
        if not self._authorized():
            self._json(401, {"error": "unauthorized"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        payload = json.loads(self.rfile.read(length) or b"{}")
        if self.path != "/v1/chat/completions":
            self._json(404, {"error": "not found"})
            return
        if payload.get("tools"):
            message = {
                "role": "assistant",
                "content": None,
                "tool_calls": [{
                    "id": "call_fixture",
                    "type": "function",
                    "function": {
                        "name": "get_weather",
                        "arguments": '{"location":"Madrid"}',
                    },
                }],
            }
        else:
            message = {"role": "assistant", "content": "fixture-ok"}
        self._json(200, {"choices": [{"message": message}]})

    def log_message(self, *_args):
        return


ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
