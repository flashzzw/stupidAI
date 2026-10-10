"""Fixed native permission-mode recovery study; synthetic history, no turns."""
import argparse
import json
from pathlib import Path
import subprocess
import tempfile

from codex_native_resume_probe import Stdio, FIELDS
from execution_config_probe import environment, SERVER


def run_case(mode):
    root = Path(tempfile.mkdtemp(prefix="netcap-permission-resume-"))
    env = environment(root)
    env["NETCAP_FIXTURE_KEY"] = "synthetic-only"
    cwd = root / "work"
    cwd.mkdir()
    extra = root / "extra"
    extra.mkdir()
    sandbox = "workspace-write" if mode == "workspace" else "read-only"
    global_sandbox = "read-only" if mode == "workspace" else "workspace-write"
    (Path(env["CODEX_HOME"]) / "config.toml").write_text(f'''model="gpt-5.4"
model_provider="fixture"
approval_policy="never"
sandbox_mode="{global_sandbox}"
[sandbox_workspace_write]
writable_roots=["{extra}"]
network_access=false
exclude_tmpdir_env_var=true
exclude_slash_tmp=true
[model_providers.fixture]
name="No model calls"
base_url="http://127.0.0.1:9/v1"
env_key="NETCAP_FIXTURE_KEY"
wire_api="responses"
requires_openai_auth=false
supports_websockets=false
request_max_retries=0
stream_max_retries=0
[features]
apps=false
plugins=false
shell_snapshot=false
''')
    result = {"mode": mode, "root": str(root), "calls": [], "events": [], "processes": [], "checks": {}, "scope": "real Codex 0.158.0 stdio, synthetic-only history, no model turn, no production socket"}
    peer = None
    try:
        result["version"] = subprocess.check_output([SERVER, "--version"], env=env, text=True).strip()
        if result["version"] != "codex-cli 0.158.0":
            raise RuntimeError("unverified version")
        peer = Stdio(env, cwd, "initial", result)
        peer.initialize()
        started = peer.rpc("thread/start", {"cwd": str(cwd), "model": "gpt-6-astra", "modelProvider": "fixture", "sandbox": sandbox, "approvalPolicy": "on-request", "historyMode": "legacy"})
        tid = started["thread"]["id"]
        result["thread_id"] = tid
        result["started"] = {k: started.get(k) for k in FIELDS}
        result["fresh_metadata"] = peer.rpc("thread/read", {"threadId": tid, "includeTurns": False})["thread"]
        result["fresh_resume"] = peer.call("thread/resume", {"threadId": tid, "excludeTurns": True})
        result["checks"]["fresh_resume_has_exact_no_rollout_error"] = result["fresh_resume"].get("error") == {"code": -32600, "message": "no rollout found for thread id " + tid}
        fresh = result["fresh_metadata"]
        result["checks"]["fresh_metadata_matches_start_identity"] = fresh["id"] == tid and fresh["cwd"] == str(cwd) and fresh["model"] == "gpt-6-astra" and fresh["modelProvider"] == "fixture" and fresh["status"]["type"] == "idle"
        peer.rpc("thread/inject_items", {"threadId": tid, "items": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "SYNTHETIC ONLY; no execution"}]}]})
        peer.close()
        peer = None
        peer = Stdio(env, cwd, "restarted", result)
        peer.initialize()
        before = peer.rpc("thread/read", {"threadId": tid, "includeTurns": False})["thread"]
        result["checks"]["unloaded_before_recovery"] = before["status"]["type"] == "notLoaded"
        recovered = peer.rpc("thread/resume", {"threadId": tid, "excludeTurns": True, "model": "gpt-6-astra", "modelProvider": "fixture", "sandbox": sandbox, "approvalPolicy": "on-request"})
        result["recovered"] = {k: recovered.get(k) for k in FIELDS}
        result["checks"]["same_native_id"] = recovered["thread"]["id"] == tid
        result["checks"]["original_fields_preserved"] = result["recovered"] == result["started"]
        result["checks"]["approval_remains_on_request"] = recovered["approvalPolicy"] == "on-request"
        result["checks"]["mode_preserved"] = recovered["sandbox"]["type"] == ("workspaceWrite" if mode == "workspace" else "readOnly")
        sampled = peer.rpc("thread/resume", {"threadId": tid, "excludeTurns": True})
        result["loaded_sample"] = {k: sampled.get(k) for k in FIELDS}
        result["checks"]["loaded_sample_does_not_override"] = result["loaded_sample"] == result["recovered"]
    except Exception as error:
        result["error"] = str(error)
    finally:
        if peer is not None:
            peer.close()
    result["checks"]["owned_processes_exited"] = bool(result["processes"]) and all(p["exited"] for p in result["processes"])
    result["passed"] = "error" not in result and all(result["checks"].values())
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    results = [run_case(mode) for mode in ["workspace", "read_only"]]
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(results, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps([{k: r.get(k) for k in ["mode", "checks", "passed", "error"]} for r in results], ensure_ascii=False))
    raise SystemExit(0 if all(r["passed"] for r in results) else 1)
