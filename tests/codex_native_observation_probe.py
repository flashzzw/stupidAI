"""Real stdio read-only observation queries; no production socket or model turn."""
import argparse
import json
from pathlib import Path

from codex_native_resume_probe import Stdio, run
from execution_config_probe import environment


def observe(peer, tid):
    calls = [
        ("thread/read", {"threadId": tid}),
        ("thread/turns/list", {"threadId": tid, "limit": 50, "sortDirection": "desc", "itemsView": "full"}),
        ("thread/queue/list", {"threadId": tid}),
        ("thread/goal/get", {"threadId": tid}),
        ("thread/backgroundTerminals/list", {"threadId": tid, "limit": 100}),
        ("thread/list", {"ancestorThreadId": tid, "limit": 100, "sourceKinds": ["subAgent", "subAgentReview", "subAgentCompact", "subAgentThreadSpawn", "subAgentOther"]}),
    ]
    return {method: peer.call(method, params) for method, params in calls}


def study():
    setup = run("loaded")
    result = {"setup": setup, "calls": [], "events": [], "processes": [], "checks": {}, "scope": "fixed Codex 0.158.0 stdio; synthetic history; read queries used by native observer; no production proxy, tools, or model turns"}
    if not setup["passed"]:
        result["passed"] = False
        return result
    root, tid = Path(setup["root"]), setup["thread_id"]
    env = environment(root)
    env["NETCAP_FIXTURE_KEY"] = "synthetic-only"
    peer = Stdio(env, root / "work", "observation", result)
    try:
        peer.initialize()
        result["unloaded_before"] = peer.rpc("thread/loaded/list", {})
        begin = len(result["calls"])
        result["unloaded_queries"] = observe(peer, tid)
        result["unloaded_methods"] = [c["method"] for c in result["calls"][begin:]]
        result["unloaded_after"] = peer.rpc("thread/loaded/list", {})
        result["checks"]["unloaded_remains_unloaded"] = tid not in result["unloaded_before"]["data"] and tid not in result["unloaded_after"]["data"]
        result["checks"]["unloaded_identity_read"] = result["unloaded_queries"]["thread/read"]["result"]["thread"]["id"] == tid
        result["checks"]["unloaded_native_status"] = result["unloaded_queries"]["thread/read"]["result"]["thread"]["status"]["type"] == "notLoaded"
        # Explicit fixture setup, outside the observation query interval.
        peer.rpc("thread/resume", {"threadId": tid, "excludeTurns": True})
        begin = len(result["calls"])
        result["loaded_queries"] = observe(peer, tid)
        result["loaded_methods"] = [c["method"] for c in result["calls"][begin:]]
        result["checks"]["loaded_queries_succeed"] = all("error" not in v for v in result["loaded_queries"].values())
        result["checks"]["same_native_id"] = result["loaded_queries"]["thread/read"]["result"]["thread"]["id"] == tid
        result["checks"]["loaded_idle_status"] = result["loaded_queries"]["thread/read"]["result"]["thread"]["status"]["type"] == "idle"
        allowed = {"thread/read", "thread/turns/list", "thread/queue/list", "thread/goal/get", "thread/backgroundTerminals/list", "thread/list"}
        result["checks"]["observation_queries_are_read_only"] = all(m in allowed for m in result["unloaded_methods"] + result["loaded_methods"])
    except Exception as error:
        result["error"] = str(error)
    finally:
        peer.close()
    result["checks"]["owned_processes_exited"] = all(p["exited"] for p in result["processes"])
    result["passed"] = "error" not in result and all(result["checks"].values())
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    result = study()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({k: result.get(k) for k in ["checks", "passed", "error"]}, ensure_ascii=False))
    raise SystemExit(0 if result["passed"] else 1)
