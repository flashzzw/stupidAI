#!/usr/bin/python3
"""Test-only owned HTTP relay around the real OpenCode server."""
import ctypes
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import threading
from urllib.parse import urlsplit

root = Path(os.environ["NETCAP_OPENCODE_FAULT_DIR"]).resolve()
assert str(root).startswith("/tmp/netcap-console-")
args = sys.argv[1:]
if args == ["--version"]:
    os.execv("/home/zzw/.opencode/bin/opencode", ["opencode", *args])
port_index = args.index("--port") + 1
port = int(args[port_index])
with socket.socket() as listener:
    listener.bind(("127.0.0.1", 0))
    upstream = listener.getsockname()[1]
args[port_index] = str(upstream)
def parent_death():
    ctypes.CDLL(None).prctl(1, signal.SIGTERM)
child = subprocess.Popen(["/home/zzw/.opencode/bin/opencode", *args], preexec_fn=parent_death)
(root / "native-pid.json").write_text(json.dumps({"pid": child.pid, "relay_pid": os.getpid()}))
lock = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def relay(self):
        path = urlsplit(self.path).path
        with lock:
            with (root / "calls.jsonl").open("a") as log:
                log.write(json.dumps({"method": self.command, "path": path}) + "\n")
            try:
                armed = json.loads((root / "armed.json").read_text())
            except FileNotFoundError:
                armed = {}
            fault = armed if armed.get("method") == self.command and armed.get("path") == path else {}
            if fault:
                (root / "armed.json").unlink()
        conn = http.client.HTTPConnection("127.0.0.1", upstream, timeout=35)
        try:
            body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
            headers = {k: v for k, v in self.headers.items() if k.lower() != "accept-encoding"}
            headers["Accept-Encoding"] = "identity"
            conn.request(self.command, self.path, body or None, headers)
            response = conn.getresponse()
            if path == "/event" and response.status == 200:
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.end_headers()
                old_part = None
                while True:
                    line = response.readline()
                    if not line:
                        return
                    mode = (root / "sse-mode").read_text() if (root / "sse-mode").exists() else ""
                    if mode == "disconnect":
                        (root / "sse-mode").unlink(missing_ok=True)
                        (root / "sse-disconnected").touch()
                        return
                    self.wfile.write(line)
                    if mode == "duplicate-late" and line.startswith(b"data: "):
                        self.wfile.write(b"\n" + line + b"\n")
                        if old_part:
                            self.wfile.write(old_part + b"\n")
                        if b'"message.part.updated"' in line and old_part is None:
                            old_part = line
                        (root / "sse-injected").touch()
                    self.wfile.flush()
            value = response.read()
            if fault.get("kind") in ("drop", "drop-partial"):
                (root / "dropped.json").write_text(json.dumps({"path": path, "status": response.status,
                    "response": json.loads(value) if value else None}))
                if fault["kind"] == "drop-partial":
                    (root / "partial-history").touch()
                self.connection.shutdown(socket.SHUT_RDWR)
                self.close_connection = True
                return
            if path.endswith("/message") and (root / "partial-history").exists():
                value = b'[{"info":'
            if self.command == "GET" and path == "/session" and (root / "hide-created").exists():
                value = b'[]'
            self.send_response(response.status)
            self.send_header("Content-Type", response.getheader("Content-Type", "application/json"))
            self.send_header("Content-Length", str(len(value)))
            self.end_headers()
            self.wfile.write(value)
        except (OSError, http.client.HTTPException):
            self.close_connection = True
        finally:
            conn.close()

    do_GET = relay
    do_POST = relay
    do_PATCH = relay
    do_DELETE = relay


server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
try:
    server.serve_forever()
finally:
    child.terminate()
    child.wait(timeout=5)
