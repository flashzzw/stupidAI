#!/usr/bin/env python3
"""Real Claude tool permission/questions and interruption through Go HTTP."""
import argparse
import json
import re
import shlex
from urllib.error import HTTPError

from claude_console_smoke import ClaudeConsoleFixture
from console_fixture import Provider, wait_for


class ClaudeInteractionProvider(Provider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.workspace = root / "work"
        self.stages = {}

    def anthropic(self, payload, answer, seq):
        markers = re.findall(r"NETCAP_CLAUDE_(APPROVE|DECLINE|QUESTION|BACKGROUND)", json.dumps(payload.get("messages", [])))
        if not markers:
            return super().anthropic(payload, answer, seq)
        kind = markers[-1]
        key = (json.dumps(payload.get("metadata", {}), sort_keys=True), kind)
        stage = self.stages.get(key, 0)
        self.stages[key] = stage + 1
        if stage:
            return super().anthropic(payload, answer, seq)
        tool = "Bash"
        command = "printf 'allowed\\n' > " + shlex.quote(str(self.workspace / (kind.lower() + ".txt")))
        args = {"command": command, "description": "Write an isolated fixture result"}
        if kind == "QUESTION":
            tool = "AskUserQuestion"
            args = {"questions": [{"question": "Fixture color?", "header": "Color", "options": [
                {"label": "green", "description": "Fixture green"}, {"label": "blue", "description": "Fixture blue"}], "multiSelect": False}]}
        elif kind == "BACKGROUND":
            args = {"command": "sleep 60", "description": "Owned fixture background process", "run_in_background": True}
        message = {"id": f"msg_{seq}", "type": "message", "role": "assistant", "model": payload["model"],
            "content": [], "stop_reason": None, "stop_sequence": None, "usage": {"input_tokens": 32, "output_tokens": 0}}
        return self.sse([
            ("message_start", {"message": message}),
            ("content_block_start", {"index": 0, "content_block": {"type": "tool_use", "id": f"tool_{seq}", "name": tool, "input": {}}}),
            ("content_block_delta", {"index": 0, "delta": {"type": "input_json_delta", "partial_json": json.dumps(args)}}),
            ("content_block_stop", {"index": 0}),
            ("message_delta", {"delta": {"stop_reason": "tool_use", "stop_sequence": None}, "usage": {"output_tokens": 32}}),
            ("message_stop", {}),
        ])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    with ClaudeConsoleFixture(args.netcap, ClaudeInteractionProvider) as fixture:
        print("Evidence directory:", fixture.root, flush=True)
        def operation(oid):
            return next(o for o in fixture.snapshot()["operations"] if o["id"] == oid)
        for kind, option in (("APPROVE", "allow"), ("DECLINE", "deny"), ("QUESTION", None)):
            sid = fixture.create(kind.lower())
            fixture.command(kind + "-send", "send", session_id=sid, text="NETCAP_CLAUDE_" + kind)
            pending = wait_for(lambda: next((p for p in fixture.session(sid)["view"]["pending"] if p["status"] == "pending"), None), "native Claude permission callback")
            (fixture.root / (kind.lower() + "-request.json")).write_text(json.dumps(pending, indent=2))
            params = {"session_id": sid, "pending_id": pending["id"]}
            if option:
                params["option_id"] = option
            else:
                params["answers"] = {q["id"]: ["green"] for q in pending["questions"]}
            receipt = fixture.command(kind + "-reply", "respond", **params)
            assert fixture.command(kind + "-reply", "respond", **params) == receipt
            wait_for(lambda: fixture.session(sid)["status"] == "idle", "tool result and turn completion")
            if option:
                assert (fixture.cwd / (kind.lower() + ".txt")).exists() == (option == "allow")
            print("PASS Claude", kind, "with duplicate reply protection", flush=True)

        sid = fixture.create("background")
        receipt = fixture.command("background-send", "send", session_id=sid, text="NETCAP_CLAUDE_BACKGROUND")
        pending = wait_for(lambda: next((p for p in fixture.session(sid)["view"]["pending"] if p["status"] == "pending"), None), "background Bash approval")
        fixture.command("background-allow", "respond", session_id=sid, pending_id=pending["id"], option_id="allow")
        wait_for(lambda: bool(fixture.session(sid)["view"].get("executions")), "SDK background task starts")
        wait_for(lambda: all(t["status"] != "inProgress" for t in fixture.session(sid)["view"]["turns"]), "parent turn ends")
        assert operation(receipt["operation_id"])["status"] == "running"
        interrupted = fixture.command("background-stop", "interrupt", session_id=sid)
        wait_for(lambda: operation(interrupted["operation_id"])["status"] == "succeeded", "owned SDK task termination confirmed")
        assert not fixture.session(sid)["view"]["executions"]
        print("PASS Claude result retains background occupancy; stop waits for owned task terminal evidence", flush=True)

        fixture.provider.stream_delay = 8
        sid = fixture.create("interrupt")
        receipt = fixture.command("interrupt-send", "send", session_id=sid, text="NETCAP_A_TURN_1")
        wait_for(lambda: any(m["role"] == "assistant" and m["status"] == "inProgress" for m in fixture.session(sid)["view"]["messages"]), "real Claude streaming turn")
        fixture.command("interrupt-current-turn", "interrupt", session_id=sid)
        wait_for(lambda: operation(receipt["operation_id"])["status"] == "interrupted", "real Claude turn interruption")
        assert fixture.session(sid)["paused"]
        print("PASS Claude interrupt: original result confirms interruption and pause remains", flush=True)

        sid = fixture.create("crash-running")
        receipt = fixture.command("crash-send", "send", session_id=sid, text="NETCAP_A_TURN_2")
        wait_for(lambda: any(m["role"] == "assistant" and m["status"] == "inProgress" for m in fixture.session(sid)["view"]["messages"]), "Claude running before Go crash")
        count = len(fixture.provider.requests)
        fixture.restart_console(crash=True)
        wait_for(lambda: operation(receipt["operation_id"])["status"] == "uncertain", "crash remains uncertain without native result")
        fixture.command("crash-takeover", "takeover", session_id=sid)
        try:
            fixture.command("crash-resume", "resume", session_id=sid)
        except HTTPError as error:
            assert error.code == 409
        else:
            raise AssertionError("unknown Claude input released by resume")
        assert len(fixture.provider.requests) == count
        print("PASS Claude Go crash: original operation retained, takeover and no blind resend", flush=True)
        (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))


if __name__ == "__main__":
    main()
