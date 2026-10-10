#!/usr/bin/env python3
"""Claude through production Go HTTP and the fixed official SDK bridge."""
import argparse
import json
import os
from pathlib import Path
import shutil
import signal
import time

from console_fixture import ConsoleFixture, Provider, wait_for


class ClaudeConsoleFixture(ConsoleFixture):
    def __init__(self, binary, provider_class=Provider):
        super().__init__(binary, provider_class)
        self.env.update(NETCAP_CLAUDE_KEY="fixture-A", CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1",
                        DISABLE_AUTOUPDATER="1", DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1")
        self.config["instances"] = [{"id": "claude-local", "kind": "claude", "executable": "/home/zzw/.local/bin/claude",
            "profile": self.env["CLAUDE_CONFIG_DIR"], "bridge": str(Path("bridges/claude/dist/main.js").resolve()),
            "node_executable": shutil.which("node"), "version": "2.1.280/0.3.280", "policy_revision": "m0-1",
            "env_allow": ["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"]}]
        self.config["routes"] = [{"id": "route-a", "instance_id": "claude-local", "provider": "anthropic", "endpoint": self.provider.url,
            "model": "claude-sonnet-4-6", "credential_ref": "env:NETCAP_CLAUDE_KEY", "credential_revision": "1", "config_revision": "1"}]

    def start_native(self):
        # The bridge owns SDK CLI children; there is no shared Claude daemon.
        pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    parser.add_argument("--hold", action="store_true")
    parser.add_argument("--info-file")
    parser.add_argument("--interactions", action="store_true")
    args = parser.parse_args()
    provider_class = Provider
    if args.interactions:
        from claude_interaction_smoke import ClaudeInteractionProvider
        provider_class = ClaudeInteractionProvider
    with ClaudeConsoleFixture(args.netcap, provider_class) as fixture:
        print("Evidence directory:", fixture.root, flush=True)
        fixture.provider.stream_delay = 1.2
        sid = fixture.create("claude-http")
        native_id = fixture.session(sid)["scope"]["native_id"]
        assert not fixture.provider.requests, "creating a Claude session unexpectedly calls the provider"
        for number in (1, 2):
            receipt = fixture.command(f"send-{number}", "send", session_id=sid, text=f"NETCAP_A_TURN_{number}")
            assert fixture.command(f"send-{number}", "send", session_id=sid, text=f"NETCAP_A_TURN_{number}") == receipt
            wait_for(lambda: any(m["role"] == "assistant" and m["status"] == "inProgress" for m in fixture.session(sid)["view"]["messages"]), "real Claude partial stream")
            wait_for(lambda: fixture.session(sid)["status"] == "idle", "native Claude result")
        fixture.restart_console()
        wait_for(lambda: fixture.session(sid)["connection"] == "ready", "bridge restores native history")
        assert fixture.session(sid)["scope"]["native_id"] == native_id
        assert len(fixture.session(sid)["view"]["messages"]) == 4
        fixture.command("send-3", "send", session_id=sid, text="NETCAP_A_TURN_3")
        wait_for(lambda: fixture.session(sid)["status"] == "idle" and len(fixture.session(sid)["view"]["messages"]) == 6, "Claude resumed third turn")
        result = fixture.provider.assert_route("claude-sonnet-4-6")
        print("PASS production Go -> SDK -> Claude: two streamed turns, dedupe, restart/history and third turn on original route", flush=True)
        (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))
        (fixture.root / "result.json").write_text(json.dumps(result, indent=2))
        if args.info_file:
            Path(args.info_file).write_text(json.dumps({**fixture.info, "root": str(fixture.root), "harness_pid": os.getpid(), "console_pid": fixture.console.pid}, indent=2))
        if args.hold:
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


if __name__ == "__main__":
    main()
