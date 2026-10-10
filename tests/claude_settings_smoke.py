#!/usr/bin/env python3
"""Project rules/hooks, isolated probe settings and per-query A/B credentials."""
import json
from pathlib import Path
import tempfile
import uuid

from claude_bridge_smoke import Bridge
from console_fixture import Provider, isolated_env, wait_for


def main():
    root = Path(tempfile.mkdtemp(prefix="netcap-claude-settings-"))
    print("Evidence directory:", root, flush=True)
    env = isolated_env(root)
    env.update(CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1", DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1")
    work = root / "work"
    work.mkdir()
    (work / "CLAUDE.md").write_text("NETCAP_PROJECT_RULE: use the fixture's exact vocabulary.\n")
    (Path(env["CLAUDE_CONFIG_DIR"]) / "CLAUDE.md").write_text("NETCAP_USER_RULE: this is an isolated test profile.\n")
    (work / ".claude").mkdir()
    sender = root / "fake_sender.py"
    sender.write_text("import json,sys\nfrom pathlib import Path\nx=json.load(sys.stdin)\nwith Path(" + repr(str(root / "hooks.jsonl")) + ").open('a') as f:f.write(json.dumps({'event':x['hook_event_name'],'session':x['session_id']})+'\\n')\n")
    (work / ".claude/settings.json").write_text(json.dumps({"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "python3 " + str(sender)}]}]}}))
    providers = [Provider(root, key) for key in ("A", "B")]
    client = None
    probe_client = None
    try:
        client = Bridge(Path("bridges/claude/dist/main.js").resolve(), root, env)
        ids = []
        operations = []
        for provider in providers:
            sid = str(uuid.uuid4())
            ids.append(sid)
            client.call("create", {"native_id": sid, "source": sid, "cwd": str(work), "executable": "/home/zzw/.local/bin/claude",
                "model": "claude-sonnet-4-6", "endpoint": provider.url, "permission": "read_only", "api_key": "fixture-" + provider.route})
            oid = str(uuid.uuid4())
            operations.append(oid)
            client.call("send", {"native_id": sid, "operation": oid, "text": f"NETCAP_{provider.route}_TURN_1", "api_key": "fixture-" + provider.route})
        for sid, oid in zip(ids, operations):
            wait_for(lambda: any(e["kind"] == "event" and e["operation"] == oid and e["event"]["type"] == "result" for e in client.call("snapshot", {"native_id": sid})["entries"]), "parallel route result")
        for provider in providers:
            assert provider.requests and all(r["auth_matches"] for r in provider.requests)
            request = json.dumps(provider.requests[-1]["body"])
            assert "NETCAP_PROJECT_RULE" in request and "NETCAP_USER_RULE" in request
            opposite = "B" if provider.route == "A" else "A"
            assert f"NETCAP_{opposite}_TURN_1" not in request
        hooks = [json.loads(line) for line in (root / "hooks.jsonl").read_text().splitlines()]
        assert len(hooks) == 2 and {h["session"] for h in hooks} == set(ids), hooks
        print("PASS concurrent A/B queries preserve credentials, project/user rules and one hook sender per session", flush=True)

        probe_root = root / "probe-instance"
        probe_root.mkdir()
        probe_env = isolated_env(probe_root)
        probe_env.update({k: env[k] for k in ("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING")})
        probe_cwd = probe_root / "work"
        probe_cwd.mkdir()
        probe_client = Bridge(Path("bridges/claude/dist/main.js").resolve(), probe_root, probe_env)
        sid = str(uuid.uuid4())
        probe_client.call("create", {"native_id": sid, "source": sid, "cwd": str(probe_cwd), "executable": "/home/zzw/.local/bin/claude",
            "model": "claude-sonnet-4-6", "endpoint": providers[0].url, "permission": "read_only", "api_key": "fixture-A", "probe": True})
        oid = str(uuid.uuid4())
        probe_client.call("send", {"native_id": sid, "operation": oid, "text": "NETCAP_A_TURN_2", "api_key": "fixture-A"})
        wait_for(lambda: any(e["kind"] == "event" and e["operation"] == oid and e["event"]["type"] == "result" for e in probe_client.call("snapshot", {"native_id": sid})["entries"]), "isolated probe result")
        request = json.dumps(providers[0].requests[-1]["body"])
        assert "NETCAP_PROJECT_RULE" not in request and "NETCAP_USER_RULE" not in request
        assert len((root / "hooks.jsonl").read_text().splitlines()) == 2
        assert providers[0].requests[-1]["auth_matches"]
        print("PASS probe uses same selected endpoint/model/credential with separate profile/cwd and no work hooks/rules", flush=True)
        (root / "result.json").write_text(json.dumps({"sessions": ids, "probe": sid, "hook_count": len(hooks), "route_auth_verified": True}, indent=2))
    finally:
        if probe_client:
            probe_client.close()
        if client:
            client.close()
        for provider in providers:
            provider.close()


if __name__ == "__main__":
    main()
