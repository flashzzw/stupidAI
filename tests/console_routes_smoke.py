#!/usr/bin/env python3
"""Read-only doctor and immutable route bindings on three real CLIs."""
import argparse
import json
from pathlib import Path
import subprocess
from urllib.error import HTTPError

from console_alpha_smoke import AlphaFixture
from console_fixture import wait_for


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    fixture = AlphaFixture(args.netcap)
    fixture.config_path.write_text(json.dumps(fixture.config))
    try:
        result = subprocess.run([fixture.binary, "--config", str(fixture.config_path), "doctor", "--console"], env=fixture.env, cwd=fixture.cwd, capture_output=True, text=True, timeout=25, check=True)
        report = json.loads(result.stdout)
        assert report["read_only"] and len(report["instances"]) == 3 and len(report["routes"]) == 3
        assert not (fixture.root / "console").exists(), "doctor created a runtime/state directory"
        assert all(not provider.requests for provider in fixture.providers.values()) and not fixture.sock.exists()
        assert all(route["health"] == "unknown" for route in report["routes"])
        assert all("fixture-" + suffix not in result.stdout for suffix in ("A", "B", "C"))
        print("Evidence directory:", fixture.root, flush=True)
        print("PASS console doctor: configuration/CLI only, no state store, native server, credential output or model call", flush=True)
        fixture.start_native()
        fixture.start_console()
        sessions = {}
        for kind, route, project in (("codex", "route-a", "fixture"), ("claude", "route-b", "claude"), ("opencode", "route-c", "opencode")):
            sid = fixture.command(kind + "-create", "create", project_id=project, route_id=route, title="固定路由 " + kind, permission="read_only")["session_id"]
            sessions[kind] = sid
            wait_for(lambda: fixture.session(sid)["status"] == "idle", kind + " creation", timeout=125)
            fixture.command(kind + "-send", "send", session_id=sid, text="NETCAP_A_TURN_1")
            wait_for(lambda: fixture.session(sid)["status"] == "idle", kind + " native result")
        before = {item["route"]["id"]: item for item in fixture.api("/api/workspace")["route_statuses"]}
        counts = {kind: len(provider.requests) for kind, provider in fixture.providers.items()}
        for route in fixture.config["routes"]:
            route["config_revision"] = "new-revision"
        fixture.restart_console()
        after = {item["route"]["id"]: item for item in fixture.api("/api/workspace")["route_statuses"]}
        assert all(before[key]["identity"] != after[key]["identity"] for key in before)
        for kind, sid in sessions.items():
            wait_for(lambda: fixture.session(sid)["connection"] == "ready", kind + " original history recovered")
            try:
                fixture.command(kind + "-drift-send", "send", session_id=sid, text="must not execute")
            except HTTPError as error:
                assert error.code == 409
            else:
                raise AssertionError("old session accepted changed route")
            assert fixture.session(sid)["scope"]["route"]["config_revision"] == "1"
            assert len(fixture.providers[kind].requests) == counts[kind]
        result = {"agents": list(sessions), "doctor": "read-only, zero native servers/model calls/state writes", "route_revision": "new identity", "old_session": "preserved and send rejected", "provider_calls_after_drift": 0}
        (fixture.root / "route-result.json").write_text(json.dumps(result, indent=2))
        print("PASS", json.dumps(result), flush=True)
    finally:
        fixture.close()


if __name__ == "__main__":
    main()
