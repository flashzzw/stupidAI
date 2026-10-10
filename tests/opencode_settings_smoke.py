#!/usr/bin/env python3
"""Same-directory OpenCode sessions, route credentials, rules and MCP scope."""
import argparse
import copy
import json
from pathlib import Path
from urllib.error import HTTPError

from opencode_console_smoke import OpenCodeConsoleFixture
from opencode_interaction_smoke import OpenCodeInteractionProvider
from console_fixture import Provider, wait_for


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    fixture = OpenCodeConsoleFixture(args.netcap, OpenCodeInteractionProvider)
    other = Provider(fixture.root, "B")
    fixture.env["NETCAP_FIXTURE_KEY_B"] = "fixture-B"
    (fixture.cwd / "AGENTS.md").write_text("Project fixture rule: NETCAP_PROJECT_RULE.\n")
    hook = fixture.root / "hook.mjs"
    hook.write_text('import { appendFileSync } from "node:fs";\nexport default async () => ({ event: async ({event}) => {\n'
        + 'if (event.type.startsWith("session.created")) appendFileSync(' + json.dumps(str(fixture.root / "hooks.jsonl"))
        + ', JSON.stringify({event: event.type, session: event.properties.info.id}) + "\\n");\n} });\n')
    mcp = fixture.root / "mcp.py"
    mcp.write_text('''import json,sys
for line in sys.stdin:
    m=json.loads(line)
    if "id" not in m: continue
    result={}
    if m["method"]=="initialize": result={"protocolVersion":m["params"]["protocolVersion"],"capabilities":{"tools":{}},"serverInfo":{"name":"fixture-"+sys.argv[1],"version":"1.0"}}
    if m["method"]=="tools/list": result={"tools":[{"name":"echo_"+sys.argv[1],"description":"Local fixture marker "+sys.argv[1],"inputSchema":{"type":"object","properties":{}}}]}
    if m["method"]=="tools/call": result={"content":[{"type":"text","text":"fixture "+sys.argv[1]}]}
    print(json.dumps({"jsonrpc":"2.0","id":m["id"],"result":result}),flush=True)
''')
    profile_a = Path(fixture.config["instances"][0]["profile"])
    config_a = json.loads((profile_a / "opencode.json").read_text())
    config_a["permission"] = {"*": "ask", "edit": "deny"}
    for letter in ("A", "B"):
        profile = profile_a if letter == "A" else fixture.root / "opencode-b"
        profile.mkdir(exist_ok=True)
        rule = profile / "rules.md"
        rule.write_text("Profile fixture rule: NETCAP_PROFILE_" + letter)
        cfg = copy.deepcopy(config_a)
        cfg["instructions"] = [str(rule)]
        cfg["plugin"] = [hook.as_uri()]
        cfg["mcp"] = {"local": {"type": "local", "command": ["/usr/bin/python3", str(mcp), letter], "enabled": True}}
        (profile / "opencode.json").write_text(json.dumps(cfg))
    instance_b = dict(fixture.config["instances"][0], id="opencode-b", profile=str(fixture.root / "opencode-b"))
    fixture.config["instances"].append(instance_b)
    fixture.config["routes"].append(dict(fixture.config["routes"][0], id="route-b", instance_id="opencode-b", endpoint=other.url + "/v1", credential_ref="env:NETCAP_FIXTURE_KEY_B"))
    probe_profile = fixture.root / "probe-profile"
    probe_profile.mkdir()
    probe_config = copy.deepcopy(config_a)
    probe_config["permission"] = "deny"
    (probe_profile / "opencode.json").write_text(json.dumps(probe_config))
    fixture.config["instances"].append(dict(instance_b, id="opencode-probe", profile=str(probe_profile)))
    fixture.config["routes"].append(dict(fixture.config["routes"][0], id="route-probe", instance_id="opencode-probe"))
    probe_cwd = fixture.root / "probe-work"
    probe_cwd.mkdir()
    fixture.config["projects"].append({"id": "probe", "name": "Isolated probe", "cwd": str(probe_cwd)})
    try:
        with fixture:
            print("Evidence directory:", fixture.root, flush=True)
            first = fixture.create("same-directory-first")
            second = fixture.create("same-directory-second")
            record_a = json.loads(fixture.server_record(sid=first).read_text())
            record_a2 = json.loads(fixture.server_record(sid=second).read_text())
            assert record_a["pid"] != record_a2["pid"]
            assert fixture.native_call("/mcp", sid=first)["local"]["status"] == "connected"
            for sid in (first, second):
                fixture.command(sid + "-send", "send", session_id=sid, text="NETCAP_OPENCODE_APPROVE")
                pending = wait_for(lambda: next((p for p in fixture.session(sid)["view"]["pending"] if p["status"] == "pending"), None), "session-specific permission even after another session always reply")
                fixture.command(sid + "-allow", "respond", session_id=sid, pending_id=pending["id"], option_id="always")
                wait_for(lambda: fixture.session(sid)["status"] == "idle", "session permission result")
            print("PASS same-directory sessions use isolated native data/server; remembered approvals do not cross sessions", flush=True)

            created = fixture.command("create-b", "create", project_id="fixture", route_id="route-b", title="separate MCP B", permission="read_only")
            third = created["session_id"]
            wait_for(lambda: fixture.session(third)["status"] == "idle", "separate B server session")
            assert fixture.native_call("/mcp", instance="opencode-b")["local"]["status"] == "connected"
            record_b = json.loads(fixture.server_record("opencode-b").read_text())
            assert record_b["pid"] != record_a["pid"]
            fixture.command("b-send", "send", session_id=third, text="NETCAP_B_TURN_1")
            wait_for(lambda: fixture.session(third)["status"] == "idle", "B request complete")
            for provider, letter in ((fixture.provider, "A"), (other, "B")):
                assert provider.requests
                for request in provider.requests:
                    body = request["body"]
                    assert request["auth_matches"] and body["model"] == "fixture-model"
                    encoded = json.dumps(body)
                    assert "NETCAP_PROJECT_RULE" in encoded and "NETCAP_PROFILE_" + letter in encoded
                    assert "NETCAP_PROFILE_" + ("B" if letter == "A" else "A") not in encoded
                    names = {tool["function"]["name"] for tool in body["tools"]}
                    assert "local_echo_" + letter in names and "local_echo_" + ("B" if letter == "A" else "A") not in names, names
            print("PASS same cwd separate instances preserve distinct credentials, profile rules and live MCP tools", flush=True)

            hooks = [json.loads(line) for line in (fixture.root / "hooks.jsonl").read_text().splitlines()]
            assert len(hooks) == 3 and {h["session"] for h in hooks} == {fixture.session(sid)["scope"]["native_id"] for sid in (first, second, third)}, hooks
            print("PASS ordinary sessions retain explicitly configured local plugin, one test hook sender per creation", flush=True)
            probe = fixture.command("create-probe", "create", project_id="probe", route_id="route-probe", title="isolated probe", permission="read_only")["session_id"]
            wait_for(lambda: fixture.session(probe)["status"] == "idle", "isolated probe create")
            fixture.command("probe-send", "send", session_id=probe, text="NETCAP_A_TURN_1")
            wait_for(lambda: fixture.session(probe)["status"] == "idle" and len(fixture.session(probe)["view"]["messages"]) == 2, "isolated probe native response")
            request = fixture.provider.requests[-1]
            assert request["auth_matches"] and request["body"]["model"] == "fixture-model"
            encoded = json.dumps(request["body"])
            assert "NETCAP_PROJECT_RULE" not in encoded and "NETCAP_PROFILE_" not in encoded and "local_echo_" not in encoded
            assert len((fixture.root / "hooks.jsonl").read_text().splitlines()) == 3
            print("PASS separate minimal profile/cwd preserves probe route while excluding work rules/MCP/hooks", flush=True)

            # Dynamic MCP registration is directory/server-scoped in native API.
            # The console never exposes it as a per-session setting.
            fixture.native_call("/mcp", {"name": "injected", "config": {"type": "local", "command": ["/usr/bin/python3", str(mcp), "EXTERNAL"], "enabled": True}}, sid=first)
            registry = fixture.native_call("/mcp", sid=first)
            assert "injected" in registry
            assert "injected" not in fixture.native_call("/mcp", sid=second)
            assert "injected" not in fixture.native_call("/mcp", instance="opencode-b")
            wait_for(lambda: fixture.session(first)["connection"] == "disconnected", "effective configuration drift blocks affected session")
            assert fixture.session(second)["connection"] == "ready" and fixture.session(third)["connection"] == "ready"
            print("PASS runtime MCP mutation invalidates affected configuration evidence; isolated peers remain ready", flush=True)
            (fixture.root / "result.json").write_text(json.dumps({"same_cwd_sessions": 3, "owned_servers": [record_a["pid"], record_a2["pid"], record_b["pid"]], "rules": True, "credentials": True, "mcp_isolation": True}, indent=2))
    finally:
        other.close()


if __name__ == "__main__":
    main()
