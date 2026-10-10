#!/usr/bin/env python3
"""Owned OpenCode/Go death, pending epochs, child occupancy and manual takeover."""
import argparse
import json
import os
from pathlib import Path
import signal
from urllib.error import HTTPError

from opencode_console_smoke import OpenCodeConsoleFixture
from opencode_interaction_smoke import OpenCodeInteractionProvider
from console_fixture import wait_for


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    with OpenCodeConsoleFixture(args.netcap, OpenCodeInteractionProvider) as fixture:
        print("Evidence directory:", fixture.root, flush=True)
        def operation(oid):
            return next(o for o in fixture.snapshot()["operations"] if o["id"] == oid)
        def owned_server(sid):
            record = json.loads(fixture.server_record(sid=sid).read_text())
            pid = record["pid"]
            fields = Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()
            assert fields[19] == record["birth"]
            assert b"serve" in Path(f"/proc/{pid}/cmdline").read_bytes().split(b"\0")
            assert Path(f"/proc/{pid}/cwd").resolve() == fixture.cwd
            return record
        def rejects(key, kind, sid, **extra):
            try:
                fixture.command(key, kind, session_id=sid, **extra)
            except HTTPError as error:
                assert error.code == 409
            else:
                raise AssertionError("unsafe command accepted: " + kind)
        def streaming(sid):
            return any(m["role"] == "assistant" and m["status"] == "inProgress" for m in fixture.session(sid)["view"]["messages"])

        fixture.provider.stream_delay = 8
        sid = fixture.create("server-death")
        sent = fixture.command("server-death-send", "send", session_id=sid, text="NETCAP_A_TURN_1")
        wait_for(lambda: streaming(sid), "OpenCode stream before server death")
        record = owned_server(sid)
        os.killpg(record["pid"], signal.SIGKILL)
        wait_for(lambda: operation(sent["operation_id"])["status"] == "uncertain", "server death has incomplete native result")
        wait_for(lambda: fixture.session(sid)["connection"] == "ready", "new server reads original DB")
        assert json.loads(fixture.server_record(sid=sid).read_text())["epoch"] != record["epoch"]
        count = len(fixture.provider.requests)
        fixture.restart_console()
        wait_for(lambda: fixture.session(sid)["connection"] == "ready", "read-only history after controller restart")
        assert operation(sent["operation_id"])["status"] == "uncertain"
        rejects("no-resume", "resume", sid)
        rejects("no-send", "send", sid, text="must not execute")
        fixture.command("manual-takeover", "takeover", session_id=sid)
        assert fixture.session(sid)["control_mode"] == "human_takeover" and len(fixture.provider.requests) == count
        print("PASS server SIGKILL and restart preserve Unknown, original operation and manual exit without replay", flush=True)

        fixture.provider.stream_delay = 0
        sid = fixture.create("pending-death")
        sent = fixture.command("pending-death-send", "send", session_id=sid, text="NETCAP_OPENCODE_APPROVE")
        p = wait_for(lambda: next((p for p in fixture.session(sid)["view"]["pending"] if p["status"] == "pending"), None), "permission before server death")
        os.killpg(owned_server(sid)["pid"], signal.SIGKILL)
        wait_for(lambda: fixture.session(sid)["connection"] == "ready" and operation(sent["operation_id"])["status"] == "uncertain", "permission epoch recovery")
        wait_for(lambda: all(p["status"] != "pending" for p in fixture.session(sid)["view"]["pending"]), "old permission loses validity")
        rejects("old-reply", "respond", sid, pending_id=p["id"], option_id="once")
        assert not (fixture.cwd / "approve.txt").exists()
        print("PASS server epoch changes invalidate old permission and prevent file execution", flush=True)

        fixture.provider.stream_delay = 8
        sid = fixture.create("go-death")
        sent = fixture.command("go-death-send", "send", session_id=sid, text="NETCAP_A_TURN_2")
        wait_for(lambda: streaming(sid), "OpenCode stream before Go death")
        record = owned_server(sid)
        fixture.restart_console(crash=True)
        wait_for(lambda: operation(sent["operation_id"])["status"] == "uncertain", "Go crash with unfinished native result")
        wait_for(lambda: not Path(f'/proc/{record["pid"]}').exists() or Path(f'/proc/{record["pid"]}/stat').read_text().rsplit(") ", 1)[1].split()[0] == "Z", "owned server exits on parent death")
        wait_for(lambda: fixture.session(sid)["connection"] == "ready", "new Go observes persisted native history")
        assert operation(sent["operation_id"])["status"] == "uncertain"
        print("PASS Go SIGKILL terminates owned server; recovery does not repeat unfinished prompt", flush=True)

        sid = fixture.create("child-occupancy")
        native = fixture.session(sid)["scope"]["native_id"]
        child = fixture.native_call("/session", {"parentID": native, "title": "owned test child", "model": {"id": "fixture-model", "providerID": "fixture"}}, sid=sid)
        fixture.native_call("/session/" + child["id"] + "/prompt_async", {"model": {"modelID": "fixture-model", "providerID": "fixture"}, "parts": [{"type": "text", "text": "NETCAP_A_TURN_3"}]}, sid=sid)
        wait_for(lambda: any(x["kind"] == "child_session" and x["id"] == child["id"] for x in (fixture.session(sid)["view"].get("executions") or [])), "child session capacity reservation")
        rejects("parent-busy", "send", sid, text="must wait for child")
        wait_for(lambda: fixture.session(sid)["view"]["execution_clear"], "child terminal history releases parent reservation")
        print("PASS native child session reserves parent until its original result is complete", flush=True)

        fixture.provider.stream_delay = 0
        fixture.native_call("/session/" + native + "/prompt_async", {"model": {"modelID": "fixture-model", "providerID": "fixture"}, "parts": [{"type": "text", "text": "explicit external terminal input"}]}, sid=sid)
        wait_for(lambda: fixture.session(sid)["control_mode"] == "human_takeover", "external input revokes automation ownership")
        assert fixture.session(sid)["paused"]
        print("PASS native terminal input revokes automatic ownership and remains visible", flush=True)
        (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))


if __name__ == "__main__":
    main()
