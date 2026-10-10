"""Local providers and owned process helpers for real runtime contract checks."""
import gzip
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.request import Request, urlopen


def isolated_env(root):
    # Inherit no credentials, runtime controls, hooks, proxies or telemetry sinks.
    env = {k: v for k, v in os.environ.items() if k in ("PATH", "USER", "LANG", "TERM", "SHELL")}
    for key, suffix in (("HOME", "home"), ("CODEX_HOME", "codex"),
                        ("CLAUDE_CONFIG_DIR", "claude"), ("XDG_CONFIG_HOME", "config"),
                        ("XDG_DATA_HOME", "data"), ("XDG_CACHE_HOME", "cache"),
                        ("XDG_STATE_HOME", "state")):
        path = root / suffix
        path.mkdir(parents=True, exist_ok=True)
        env[key] = str(path)
    return env


def wait_for(check, description, timeout=30):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = check()
        if last:
            return last
        time.sleep(.1)
    raise AssertionError(f"timed out: {description}; last={last!r}")


def stop(proc):
    if proc is None or proc.poll() is not None:
        return
    # Only processes returned by our Popen(start_new_session=True) reach here.
    os.killpg(proc.pid, signal.SIGTERM)
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        os.killpg(proc.pid, signal.SIGKILL)
        proc.wait(timeout=5)


def http(base, path, body=None, method=None, headers=None):
    request = Request(base + path, data=None if body is None else json.dumps(body).encode(),
                      headers={"Content-Type": "application/json", **(headers or {})}, method=method)
    with urlopen(request, timeout=30) as response:
        data = response.read()
        return json.loads(data) if data else None


