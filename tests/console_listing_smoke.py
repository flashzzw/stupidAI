#!/usr/bin/env python3
"""Real native history through metadata lists, cursor pages and local archive."""
import argparse
import json
import os
from pathlib import Path
import signal
import time

from opencode_console_smoke import OpenCodeConsoleFixture
from console_fixture import wait_for


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    parser.add_argument("--hold", action="store_true")
    parser.add_argument("--info-file")
    args = parser.parse_args()
    fixture = OpenCodeConsoleFixture(args.netcap)
    second_cwd = fixture.root / "second-work"
    second_cwd.mkdir()
    fixture.config["projects"].append({"id": "second", "name": "第二项目", "cwd": str(second_cwd)})
    with fixture:
        print("Evidence directory:", fixture.root, flush=True)
        sid = fixture.command("paging-create", "create", project_id="fixture", route_id="route-a", title="分页验收", permission="read_only")["session_id"]
        wait_for(lambda: fixture.session(sid)["status"] == "idle", "paging session creation")
        for turn in range(1, 28):
            fixture.command(f"send-{turn}", "send", session_id=sid, text=f"NETCAP_A_TURN_{turn}")
            wait_for(lambda: fixture.session(sid)["status"] == "idle" and len(fixture.session(sid)["view"]["messages"]) == turn * 2, "native turn " + str(turn))
        second = fixture.command("second-create", "create", project_id="second", route_id="route-a", title="另一项目会话", permission="read_only")["session_id"]
        wait_for(lambda: fixture.session(second)["status"] == "idle", "second project session")
        metadata = fixture.api("/api/workspace")
        assert all(not s["view"].get("messages") for s in metadata["sessions"]) and metadata["operations"] == []
        assert "NETCAP_A_TURN_" not in json.dumps(metadata)
        messages = []
        cursor = ""
        while True:
            page = fixture.api(f"/api/sessions/{sid}?limit=20&before=" + cursor)
            assert page["total_messages"] == 54 and len(page["session"]["view"]["messages"]) <= 20
            messages = page["session"]["view"]["messages"] + messages
            cursor = page.get("previous_cursor", "")
            if not cursor:
                break
        assert len(messages) == 54 and len({m["id"] for m in messages}) == 54
        assert messages[0]["text"] == "NETCAP_A_TURN_1" and messages[-2]["text"] == "NETCAP_A_TURN_27"
        original = fixture.session(sid)["scope"]["native_id"]
        fixture.command("archive-history", "archive", session_id=sid)
        fixture.restart_console()
        wait_for(lambda: fixture.session(sid)["connection"] == "ready", "archive preserved across restart")
        assert fixture.session(sid)["archived"] and fixture.session(sid)["scope"]["native_id"] == original
        fixture.command("show-history", "unarchive", session_id=sid)
        assert not fixture.session(sid)["archived"] and len(fixture.provider.requests) == 27
        print("PASS metadata-only lists, 54 real messages across stable pages, independent projects and archive/restart without native deletion or replay", flush=True)
        (fixture.root / "result.json").write_text(json.dumps({"messages": len(messages), "model_calls": len(fixture.provider.requests), "session_id": sid, "native_id": original, "metadata_only": True, "archive_persisted": True}, indent=2))
        if args.info_file:
            Path(args.info_file).write_text(json.dumps({**fixture.info, "root": str(fixture.root), "harness_pid": os.getpid(), "console_pid": fixture.console.pid, "session_id": sid}, indent=2))
        if args.hold:
            finished = False
            def finish(*_):
                nonlocal finished
                finished = True
            signal.signal(signal.SIGTERM, finish)
            signal.signal(signal.SIGINT, finish)
            fixture.provider.stream_delay = 1.2
            print("READY", fixture.info["url"], flush=True)
            while not finished:
                assert fixture.console.poll() is None
                time.sleep(.2)


if __name__ == "__main__":
    main()
