#!/usr/bin/env python3
"""Owned OpenCode HTTP/SSE server behind the production console."""
import argparse
import base64
import json
import os
from pathlib import Path
import signal
import time
from urllib.request import Request, urlopen

from console_fixture import ConsoleFixture, Provider, wait_for


class OpenCodeConsoleFixture(ConsoleFixture):
    def __init__(self, binary, provider_class=Provider):
        super().__init__(binary, provider_class)
        profile = self.root / "opencode"
        profile.mkdir()
        (profile / "opencode.json").write_text(json.dumps({"permission": "ask", "plugin": [],
            "provider": {"fixture": {"npm": "@ai-sdk/openai-compatible", "name": "Local fixture", "options": {},
                "models": {"fixture-model": {"name": "Fixture model", "limit": {"context": 32768, "output": 1024}}}}}}))
        self.config["instances"] = [{"id": "opencode-local", "kind": "opencode", "executable": "/home/zzw/.opencode/bin/opencode",
            "profile": str(profile), "version": "1.18.32", "policy_revision": "m0-1"}]
        self.config["routes"] = [{"id": "route-a", "instance_id": "opencode-local", "provider": "fixture", "endpoint": self.provider.url + "/v1",
            "model": "fixture-model", "credential_ref": "env:NETCAP_FIXTURE_KEY", "credential_revision": "1", "config_revision": "1"}]

    def start_native(self):
        # The Go adapter owns all native server processes for this fixture.
        pass

    def create(self, key):
        receipt = self.command(key, "create", project_id="fixture", route_id="route-a", title=key, permission="read_only")
        wait_for(lambda: self.session(receipt["session_id"])["status"] == "idle", "native OpenCode session creation", timeout=125)
        return receipt["session_id"]

    def server_record(self, instance="opencode-local", sid=None):
        records = list((self.root / "console/runtimes" / instance).glob("*/server.json"))
        if sid:
            records = [p for p in records if json.loads(p.read_text()).get("session_id") == sid]
        assert len(records) == 1, records
        return records[0]

    def native_call(self, path, body=None, method=None, instance="opencode-local", sid=None):
        record = self.server_record(instance, sid)
        server = json.loads(record.read_text())
        password = (record.parent / "server-token").read_text()
        auth = base64.b64encode(("opencode:" + password).encode()).decode()
        request = Request(server["url"] + path, data=json.dumps(body).encode() if body is not None else None,
            method=method, headers={"Content-Type": "application/json", "Authorization": "Basic " + auth})
        with urlopen(request, timeout=15) as response:
            value = response.read()
            return json.loads(value) if value else None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    parser.add_argument("--hold", action="store_true")
    parser.add_argument("--info-file")
    parser.add_argument("--interactions", action="store_true")
    args = parser.parse_args()
    provider_class = Provider
    if args.interactions:
        from opencode_interaction_smoke import OpenCodeInteractionProvider
        provider_class = OpenCodeInteractionProvider
    with OpenCodeConsoleFixture(args.netcap, provider_class) as fixture:
        print("Evidence directory:", fixture.root, flush=True)
        fixture.provider.stream_delay = 1.2
        sid = fixture.create("opencode-http")
        native_id = fixture.session(sid)["scope"]["native_id"]
        assert not fixture.provider.requests
        for n in (1, 2):
            sent = fixture.command(f"send-{n}", "send", session_id=sid, text=f"NETCAP_A_TURN_{n}")
            assert fixture.command(f"send-{n}", "send", session_id=sid, text=f"NETCAP_A_TURN_{n}") == sent
            wait_for(lambda: any(m["role"] == "assistant" and m["status"] == "inProgress" for m in fixture.session(sid)["view"]["messages"]), "OpenCode partial model stream")
            wait_for(lambda: fixture.session(sid)["status"] == "idle" and len(fixture.session(sid)["view"]["messages"]) == n * 2, "OpenCode native turn result")
        fixture.restart_console()
        wait_for(lambda: fixture.session(sid)["connection"] == "ready", "native history after server/controller restart")
        assert fixture.session(sid)["scope"]["native_id"] == native_id
        fixture.command("send-3", "send", session_id=sid, text="NETCAP_A_TURN_3")
        wait_for(lambda: fixture.session(sid)["status"] == "idle" and len(fixture.session(sid)["view"]["messages"]) == 6, "OpenCode original history continues")
        assert len(fixture.provider.requests) == 3
        result = fixture.provider.assert_route("fixture-model")
        (fixture.root / "result.json").write_text(json.dumps(result, indent=2))
        (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))
        print("PASS OpenCode production HTTP/SSE: two streamed turns, command dedupe, server restart/history and third turn", flush=True)
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
