#!/usr/bin/env python3
"""Confirmed idle SDK processes are reclaimed and resume the same history."""
import argparse
import json
from pathlib import Path

from claude_console_smoke import ClaudeConsoleFixture
from console_fixture import wait_for


def rss(pid):
    try:
        values = Path(f"/proc/{pid}/status").read_text().splitlines()
        return next(int(line.split()[1]) for line in values if line.startswith("VmRSS:"))
    except (FileNotFoundError, StopIteration):
        return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    with ClaudeConsoleFixture(args.netcap) as fixture:
        print("Evidence directory:", fixture.root, flush=True)
        sid = fixture.create("idle-reclaim")
        native_id = fixture.session(sid)["scope"]["native_id"]
        fixture.command("first", "send", session_id=sid, text="NETCAP_A_TURN_1")
        wait_for(lambda: fixture.session(sid)["status"] == "idle" and len(fixture.session(sid)["view"]["messages"]) == 2, "first result")
        journal = fixture.root / "console/runtimes/claude-local" / (native_id + ".jsonl")
        entries = [json.loads(line) for line in journal.read_text().splitlines()]
        cli_pid = next(e["pid"] for e in reversed(entries) if e["kind"] == "initialized")
        bridge_pid = int(Path(f"/proc/{cli_pid}/stat").read_text().rsplit(")", 1)[1].split()[1])
        measured = {"go_rss_kib": rss(fixture.console.pid), "bridge_rss_kib": rss(bridge_pid), "cli_rss_kib": rss(cli_pid)}
        assert measured["bridge_rss_kib"] > 0 and measured["cli_rss_kib"] > 0
        print("Warm idle RSS KiB:", measured, flush=True)
        wait_for(lambda: not Path(f"/proc/{cli_pid}").exists() and not Path(f"/proc/{bridge_pid}").exists(), "confirmed idle CLI and bridge reclamation", timeout=45)
        assert fixture.session(sid)["status"] == "idle"
        measured["after_reclaim_go_rss_kib"] = rss(fixture.console.pid)
        fixture.command("idle-takeover", "takeover", session_id=sid)
        handoff = fixture.command("idle-resume", "resume", session_id=sid)
        wait_for(lambda: any(o["id"] == handoff["operation_id"] and o["status"] == "succeeded"
                             for o in fixture.snapshot()["operations"]), "fresh control check after bridge reclamation")
        assert fixture.session(sid)["scope"]["native_id"] == native_id and not fixture.session(sid)["paused"]
        assert len(fixture.provider.requests) == 1, "control check sent unexpected model input"
        fixture.command("second", "send", session_id=sid, text="NETCAP_A_TURN_2")
        wait_for(lambda: fixture.session(sid)["status"] == "idle" and len(fixture.session(sid)["view"]["messages"]) == 4, "same native session resumes after idle reclamation")
        assert fixture.session(sid)["scope"]["native_id"] == native_id and len(fixture.provider.requests) == 2
        (fixture.root / "result.json").write_text(json.dumps({**measured, "native_id": native_id, "idle_processes_reclaimed": True,
            "fresh_handoff_without_model_input": True, "resumed_same_native_history": True}, indent=2))
        print("PASS idle reclamation, fresh read-only handoff and next send on original session/history", flush=True)


if __name__ == "__main__":
    main()
