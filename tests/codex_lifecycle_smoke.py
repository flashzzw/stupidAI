#!/usr/bin/env python3
"""Real native ownership, interruptions and independent process lifetimes."""
import argparse
import json
from urllib.error import HTTPError

from console_fixture import CLI, ConsoleFixture, wait_for, stop
from codex_interaction_smoke import InteractionProvider
from go_cli_smoke import Proxy


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    with ConsoleFixture(args.netcap, InteractionProvider) as fixture:
        print("Evidence directory:", fixture.root, flush=True)

        def operation(oid):
            return next(o for o in fixture.snapshot()["operations"] if o["id"] == oid)

        def send_running(sid, key):
            count = len(fixture.provider.requests)
            receipt = fixture.command(key, "send", session_id=sid, text="NETCAP_A_TURN_1")
            wait_for(lambda: len(fixture.provider.requests) > count, "real model request starts")
            wait_for(lambda: operation(receipt["operation_id"])["receipt"].get("native_turn"), "native turn receipt")
            return receipt["operation_id"]

        fixture.provider.stream_delay = 8
        sid = fixture.create("running-crash")
        oid = send_running(sid, "running-crash-send")
        count = len(fixture.provider.requests)
        original = operation(oid)["receipt"].copy()
        fixture.restart_console(crash=True)
        wait_for(lambda: operation(oid)["status"] == "running", "same native execution still running after Go crash")
        assert operation(oid)["receipt"] == original
        wait_for(lambda: operation(oid)["status"] == "succeeded", "original native execution completes")
        assert len(fixture.provider.requests) == count
        print("PASS Go crash while native running: same turn completes with no resend", flush=True)

        sid = fixture.create("owned-interrupt")
        oid = send_running(sid, "interrupt-send")
        fixture.command("interrupt", "interrupt", session_id=sid)
        wait_for(lambda: operation(oid)["status"] == "interrupted", "owned turn interrupted")
        assert fixture.session(sid)["paused"]
        print("PASS interrupt owned turn and persist pause", flush=True)

        sid = fixture.create("human-steer")
        oid = send_running(sid, "human-steer-send")
        native = Proxy(CLI, fixture.sock, fixture.env)
        try:
            refs = operation(oid)["receipt"]
            native.call("thread/resume", {"threadId": refs["native_id"], "excludeTurns": True})
            native.call("turn/steer", {"threadId": refs["native_id"], "expectedTurnId": refs["native_turn"],
                                       "clientUserMessageId": "fixture-human-input", "input": [{"type": "text", "text": "human steering"}]})
            # A steer receipt precedes consumption. In this native version the
            # input is absent from both history and queue until the next model
            # step; establish that the human input has actually joined the turn.
            wait_for(lambda: any(m.get("operation") == "fixture-human-input" for m in fixture.session(sid)["view"]["messages"]), "human input joins native turn")
            interrupted = fixture.command("must-not-interrupt", "interrupt", session_id=sid)
            wait_for(lambda: operation(interrupted["operation_id"])["status"] == "not_sent", "human input blocks automatic interrupt")
            assert "人工输入" in operation(interrupted["operation_id"])["error"]
            wait_for(lambda: fixture.session(sid)["control_mode"] == "human_takeover", "human activity pauses automation")
            native.call("turn/interrupt", {"threadId": refs["native_id"], "turnId": refs["native_turn"]})
            print("PASS mixed human input refuses console interrupt and yields control", flush=True)
        finally:
            native.close()

        fixture.provider.stream_delay = 0
        sid = fixture.create("native-approval-first")
        fixture.command("expire-send", "send", session_id=sid, text="NETCAP_EXPIRE")
        action = wait_for(lambda: next((p for p in fixture.session(sid)["view"]["pending"] if p["status"] == "pending"), None), "pending approval")
        native = Proxy(CLI, fixture.sock, fixture.env)
        try:
            native.call("thread/resume", {"threadId": action["native_id"], "excludeTurns": True})
            native.call("thread/read", {"threadId": action["native_id"]})
            requests = [e for e in native.events if e.get("method") == "item/commandExecution/requestApproval"]
            assert requests, native.events
            native.send({"id": requests[-1]["id"], "result": {"decision": "accept"}})
            wait_for(lambda: (fixture.cwd / "expire.txt").exists(), "original client approves")
            wait_for(lambda: fixture.session(sid)["status"] == "idle", "native approval resolves")
            try:
                fixture.command("expired-reply", "respond", session_id=sid, pending_id=action["id"], option_id=action["options"][0]["id"])
            except HTTPError as error:
                assert error.code == 409
            else:
                raise AssertionError("original client reply did not expire console action")
            print("PASS original client approval clears console request; late reply rejected", flush=True)
        finally:
            native.close()

        sid = fixture.create("background-terminal")
        sent = fixture.command("background-send", "send", session_id=sid, text="NETCAP_BACKGROUND")
        wait_for(lambda: any(x["kind"] == "terminal" for x in fixture.session(sid)["view"].get("executions") or []), "background terminal starts")
        wait_for(lambda: all(t["status"] != "inProgress" for t in fixture.session(sid)["view"]["turns"]), "native turn ends before background process")
        assert fixture.session(sid)["status"] == "running"
        assert operation(sent["operation_id"])["status"] == "running"
        native = Proxy(CLI, fixture.sock, fixture.env)
        try:
            session = fixture.session(sid)
            for execution in session["view"]["executions"]:
                if execution["kind"] == "terminal":
                    native.call("thread/backgroundTerminals/terminate", {"threadId": session["scope"]["native_id"], "processId": execution["id"]})
            wait_for(lambda: operation(sent["operation_id"])["status"] == "succeeded", "explicit background termination releases operation")
            print("PASS terminal remains occupied after turn; explicit native termination releases it", flush=True)
        finally:
            native.close()

        sid = fixture.create("pending-restart")
        fixture.command("pending-restart-send", "send", session_id=sid, text="NETCAP_APPROVE")
        old = wait_for(lambda: next((p for p in fixture.session(sid)["view"]["pending"] if p["status"] == "pending"), None), "approval before Go restart")
        fixture.restart_console(crash=True)
        wait_for(lambda: fixture.session(sid)["connection"] == "ready", "pending thread reconnected")
        restored = next(p for p in fixture.session(sid)["view"]["pending"] if p["id"] == old["id"])
        assert restored["status"] != "pending"
        try:
            fixture.command("old-request-reply", "respond", session_id=sid, pending_id=old["id"], option_id=old["options"][0]["id"])
        except HTTPError as error:
            assert error.code == 409
        else:
            raise AssertionError("old connection request became directly replyable")
        fixture.command("pending-manual", "takeover", session_id=sid)
        assert fixture.session(sid)["paused"]
        native = Proxy(CLI, fixture.sock, fixture.env)
        try:
            native.call("turn/interrupt", {"threadId": old["native_id"], "turnId": old["native_turn"]})
            wait_for(lambda: fixture.session(sid)["status"] == "idle", "manual terminal ends restored request")
            print("PASS pending survives Go crash; old request ID rejected and manual exit works", flush=True)
        finally:
            native.close()

        fixture.provider.stream_delay = 8
        sid = fixture.create("native-exit")
        oid = send_running(sid, "native-exit-send")
        count = len(fixture.provider.requests)
        stop(fixture.native)
        wait_for(lambda: operation(oid)["status"] == "uncertain", "native exit retains unknown occupancy")
        try:
            fixture.command("native-exit-resume", "resume", session_id=sid)
        except HTTPError as error:
            assert error.code == 409
        else:
            raise AssertionError("native exit silently released execution")
        fixture.start_native()
        fixture.command("native-exit-recheck", "reconcile", session_id=sid)
        wait_for(lambda: fixture.session(sid)["connection"] == "ready", "native history recovers")
        assert len(fixture.provider.requests) == count
        print("PASS native daemon exit/restart retains original execution without model resend:", operation(oid)["status"], flush=True)
        (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))


if __name__ == "__main__":
    main()
