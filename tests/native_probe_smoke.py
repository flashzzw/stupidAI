#!/usr/bin/env python3
"""Real Runtime probe policy matrix using only owned local providers/configs."""
import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import signal
import subprocess
import tempfile
import time

from console_fixture import CLI, SERVER_CLI, isolated_env, stop
from route_probe_evidence import DateProvider

REPO_ROOT = Path(__file__).resolve().parent.parent


def alive(pid, birth):
    try:
        fields = Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()
        return fields[0] != "Z" and fields[19] == birth
    except (OSError, IndexError):
        return False


def owned_servers(root):
    for path in (root / "r").glob("**/server.json"):
        record = json.loads(path.read_text())
        pid, birth = record.get("pid", 0), record.get("birth", "")
        if pid > 0 and birth and record.get("session_id") and alive(pid, birth):
            yield pid, birth


def cleanup_owned_servers(root):
    for pid, birth in owned_servers(root):
        if os.getpgid(pid) != pid:
            raise RuntimeError("owned native process group needs inspection: " + str(pid))
        os.killpg(pid, signal.SIGTERM)
        deadline = time.monotonic() + 3
        while alive(pid, birth) and time.monotonic() < deadline:
            time.sleep(.05)
        if alive(pid, birth):
            os.killpg(pid, signal.SIGKILL)


