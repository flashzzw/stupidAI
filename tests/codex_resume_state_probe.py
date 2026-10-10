"""Loaded resume side effects with a paused native Goal.

The optional queue negative control can start a native turn against the
unavailable local fixture endpoint; it must not be used with real credentials.
"""
import argparse
import json
from pathlib import Path
import subprocess
import tempfile

from codex_native_resume_probe import Stdio, FIELDS
from execution_config_probe import environment, SERVER


def snapshot(peer, tid):
    return {
        "thread": peer.rpc("thread/read", {"threadId": tid, "includeTurns": False})["thread"],
        "goal": peer.rpc("thread/goal/get", {"threadId": tid}),
        "queue": peer.rpc("thread/queue/list", {"threadId": tid}),
        "turns": peer.rpc("thread/turns/list", {"threadId": tid, "itemsView": "full", "sortDirection": "asc", "limit": 50}),
        "terminals": peer.rpc("thread/backgroundTerminals/list", {"threadId": tid, "limit": 100}),
    }


def run(with_queue=False):
    root = Path(tempfile.mkdtemp(prefix="netcap-resume-state-"))
    env = environment(root)
    env["NETCAP_FIXTURE_KEY"] = "synthetic-only"
    cwd = root / "work"
    cwd.mkdir()
    (Path(env["CODEX_HOME"]) / "config.toml").write_text('''model="gpt-6-astra"
model_provider="fixture"
approval_policy="on-request"
sandbox_mode="read-only"
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
    result = {"root": str(root), "with_queue": with_queue, "calls": [], "events": [], "processes": [], "checks": {}, "after": [], "scope": "fixed isolated Codex stdio; synthetic history and paused Goal; optional queue negative control; no production socket/shared daemon"}
    peer = None
    try:
        result["version"] = subprocess.check_output([SERVER, "--version"], env=env, text=True).strip()
        if result["version"] != "codex-cli 0.158.0":
            raise RuntimeError("unverified native version")
        peer = Stdio(env, cwd, "state-probe", result)
        peer.initialize()
        started = peer.rpc("thread/start", {"cwd": str(cwd), "model": "gpt-6-astra", "modelProvider": "fixture", "sandbox": "read-only", "approvalPolicy": "on-request", "historyMode": "legacy"})
        tid = started["thread"]["id"]
        result["thread_id"] = tid
        result["started_policy"] = {k: started.get(k) for k in FIELDS}
        peer.rpc("thread/inject_items", {"threadId": tid, "items": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "SYNTHETIC HISTORY; no execution"}]}]})
        result["goal_setup"] = peer.rpc("thread/goal/set", {"threadId": tid, "objective": "SYNTHETIC PAUSED GOAL; never execute", "status": "paused"})
        if with_queue:
            result["queue_setup"] = peer.rpc("thread/queue/add", {"threadId": tid, "clientUserMessageId": "synthetic-queue-entry", "input": [{"type": "text", "text": "SYNTHETIC QUEUED INPUT"}]})
        before = result["before"] = snapshot(peer, tid)
        result["checks"]["paused_goal_ready"] = before["goal"]["goal"]["status"] == "paused"
        result["checks"]["queue_ready_without_turn" if with_queue else "no_input_started"] = len(before["queue"]["data"]) == (1 if with_queue else 0) and not before["turns"]["data"] and before["thread"]["status"]["type"] == "idle"
        if not all(result["checks"].values()):
            raise RuntimeError("fixture became runnable before the resume experiment")
        for index in range(3):
            resumed = peer.rpc("thread/resume", {"threadId": tid, "excludeTurns": True})
            after = snapshot(peer, tid)
            result["after"].append({"resume": {k: resumed.get(k) for k in FIELDS}, "snapshot": after})
            prefix = f"resume_{index + 1}_"
            goal_before, goal_after = before["goal"]["goal"], after["goal"].get("goal")
            result["checks"][prefix + "goal_preserved"] = goal_after == goal_before
            result["checks"][prefix + "queue_preserved"] = after["queue"] == before["queue"]
            result["checks"][prefix + "no_turn_started"] = after["turns"] == before["turns"] and after["thread"]["status"]["type"] == "idle"
            result["checks"][prefix + "same_id"] = resumed["thread"]["id"] == tid and after["thread"]["id"] == tid
            result["checks"][prefix + "policy_preserved"] = {k: resumed.get(k) for k in FIELDS} == result["started_policy"]
            result["checks"][prefix + "terminals_unchanged"] = after["terminals"] == before["terminals"]
            if not all(result["checks"].values()):
                break
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
    parser.add_argument("--with-queue", action="store_true")
    args = parser.parse_args()
    result = run(args.with_queue)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({k: result.get(k) for k in ["checks", "passed", "error"]}, ensure_ascii=False))
    raise SystemExit(0 if result["passed"] else 1)
