#!/usr/bin/env python3
"""Real OpenCode permissions/questions, stale replies and verified abort."""
import argparse
import json
import re
import shlex
import time
from urllib.error import HTTPError

from opencode_console_smoke import OpenCodeConsoleFixture
from console_fixture import Provider, wait_for


class OpenCodeInteractionProvider(Provider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.workspace = root / "work"

    def chat(self, payload, answer, seq):
        messages = payload.get("messages", [])
        marker = next((re.search(r"NETCAP_OPENCODE_(APPROVE|DECLINE|QUESTION|MULTI|EXTERNAL|CHILD)", json.dumps(m))
                       for m in reversed(messages) if m.get("role") == "user"), None)
        if not marker or messages[-1].get("role") == "tool":
            return super().chat(payload, answer, seq)
        kind = marker[1]
        tool = "bash"
        args = {"command": "printf 'allowed\\n' > " + shlex.quote(str(self.workspace / (kind.lower() + ".txt"))),
                "description": "Write isolated fixture result"}
        if kind in ("QUESTION", "MULTI"):
            tool = "question"
            args = {"questions": [{"question": "Fixture color?", "header": "Color", "options": [
                {"label": "green", "description": "Fixture green"}, {"label": "blue", "description": "Fixture blue"}], "multiple": kind == "MULTI"}]}
        if kind == "CHILD":
            tool = "task"
            args = {"description": "Owned fixture child", "prompt": "NETCAP_A_TURN_1", "subagent_type": "general"}
        offered = {t["function"]["name"] for t in payload.get("tools", [])}
        assert tool in offered, (tool, offered)
        base = {"id": f"chatcmpl_{seq}", "object": "chat.completion.chunk", "created": int(time.time()), "model": payload["model"]}
        chunks = [dict(base, choices=[{"index": 0, "delta": {"role": "assistant", "tool_calls": [{"index": 0,
                  "id": f"call_{seq}", "type": "function", "function": {"name": tool, "arguments": json.dumps(args)}}]}, "finish_reason": None}]),
                  dict(base, choices=[{"index": 0, "delta": {}, "finish_reason": "tool_calls"}],
                       usage={"prompt_tokens": 32, "completion_tokens": 8, "total_tokens": 40})]
        return "".join("data: " + json.dumps(c) + "\n\n" for c in chunks) + "data: [DONE]\n\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    with OpenCodeConsoleFixture(args.netcap, OpenCodeInteractionProvider) as fixture:
        print("Evidence directory:", fixture.root, flush=True)
        def operation(oid):
            return next(o for o in fixture.snapshot()["operations"] if o["id"] == oid)
        def pending(sid):
            return next((p for p in fixture.session(sid)["view"]["pending"] if p["status"] == "pending"), None)
        for kind, option in (("APPROVE", "once"), ("DECLINE", "reject"), ("QUESTION", None), ("EXTERNAL", "once")):
            sid = fixture.create(kind.lower())
            sent = fixture.command(kind + "-send", "send", session_id=sid, text="NETCAP_OPENCODE_" + kind)
            p = wait_for(lambda: pending(sid), "native OpenCode " + kind)
            assert p["native_turn"] == operation(sent["operation_id"])["receipt"]["native_turn"]
            (fixture.root / (kind.lower() + "-request.json")).write_text(json.dumps(p, indent=2))
            params = {"session_id": sid, "pending_id": p["id"]}
            if option:
                params["option_id"] = option
            else:
                params["answers"] = {q["id"]: ["green"] for q in p["questions"]}
            if kind == "EXTERNAL":
                assert fixture.native_call("/permission/" + p["request_id"] + "/reply", {"reply": "once"}, sid=sid)
                wait_for(lambda: fixture.session(sid)["status"] == "idle", "original terminal resolves permission")
                try:
                    fixture.command(kind + "-reply", "respond", **params)
                except HTTPError as error:
                    assert error.code == 409
                else:
                    raise AssertionError("expired permission was accepted")
            else:
                receipt = fixture.command(kind + "-reply", "respond", **params)
                assert fixture.command(kind + "-reply", "respond", **params) == receipt
                wait_for(lambda: fixture.session(sid)["status"] == "idle", "OpenCode reply and real tool result")
            if option:
                assert (fixture.cwd / (kind.lower() + ".txt")).exists() == (option == "once")
            else:
                assert any('green' in json.dumps(r["body"]["messages"][-1]) for r in fixture.provider.requests)
            print("PASS OpenCode", kind, "native request identity and duplicate/expired reply protection", flush=True)

        fixture.provider.stream_delay = 8
        sid = fixture.create("interrupt")
        sent = fixture.command("interrupt-send", "send", session_id=sid, text="NETCAP_A_TURN_1")
        wait_for(lambda: any(m["role"] == "assistant" and m["status"] == "inProgress" for m in fixture.session(sid)["view"]["messages"]), "OpenCode streaming before abort")
        stopped = fixture.command("interrupt-current-turn", "interrupt", session_id=sid)
        wait_for(lambda: operation(sent["operation_id"])["status"] == "interrupted", "original OpenCode turn abort evidence")
        wait_for(lambda: operation(stopped["operation_id"])["status"] == "succeeded", "abort operation verified")
        assert fixture.session(sid)["paused"]
        print("PASS OpenCode abort confirms original terminal result and retains pause", flush=True)

        pty = fixture.native_call("/pty", {"command": "/bin/sh", "args": ["-c", "sleep 30"], "cwd": str(fixture.cwd)}, sid=sid)
        wait_for(lambda: any(x["id"] == pty["id"] for x in fixture.session(sid)["view"]["executions"]), "server PTY occupancy")
        assert not fixture.session(sid)["view"]["execution_clear"]
        fixture.native_call("/pty/" + pty["id"], method="DELETE", sid=sid)
        wait_for(lambda: fixture.session(sid)["view"]["execution_clear"], "PTY deletion confirmed")
        print("PASS OpenCode server PTY reserves capacity until actual removal", flush=True)
        (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))


if __name__ == "__main__":
    main()
