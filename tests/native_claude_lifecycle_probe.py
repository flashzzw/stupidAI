#!/usr/bin/env python3
"""Native Claude background approvals and failures with owned fixture processes.

All model requests use a local synthetic provider. This exercises native CLI
contracts, not the production SDK adapter or arbitrary foreground adoption.
"""
import argparse
import json
import os
from pathlib import Path
import re
import shlex
import signal
import sys
import tempfile
import time

from execution_config_probe import Provider, verify_versions, wait_for, write
from workbench_boundary_probe import ClaudeProfile, is_primary


class ToolProvider(Provider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.workspace = root / "work"
        self.issued = []

    def anthropic(self, payload, answer, seq):
        primary = any(m.get("role") == "user" and (
            m.get("content") == "NETCAP_NATIVE_TOOL" or
            isinstance(m.get("content"), list) and any(
                p.get("text") == "NETCAP_NATIVE_TOOL" for p in m["content"] if isinstance(p, dict)))
            for m in payload.get("messages", []))
        if not primary or self.issued:
            return super().anthropic(payload, answer, seq)
        self.issued.append(f"tool_{seq}")
        args = {"command": shlex.join([sys.executable, str(self.workspace / "owned_tool.py")]),
                "description": "Run the owned lifecycle fixture; wait for its release file", "timeout": 180000}
        message = {"id": f"msg_{seq}", "type": "message", "role": "assistant", "model": payload["model"],
                   "content": [], "stop_reason": None, "stop_sequence": None,
                   "usage": {"input_tokens": 32, "output_tokens": 0}}
        return self.sse([("message_start", {"message": message}),
                         ("content_block_start", {"index": 0, "content_block": {
                             "type": "tool_use", "id": self.issued[0], "name": "Bash", "input": {}}}),
                         ("content_block_delta", {"index": 0, "delta": {"type": "input_json_delta", "partial_json": json.dumps(args)}}),
                         ("content_block_stop", {"index": 0}),
                         ("message_delta", {"delta": {"stop_reason": "tool_use", "stop_sequence": None}, "usage": {"output_tokens": 32}}),
                         ("message_stop", {})])


def alive(pid):
    try:
        return Path(f"/proc/{pid}/stat").read_text().split(") ", 1)[1][0] != "Z"
    except FileNotFoundError:
        return False


def crash_owned(pid, profile):
    # Open the pidfd before checking ownership to avoid signalling a reused PID.
    fd = os.pidfd_open(pid)
    try:
        env = Path(f"/proc/{pid}/environ").read_bytes().split(b"\0")
        expected = ("CLAUDE_CONFIG_DIR=" + profile.env["CLAUDE_CONFIG_DIR"]).encode()
        assert expected in env, "refuse to signal a process outside this fixture profile"
        signal.pidfd_send_signal(fd, signal.SIGKILL)
    finally:
        os.close(fd)


def run(root, case):
    profile = ClaudeProfile(root, "A", provider_factory=ToolProvider)
    checks = {}
    result = {"scope": "native --bg, real TTY approval, owned tool; local synthetic provider", "checks": checks}
    tool_pid = None
    try:
        write(profile.cwd / "owned_tool.py", '''import os,time
from pathlib import Path
root=Path(__file__).parent
with (root/'starts.txt').open('a') as f:f.write('start\\n')
(root/'tool.pid').write_text(str(os.getpid()))
until=time.monotonic()+150
while not (root/'release').exists():
 if time.monotonic()>until:raise TimeoutError('fixture release missing')
 time.sleep(.05)
(root/'tool.done').write_text('done')
print('NETCAP_NATIVE_TOOL_FINISHED')
''')
        profile.cli(["--bg", "NETCAP_NATIVE_TOOL", "--permission-mode", "default"], "launch")

        def job(tag="roster"):
            rows = json.loads(profile.cli(["agents", "--json", "--all"], tag))
            assert len(rows) == 1, "unexpected fixture session count"
            return rows[0]

        pending = wait_for(lambda: (j if (j := job()).get("waitingFor") == "permission prompt" else None), "native permission request", timeout=40)
        result["original_job"] = pending
        sid, worker = pending["sessionId"], pending["pid"]
        checks["approval_waits_without_tool_effect"] = not (profile.cwd / "tool.pid").exists()
        terminal = profile.attach(pending["id"], "approval")
        terminal.until(lambda: "Yes" in terminal.text() and "owned_tool.py" in terminal.text(), "original approval shown")
        crash_owned(terminal.proc.pid, profile)
        terminal.proc.wait(timeout=5)
        after = job("after-attachment-crash")
        checks["attachment_crash_preserves_approval"] = after.get("pid") == worker and after.get("waitingFor") == "permission prompt" and not (profile.cwd / "tool.pid").exists()
        again = profile.attach(pending["id"], "reattach-approval")
        again.until(lambda: "Yes" in again.text() and "owned_tool.py" in again.text(), "approval reappears")
        again.send("\r")
        again.until(lambda: (profile.cwd / "tool.pid").exists(), "owned tool starts after explicit approval")
        tool_pid = int((profile.cwd / "tool.pid").read_text())
        result["tool_pid"] = tool_pid
        active = job("tool-running")
        checks["in_flight_tool_visible"] = active.get("status") == "busy" and active.get("sessionId") == sid
        crash_owned(again.proc.pid, profile)
        again.proc.wait(timeout=5)
        checks["attachment_crash_preserves_tool"] = alive(tool_pid) and job("tool-detached").get("pid") == worker

        if case == "supervisor-crash":
            status = profile.cli(["daemon", "status"], "daemon-before")
            match = re.search(r"\bpid\D+(\d+)", status, re.I)
            assert match, "daemon status did not report its PID"
            supervisor = int(match.group(1))
            result["supervisor_pid"] = supervisor
            crash_owned(supervisor, profile)
            wait_for(lambda: not alive(supervisor), "owned supervisor exits")
            terminal = profile.attach(pending["id"], "restart-supervisor")
            terminal.until(lambda: "NETCAP_NATIVE_TOOL" in terminal.text(), "original live transcript reattached")
            status = profile.cli(["daemon", "status"], "daemon-after")
            new_pid = re.search(r"\bpid\D+(\d+)", status, re.I)
            checks["supervisor_restarted"] = bool(new_pid) and int(new_pid.group(1)) != supervisor and alive(int(new_pid.group(1)))
            restored = wait_for(lambda: (j if (j := job("after-supervisor-crash")).get("pid") == worker else None), "supervisor reconnects to original worker", timeout=40)
            checks["original_worker_and_tool_survive"] = alive(worker) and alive(tool_pid)
        else:
            crash_owned(worker, profile)
            wait_for(lambda: not alive(worker), "owned worker exits")
            restored = wait_for(lambda: (j if (j := job("after-worker-crash")).get("pid") and j["pid"] != worker else None), "supervisor starts replacement worker", timeout=40)
            restored = wait_for(lambda: (j if (j := job("replacement-idle")).get("status") == "idle" else None), "replacement worker reaches its prompt", timeout=20)
            result["tool_alive_after_worker_crash"] = alive(tool_pid)

        result["restored_job"] = restored
        checks["same_native_identity"] = restored.get("sessionId") == sid
        result["roster_idle_while_original_tool_alive"] = restored.get("status") == "idle" and alive(tool_pid)
        write(profile.cwd / "release", "release")
        if case != "supervisor-crash":
            terminal = profile.attach(pending["id"], "after-failure")
            terminal.until(lambda: (profile.cwd / "tool.done").exists(), "original tool finishes after release")
            terminal.drain(3)
            terminal.send("NETCAP_A_TURN_2")
            terminal.drain(.4)
            terminal.send("\r")
            terminal.until(lambda: any(is_primary(r["body"], "A", 2) for r in profile.provider.requests), "resumed worker accepts next explicit prompt")
        terminal.until(lambda: job("settled").get("status") == "idle", "native session settles", timeout=40)
        tool_results = [p for r in profile.provider.requests for m in r["body"].get("messages", [])
                        if isinstance(m.get("content"), list) for p in m["content"] if p.get("type") == "tool_result"]
        matched = [p for p in tool_results if p.get("tool_use_id") in profile.provider.issued]
        result["tool_result_returned"] = any("NETCAP_NATIVE_TOOL_FINISHED" in json.dumps(p) and not p.get("is_error") for p in matched)
        result["tool_error_returned"] = any(p.get("is_error") for p in matched)
        checks["tool_executed_once"] = (profile.cwd / "starts.txt").read_text().splitlines() == ["start"]
        checks["all_local_requests_authenticated"] = bool(profile.provider.requests) and all(r["auth_matches"] for r in profile.provider.requests)
        checks["provider_has_no_errors"] = not profile.provider.errors
        if case == "supervisor-crash":
            checks["original_tool_result_preserved"] = (profile.cwd / "tool.done").exists() and result["tool_result_returned"]
        else:
            resumed = [r["body"] for r in profile.provider.requests if is_primary(r["body"], "A", 2)]
            checks["next_prompt_keeps_identity_and_history"] = bool(resumed) and all(
                json.loads(r["metadata"]["user_id"])["session_id"] == sid and "NETCAP_NATIVE_TOOL" in json.dumps(r["messages"]) for r in resumed)
            result["tool_completed_on_disk"] = (profile.cwd / "tool.done").exists()
        result["original_tool_output_continuity"] = "verified" if result["tool_result_returned"] else "unverified"
        result["final_job"] = job("final")
        return result
    finally:
        write(profile.cwd / "release", "release")
        profile.close()
        if tool_pid and alive(tool_pid):
            wait_for(lambda: not alive(tool_pid), "released owned tool exits", timeout=5)
        checks["owned_supervisor_stopped"] = profile.cleaned
        write(root / "observations.json", result)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", choices=["supervisor-crash", "worker-crash"], default="supervisor-crash")
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix="netcap-claude-native-"))
    print("Evidence directory:", root, flush=True)
    verify_versions(root, ["claude"])
    try:
        observations = run(root / args.case, args.case)
        result = {"execution": "completed", "expectations_met": bool(observations["checks"]) and all(observations["checks"].values()), "observations": observations}
    except Exception as error:
        result = {"execution": "failed", "error": str(error)}
    write(root / "result.json", result)
    print(json.dumps(result, ensure_ascii=False), flush=True)
    return int(not result.get("expectations_met"))


if __name__ == "__main__":
    raise SystemExit(main())
