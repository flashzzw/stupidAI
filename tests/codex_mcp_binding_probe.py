#!/usr/bin/env python3
"""W02: invoke the shared MCP asset in two isolated Codex profiles, then exclude it.

Model responses are local fixtures. Native Codex executes the real stdio MCP.
Exclusion is applied while resuming the same ID after the owned daemon stops.
"""
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import subprocess
import tempfile
import time

from execution_config_probe import CODEX, SERVER, Provider, environment, stop, verify_versions, wait_for, write
from go_cli_smoke import Proxy
from mcp_binding_probe import shared_definition


class MCPProvider(Provider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.issued = set()

    def responses(self, payload, answer, seq):
        sid = payload.get("client_metadata", {}).get("thread_id")
        primary = any(m.get("role") == "user" and isinstance(m.get("content"), list) and any(
            p.get("text") == f"NETCAP_{self.route}_TURN_1" for p in m["content"] if isinstance(p, dict))
            for m in payload.get("input", []))
        if not primary or sid in self.issued:
            return super().responses(payload, answer, seq)
        self.issued.add(sid)
        item = {"id": f"ctc_{seq}", "type": "custom_tool_call", "call_id": f"call_{seq}", "name": "exec",
                "namespace": "functions", "input": "text(await tools.mcp__identity__whoami({}));", "status": "completed"}
        response = {"id": f"resp_{seq}", "object": "response", "created_at": int(time.time()), "status": "completed",
                    "model": payload["model"], "output": [item], "usage": {"input_tokens": 32, "output_tokens": 16, "total_tokens": 48}}
        return self.sse([("response.created", {"response": dict(response, status="in_progress", output=[])}),
                         ("response.output_item.added", {"output_index": 0, "item": dict(item, status="in_progress")}),
                         ("response.output_item.done", {"output_index": 0, "item": item}), ("response.completed", {"response": response})])


def run(root, route, definition):
    root.mkdir()
    env = environment(root)
    env["NETCAP_FIXTURE_KEY"] = "fixture-" + route
    cwd = root / "work"
    cwd.mkdir()
    log = root / "calls.jsonl"
    provider = MCPProvider(root, route)
    native = client = None
    try:
        config = f'''model = "gpt-6-astra"
model_provider = "fixture"
approval_policy = "on-request"
sandbox_mode = "read-only"
[model_providers.fixture]
name = "Fixture"
base_url = "{provider.url}/v1"
env_key = "NETCAP_FIXTURE_KEY"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = false
[features]
apps = false
plugins = false
shell_snapshot = false
[mcp_servers.identity]
command = {json.dumps(definition['command'])}
args = {json.dumps(definition['args'])}
cwd = {json.dumps(str(cwd))}
[mcp_servers.identity.env]
NETCAP_MCP_PROFILE = {json.dumps(route)}
NETCAP_MCP_KEY = {json.dumps('fixture-mcp-' + route)}
NETCAP_MCP_LOG = {json.dumps(str(log))}
'''
        write(Path(env["CODEX_HOME"]) / "config.toml", config)
        sock = root / "codex.sock"

        def start(tag):
            with (root / (tag + ".log")).open("w") as out:
                process = subprocess.Popen([SERVER, "app-server", "--listen", "unix://" + str(sock)],
                    env=env, cwd=cwd, stdout=out, stderr=out, start_new_session=True)
            try:
                wait_for(sock.exists, "owned Codex socket")
                return process, Proxy(CODEX, sock, env)
            except Exception:
                stop(process)
                raise

        native, client = start("native-enabled")
        thread = client.call("thread/start", {"cwd": str(cwd), "model": "gpt-6-astra", "modelProvider": "fixture",
            "historyMode": "legacy", "sandbox": "read-only", "approvalPolicy": "on-request"})["thread"]
        sid = thread["id"]

        def turn(number, tag):
            receipt = client.call("turn/start", {"threadId": sid, "input": [{"type": "text", "text": f"NETCAP_{route}_TURN_{number}"}]})
            approved = set()
            def done():
                history = client.call("thread/read", {"threadId": sid, "includeTurns": True})["thread"]
                write(root / (tag + "-latest.json"), history)
                write(root / (tag + "-events.json"), client.events)
                for event in client.events:
                    if event.get("method") == "mcpServer/elicitation/request" and event.get("id") not in approved:
                        params = event["params"]
                        assert number == 1 and params["threadId"] == sid and params["serverName"] == "identity"
                        assert params.get("_meta", {}).get("codex_approval_kind") == "mcp_tool_call"
                        assert params["requestedSchema"] == {"type": "object", "properties": {}}
                        assert not log.exists(), "MCP tool ran before approval"
                        client.send({"id": event["id"], "result": {"action": "accept", "content": {}}})
                        approved.add(event["id"])
                current = next((t for t in history["turns"] if t["id"] == receipt["turn"]["id"]), None)
                return history if current and current["status"] == "completed" else None
            history = wait_for(done, tag + " native turn", timeout=45)
            write(root / (tag + "-history.json"), history)
            return history

        history = turn(1, "enabled")
        inventory = client.call("mcpServerStatus/list", {"threadId": sid})
        write(root / "enabled-mcp.json", inventory)
        calls = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
        expected = {"profile": route, "cwd": str(cwd), "credential_matches": True}
        outputs = [item.get("output") for request in provider.requests for item in request["body"].get("input", [])
                   if item.get("type") == "custom_tool_call_output"]
        # The exec output contains a text block with the MCP JSON result. Decode
        # both layers and compare the complete identity, not keyword presence.
        returned = []
        for output in outputs:
            blocks = output if isinstance(output, list) else [{"text": output}]
            for block in blocks:
                try:
                    mcp = json.loads(block.get("text", ""))
                    for content in mcp.get("content", []):
                        if content.get("type") == "text":
                            returned.append(json.loads(content["text"]))
                except (json.JSONDecodeError, TypeError, AttributeError):
                    pass
        checks = {"tool_called_once_with_bound_identity": calls == [expected],
                  "exact_tool_result_returned_to_model": expected in returned,
                  "selected_tool_in_native_inventory": "whoami" in json.dumps(inventory)}
        original_log = log.read_bytes() if log.exists() else b""
        client.close()
        client = None
        stop(native)
        native = None
        sock.unlink(missing_ok=True)
        native, client = start("native-excluded")
        resumed = client.call("thread/resume", {"threadId": sid, "config": {"mcp_servers": {"identity": {"enabled": False}}}})["thread"]
        excluded = turn(2, "excluded")
        inventory = client.call("mcpServerStatus/list", {"threadId": sid})
        write(root / "excluded-mcp.json", inventory)
        primary = [r["body"] for r in provider.requests if r["body"].get("client_metadata", {}).get("thread_id") == sid]
        checks.update(same_native_id=resumed["id"] == sid and excluded["id"] == sid,
                      original_history_preserved=len(excluded["turns"]) == 2 and all(f"NETCAP_{route}_TURN_{n}" in json.dumps(excluded) for n in (1, 2)),
                      excluded_from_native_inventory="whoami" not in json.dumps(inventory),
                      excluded_from_model_tools="whoami" not in json.dumps(primary[-1].get("tools", [])),
                      exclusion_did_not_invoke_tool=(log.read_bytes() if log.exists() else b"") == original_log,
                      model_credentials_bound=bool(provider.requests) and all(r["auth_matches"] for r in provider.requests),
                      provider_has_no_errors=not provider.errors)
        return {"execution": "completed", "expectations_met": all(checks.values()), "checks": checks, "native_id": sid, "actual_calls": calls}
    except Exception as error:
        return {"execution": "failed", "error": str(error)}
    finally:
        if client:
            client.close()
        stop(native)
        provider.close()


def main():
    root = Path(tempfile.mkdtemp(prefix="netcap-codex-mcp-"))
    print("Evidence directory:", root, flush=True)
    verify_versions(root, ["codex", "server"])
    definition = shared_definition(root)
    with ThreadPoolExecutor(max_workers=2) as pool:
        futures = {route: pool.submit(run, root / route, route, definition) for route in ("A", "B")}
        results = {route: future.result() for route, future in futures.items()}
    result = {"scope": "same stdio MCP definition as Claude; two isolated native Codex profiles; invocation then excluded same-ID resume after owned daemon restart", "results": results}
    write(root / "result.json", result)
    print(json.dumps(result, ensure_ascii=False), flush=True)
    return int(any(not r.get("expectations_met") for r in results.values()))


if __name__ == "__main__":
    raise SystemExit(main())
