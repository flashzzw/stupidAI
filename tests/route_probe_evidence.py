#!/usr/bin/env python3
"""Inspect real date-tool evidence before implementing automatic route probes."""
import argparse
import base64
import json
from pathlib import Path
import re
import time
from urllib.request import Request, urlopen

from console_alpha_smoke import AlphaFixture
from console_fixture import CLI, Provider, wait_for
from go_cli_smoke import Proxy

COMMAND = 'netcap_probe_utc=$(/usr/bin/date -u +%FT%TZ); netcap_probe_exit=$?; printf \'__NETCAP_UTC__%s %s\\n\' "$netcap_probe_exit" "$netcap_probe_utc"; exit "$netcap_probe_exit"'


class DateProvider(Provider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.issued = False

    def command(self):
        return COMMAND

    def final(self, payload):
        if not self.issued:
            self.issued = True
            return None
        values = re.findall(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", json.dumps(payload))
        assert values, "agent did not return a real UTC command output"
        return values[-1]

    def responses(self, payload, answer, seq):
        final = self.final(payload)
        if final:
            return super().responses(payload, final, seq)
        code = "text(await tools.exec_command(" + json.dumps({"cmd": self.command(), "login": False}) + "));"
        item = {"id": f"ctc_{seq}", "type": "custom_tool_call", "call_id": f"call_{seq}", "name": "exec", "namespace": "functions", "input": code, "status": "completed"}
        response = {"id": f"resp_{seq}", "object": "response", "created_at": int(time.time()), "status": "completed", "model": payload["model"], "output": [item], "usage": {"input_tokens": 32, "output_tokens": 16, "total_tokens": 48}}
        return self.sse([("response.created", {"response": dict(response, status="in_progress", output=[])}),
                         ("response.output_item.added", {"output_index": 0, "item": dict(item, status="in_progress")}),
                         ("response.output_item.done", {"output_index": 0, "item": item}), ("response.completed", {"response": response})])

    def anthropic(self, payload, answer, seq):
        final = self.final(payload)
        if final:
            return super().anthropic(payload, final, seq)
        message = {"id": f"msg_{seq}", "type": "message", "role": "assistant", "model": payload["model"], "content": [], "stop_reason": None, "stop_sequence": None, "usage": {"input_tokens": 32, "output_tokens": 0}}
        return self.sse([("message_start", {"message": message}),
            ("content_block_start", {"index": 0, "content_block": {"type": "tool_use", "id": f"tool_{seq}", "name": "Bash", "input": {}}}),
            ("content_block_delta", {"index": 0, "delta": {"type": "input_json_delta", "partial_json": json.dumps({"command": self.command(), "description": "Read UTC time"})}}),
            ("content_block_stop", {"index": 0}), ("message_delta", {"delta": {"stop_reason": "tool_use", "stop_sequence": None}, "usage": {"output_tokens": 32}}), ("message_stop", {})])

    def chat(self, payload, answer, seq):
        final = self.final(payload)
        if final:
            return super().chat(payload, final, seq)
        base = {"id": f"chatcmpl_{seq}", "object": "chat.completion.chunk", "created": int(time.time()), "model": payload["model"]}
        chunks = [dict(base, choices=[{"index": 0, "delta": {"role": "assistant", "tool_calls": [{"index": 0, "id": f"call_{seq}", "type": "function", "function": {"name": "bash", "arguments": json.dumps({"command": self.command(), "description": "Read UTC time"})}}]}, "finish_reason": None}]),
                  dict(base, choices=[{"index": 0, "delta": {}, "finish_reason": "tool_calls"}], usage={"prompt_tokens": 32, "completion_tokens": 8, "total_tokens": 40})]
        return "".join("data: " + json.dumps(c) + "\n\n" for c in chunks) + "data: [DONE]\n\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    with AlphaFixture(args.netcap, {kind: DateProvider for kind in ("codex", "claude", "opencode")}) as fixture:
        print("Evidence directory:", fixture.root, flush=True)
        sessions = {}
        for kind, route, project in (("codex", "route-a", "fixture"), ("claude", "route-b", "claude"), ("opencode", "route-c", "opencode")):
            sid = fixture.command(kind + "-create", "create", project_id=project, route_id=route, title="UTC evidence " + kind, permission="read_only")["session_id"]
            sessions[kind] = sid
            wait_for(lambda: fixture.session(sid)["status"] == "idle", kind + " create", timeout=125)
            fixture.command(kind + "-date", "send", session_id=sid, text="Execute this exact command, then return only the UTC value after the exit code: " + COMMAND)
            def advance():
                s = fixture.session(sid)
                pending = next((p for p in s["view"]["pending"] if p["status"] == "pending"), None)
                if pending:
                    assert "__NETCAP_UTC__" in json.dumps(pending), "unexpected probe tool request"
                    choice = {"claude": "allow", "opencode": "once"}.get(kind)
                    if kind == "codex":
                        choice = next(o["id"] for o in pending["options"] if o["value"] == "accept")
                    fixture.command(kind + "-allow", "respond", session_id=sid, pending_id=pending["id"], option_id=choice)
                return s if s["status"] == "idle" else None
            session = wait_for(advance, kind + " UTC native result")
            reply = [m["text"].strip() for m in session["view"]["messages"] if m["role"] == "assistant"][-1]
            assert re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", reply), reply
            assert len(fixture.providers[kind].requests) == 2 and all(r["auth_matches"] for r in fixture.providers[kind].requests)
            print("PASS real", kind, "UTC tool and exact final response", flush=True)
        client = Proxy(CLI, fixture.sock, fixture.env)
        try:
            native = fixture.session(sessions["codex"])["scope"]["native_id"]
            history = client.call("thread/read", {"threadId": native, "includeTurns": True})
            (fixture.root / "codex-date-native.json").write_text(json.dumps(history, indent=2))
            rollout = Path(history["thread"]["path"])
            assert rollout.is_relative_to(fixture.profile)
            (fixture.root / "codex-date-rollout.jsonl").write_bytes(rollout.read_bytes())
        finally:
            client.close()
        claude = fixture.session(sessions["claude"])["scope"]["native_id"]
        journal = fixture.root / "console/runtimes/claude-local" / (claude + ".jsonl")
        (fixture.root / "claude-date-native.json").write_text(json.dumps([json.loads(line) for line in journal.read_text().splitlines()], indent=2))
        opencode = fixture.session(sessions["opencode"])["scope"]["native_id"]
        record = next(p for p in (fixture.root / "console/runtimes/opencode-local").glob("*/server.json") if json.loads(p.read_text())["session_id"] == sessions["opencode"])
        server = json.loads(record.read_text())
        auth = base64.b64encode(("opencode:" + (record.parent / "server-token").read_text()).encode()).decode()
        request = Request(server["url"] + "/session/" + opencode + "/message", headers={"Authorization": "Basic " + auth})
        with urlopen(request, timeout=10) as response:
            (fixture.root / "opencode-date-native.json").write_text(json.dumps(json.load(response), indent=2))
        (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))
        for kind, provider in fixture.providers.items():
            # Inspect the tool output actually sent back by the CLI, rather
            # than a wall-clock value supplied by this fixture.
            native_output = json.dumps(provider.requests[-1]["body"], ensure_ascii=False)
            assert re.search(r"__NETCAP_UTC__0 \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", native_output), kind
        print("Native evidence saved for three adapters; automatic probe policy is not implemented by this experiment", flush=True)


if __name__ == "__main__":
    main()
