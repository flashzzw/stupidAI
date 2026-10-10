#!/usr/bin/env python3
"""W01 continuation/control checks against real CLIs and owned local fixtures."""
import argparse
import json
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import time
from urllib.error import HTTPError

from workbench_boundary_probe import ClaudeProfile, Terminal, is_primary
from execution_config_probe import CLAUDE, CODEX, verify_versions, wait_for, write
from console_fixture import ConsoleFixture, Provider


def claude_stopped_resume(root):
    profile = ClaudeProfile(root, "A")
    result = {"checks": {}, "scope": "completed independent foreground exits, then full-ID --resume --bg; not live adoption"}
    try:
        terminal = Terminal([CLAUDE, "NETCAP_A_TURN_1"], profile.env, profile.cwd, root / "foreground.ansi")
        profile.terminals.append(terminal)
        handled = set()

        def started():
            rendered = re.sub(r"\s+", "", terminal.text())
            for key, phrase in [("theme", "Choosethetextstyle"), ("notes", "PressEntertocontinue"), ("trust", "Yes,Itrustthisfolder")]:
                if key not in handled and phrase in rendered:
                    handled.add(key)
                    terminal.send("\x1b[B\r" if key == "trust" else "\r")
                    return False
            return any(is_primary(r["body"], "A") for r in profile.provider.requests)

        terminal.until(started, "independent foreground request")
        first = next(r["body"] for r in profile.provider.requests if is_primary(r["body"], "A"))
        sid = json.loads(first["metadata"]["user_id"])["session_id"]
        terminal.until(lambda: "fixtureA:NETCAP_A_TURN_1" in re.sub(r"\s+", "", terminal.text()), "completed first reply")
        terminal.drain(.5)
        terminal.send("/exit\r")
        terminal.until(lambda: terminal.proc.poll() is not None, "original foreground exits")
        output = profile.cli(["--resume", sid, "--bg", "NETCAP_A_TURN_2"], "resume-full-id")
        wait_for(lambda: any(is_primary(r["body"], "A", 2) for r in profile.provider.requests), "resumed main request", timeout=35)
        second = next(r["body"] for r in reversed(profile.provider.requests) if is_primary(r["body"], "A", 2))
        resumed = json.loads(second["metadata"]["user_id"])["session_id"]
        result.update(native_id=sid, resumed_native_id=resumed, launch_output=output)
        result["checks"] = {"old_foreground_exited": terminal.proc.poll() is not None,
                            "same_native_id": resumed == sid, "original_history_preserved": is_primary(second, "A", 1),
                            "bound_credentials": all(r["auth_matches"] for r in profile.provider.requests)}
        return result
    finally:
        profile.close()
        result["checks"]["owned_supervisor_stopped"] = profile.cleaned
        write(root / "observations.json", result)


