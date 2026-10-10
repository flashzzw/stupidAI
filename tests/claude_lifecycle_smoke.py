#!/usr/bin/env python3
"""Owned bridge/CLI death, pending validity and durable unknown occupancy."""
import argparse
import json
import os
from pathlib import Path
import signal

from claude_console_smoke import ClaudeConsoleFixture
from claude_interaction_smoke import ClaudeInteractionProvider
from console_fixture import wait_for


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    with ClaudeConsoleFixture(args.netcap, ClaudeInteractionProvider) as fixture:
        print("Evidence directory:", fixture.root, flush=True)
        def operation(oid):
            return next(o for o in fixture.snapshot()["operations"] if o["id"] == oid)
        def cli_pid(sid):
            native = fixture.session(sid)["scope"]["native_id"]
            journal = fixture.root / "console/runtimes/claude-local" / (native + ".jsonl")
            records = [json.loads(line) for line in journal.read_text().splitlines()]
            pid = next(e["pid"] for e in reversed(records) if e["kind"] == "initialized")
            argv = Path(f"/proc/{pid}/cmdline").read_bytes().split(b"\0")
            environment = Path(f"/proc/{pid}/environ").read_bytes().split(b"\0")
            assert ("CLAUDE_CONFIG_DIR=" + fixture.env["CLAUDE_CONFIG_DIR"]).encode() in environment
            assert Path(f"/proc/{pid}/cwd").resolve() == fixture.cwd and b"stream-json" in argv, "PID does not own the fixture SDK process"
            return pid

        fixture.provider.stream_delay = 8
        sid = fixture.create("cli-exit")
        sent = fixture.command("cli-exit-send", "send", session_id=sid, text="NETCAP_A_TURN_1")
        wait_for(lambda: any(m["role"] == "assistant" and m["status"] == "inProgress" for m in fixture.session(sid)["view"]["messages"]), "real native stream before CLI death")
        pid = cli_pid(sid)
        os.kill(pid, signal.SIGKILL)
        wait_for(lambda: operation(sent["operation_id"])["status"] == "uncertain", "CLI death has no successful terminal evidence")
        count = len(fixture.provider.requests)
        fixture.restart_console()
        wait_for(lambda: fixture.session(sid)["connection"] == "ready", "read-only SDK journal/history recovery")
        assert operation(sent["operation_id"])["status"] == "uncertain" and len(fixture.provider.requests) == count
        print("PASS independent CLI death: Unknown survives controller restart and no input replay", flush=True)

        fixture.provider.stream_delay = 0
        sid = fixture.create("bridge-exit-pending")
        sent = fixture.command("bridge-exit-send", "send", session_id=sid, text="NETCAP_CLAUDE_APPROVE")
        old = wait_for(lambda: next((p for p in fixture.session(sid)["view"]["pending"] if p["status"] == "pending"), None), "approval before bridge death")
        pid = cli_pid(sid)
        fields = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
        bridge_pid = int(fields[1])
        argv = Path(f"/proc/{bridge_pid}/cmdline").read_bytes()
        assert str(fixture.root / "console/runtimes/claude-local").encode() in argv
        os.kill(bridge_pid, signal.SIGKILL)
        wait_for(lambda: operation(sent["operation_id"])["status"] == "uncertain", "bridge death retains parent operation")
        wait_for(lambda: fixture.session(sid)["connection"] == "ready", "new read-only bridge recovers")
        pending = next(p for p in fixture.session(sid)["view"]["pending"] if p["id"] == old["id"])
        assert pending["status"] != "pending"
        assert not (fixture.cwd / "approve.txt").exists()
        fixture.command("bridge-takeover", "takeover", session_id=sid)
        # The killed bridge's stdin closes in the CLI; ensure the concrete
        # child captured above exits, not merely that its parent disappeared.
        wait_for(lambda: not Path(f"/proc/{pid}").exists() or Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[0] == "Z", "owned CLI exits after bridge pipe closes", timeout=15)
        print("PASS bridge SIGKILL: old approval expires, no file effect, child exits and manual Unknown remains", flush=True)
        (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))


if __name__ == "__main__":
    main()
