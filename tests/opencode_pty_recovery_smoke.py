#!/usr/bin/env python3
"""A restarted server's empty PTY list must not forget a surviving old process."""
import argparse
import json
import os
from pathlib import Path
import signal

from opencode_console_smoke import OpenCodeConsoleFixture
from console_fixture import wait_for


def proc(pid):
    try:
        fields = Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()
        return None if fields[0] == "Z" else {"birth": fields[19], "group": int(fields[2])}
    except FileNotFoundError:
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    with OpenCodeConsoleFixture(args.netcap) as fixture:
        print("Evidence directory:", fixture.root, flush=True)
        sid = fixture.create("pty-crash")
        record = json.loads(fixture.server_record(sid=sid).read_text())
        pty = fixture.native_call("/pty", {"command": "/bin/sh", "args": ["-c", "trap '' HUP; exec sleep 90"], "cwd": str(fixture.cwd)}, sid=sid)
        ownership = proc(pty["pid"])
        assert ownership and ownership["group"] == pty["pid"] and ownership["group"] != os.getpgrp()
        try:
            wait_for(lambda: any(x["id"] == pty["id"] for x in (fixture.session(sid)["view"].get("executions") or [])), "original native PTY observed")
            assert proc(record["pid"])["birth"] == record["birth"]
            os.killpg(record["pid"], signal.SIGKILL)
            wait_for(lambda: json.loads(fixture.server_record(sid=sid).read_text())["epoch"] != record["epoch"], "new native server epoch")
            new_server = json.loads(fixture.server_record(sid=sid).read_text())
            wait_for(lambda: fixture.session(sid)["connection"] == "ready" and new_server["url"] in fixture.session(sid)["view"].get("terminal_command", ""), "new native server observations")
            alive = proc(pty["pid"])
            assert alive == ownership, "fixture PTY must survive SIGKILL to exercise retained occupancy"
            view = fixture.session(sid)["view"]
            assert not view["execution_clear"] and any(x["id"] == pty["id"] for x in view["executions"]), "new server forgot the live old PTY"
            fixture.restart_console()
            wait_for(lambda: json.loads(fixture.server_record(sid=sid).read_text())["epoch"] != new_server["epoch"], "controller restarts native process")
            resumed = json.loads(fixture.server_record(sid=sid).read_text())
            wait_for(lambda: fixture.session(sid)["connection"] == "ready" and resumed["url"] in fixture.session(sid)["view"].get("terminal_command", ""), "controller restart retains PTY lease")
            assert not fixture.session(sid)["view"]["execution_clear"]
            assert proc(pty["pid"]) == ownership
            os.killpg(ownership["group"], signal.SIGTERM)
            wait_for(lambda: fixture.session(sid)["view"]["execution_clear"], "old PTY group exit releases occupancy")
            print("PASS live old PTY survives server/controller restart, remains reserved, and releases only after verified process exit", flush=True)
            (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))
        finally:
            if proc(pty["pid"]) == ownership:
                os.killpg(ownership["group"], signal.SIGTERM)


if __name__ == "__main__":
    main()