class Provider:
    """Three wire protocols, with deterministic synthetic model responses only."""

    def __init__(self, root, route):
        self.route = route
        self.requests = []
        self.errors = []
        self.stream_delay = 0
        self.root = root / ("provider-" + route)
        self.root.mkdir()
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_GET(self):
                self.reply({"object": "list", "data": []})

            def reply(self, value, content_type="application/json", status=200, headers=None):
                body = value.encode() if isinstance(value, str) else json.dumps(value).encode()
                self.send_response(status)
                self.send_header("Content-Type", content_type)
                self.send_header("Content-Length", str(len(body)))
                for name, value in (headers or {}).items():
                    self.send_header(name, value)
                self.end_headers()
                try:
                    if owner.stream_delay and body.startswith(b"data: "):
                        boundary = body.find(b"\n\ndata: ") + 2
                        self.wfile.write(body[:boundary])
                        self.wfile.flush()
                        time.sleep(owner.stream_delay)
                        self.wfile.write(body[boundary:])
                        return
                    separator = b"event: response.output_item.done"
                    if b"event: content_block_stop" in body:
                        separator = b"event: content_block_stop"
                    if owner.stream_delay and separator in body:
                        prefix, suffix = body.split(separator, 1)
                        self.wfile.write(prefix)
                        self.wfile.flush()
                        time.sleep(owner.stream_delay)
                        self.wfile.write(separator + suffix)
                    else:
                        self.wfile.write(body)
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def do_POST(self):
                try:
                    raw = self.rfile.read(int(self.headers.get("Content-Length", 0)))
                    if self.headers.get("Content-Encoding") == "gzip":
                        raw = gzip.decompress(raw)
                    payload = json.loads(raw)
                    record = {"path": self.path, "body": payload,
                              "auth_matches": self.headers.get("Authorization") == "Bearer fixture-" + route
                              or self.headers.get("x-api-key") == "fixture-" + route}
                    owner.requests.append(record)
                    seq = len(owner.requests)
                    (owner.root / f"request-{seq}.json").write_text(json.dumps(record, indent=2))
                    if "count_tokens" in self.path:
                        self.reply({"input_tokens": 64})
                        return
                    failure = owner.failure_response(self.path, payload)
                    if failure:
                        value, status, headers = failure
                        self.reply(value, status=status, headers=headers)
                        return
                    messages = payload.get("input", payload.get("messages", []))
                    markers = re.findall(r"NETCAP_(?:A|B)_TURN_[123]", json.dumps(messages))
                    answer = f"fixture {route}: {markers[-1] if markers else 'auxiliary'}"
                    if "responses" in self.path:
                        self.reply(owner.responses(payload, answer, seq), "text/event-stream")
                    elif "/messages" in self.path:
                        self.reply(owner.anthropic(payload, answer, seq), "text/event-stream")
                    elif "chat/completions" in self.path:
                        self.reply(owner.chat(payload, answer, seq), "text/event-stream")
                    else:
                        self.reply({"error": "unexpected fixture path"}, status=404)
                except Exception as error:
                    owner.errors.append(repr(error))
                    self.reply({"error": str(error)}, status=500)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f"http://127.0.0.1:{self.server.server_port}"

    @staticmethod
    def sse(events):
        return "".join(f"event: {kind}\ndata: {json.dumps(dict(data, type=kind))}\n\n" for kind, data in events)

    def failure_response(self, path, payload):
        return None

    def responses(self, payload, answer, seq):
        part = {"type": "output_text", "text": answer, "annotations": []}
        item = {"id": f"msg_{seq}", "type": "message", "role": "assistant", "status": "completed",
                "phase": "final_answer", "content": [part]}
        response = {"id": f"resp_{seq}", "object": "response", "status": "completed",
                    "created_at": int(time.time()), "model": payload["model"], "output": [item],
                    "usage": {"input_tokens": 32, "output_tokens": 8, "total_tokens": 40}}
        return self.sse([
            ("response.created", {"response": dict(response, output=[], status="in_progress")}),
            ("response.output_item.added", {"output_index": 0, "item": dict(item, content=[], status="in_progress")}),
            ("response.output_text.delta", {"item_id": item["id"], "output_index": 0, "content_index": 0, "delta": answer}),
            ("response.output_item.done", {"output_index": 0, "item": item}),
            ("response.completed", {"response": response}),
        ])

    def anthropic(self, payload, answer, seq):
        message = {"id": f"msg_{seq}", "type": "message", "role": "assistant", "model": payload["model"],
                   "content": [], "stop_reason": None, "stop_sequence": None,
                   "usage": {"input_tokens": 32, "output_tokens": 0}}
        return self.sse([
            ("message_start", {"message": message}),
            ("content_block_start", {"index": 0, "content_block": {"type": "text", "text": ""}}),
            ("content_block_delta", {"index": 0, "delta": {"type": "text_delta", "text": answer}}),
            ("content_block_stop", {"index": 0}),
            ("message_delta", {"delta": {"stop_reason": "end_turn", "stop_sequence": None}, "usage": {"output_tokens": 8}}),
            ("message_stop", {}),
        ])

    def chat(self, payload, answer, seq):
        base = {"id": f"chatcmpl_{seq}", "object": "chat.completion.chunk", "created": int(time.time()),
                "model": payload["model"]}
        chunks = [dict(base, choices=[{"index": 0, "delta": {"role": "assistant", "content": answer}, "finish_reason": None}]),
                  dict(base, choices=[{"index": 0, "delta": {}, "finish_reason": "stop"}],
                       usage={"prompt_tokens": 32, "completion_tokens": 8, "total_tokens": 40})]
        return "".join("data: " + json.dumps(c) + "\n\n" for c in chunks) + "data: [DONE]\n\n"

    def assert_route(self, model):
        assert not self.errors, self.errors
        model_requests = [r for r in self.requests if "count_tokens" not in r["path"]]
        assert len(model_requests) >= 3, model_requests
        for record in model_requests:
            assert record["auth_matches"], "fixture credentials did not reach selected route"
            assert record["body"]["model"] == model, record["body"].get("model")
            opposite = "B" if self.route == "A" else "A"
            assert f"NETCAP_{opposite}_TURN_" not in json.dumps(record["body"])
        for turn in (1, 2, 3):
            assert any(f"NETCAP_{self.route}_TURN_{turn}" in json.dumps(r["body"]) for r in model_requests)
        # The resumed third request must carry context from both earlier turns.
        resumed = [r for r in model_requests if f"NETCAP_{self.route}_TURN_3" in json.dumps(r["body"])]
        assert any(all(f"NETCAP_{self.route}_TURN_{n}" in json.dumps(r["body"]) for n in (1, 2, 3)) for r in resumed)
        return {"endpoint": self.url, "model_requests": len(model_requests), "model": model,
                "test_auth_verified": True, "resumed_context_verified": True}

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