class ProbeProvider(DateProvider):
    def failure_response(self, path, payload):
        scenario = next((part.removeprefix("netcap-fault-") for part in path.split("/") if part.startswith("netcap-fault-")), "")
        self.tool_failure = scenario == "tool_failure"
        status = {"authentication": 401, "quota": 402, "rate_limit": 429, "service": 503}.get(scenario)
        if status is None:
            return None
        kind = {401: "authentication_error", 402: "billing_error", 429: "rate_limit_error", 503: "overloaded_error"}[status]
        return {"type": "error", "error": {"type": kind, "code": kind, "message": "Local fixture " + scenario}}, status, ({"Retry-After": "1"} if status == 429 else {})

    def command(self):
        return "exit 7" if getattr(self, "tool_failure", False) else super().command()

    def final(self, payload):
        if getattr(self, "tool_failure", False):
            if not getattr(self, "tool_issued", False):
                self.tool_issued = True
                return None
            return "TOOL_FAILURE"
        if "Return exactly NETCAP_GENERATION_OK without calling any tools." in json.dumps(payload):
            return "NETCAP_GENERATION_OK"
        return super().final(payload)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runner", required=True)
    parser.add_argument("--agent", action="append", choices=["codex", "claude", "opencode"])
    parser.add_argument("--failures", action="store_true", help="also exercise terminal provider errors and tool assertion failures")
    args = parser.parse_args()
    agents = args.agent or ["codex", "claude", "opencode"]
    assert len(agents) == len(set(agents)), "duplicate agent selection"
    root = Path(tempfile.mkdtemp(prefix="netcap-native-probe-"))
    env = isolated_env(root)
    (root / "work").mkdir()
    marker = "NETCAP_PROBE_WORK_RULE_MUST_NOT_LOAD"
    for name in ("AGENTS.md", "CLAUDE.md"):
        (root / name).write_text(marker + "\n")
    sender = root / "fake_sender.py"
    sender.write_text("from pathlib import Path\nPath(" + repr(str(root / "unexpected-hook")) + ").write_text('work integration ran')\n")
    command = shlex.quote(shutil.which("python3")) + " " + shlex.quote(str(sender))
    providers = {}
    worker = None
    try:
        cases = []
        for kind in agents:
            profile = root / (kind + "-work-profile")
            profile.mkdir()
            (profile / "rules.md").write_text(marker + "\n")
            for label in ("A", "B"):
                provider_root = root / (kind + "-" + label)
                provider_root.mkdir()
                providers[(kind, label)] = ProbeProvider(provider_root, label)
                env["NETCAP_NATIVE_PROBE_" + kind.upper() + "_" + label] = "fixture-" + label
            if kind == "codex":
                (profile / "config.toml").write_text('model = "gpt-6-astra"\nmodel_provider = "fixture"\n[model_providers.fixture]\nname = "work fixture"\nbase_url = ' + json.dumps(providers[(kind, "A")].url + "/v1") + '\nwire_api = "responses"\nenv_key = "NETCAP_NATIVE_PROBE_CODEX_A"\n[mcp_servers.work]\ncommand = "python3"\nargs = [' + json.dumps(str(sender)) + ']\n')
                instance = {"executable": CLI, "server_executable": SERVER_CLI}
                model, provider_name = "gpt-6-astra", "fixture"
            elif kind == "claude":
                (profile / "CLAUDE.md").write_text(marker + "\n")
                (profile / "settings.json").write_text(json.dumps({"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": command}]}]}}))
                instance = {"executable": "/home/zzw/.local/bin/claude", "bridge": str(REPO_ROOT / "bridges/claude/dist/main.js"), "node_executable": shutil.which("node")}
                model, provider_name = "claude-sonnet-4-6", "anthropic"
            else:
                plugin = root / "work-plugin.mjs"
                plugin.write_text('import {writeFileSync} from "node:fs"; export default async () => { writeFileSync(' + json.dumps(str(root / "unexpected-plugin")) + ', "work integration ran"); return {}; };\n')
                (profile / "opencode.json").write_text(json.dumps({"plugin": [plugin.as_uri()], "instructions": [str(profile / "rules.md")],
                    "mcp": {"work": {"type": "local", "command": [shutil.which("python3"), str(sender)], "enabled": True}},
                    "provider": {"fixture": {"npm": "@ai-sdk/openai-compatible", "name": "Work fixture", "options": {"apiKey": "work-only-credential-must-not-copy"},
                        "models": {"fixture-model": {"name": "Fixture", "limit": {"context": 32768, "output": 1024}}}}}}))
                instance = {"executable": "/home/zzw/.opencode/bin/opencode"}
                model, provider_name = "fixture-model", "fixture"
            instance.update(id=kind + "-fixture", kind=kind, profile=str(profile), policy_revision="probe-native-1")
            for label in ("A", "B"):
                route = {"id": kind + "-" + label, "instance_id": instance["id"], "provider": provider_name,
                         "model": model, "endpoint": providers[(kind, label)].url + ("" if kind == "claude" else "/v1"),
                         "credential_ref": "env:NETCAP_NATIVE_PROBE_" + kind.upper() + "_" + label,
                         "credential_revision": "1", "config_revision": "1"}
                cases.append({"label": label, "instance": instance, "route": route})
            if args.failures:
                primary = next(c["route"] for c in cases if c["instance"]["kind"] == kind and c["label"] == "A" and not c.get("scenario"))
                for scenario in ("authentication", "quota", "rate_limit", "service", "tool_failure"):
                    route = dict(primary, id=kind + "-" + scenario,
                                 endpoint=providers[(kind, "A")].url + "/netcap-fault-" + scenario + ("" if kind == "claude" else "/v1"))
                    cases.append({"label": "A", "scenario": scenario, "instance": instance, "route": route})
        manifest = root / "fixture.json"
        manifest.write_text(json.dumps({"root": str(root), "cases": cases}))
        print("Evidence directory:", root, flush=True)
        with (root / "runner.log").open("w") as log:
            worker = subprocess.Popen([str(Path(args.runner).resolve()), "--fixture", str(manifest)], env=env, cwd=REPO_ROOT, stdout=log, stderr=log, start_new_session=True)
            code = worker.wait(timeout=1000)
        assert code == 0, "native policy runner failed; inspect " + str(root / "runner.log")
        assert not (root / "unexpected-hook").exists() and not (root / "unexpected-plugin").exists(), "work integration ran inside probe"
        counts = {}
        for (kind, label), provider in providers.items():
            assert not provider.errors, provider.errors
            requests = [r for r in provider.requests if "count_tokens" not in r["path"] and "netcap-fault-" not in r["path"]]
            assert len(requests) == 3, (kind, label, len(requests))
            assert all(r["auth_matches"] for r in provider.requests), (kind, label, "wrong route credentials")
            expected_model = {"codex": "gpt-6-astra", "claude": "claude-sonnet-4-6", "opencode": "fixture-model"}[kind]
            assert all(r["body"]["model"] == expected_model for r in provider.requests if "count_tokens" not in r["path"]), (kind, label, "wrong model")
            body = json.dumps([r["body"] for r in provider.requests])
            assert marker not in body and "work-only-credential-must-not-copy" not in body, "probe inherited work context"
            for mode in ("utc-date", "text"):
                evidence = json.loads((root / "results" / (kind + "-" + label + "-" + mode + ".json")).read_text())
                assert evidence["receipt"]["native_id"] and evidence["proof"]["complete"]
            counts[kind + "-" + label] = len(requests)
        assert not list(owned_servers(root)), "Runtime.Close left owned native servers alive"
        failures = {}
        if args.failures:
            for case in cases:
                if not case.get("scenario"):
                    continue
                kind, label, scenario = case["instance"]["kind"], case["label"], case["scenario"]
                evidence = json.loads((root / "results" / (kind + "-" + label + "-" + scenario + "-utc-date.json")).read_text())
                assert evidence["proof"]["complete"]
                records = [r for r in providers[(kind, label)].requests if "netcap-fault-" + scenario + "/" in r["path"] and "count_tokens" not in r["path"]]
                assert records, (kind, scenario, "no real model request")
                failures[kind + "-" + scenario] = {"requests": len(records), "result": evidence["result"]}
        result = {"agents": agents, "routes_per_agent": 2, "modes": ["utc-date", "text"], "model_requests": counts,
                  "route_credentials": "matched", "work_hooks_plugins_rules": "excluded", "failures": failures, "production_capabilities_changed": False}
        (root / "result.json").write_text(json.dumps(result, indent=2) + "\n")
        print("PASS", json.dumps(result), flush=True)
    finally:
        stop(worker)
        for provider in providers.values():
            provider.close()
        cleanup_owned_servers(root)


if __name__ == "__main__":
    main()
