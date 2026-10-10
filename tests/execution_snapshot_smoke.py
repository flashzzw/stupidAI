#!/usr/bin/env python3
"""Production W02 snapshot/reference checks using real CLIs and local providers.

The registered asset is intentionally outside native discovery directories.
Recording its reference must not silently install or load it.
"""
import argparse
import json
import os
from pathlib import Path
import signal
import time
from urllib.error import HTTPError

from claude_console_smoke import ClaudeConsoleFixture
from console_fixture import ConsoleFixture, wait_for


def fixture_for(binary, backend):
    fixture = (ClaudeConsoleFixture if backend == "claude" else ConsoleFixture)(binary)
    asset = fixture.root / "registered-skill"
    asset.mkdir()
    (asset / "SKILL.md").write_text("---\nname: w02-record-only\ndescription: W02_NOT_IMPLICITLY_LOADED\n---\nLocal fixture.\n")
    (asset / "tool.py").write_text("# W02_PRIVATE_SOURCE_CONTENT\n")
    fixture.config["capability_assets"] = [{"id": "shared-skill", "name": "快照验收 Skill", "kind": "skill",
        "version": "v1", "source": str(asset), "backends": ["codex", "claude"]}]
    return fixture, asset


def run(binary, backend, hold=False, info_file=None):
    fixture, asset = fixture_for(binary, backend)
    with fixture:
        print("Evidence directory:", fixture.root, flush=True)
        if hold:
            info = {**fixture.info, "root": str(fixture.root), "harness_pid": os.getpid(), "asset": str(asset), "backend": backend}
            if info_file:
                Path(info_file).write_text(json.dumps(info))
            stopped = False
            def finish(*_):
                nonlocal stopped
                stopped = True
            signal.signal(signal.SIGTERM, finish)
            signal.signal(signal.SIGINT, finish)
            print("READY", fixture.info["url"], flush=True)
            while not stopped:
                assert fixture.console.poll() is None
                time.sleep(.2)
            return

        checks = {}
        fields = dict(project_id="fixture", route_id="route-a", title="snapshot-" + backend,
                      permission="read_only", capability_refs=["shared-skill"])
        receipt = fixture.command("snapshot-create", "create", **fields)
        sid = receipt["session_id"]
        wait_for(lambda: fixture.session(sid)["status"] == "idle", "native creation")
        native = fixture.session(sid)["scope"]["native_id"]
        path = f"/api/sessions/{sid}/execution"
        count = len(fixture.provider.requests)
        initial = fixture.api(path)
        snapshot = initial["snapshot"]
        checks["snapshot_records_version_and_binding"] = bool(snapshot["cli_version"]) and snapshot["session_id"] == sid and snapshot["state_namespace"] == fixture.session(sid)["scope"]["state_namespace"]
        checks["reference_is_recorded_without_loading_claim"] = snapshot["coverage"] == "recorded_sources_only" and snapshot["capability_refs"][0]["asset"]["version"] == "v1"
        checks["source_content_not_exposed"] = "W02_PRIVATE_SOURCE_CONTENT" not in json.dumps(initial)
        checks["inspection_has_no_model_side_effect"] = len(fixture.provider.requests) == count
        checks["create_retry_is_idempotent"] = fixture.command("snapshot-create", "create", **fields) == receipt

        def turn(number):
            fixture.command("turn-" + str(number), "send", session_id=sid, text=f"NETCAP_A_TURN_{number}")
            wait_for(lambda: fixture.session(sid)["status"] == "idle" and len(fixture.session(sid)["view"]["messages"]) == 2 * number, "native turn " + str(number), timeout=45)

        turn(1)
        checks["reference_did_not_implicitly_load_asset"] = all("W02_NOT_IMPLICITLY_LOADED" not in json.dumps(r["body"]) for r in fixture.provider.requests)
        # Registry metadata evolves; prior referenced version and original files
        # remain authoritative for the running execution.
        fixture.config["capability_assets"][0].update(version="v2", disabled=True)
        fixture.restart_console(crash=True)
        wait_for(lambda: fixture.session(sid)["connection"] == "ready", "original native identity reconnects")
        restored = fixture.api(path)
        checks["restart_and_registry_update_preserve_snapshot"] = restored["snapshot"] == snapshot and restored["status"] == "unchanged"
        turn(2)
        script = asset / "tool.py"
        old = script.read_bytes()
        script.write_text("# changed dependency, same skill entry point\n")
        drifted = fixture.api(path)
        checks["dependent_script_drift_visible"] = drifted["status"] == "drifted" and drifted["sources"][1]["status"] == "changed"
        count = len(fixture.provider.requests)
        for kind in ("send", "resume"):
            try:
                fixture.command("drift-" + kind, kind, session_id=sid, text="must not send")
            except HTTPError as error:
                checks["drift_refuses_" + kind] = error.code == 409
            else:
                checks["drift_refuses_" + kind] = False
        checks["drift_did_not_send_model_input"] = len(fixture.provider.requests) == count
        checks["drift_did_not_overwrite_snapshot"] = drifted["snapshot"] == snapshot
        script.write_bytes(old)
        turn(3)
        checks["original_history_and_identity_preserved"] = fixture.session(sid)["scope"]["native_id"] == native and len(fixture.session(sid)["view"]["messages"]) == 6
        checks["all_operations_reference_original_snapshot"] = all(op.get("execution_snapshot_id") == snapshot["id"] for op in fixture.snapshot()["operations"] if op["session_id"] == sid)
        checks["model_credentials_bound"] = all(r["auth_matches"] for r in fixture.provider.requests)
        checks["provider_has_no_errors"] = not fixture.provider.errors
        result = {"backend": backend, "execution": "completed", "expectations_met": all(checks.values()), "checks": checks,
                  "native_id": native, "snapshot": snapshot}
        (fixture.root / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2))
        print(json.dumps({"backend": backend, "checks": checks, "expectations_met": result["expectations_met"]}), flush=True)
        assert result["expectations_met"], "snapshot assertions failed"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    parser.add_argument("--backend", choices=["codex", "claude"], required=True)
    parser.add_argument("--hold", action="store_true")
    parser.add_argument("--info-file")
    args = parser.parse_args()
    run(str(Path(args.netcap).resolve()), args.backend, args.hold, args.info_file)


if __name__ == "__main__":
    main()
