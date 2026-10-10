#!/usr/bin/env python3
"""Invoke one shared MCP definition with separate native Claude bindings.

Real CLI and MCP processes, local synthetic model providers, no user profiles.
This proves invocation and exclusion for this asset, not arbitrary plugins.
"""
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import sys
import tempfile

from execution_config_probe import Provider, claude_turn, environment, settings, verify_versions, write


class ToolProvider(Provider):
    def anthropic(self, payload, answer, seq):
        name = next((t["name"] for t in payload.get("tools", []) if t.get("name", "").endswith("__whoami")), None)
        has_result = any(p.get("type") == "tool_result" for m in payload.get("messages", []) if isinstance(m.get("content"), list) for p in m["content"])
        if not name or has_result:
            return super().anthropic(payload, answer, seq)
        message = {"id": f"msg_{seq}", "type": "message", "role": "assistant", "model": payload["model"],
                   "content": [], "stop_reason": None, "stop_sequence": None, "usage": {"input_tokens": 32, "output_tokens": 0}}
        return self.sse([("message_start", {"message": message}),
                         ("content_block_start", {"index": 0, "content_block": {"type": "tool_use", "id": f"tool_{seq}", "name": name, "input": {}}}),
                         ("content_block_delta", {"index": 0, "delta": {"type": "input_json_delta", "partial_json": "{}"}}),
                         ("content_block_stop", {"index": 0}),
                         ("message_delta", {"delta": {"stop_reason": "tool_use", "stop_sequence": None}, "usage": {"output_tokens": 32}}),
                         ("message_stop", {})])


def shared_definition(root):
    """One definition reused by the Claude and Codex invocation probes."""
    server = root / "shared_mcp.py"
    write(server, '''import json,os,sys
from pathlib import Path
profile=os.environ['NETCAP_MCP_PROFILE']
log=Path(os.environ['NETCAP_MCP_LOG'])
for line in sys.stdin:
 request=json.loads(line)
 if 'id' not in request:continue
 method=request['method']
 if method=='initialize':result={'protocolVersion':request['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'shared-identity','version':'1'}}
 elif method=='tools/list':result={'tools':[{'name':'whoami','description':'Return this fixture execution binding identity','inputSchema':{'type':'object','properties':{}}}]}
 elif method=='tools/call':
  identity={'profile':profile,'cwd':os.getcwd(),'credential_matches':os.environ.get('NETCAP_MCP_KEY')=='fixture-mcp-'+profile}
  with log.open('a') as output:output.write(json.dumps(identity)+'\\n')
  result={'content':[{'type':'text','text':json.dumps(identity)}]}
 else:result={}
 print(json.dumps({'jsonrpc':'2.0','id':request['id'],'result':result}),flush=True)
''')
    definition = {"command": sys.executable, "args": [str(server)]}
    write(root / "shared-definition.json", definition)
    return definition


def main():
    root = Path(tempfile.mkdtemp(prefix="netcap-mcp-binding-"))
    print("Evidence directory:", root, flush=True)
    verify_versions(root, ["claude"])
    definition = shared_definition(root)

    def run(route):
        case = root / route
        case.mkdir()
        env = environment(case)
        cwd = case / "work"
        cwd.mkdir()
        log = case / "calls.jsonl"
        bound = {**definition, "env": {"NETCAP_MCP_PROFILE": route, "NETCAP_MCP_KEY": "fixture-mcp-" + route, "NETCAP_MCP_LOG": str(log)}}
        mcp = case / "mcp.json"
        empty = case / "empty-mcp.json"
        write(mcp, {"mcpServers": {"identity": bound}})
        write(empty, {"mcpServers": {}})
        provider = ToolProvider(case, route)
        try:
            config = settings(case, provider)
            sid, events = claude_turn(case, env, cwd, config, f"NETCAP_{route}_TURN_1", "called",
                ["--tools", "", "--strict-mcp-config", "--mcp-config", str(mcp), "--allowedTools", "mcp__identity__whoami"])
            assert log.exists(), "selected MCP tool was not actually called"
            calls = [json.loads(line) for line in log.read_text().splitlines()]
            tool_results = [part for request in provider.requests for message in request["body"].get("messages", [])
                            if isinstance(message.get("content"), list) for part in message["content"] if part.get("type") == "tool_result"]
            returned = []
            for result in tool_results:
                for part in result.get("content", []):
                    if isinstance(part, dict) and part.get("type") == "text":
                        try:
                            returned.append(json.loads(part["text"]))
                        except json.JSONDecodeError:
                            pass
            expected = {"profile": route, "cwd": str(cwd), "credential_matches": True}
            checks = {"tool_called_once": len(calls) == 1,
                      "bound_identity_and_credential": all(c["profile"] == route and c["credential_matches"] for c in calls),
                      "bound_working_directory": all(c["cwd"] == str(cwd) for c in calls),
                      "tool_result_returned_to_model": expected in returned}
            original_log = log.read_bytes()
            resumed, excluded = claude_turn(case, env, cwd, config, f"NETCAP_{route}_TURN_2", "excluded",
                ["--tools", "", "--strict-mcp-config", "--mcp-config", str(empty)], sid)
            init = next(e for e in excluded if e.get("subtype") == "init")
            checks.update(same_native_id=resumed == sid,
                          excluded_from_native_inventory=not any("identity" in t for t in init.get("tools", [])),
                          excluded_from_model_tools=not any("identity" in t.get("name", "") for t in provider.requests[-1]["body"].get("tools", [])),
                          exclusion_did_not_invoke_tool=log.read_bytes() == original_log,
                          model_credentials_bound=all(r["auth_matches"] for r in provider.requests))
            return {"execution": "completed", "expectations_met": all(checks.values()), "checks": checks, "native_id": sid, "actual_calls": calls}
        except Exception as error:
            return {"execution": "failed", "error": str(error)}
        finally:
            provider.close()

    with ThreadPoolExecutor(max_workers=2) as pool:
        results = dict(zip(("A", "B"), pool.map(run, ("A", "B"))))
    result = {"scope": "shared stdio MCP definition; separate native Claude profiles/cwd/model and MCP credentials; selected invocation then excluded same-ID resume", "results": results}
    write(root / "result.json", result)
    print(json.dumps(result, ensure_ascii=False), flush=True)
    return int(any(not r.get("expectations_met") for r in results.values()))


if __name__ == "__main__":
    raise SystemExit(main())