class HandoffProvider(Provider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.workspace = root / "work"
        self.issued = set()

    def responses(self, payload, answer, seq):
        sid = payload.get("client_metadata", {}).get("thread_id")
        primary = any(item.get("role") == "user" and isinstance(item.get("content"), list) and any(
            p.get("text") == "NETCAP_HANDOFF_TOOL" for p in item["content"] if isinstance(p, dict)) for item in payload.get("input", []))
        if not primary or sid in self.issued:
            return super().responses(payload, answer, seq)
        self.issued.add(sid)
        command = shlex.join([sys.executable, str(self.workspace / "owned_tool.py")])
        code = "text(await tools.exec_command(" + json.dumps({"cmd": command, "login": False,
            "sandbox_permissions": "require_escalated", "justification": "Run only the owned handoff fixture tool?", "yield_time_ms": 1000}) + "));"
        item = {"id": f"ctc_{seq}", "type": "custom_tool_call", "call_id": f"call_{seq}", "name": "exec",
                "namespace": "functions", "input": code, "status": "completed"}
        response = {"id": f"resp_{seq}", "object": "response", "created_at": int(time.time()), "status": "completed",
                    "model": payload["model"], "output": [item], "usage": {"input_tokens": 32, "output_tokens": 16, "total_tokens": 48}}
        return self.sse([("response.created", {"response": dict(response, status="in_progress", output=[])}),
                         ("response.output_item.added", {"output_index": 0, "item": dict(item, status="in_progress")}),
                         ("response.output_item.done", {"output_index": 0, "item": item}), ("response.completed", {"response": response})])


def codex_control(root, binary):
    root.mkdir(parents=True)
    fixture = ConsoleFixture(binary, HandoffProvider)
    terminals = []
    result = {"checks": {}, "fixture": str(fixture.root), "scope": "production Application, real shared-backend TUI, owned tool and Go crash; no independent foreground adoption"}
    checks = result["checks"]
    try:
        # Complete this known TUI preference before binding the fixture config;
        # otherwise first launch edits config.toml and correctly trips drift.
        config = fixture.profile / "config.toml"
        write(config, config.read_text() + "\n[tui]\nscreen_reader_detection_done = true\n")
        fixture.start_native()
        fixture.start_console()
        sid = fixture.create("handoff")
        native = fixture.session(sid)["scope"]["native_id"]
        fixture.command("seed-history", "send", session_id=sid, text="NETCAP_A_TURN_1")
        wait_for(lambda: fixture.session(sid)["status"] == "idle", "first persisted native turn")
        tui = Terminal([CODEX, "resume", "--remote", "unix://" + str(fixture.sock), "--no-alt-screen", native, "NETCAP_A_TURN_2"],
                       fixture.env, fixture.cwd, root / "human.ansi")
        terminals.append(tui)
        tui.until(lambda: fixture.session(sid)["control_mode"] == "human_takeover" and fixture.session(sid)["status"] == "idle", "real terminal input yields platform control")
        tui.send("/exit")
        tui.drain(.4)
        tui.send("\r")
        tui.until(lambda: tui.proc.poll() is not None, "human terminal exits")

        def operation(oid):
            return next(o for o in fixture.snapshot()["operations"] if o["id"] == oid)

        receipt = fixture.command("return-control", "resume", session_id=sid)
        wait_for(lambda: operation(receipt["operation_id"])["status"] == "succeeded", "fresh handoff succeeds")
        # A subsequent poll must not treat acknowledged historical input as new.
        time.sleep(.8)
        checks["historical_terminal_input_acknowledged"] = not fixture.session(sid)["paused"] and fixture.session(sid)["control_mode"] == "managed"
        checks["duplicate_handoff_idempotent"] = fixture.command("return-control", "resume", session_id=sid) == receipt
        write(fixture.cwd / "owned_tool.py", '''import os,time
from pathlib import Path
root=Path(__file__).parent
(root/'tool.pid').write_text(str(os.getpid()))
with (root/'starts.txt').open('a') as f:f.write('start\\n')
until=time.monotonic()+70
while not (root/'release').exists():
 if time.monotonic()>until:raise TimeoutError('fixture release missing')
 time.sleep(.05)
(root/'tool.done').write_text('done')
print('owned tool completed')
''')
        send = fixture.command("tool", "send", session_id=sid, text="NETCAP_HANDOFF_TOOL")
        pending = wait_for(lambda: next((p for p in fixture.session(sid)["view"]["pending"] if p["status"] == "pending"), None), "native tool approval")
        terminal = Terminal([CODEX, "resume", "--remote", "unix://" + str(fixture.sock), "--no-alt-screen", native],
                            fixture.env, fixture.cwd, root / "approval.ansi")
        terminals.append(terminal)
        terminal.until(lambda: "Would you like" in terminal.text() or "Yes" in terminal.text(), "approval visible in real TUI")
        terminal.send("\r")
        terminal.until(lambda: (fixture.cwd / "tool.pid").exists(), "approved native tool starts")
        tool_pid = int((fixture.cwd / "tool.pid").read_text())
        fixture.command("manual-during-tool", "takeover", session_id=sid)
        try:
            fixture.command("must-not-resume", "resume", session_id=sid)
        except HTTPError as error:
            checks["in_flight_resume_refused"] = error.code == 409
        else:
            checks["in_flight_resume_refused"] = False
        fixture.restart_console(crash=True)
        wait_for(lambda: fixture.session(sid)["scope"]["native_id"] == native and fixture.session(sid)["connection"] == "ready", "Go restart reconciles original executor")
        checks["tool_survives_controller_crash"] = Path(f"/proc/{tool_pid}").exists()
        checks["same_native_identity_after_crash"] = fixture.session(sid)["scope"]["native_id"] == native
        write(fixture.cwd / "release", "release")
        terminal.until(lambda: (fixture.cwd / "tool.done").exists(), "owned tool finishes")
        wait_for(lambda: fixture.session(sid)["status"] == "idle" and operation(send["operation_id"])["status"] == "succeeded", "original tool operation reconciles")
        checks["tool_executed_once"] = (fixture.cwd / "starts.txt").read_text().splitlines() == ["start"]
        terminal.send("/exit")
        terminal.drain(.4)
        terminal.send("\r")
        terminal.until(lambda: terminal.proc.poll() is not None, "approval terminal exits")
        restored = fixture.command("resume-after-tool", "resume", session_id=sid)
        wait_for(lambda: operation(restored["operation_id"])["status"] == "succeeded", "handoff after tool completion")
        checks["control_restored_after_tool"] = not fixture.session(sid)["paused"]
        checks["all_requests_authenticated"] = all(r["auth_matches"] for r in fixture.provider.requests)
        write(root / "snapshot.json", fixture.snapshot())
        result.update(native_id=native, tool_pid=tool_pid, approval_id=pending["id"])
        return result
    finally:
        write(fixture.cwd / "release", "release")
        for terminal in terminals:
            terminal.close()
        fixture.close()
        write(root / "observations.json", result)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", choices=["claude-stopped-resume", "codex-control"], required=True)
    parser.add_argument("--netcap")
    args = parser.parse_args()
    if args.case == "codex-control" and not args.netcap:
        parser.error("--netcap is required for production control checks")
    root = Path(tempfile.mkdtemp(prefix="netcap-handoff-"))
    print("Evidence directory:", root, flush=True)
    verify_versions(root, ["claude"] if args.case.startswith("claude") else ["codex", "server"])
    try:
        observations = claude_stopped_resume(root / args.case) if args.case.startswith("claude") else codex_control(root / args.case, str(Path(args.netcap).resolve()))
        result = {"execution": "completed", "expectations_met": all(observations["checks"].values()), "observations": observations}
    except Exception as error:
        detail = error.read().decode() if isinstance(error, HTTPError) else str(error)
        result = {"execution": "failed", "error": detail}
    write(root / "result.json", result)
    print(json.dumps(result, ensure_ascii=False), flush=True)
    return int(result["execution"] != "completed" or not result.get("expectations_met"))


if __name__ == "__main__":
    raise SystemExit(main())
