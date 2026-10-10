#!/usr/bin/env python3
"""W02.04 native boundary: loaded resume vs unloaded same-ID MCP selection.

Uses pinned real Codex and the existing isolated local-provider/MCP fixtures.
Only the daemon owned by this test is restarted; user state is never loaded.
"""
import json
from pathlib import Path
import subprocess

from console_fixture import CLI, SERVER_CLI, wait_for
from go_cli_smoke import Proxy
from native_mcp_selection_smoke import fixture_for


def run():
    f = fixture_for("")  # This probe exercises native RPC, without the console.
    client = None
    print("Evidence directory:", f.root, flush=True)
    result = {"scope": "real pinned Codex; local provider; owned daemon only", "checks": {}, "observations": {}}
    checks, observed = result["checks"], result["observations"]
    try:
        result["versions"] = {name: subprocess.check_output([binary, "--version"], env=f.env, text=True).strip()
                              for name, binary in (("client", CLI), ("server", SERVER_CLI))}
        assert result["versions"] == {"client": "codex-cli 0.157.1", "server": "codex-cli 0.158.0"}
        original = (f.profile / "config.toml").read_bytes()
        f.start_native()
        client = Proxy(CLI, f.sock, f.env)
        params = {"cwd": str(f.cwd), "model": "gpt-6-astra", "modelProvider": "fixture",
                  "historyMode": "legacy", "sandbox": "read-only", "approvalPolicy": "on-request"}
        sid = client.call("thread/start", params)["thread"]["id"]
        other = client.call("thread/start", params)["thread"]["id"]
        result["native_id"], result["sibling_native_id"] = sid, other

        def turn(native_id, number):
            receipt = client.call("turn/start", {"threadId": native_id, "input": [{"type": "text", "text": f"NETCAP_A_TURN_{number}"}]})
            def done():
                history = client.call("thread/read", {"threadId": native_id, "includeTurns": True})["thread"]
                current = next((t for t in history["turns"] if t["id"] == receipt["turn"]["id"]), None)
                return history if current and current["status"] == "completed" else None
            return wait_for(done, "completed native turn", timeout=45)

        def inventory(native_id):
            return client.call("mcpServerStatus/list", {"threadId": native_id})["data"]

        def identity_status(native_id):
            return next(s["runtimeStatus"] for s in inventory(native_id) if s["name"] == "identity")

        before = turn(sid, 0)
        turn(other, 0)
        checks["initial_identity_connected"] = identity_status(sid) == "connected"
        observed["loaded_before"] = inventory(sid)
        disabled = {"threadId": sid, "config": {"mcp_servers.identity.enabled": False}}
        resumed = client.call("thread/resume", disabled)
        observed["loaded_resume_native_id"] = resumed["thread"]["id"]
        observed["loaded_after_resume"] = inventory(sid)
        # Re-read after an actual turn as a queued update could apply at turn start.
        history = turn(sid, 2)
        observed["loaded_after_next_turn"] = inventory(sid)
        checks["loaded_resume_keeps_old_selection"] = identity_status(sid) == "connected"
        checks["loaded_resume_keeps_native_id"] = resumed["thread"]["id"] == sid
        checks["sibling_still_connected"] = identity_status(other) == "connected"
        checks["loaded_old_turn_unchanged"] = history["turns"][0] == before["turns"][0]
        observed["unsubscribe"] = client.call("thread/unsubscribe", {"threadId": sid})
        observed["after_unsubscribe"] = client.call("thread/read", {"threadId": sid})["thread"]["status"]
        checks["unsubscribe_does_not_immediately_unload"] = observed["after_unsubscribe"]["type"] == "idle"
        client.close()
        client = None
        f.restart_native()
        client = Proxy(CLI, f.sock, f.env)
        observed["after_owned_restart"] = client.call("thread/read", {"threadId": sid})["thread"]["status"]
        checks["restart_exposes_unloaded_thread"] = observed["after_owned_restart"]["type"] == "notLoaded"
        observed["unloaded_control_queries"] = {}
        for method in ("thread/queue/list", "thread/goal/get", "thread/backgroundTerminals/list"):
            try:
                observed["unloaded_control_queries"][method] = client.call(method, {"threadId": sid})
            except RuntimeError as error:
                observed["unloaded_control_queries"][method] = {"error": str(error)}
        checks["unloaded_control_queries_do_not_load"] = client.call("thread/read", {"threadId": sid})["thread"]["status"]["type"] == "notLoaded"
        resumed = client.call("thread/resume", disabled)
        history_after = turn(sid, 3)
        observed["unloaded_resume_selection"] = inventory(sid)
        checks["unloaded_resume_disables_identity"] = identity_status(sid) == "disabled"
        checks["unloaded_resume_keeps_id_and_history"] = resumed["thread"]["id"] == sid and history_after["turns"][:2] == history["turns"]
        checks["unselected_mcp_still_inherited"] = any(s["name"] == "inherited" and s["runtimeStatus"] == "connected" for s in inventory(sid))
        client.call("thread/resume", {"threadId": other})
        checks["sibling_selection_unchanged_after_restart"] = identity_status(other) == "connected"
        checks["profile_file_unchanged"] = (f.profile / "config.toml").read_bytes() == original
        checks["no_mcp_tool_calls"] = not (f.root / "identity-calls.jsonl").exists()
        checks["local_provider_only"] = bool(f.provider.requests) and all(r["auth_matches"] for r in f.provider.requests) and not f.provider.errors
        result["expectations_met"] = all(checks.values())
        (f.root / "history-before.json").write_text(json.dumps(history, indent=2))
        (f.root / "history-after.json").write_text(json.dumps(history_after, indent=2))
    except Exception as error:
        result["error"] = str(error)
        result["expectations_met"] = False
    finally:
        if client:
            (f.root / "events.json").write_text(json.dumps(client.events, indent=2))
            client.close()
        f.close()
        (f.root / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2))
        print(json.dumps(result, ensure_ascii=False), flush=True)
    return int(not result["expectations_met"])


if __name__ == "__main__":
    raise SystemExit(run())
