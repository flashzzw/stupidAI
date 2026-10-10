#!/usr/bin/env python3
"""One production console with three real CLIs and isolated local providers."""
import argparse
import json
import os
from pathlib import Path
import shutil
import signal
import time
from urllib.error import HTTPError

from console_fixture import ConsoleFixture, wait_for
from codex_interaction_smoke import InteractionProvider
from claude_interaction_smoke import ClaudeInteractionProvider
from opencode_interaction_smoke import OpenCodeInteractionProvider


class AlphaFixture(ConsoleFixture):
    def __init__(self, binary, provider_types=None):
        provider_types = provider_types or {"codex": InteractionProvider, "claude": ClaudeInteractionProvider, "opencode": OpenCodeInteractionProvider}
        super().__init__(binary, provider_types["codex"])
        self.providers = {"codex": self.provider}
        self.workspaces = {"codex": self.cwd}
        self.config["instances"][0]["notification_sender"] = "console"
        for kind, label in (("claude", "B"), ("opencode", "C")):
            root = self.root / (kind + "-fixture")
            root.mkdir()
            cwd = root / "work"
            cwd.mkdir()
            self.workspaces[kind] = cwd
            provider = provider_types[kind](root, label)
            self.providers[kind] = provider
            key = "NETCAP_" + kind.upper() + "_KEY"
            self.env[key] = "fixture-" + label
            instance = {"id": kind + "-local", "kind": kind, "policy_revision": "m0-1", "notification_sender": "console"}
            if kind == "claude":
                self.env.update(CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1", DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1")
                instance.update(executable="/home/zzw/.local/bin/claude", profile=self.env["CLAUDE_CONFIG_DIR"],
                    bridge=str(Path("bridges/claude/dist/main.js").resolve()), node_executable=shutil.which("node"), version="2.1.280/0.3.280",
                    env_allow=["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"])
            else:
                profile = root / "profile"
                profile.mkdir()
                (profile / "opencode.json").write_text(json.dumps({"permission": "ask", "plugin": [], "provider": {"fixture": {
                    "npm": "@ai-sdk/openai-compatible", "name": "Local fixture", "options": {}, "models": {
                        "fixture-model": {"name": "Fixture model", "limit": {"context": 32768, "output": 1024}}}}}}))
                instance.update(executable="/home/zzw/.opencode/bin/opencode", profile=str(profile), version="1.18.32")
            self.config["instances"].append(instance)
            self.config["projects"].append({"id": kind, "name": kind + " 隔离项目", "cwd": str(cwd)})
            self.config["routes"].append({"id": "route-" + label.lower(), "instance_id": instance["id"],
                "provider": "anthropic" if kind == "claude" else "fixture", "endpoint": provider.url + ("" if kind == "claude" else "/v1"),
                "model": "claude-sonnet-4-6" if kind == "claude" else "fixture-model", "credential_ref": "env:" + key,
                "credential_revision": "1", "config_revision": "1"})

    def close(self):
        super().close()
        for kind, provider in self.providers.items():
            if kind != "codex":
                provider.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    parser.add_argument("--hold", action="store_true")
    parser.add_argument("--info-file")
    args = parser.parse_args()
    with AlphaFixture(args.netcap) as fixture:
        print("Evidence directory:", fixture.root, flush=True)
        instances = fixture.api("/api/workspace")["instances"]
        assert len(instances) == 3 and all(i["description"]["compatibility"] == "supported" and not i.get("config_error") for i in instances), instances
        sessions = {}
        for kind, route, project in (("codex", "route-a", "fixture"), ("claude", "route-b", "claude"), ("opencode", "route-c", "opencode")):
            sid = fixture.command(kind + "-create", "create", project_id=project, route_id=route, title="Alpha " + kind, permission="read_only")["session_id"]
            sessions[kind] = sid
            wait_for(lambda: fixture.session(sid)["status"] == "idle", kind + " creation", timeout=125)
            fixture.providers[kind].stream_delay = 8
        for kind, sid in sessions.items():
            fixture.command(kind + "-stream", "send", session_id=sid, text="NETCAP_" + ("B" if kind == "claude" else "A") + "_TURN_1")
        def simultaneous_streams():
            current = [fixture.session(sid) for sid in sessions.values()]
            return all(s["status"] == "running" and any(m["role"] == "assistant" and m["status"] == "inProgress" for m in s["view"]["messages"]) for s in current)
        wait_for(simultaneous_streams, "three real agents stream concurrently")
        for kind, sid in sessions.items():
            wait_for(lambda: fixture.session(sid)["status"] == "idle", kind + " stream completion")
            fixture.providers[kind].stream_delay = 0
            assert len(fixture.providers[kind].requests) == 1
            assert fixture.providers[kind].requests[0]["auth_matches"]
            assert fixture.session(sid)["scope"]["instance"]["kind"] == kind
        for kind, sid in sessions.items():
            marker = {"codex": "NETCAP_APPROVE", "claude": "NETCAP_CLAUDE_APPROVE", "opencode": "NETCAP_OPENCODE_APPROVE"}[kind]
            fixture.command(kind + "-approval", "send", session_id=sid, text=marker)
        actions = {}
        for kind, sid in sessions.items():
            actions[kind] = wait_for(lambda: next((p for p in fixture.session(sid)["view"]["pending"] if p["status"] == "pending"), None), kind + " approval")
        assert len({p["id"] for p in actions.values()}) == 3
        try:
            fixture.command("wrong-session-reply", "respond", session_id=sessions["claude"], pending_id=actions["codex"]["id"], option_id="allow")
        except HTTPError as error:
            assert error.code == 409
        else:
            raise AssertionError("foreign request accepted")
        for kind, sid in sessions.items():
            pending = actions[kind]
            option = next(o["id"] for o in pending["options"] if o["value"] == "accept") if kind == "codex" else "deny" if kind == "claude" else "once"
            fixture.command(kind + "-reply", "respond", session_id=sid, pending_id=pending["id"], option_id=option)
        for kind, sid in sessions.items():
            wait_for(lambda: fixture.session(sid)["status"] == "idle", kind + " approval resolves")
            assert (fixture.workspaces[kind] / "approve.txt").exists() == (kind != "claude")
        ledger = fixture.api("/api/domain-events")
        for kind, sid in sessions.items():
            selected = [event for event in ledger if event.get("session_id") == sid]
            assert any(event["kind"] == "permission_required" for event in selected)
            assert all(event["agent_kind"] == kind and event["instance_id"] == kind + "-local" and event["notification_sender"] == "console" for event in selected)
            operations = {op["id"] for op in fixture.api("/api/sessions/" + sid)["operations"]}
            assert all(event["operation_id"] in operations for event in selected if event.get("operation_id"))
        counts = {kind: len(provider.requests) for kind, provider in fixture.providers.items()}
        native_ids = {kind: fixture.session(sid)["scope"]["native_id"] for kind, sid in sessions.items()}
        fixture.restart_console()
        for kind, sid in sessions.items():
            wait_for(lambda: fixture.session(sid)["connection"] == "ready", kind + " history recovery")
            assert fixture.session(sid)["scope"]["native_id"] == native_ids[kind]
            assert len(fixture.providers[kind].requests) == counts[kind]
            fixture.command(kind + "-takeover", "takeover", session_id=sid)
            assert fixture.session(sid)["control_mode"] == "human_takeover"
            assert fixture.session(sid)["view"].get("terminal_command")
        result = {"agents": list(sessions), "simultaneous_streams": 3, "simultaneous_approvals": 3, "foreign_request": "rejected",
                  "permissions": {"codex": "accept", "claude": "deny", "opencode": "once"}, "operation_event_identity": "isolated",
                  "credentials": "three local endpoints", "restart": "original identities, no replay", "takeover": "all three"}
        (fixture.root / "result.json").write_text(json.dumps(result, indent=2))
        print("PASS", json.dumps(result), flush=True)
        if args.info_file:
            Path(args.info_file).write_text(json.dumps({**fixture.info, "root": str(fixture.root), "harness_pid": os.getpid(), "console_pid": fixture.console.pid, "sessions": sessions}, indent=2))
        if args.hold:
            for provider in fixture.providers.values():
                provider.stream_delay = 1.5
            finished = False
            def finish(*_):
                nonlocal finished
                finished = True
            signal.signal(signal.SIGTERM, finish)
            signal.signal(signal.SIGINT, finish)
            print("READY", fixture.info["url"], flush=True)
            while not finished:
                assert fixture.console.poll() is None
                time.sleep(.2)


if __name__ == "__main__":
    main()
