#!/usr/bin/env python3
"""Dropped real bridge receipts recover original native IDs without replay."""
import argparse
import json
from pathlib import Path
from urllib.error import HTTPError

from claude_console_smoke import ClaudeConsoleFixture
from console_fixture import wait_for, stop


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    fixture = ClaudeConsoleFixture(args.netcap)
    faults = fixture.root / "faults"
    faults.mkdir()
    fixture.env["NETCAP_CLAUDE_FAULT_DIR"] = str(faults)
    fixture.config["instances"][0]["env_allow"].append("NETCAP_CLAUDE_FAULT_DIR")
    fixture.config["instances"][0]["node_executable"] = str(Path(__file__).with_name("fault_claude_node.py").resolve())
    with fixture:
        print("Evidence directory:", fixture.root, flush=True)
        def operation(oid):
            return next(o for o in fixture.snapshot()["operations"] if o["id"] == oid)
        def arm(method):
            (faults / "dropped.json").unlink(missing_ok=True)
            (faults / "armed.json").write_text(json.dumps({"method": method}))
        def count(method):
            return sum(json.loads(line)["method"] == method for line in (faults / "calls.jsonl").read_text().splitlines())
        arm("create")
        created = fixture.command("create", "create", project_id="fixture", route_id="route-a", title="lost-create", permission="read_only")
        # Native initialization has a 120-second budget to allow cold plugin
        # startup. A dropped receipt reaches reconciliation after that budget.
        wait_for(lambda: operation(created["operation_id"])["status"] == "succeeded", "lost SDK create receipt reconciliation", timeout=150)
        expected = json.loads((faults / "dropped.json").read_text())["result"]["native_id"]
        sid = created["session_id"]
        assert fixture.session(sid)["scope"]["native_id"] == expected and count("create") == 1
        print("PASS dropped bridge create receipt recovers original native ID, exactly one initialization", flush=True)
        arm("send")
        sent = fixture.command("send", "send", session_id=sid, text="NETCAP_A_TURN_1")
        wait_for(lambda: operation(sent["operation_id"])["status"] == "succeeded", "lost SDK send receipt reconciliation", timeout=45)
        assert count("send") == 1 and len(fixture.provider.requests) == 1
        assert fixture.command("send", "send", session_id=sid, text="NETCAP_A_TURN_1") == sent
        print("PASS dropped bridge send receipt verifies native input/result without duplicate model call", flush=True)
        stop(fixture.console)
        journal = fixture.root / "console/runtimes/claude-local" / (expected + ".jsonl")
        intact = journal.read_bytes()
        journal.write_bytes(intact + b'{"kind":"event"')
        fixture.start_console()
        wait_for(lambda: fixture.session(sid)["status"] == "unknown", "truncated SDK journal is incomplete evidence")
        try:
            fixture.command("cannot-send", "send", session_id=sid, text="must not execute")
        except HTTPError as error:
            assert error.code == 409
        else:
            raise AssertionError("incomplete SDK journal permitted new input")
        assert count("send") == 1
        stop(fixture.console)
        journal.write_bytes(intact)
        fixture.start_console()
        wait_for(lambda: fixture.session(sid)["status"] == "idle", "complete original journal restores evidence")
        print("PASS truncated SDK journal blocks new input; restoring original evidence recovers without replay", flush=True)
        (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))


if __name__ == "__main__":
    main()
