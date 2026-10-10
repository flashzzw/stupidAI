#!/usr/bin/env python3
"""Exercise read-only hook/plugin/OTEL ingress through the production console."""
import argparse
import json
import os
from pathlib import Path
import secrets
import signal
import time
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from console_fixture import ConsoleFixture


class ObservationFixture(ConsoleFixture):
    def __init__(self, binary):
        super().__init__(binary)
        self.tokens = {}
        for kind in ("claude", "opencode"):
            self.config["instances"].append({
                "id": kind + "-local", "kind": kind, "executable": str(self.root / ("missing-" + kind)),
                "profile": str(self.root / kind), "policy_revision": "observer-only",
                **({"bridge": str(self.root / "unused-bridge.js")} if kind == "claude" else {}),
            })
        future_cli = self.root / "future-codex"
        future_cli.write_text("#!/bin/sh\n[ \"$1\" = \"--version\" ] || exit 99\nprintf 'codex-cli 99.0.0\\n'\n")
        future_cli.chmod(0o700)
        self.config["instances"].append({**self.config["instances"][0], "id": "codex-future", "executable": str(future_cli), "socket": str(self.root / "future.sock")})
        self.config["routes"].append({**self.config["routes"][0], "id": "route-future", "instance_id": "codex-future"})
        self.config["observers"] = []
        for source, kind, format_ in (("codex-hooks", "codex", "hook"), ("claude-hooks", "claude", "hook"),
                                     ("opencode-plugin", "opencode", "opencode"), ("codex-otel", "codex", "otel")):
            key = "OBSERVER_" + source.upper().replace("-", "_")
            self.tokens[source] = self.env[key] = secrets.token_hex(32)
            self.config["observers"].append({"id": source, "instance_id": kind + "-local", "project_id": "fixture",
                                              "format": format_, "token_ref": "env:" + key})

    def start_native(self):
        pass

    def request(self, path, body, token, status):
        request = Request(self.info["url"] + path, data=json.dumps(body).encode() if body is not None else None,
                          headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
        try:
            response = urlopen(request, timeout=10)
        except HTTPError as error:
            response = error
        with response:
            assert response.status == status, (path, response.status, status, response.read().decode())
            return json.load(response)

    def deliver(self, source, event_id, payload, second=1):
        return self.request("/api/observations/" + source, {
            "event_id": event_id, "occurred_at": f"2026-09-29T00:00:{second:02d}Z", "payload": payload,
        }, self.tokens[source], 202)


def otel_payload(**values):
    return {"resourceLogs": [{"scopeLogs": [{"logRecords": [{"attributes": [
        {"key": key, "value": {"stringValue": value}} for key, value in values.items()
    ]}]}]}]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    parser.add_argument("--hold", action="store_true")
    parser.add_argument("--info-file")
    args = parser.parse_args()
    with ObservationFixture(args.netcap) as fixture:
        print("Evidence directory:", fixture.root, flush=True)
        sessions = {}
        statuses = {i["id"]: i for i in fixture.api("/api/workspace")["instances"]}
        assert statuses["codex-future"]["description"]["compatibility"] == "unverified"
        assert statuses["claude-local"]["description"]["compatibility"] == "unavailable"
        fixture.request("/api/commands", {"command_id": "unverified-create", "kind": "create", "project_id": "fixture", "route_id": "route-future", "permission": "read_only"}, fixture.token, 409)
        for kind in ("codex", "claude"):
            receipt = fixture.deliver(kind + "-hooks", "start", {"hook_event_name": "SessionStart", "session_id": "same-native-id"})
            sessions[kind] = receipt["session_ids"][0]
        receipt = fixture.deliver("opencode-plugin", "start", {"type": "session.created", "properties": {"info": {"id": "same-native-id"}}})
        sessions["opencode"] = receipt["session_ids"][0]
        assert len(set(sessions.values())) == 3
        approval = {"hook_event_name": "PermissionRequest", "session_id": "same-native-id", "request_id": "request-one",
                    "connection_id": "connection-one", "message": "请在原终端确认命令", "control_mode": "managed", "operation_id": "invented"}
        first = fixture.deliver("codex-hooks", "approval", approval, 2)
        assert fixture.deliver("codex-hooks", "approval", approval, 2) == first
        fixture.request("/api/observations/codex-hooks", {"event_id": "approval", "occurred_at": "2026-09-29T00:00:02Z", "payload": {**approval, "message": "changed"}}, fixture.tokens["codex-hooks"], 400)
        fixture.deliver("claude-hooks", "question", {"hook_event_name": "Notification", "notification_type": "elicitation_dialog",
                                                   "session_id": "same-native-id", "message": "请在原终端回答问题"}, 2)
        fixture.deliver("opencode-plugin", "question", {"type": "question.asked", "properties": {
            "sessionID": "same-native-id", "id": "request-one", "questions": [{"question": "选择继续方式"}]}}, 2)
        fixture.deliver("codex-otel", "failure", otel_payload(**{
            "event.name": "codex.api_request", "conversation.id": "same-native-id", "endpoint": "/v1/responses",
            "error.message": "隔离供应商返回错误", "turn.id": "otel-turn",
        }), 3)
        ignored = fixture.deliver("codex-otel", "discovery", otel_payload(**{
            "event.name": "codex.api_request", "conversation.id": "discovery-only", "endpoint": "/v1/models", "error.message": "ignored",
        }), 4)
        assert not ignored["event_ids"] and not ignored["session_ids"]
        fixture.deliver("codex-hooks", "activity", {"hook_event_name": "UserPromptSubmit", "session_id": "same-native-id"}, 5)
        fixture.deliver("codex-hooks", "late-approval", {**approval, "request_id": "late"}, 4)
        codex = fixture.session(sessions["codex"])
        assert codex["status"] == "running" and all(p["status"] == "expired" for p in codex["view"]["pending"])
        assert all(not event["active"] for event in fixture.api("/api/domain-events?session_id=" + sessions["codex"]) if event["kind"] == "permission_required")
        for kind, sid in sessions.items():
            session = fixture.session(sid)
            assert session["control_mode"] == "observed" and session["connection"] == "observed" and session["paused"]
            assert not session["scope"]["route"]["id"] and not session["view"]["execution_clear"]
            for command in ("send", "respond", "interrupt", "pause", "resume", "takeover", "reconcile", "associate"):
                fixture.request("/api/commands", {"command_id": kind + "-" + command, "kind": command, "session_id": sid, "text": "must not execute"}, fixture.token, 409)
        payload = {"event_id": "unauthorized", "occurred_at": "2026-09-29T00:00:09Z", "payload": approval}
        fixture.request("/api/observations/codex-hooks", payload, fixture.token, 401)
        fixture.request("/api/observations/codex-hooks", payload, fixture.tokens["claude-hooks"], 401)
        fixture.request("/api/workspace", None, fixture.tokens["codex-hooks"], 401)
        fixture.request("/api/commands", {"command_id": "escalation", "kind": "send", "session_id": sessions["codex"], "text": "blocked"}, fixture.tokens["codex-hooks"], 401)
        fixture.request("/api/observations/codex-hooks", {**payload, "control_mode": "managed"}, fixture.tokens["codex-hooks"], 400)
        fixture.request("/api/observations/codex-hooks", {**payload, "payload": {**approval, "cwd": "/"}}, fixture.tokens["codex-hooks"], 400)
        fixture.command("archive", "archive", session_id=sessions["claude"])
        fixture.restart_console()
        assert fixture.session(sessions["claude"])["archived"]
        assert fixture.session(sessions["claude"])["view"]["pending"][0]["status"] == "pending", "console restart invented an observed connection change"
        assert fixture.deliver("codex-hooks", "approval", approval, 2) == first
        fixture.command("unarchive", "unarchive", session_id=sessions["claude"])
        time.sleep(.7)
        assert not fixture.provider.requests and fixture.native is None and not fixture.sock.exists()
        assert not list(fixture.root.glob("**/server.json")) and not list(fixture.root.glob("**/bridge.pid"))
        result = {"agents": list(sessions), "formats": ["hook", "opencode", "otel"], "identity_isolation": True,
                  "command_denials": 24, "provider_calls": 0, "native_servers": 0, "late_pending": "expired",
                  "delivery_dedup": "persisted", "archive": "persisted", "authorization": "source scoped"}
        (fixture.root / "result.json").write_text(json.dumps(result, indent=2))
        print("PASS", json.dumps(result), flush=True)
        if args.info_file:
            Path(args.info_file).write_text(json.dumps({**fixture.info, "root": str(fixture.root), "harness_pid": os.getpid(), "console_pid": fixture.console.pid, "sessions": sessions}, indent=2))
        if args.hold:
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
