#!/usr/bin/env python3
"""Lost real HTTP receipts, SSE faults, partial history and no prompt replay."""
import argparse
import json
import os
from pathlib import Path
import signal
import time
from urllib.error import HTTPError

from opencode_console_smoke import OpenCodeConsoleFixture
from console_fixture import wait_for


def hold(fixture, args):
    if args.info_file:
        Path(args.info_file).write_text(json.dumps({**fixture.info, "root": str(fixture.root), "harness_pid": os.getpid(), "console_pid": fixture.console.pid}, indent=2))
    if not args.hold:
        return
    finished = False
    def finish(*_):
        nonlocal finished
        finished = True
    signal.signal(signal.SIGTERM, finish)
    signal.signal(signal.SIGINT, finish)
    print("READY", fixture.info["url"], flush=True)
    while not finished:
        assert fixture.console.poll() is None
        time.sleep(.2)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    parser.add_argument("--hold", action="store_true")
    parser.add_argument("--info-file")
    parser.add_argument("--browser-only", action="store_true")
    args = parser.parse_args()
    fixture = OpenCodeConsoleFixture(args.netcap)
    faults = fixture.root / "faults"
    faults.mkdir()
    fixture.env["NETCAP_OPENCODE_FAULT_DIR"] = str(faults)
    fixture.config["instances"][0]["env_allow"] = ["NETCAP_OPENCODE_FAULT_DIR"]
    fixture.config["instances"][0]["executable"] = str(Path(__file__).with_name("fault_opencode_server.py").resolve())
    with fixture:
        print("Evidence directory:", fixture.root, flush=True)
        if args.browser_only:
            fixture.provider.stream_delay = 4
            hold(fixture, args)
            return
        def operation(oid):
            return next(o for o in fixture.snapshot()["operations"] if o["id"] == oid)
        def arm(path):
            (faults / "armed.json").write_text(json.dumps({"method": "POST", "path": path, "kind": "drop"}))
        def count(path):
            return sum(json.loads(line) == {"method": "POST", "path": path} for line in (faults / "calls.jsonl").read_text().splitlines())
        arm("/session")
        created = fixture.command("create-lost", "create", project_id="fixture", route_id="route-a", title="lost-create", permission="read_only")
        wait_for(lambda: operation(created["operation_id"])["status"] == "succeeded", "lost OpenCode create receipt")
        sid = created["session_id"]
        native = fixture.session(sid)["scope"]["native_id"]
        assert native == json.loads((faults / "dropped.json").read_text())["response"]["id"]
        assert count("/session") == 1
        print("PASS OpenCode lost create receipt finds original metadata, one creation", flush=True)

        path = "/session/" + native + "/prompt_async"
        fixture.provider.stream_delay = 1.2
        arm(path)
        (faults / "sse-mode").write_text("duplicate-late")
        sent = fixture.command("send-lost", "send", session_id=sid, text="NETCAP_A_TURN_1")
        wait_for(lambda: operation(sent["operation_id"])["status"] == "succeeded", "lost prompt receipt and duplicated SSE")
        assert count(path) == 1 and len(fixture.provider.requests) == 1
        assert (faults / "sse-injected").exists()
        assert fixture.command("send-lost", "send", session_id=sid, text="NETCAP_A_TURN_1") == sent
        assert fixture.session(sid)["view"]["messages"][-1]["text"] == "fixture A: NETCAP_A_TURN_1"
        print("PASS OpenCode lost prompt receipt, duplicate/late SSE and no second model call", flush=True)

        (faults / "sse-mode").write_text("disconnect")
        sent = fixture.command("send-disconnect", "send", session_id=sid, text="NETCAP_A_TURN_2")
        wait_for(lambda: operation(sent["operation_id"])["status"] == "succeeded", "REST recovers disconnected SSE")
        assert (faults / "sse-disconnected").exists() and count(path) == 2
        print("PASS SSE disconnect recovers by REST without resending prompt", flush=True)

        fixture.provider.stream_delay = 2
        sent = fixture.command("send-partial", "send", session_id=sid, text="NETCAP_A_TURN_3")
        wait_for(lambda: len(fixture.provider.requests) == 3, "third native model request")
        (faults / "partial-history").touch()
        wait_for(lambda: operation(sent["operation_id"])["status"] == "uncertain", "partial native history retains Unknown")
        try:
            fixture.command("resume-partial", "resume", session_id=sid)
        except HTTPError as error:
            assert error.code == 409
        else:
            raise AssertionError("resume cleared incomplete evidence")
        (faults / "partial-history").unlink()
        wait_for(lambda: operation(sent["operation_id"])["status"] == "succeeded", "original full history restores terminal evidence")
        fixture.restart_console()
        wait_for(lambda: fixture.session(sid)["status"] == "idle", "controller and native relay restart")
        assert count(path) == 3 and len(fixture.provider.requests) == 3
        fixture.provider.assert_route("fixture-model")
        print("PASS partial history blocks resume; full evidence and restart recover original three prompts", flush=True)
        (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))
        hold(fixture, args)


if __name__ == "__main__":
    main()
