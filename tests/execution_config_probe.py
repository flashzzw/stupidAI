#!/usr/bin/env python3
"""Product feasibility probes: real CLIs, owned profiles, synthetic providers.

This is an experiment runner, not a production capability declaration. It never
loads the user's configuration or invokes a real model. See the evidence report
for the distinction between discovery, execution, and strict isolation.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
from types import SimpleNamespace
import uuid

sys.path.insert(0, str(Path(__file__).parent / "runtime"))
from common import Provider, isolated_env, stop, wait_for
import codex_probe
from go_cli_smoke import Proxy

CODEX = "/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex"
SERVER = "/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex"
CLAUDE = "/home/zzw/.local/bin/claude"


def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(value if isinstance(value, str) else json.dumps(value, indent=2))


def environment(root):
    env = isolated_env(root)
    env.update(CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1",
               DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1")
    return env


def verify_versions(root, names):
    expected = {"codex": (CODEX, "codex-cli 0.157.1"), "server": (SERVER, "codex-cli 0.158.0"),
                "claude": (CLAUDE, "2.1.280 (Claude Code)")}
    result = {}
    for name in names:
        binary, version = expected[name]
        actual = subprocess.check_output([binary, "--version"], env=environment(root / "version-check"),
                                         cwd=root, text=True, timeout=15).strip()
        result[name] = {"version": actual, "path": str(Path(binary).resolve())}
        assert actual == version, name + " version changed; revalidate before updating the pin"
    write(root / "versions.json", result)


def settings(root, provider):
    path = root / "provider.settings.json"
    write(path, {"env": {"ANTHROPIC_BASE_URL": provider.url,
                        "ANTHROPIC_API_KEY": "fixture-" + provider.route},
                 "model": "claude-sonnet-4-6"})
    return path


def claude_turn(root, env, cwd, config, marker, tag, extra=(), sid=None):
    argv = [CLAUDE, "-p", marker, "--output-format", "stream-json", "--verbose",
            "--settings", str(config), "--setting-sources", "user,project,local"]
    argv += ["--resume", sid] if sid else ["--session-id", str(uuid.uuid4())]
    argv += list(extra)
    with (root / (tag + ".stderr")).open("w") as err, (root / (tag + ".jsonl")).open("w") as out:
        proc = subprocess.Popen(argv, env=env, cwd=cwd, stdout=out, stderr=err, start_new_session=True)
        try:
            assert proc.wait(timeout=90) == 0, "CLI exit; see " + tag + ".stderr"
        finally:
            stop(proc)
    events = [json.loads(line) for line in (root / (tag + ".jsonl")).read_text().splitlines() if line.strip()]
    result = next(e for e in reversed(events) if e.get("type") == "result")
    assert not result.get("is_error"), result.get("subtype")
    return result["session_id"], events


class BarrierProvider(Provider):
    def __init__(self, root, route, barrier):
        self.barrier = barrier
        self.arrival = None
        self.release = None
        super().__init__(root, route)

    def failure_response(self, path, payload):
        if self.arrival is None:
            self.arrival = time.monotonic()
            self.barrier.wait(timeout=70)
            self.release = time.monotonic()
        return None


def concurrent_profiles(root):
    barrier = threading.Barrier(4)
    args = SimpleNamespace(codex=CODEX, codex_server=SERVER)

    def run(kind, route):
        case = root / (kind + "-" + route)
        case.mkdir(parents=True)
        provider = BarrierProvider(case, route, barrier)
        try:
            if kind == "codex":
                result = codex_probe.probe(case, provider, args)
            else:
                env = environment(case)
                cwd = case / "work"
                cwd.mkdir()
                config = settings(case, provider)
                sid = None
                for turn in (1, 2, 3):
                    native, _ = claude_turn(case, env, cwd, config, f"NETCAP_{route}_TURN_{turn}",
                                           "turn-" + str(turn), ["--tools", ""], sid)
                    assert sid is None or native == sid, "resume changed native identity"
                    sid = native
                result = {"native_id": sid, "route": provider.assert_route("claude-sonnet-4-6"),
                          "settings_file": str(config), "state_dir": env["CLAUDE_CONFIG_DIR"]}
            opposite = "B" if route == "A" else "A"
            assert all(f"NETCAP_{opposite}_TURN_" not in json.dumps(r["body"]) for r in provider.requests)
            result.update(first_request=provider.arrival, first_release=provider.release)
            return kind + "-" + route, result
        finally:
            provider.close()

    with ThreadPoolExecutor(max_workers=4) as pool:
        futures = [pool.submit(run, kind, route) for kind in ("codex", "claude") for route in ("A", "B")]
        result = dict(f.result() for f in futures)
    assert len({r["native_id"] for r in result.values()}) == 4
    assert max(r["first_request"] for r in result.values()) <= min(r["first_release"] for r in result.values())
    return {"four_requests_in_flight_together": True, "profiles": result,
            "scope": "separate HOME/state/config/cwd; local provider; completed-turn resume"}


def codex_live(root):
    from console_fixture import ConsoleFixture
    root.mkdir(parents=True)
    fixture = ConsoleFixture("")
    first = second = None
    try:
        fixture.provider.stream_delay = 2
        fixture.start_native()
        first = Proxy(CODEX, fixture.sock, fixture.env)
        thread = first.call("thread/start", {"cwd": str(fixture.cwd), "model": "gpt-6-astra",
            "modelProvider": "fixture", "historyMode": "legacy", "sandbox": "read-only", "approvalPolicy": "on-request"})["thread"]
        sid = thread["id"]
        original = first.call("turn/start", {"threadId": sid, "input": [{"type": "text", "text": "NETCAP_A_TURN_1"}]})["turn"]
        wait_for(lambda: fixture.provider.requests, "in-flight request")
        second = Proxy(CODEX, fixture.sock, fixture.env)
        live = second.call("thread/read", {"threadId": sid, "includeTurns": True})["thread"]
        observed = any(t["id"] == original["id"] and t["status"] == "inProgress" for t in live["turns"])
        def finished():
            history = second.call("thread/read", {"threadId": sid, "includeTurns": True})["thread"]
            return history if history["turns"][-1]["status"] == "completed" else None
        wait_for(finished, "original execution completes")
        first.close()
        first = None
        second.call("thread/resume", {"threadId": sid})
        second.call("turn/start", {"threadId": sid, "input": [{"type": "text", "text": "NETCAP_A_TURN_2"}]})
        history = wait_for(finished, "second client input")
        write(root / "history.json", history)
        checks = {"observed_same_running_turn": observed, "same_thread": history["id"] == sid,
                  "two_turns": len(history["turns"]) == 2, "two_model_requests": len(fixture.provider.requests) == 2,
                  "authentication_verified": all(r["auth_matches"] for r in fixture.provider.requests)}
        return {"enabled": checks, "fixture": str(fixture.root),
                "scope": "two native proxy clients sharing one prearranged daemon; no independent foreground TUI adoption or ownership arbiter"}
    finally:
        if first:
            first.close()
        if second:
            second.close()
        fixture.close()


def skill(path, name, marker):
    write(path / "SKILL.md", f"---\nname: {name}\ndescription: {marker} for configuration experiments.\n---\nOnly inspect the local fixture.\n")


def mcp(root):
    path = root / "mcp.py"
    write(path, '''import json,sys
from pathlib import Path
with Path(sys.argv[1]).open('a') as log:
 log.write('started\\n');log.flush()
 for line in sys.stdin:
  req=json.loads(line)
  if 'id' not in req: continue
  method=req.get('method')
  if method=='initialize': result={'protocolVersion':req['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'v03-fixture','version':'1'}}
  elif method=='tools/list': result={'tools':[{'name':'v03_ping','description':'V03_MCP_CAPABILITY','inputSchema':{'type':'object','properties':{}}}]}
  elif method=='tools/call': result={'content':[{'type':'text','text':'fixture pong'}]}
  else: result={}
  log.write(method+'\\n');log.flush()
  print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}),flush=True)
''')
    return path


def claude_assets(root):
    root.mkdir(parents=True)
    env = environment(root)
    cwd = root / "work"
    cwd.mkdir()
    skill(cwd / ".claude/skills/v03-project", "v03-project", "V03_PROJECT_CAPABILITY")
    write(cwd / "CLAUDE.md", "V03_PROJECT_RULE: preserve this rule.\n")
    plugin = root / "plugin"
    write(plugin / ".claude-plugin/plugin.json", {"name": "v03-plugin", "version": "1.0.0", "description": "Fixture"})
    skill(plugin / "skills/v03-bundled", "v03-bundled", "V03_PLUGIN_CAPABILITY")
    server = mcp(root)
    servers = {"v03": {"command": sys.executable, "args": [str(server), str(root / "mcp.log")]}}
    config_mcp = root / "mcp.json"
    write(config_mcp, {"mcpServers": servers})
    provider = Provider(root, "A")
    try:
        config = settings(root, provider)
        sid, positive = claude_turn(root, env, cwd, config, "NETCAP_A_TURN_1", "enabled",
            ["--plugin-dir", str(plugin), "--strict-mcp-config", "--mcp-config", str(config_mcp)])
        positive_body = json.dumps(provider.requests[-1]["body"])
        init = next(e for e in positive if e.get("subtype") == "init")
        positive_checks = {"project_skill": "V03_PROJECT_CAPABILITY" in positive_body,
                           "plugin_skill": "V03_PLUGIN_CAPABILITY" in positive_body,
                           "mcp_connected": any(s.get("status") == "connected" for s in init.get("mcp_servers", [])),
                           "project_rule": "V03_PROJECT_RULE" in positive_body}
        before = (root / "mcp.log").read_text() if (root / "mcp.log").exists() else ""
        data = json.loads(config.read_text())
        data["skillOverrides"] = {"v03-project": "off"}
        excluded = root / "excluded.settings.json"
        write(excluded, data)
        empty = root / "empty-mcp.json"
        write(empty, {"mcpServers": {}})
        _, negative = claude_turn(root, env, cwd, excluded, "NETCAP_A_TURN_2", "excluded",
            ["--strict-mcp-config", "--mcp-config", str(empty)])
        negative_body = json.dumps(provider.requests[-1]["body"])
        negative_init = next(e for e in negative if e.get("subtype") == "init")
        negative_checks = {"project_skill_absent": "V03_PROJECT_CAPABILITY" not in negative_body,
                           "plugin_skill_absent": "V03_PLUGIN_CAPABILITY" not in negative_body,
                           "mcp_tool_absent": "mcp__v03__v03_ping" not in negative_init.get("tools", []),
                           "mcp_not_started_again": before == ((root / "mcp.log").read_text() if (root / "mcp.log").exists() else ""),
                           "project_rule_preserved": "V03_PROJECT_RULE" in negative_body}
        restored, resumed = claude_turn(root, env, cwd, excluded, "NETCAP_A_TURN_3", "resumed-excluded",
            ["--strict-mcp-config", "--mcp-config", str(empty)], sid)
        resumed_init = next(e for e in resumed if e.get("subtype") == "init")
        resumed_checks = {"same_native_id": restored == sid,
                          "project_skill_absent": "v03-project" not in resumed_init.get("skills", []),
                          "plugin_absent": not resumed_init.get("plugins"),
                          "mcp_absent": not resumed_init.get("mcp_servers"),
                          "prior_conversation_preserved": "NETCAP_A_TURN_1" in json.dumps(provider.requests[-1]["body"])}
        assert all(r["auth_matches"] for r in provider.requests)
        return {"enabled": positive_checks, "excluded": negative_checks,
                "resumed": resumed_checks, "positive_init": init, "negative_init": negative_init,
                "resumed_init": resumed_init,
                "scope": "new and completed-session resume; local plugin/stdio MCP; visibility and initialization, not model invocation"}
    finally:
        provider.close()


def codex_assets(root):
    root.mkdir(parents=True)
    env = environment(root)
    env["NETCAP_FIXTURE_KEY"] = "fixture-A"
    cwd = root / "work"
    cwd.mkdir()
    shared = Path(env["HOME"]) / ".agents/skills/v03-shared"
    skill(shared, "v03-shared", "V03_SHARED_CAPABILITY")
    write(cwd / "AGENTS.md", "V03_PROJECT_RULE: preserve this rule.\n")
    marketplace = cwd / ".agents/plugins/marketplace.json"
    plugin = cwd / "plugins/v03-plugin"
    write(plugin / ".codex-plugin/plugin.json", {"name": "v03-plugin", "version": "1.0.0", "description": "Fixture"})
    skill(plugin / "skills/v03-bundled", "v03-bundled", "V03_PLUGIN_CAPABILITY")
    write(marketplace, {"name": "v03-local", "plugins": [{"name": "v03-plugin",
        "source": {"source": "local", "path": "./plugins/v03-plugin"},
        "policy": {"installation": "AVAILABLE", "authentication": "ON_INSTALL"}, "category": "Productivity"}]})
    provider = Provider(root, "A")
    server = mcp(root)
    config = Path(env["CODEX_HOME"]) / "config.toml"
    write(config, f'''model = "gpt-6-astra"
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
plugins = true
plugin_sharing = false
shell_snapshot = false
[mcp_servers.v03]
command = {json.dumps(sys.executable)}
args = {json.dumps([str(server), str(root / 'mcp.log')])}
''')
    sock = root / "codex.sock"
    client = native = None
    with (root / "server.log").open("w") as log:
        try:
            native = subprocess.Popen([SERVER, "app-server", "--listen", "unix://" + str(sock)],
                env=env, cwd=cwd, stdout=log, stderr=log, start_new_session=True)
            wait_for(sock.exists, "owned socket")
            client = Proxy(CODEX, sock, env)
            installed = client.call("plugin/install", {"pluginName": "v03-plugin", "marketplacePath": str(marketplace)})
            write(root / "plugin-install.json", installed)
            listing = client.call("skills/list", {"cwds": [str(cwd)], "forceReload": True})
            write(root / "skills.json", listing)

            def turn(tag, overrides):
                thread = client.call("thread/start", {"cwd": str(cwd), "model": "gpt-6-astra", "modelProvider": "fixture",
                    "historyMode": "legacy", "sandbox": "read-only", "approvalPolicy": "on-request", "config": overrides})["thread"]
                result = client.call("turn/start", {"threadId": thread["id"], "input": [{"type": "text", "text": "NETCAP_A_TURN_1"}]})
                def complete():
                    history = client.call("thread/read", {"threadId": thread["id"], "includeTurns": True})["thread"]
                    return history if any(t["id"] == result["turn"]["id"] and t["status"] == "completed" for t in history["turns"]) else None
                write(root / (tag + "-history.json"), wait_for(complete, tag))
                inventory = client.call("mcpServerStatus/list", {"threadId": thread["id"]})
                write(root / (tag + "-mcp.json"), inventory)
                return json.dumps(provider.requests[-1]["body"]), inventory

            positive, positive_mcp = turn("enabled", {})
            before = (root / "mcp.log").read_text() if (root / "mcp.log").exists() else ""
            negative, negative_mcp = turn("excluded", {"skills": {"config": [{"path": str(shared / "SKILL.md"), "enabled": False}]},
                                          "mcp_servers": {"v03": {"enabled": False}},
                                          "plugins": {"v03-plugin@v03-local": {"enabled": False}}})
            after = (root / "mcp.log").read_text() if (root / "mcp.log").exists() else ""
            enabled_again, _ = turn("enabled-again", {})
            assert all(r["auth_matches"] for r in provider.requests)
            return {"enabled": {"shared_skill": "V03_SHARED_CAPABILITY" in positive, "mcp_initialized": "tools/list" in before,
                                "mcp_tool_listed": "v03_ping" in json.dumps(positive_mcp),
                                "project_rule": "V03_PROJECT_RULE" in positive, "plugin_skill": "V03_PLUGIN_CAPABILITY" in positive},
                    "excluded": {"shared_skill_absent": "V03_SHARED_CAPABILITY" not in negative,
                                 "plugin_skill_absent": "V03_PLUGIN_CAPABILITY" not in negative,
                                 "mcp_tool_absent": "v03_ping" not in json.dumps(negative_mcp),
                                 "mcp_not_started_again": before == after, "project_rule_preserved": "V03_PROJECT_RULE" in negative},
                    "reenabled": {"shared_skill": "V03_SHARED_CAPABILITY" in enabled_again,
                                  "plugin_skill": "V03_PLUGIN_CAPABILITY" in enabled_again},
                    "scope": "three threads in one owned daemon; per-thread overrides; locally installed skill-only CLI plugin"}
        finally:
            if client:
                client.close()
            stop(native)
            provider.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", choices=["all", "concurrent", "claude-assets", "codex-assets", "codex-live"], default="all")
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix="netcap-config-probe-"))
    print("Evidence directory:", root, flush=True)
    verify_versions(root, ["claude"] if args.case == "claude-assets" else
                    ["codex", "server"] if args.case.startswith("codex-") else ["codex", "server", "claude"])
    results = {}
    try:
        for name, run in [("concurrent", concurrent_profiles), ("claude-assets", claude_assets), ("codex-assets", codex_assets), ("codex-live", codex_live)]:
            if args.case not in (name, "all"):
                continue
            try:
                observations = run(root / name)
                checks = [v for k in ("enabled", "excluded", "resumed", "reenabled") for v in observations.get(k, {}).values()]
                results[name] = {"execution": "completed", "expectations_met": all(checks), "observations": observations}
            except Exception as error:
                results[name] = {"execution": "failed", "error": str(error)}
            write(root / "results.json", results)
            print(name, json.dumps(results[name], ensure_ascii=False), flush=True)
    finally:
        write(root / "results.json", results)
    return int(any(r["execution"] != "completed" or not r.get("expectations_met", False) for r in results.values()))


if __name__ == "__main__":
    raise SystemExit(main())
