#!/usr/bin/env python3
"""Observe native background attach under a new, isolated CLAUDE_CONFIG_DIR.

Does not claim arbitrary foreground TUI adoption or platform input ownership.
Only the supervisor in this newly created fixture profile is stopped.
"""
import fcntl
import argparse
import json
import os
from pathlib import Path
import pty
import re
import select
import shlex
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time

from execution_config_probe import CLAUDE, Provider, environment, settings, stop, wait_for, write, verify_versions


def primary_request(payload):
    for message in payload.get("messages", []):
        if message.get("role") != "user":
            continue
        content = message.get("content")
        if content == "NETCAP_A_TURN_1" or (isinstance(content, list) and any(
            part.get("text") == "NETCAP_A_TURN_1" for part in content if isinstance(part, dict))):
            return True
    return False


class HoldingProvider(Provider):
    def __init__(self, root):
        self.waiting = threading.Event()
        self.release = threading.Event()
        super().__init__(root, "A")

    def failure_response(self, path, payload):
        if primary_request(payload) and not self.waiting.is_set():
            self.waiting.set()
            assert self.release.wait(timeout=40), "fixture response release timed out"
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--auth-source", choices=["settings", "process", "helper"], default="helper")
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix="netcap-claude-attach-"))
    print("Evidence directory:", root, flush=True)
    verify_versions(root, ["claude"])
    env = environment(root)
    cwd = root / "work"
    cwd.mkdir()
    provider = HoldingProvider(root)
    if args.auth_source == "process":
        env.update(ANTHROPIC_API_KEY="fixture-A", ANTHROPIC_BASE_URL=provider.url)
    config = settings(root, provider)
    if args.auth_source == "helper":
        helper = root / "fixture-key.py"
        write(helper, "print('fixture-A')\n")
        data = json.loads(config.read_text())
        data["env"].pop("ANTHROPIC_API_KEY")
        data["apiKeyHelper"] = shlex.join([sys.executable, str(helper)])
        write(config, data)
    # The supervisor gets settings independently of the shell which spawned it.
    write(Path(env["CLAUDE_CONFIG_DIR"]) / "settings.json", json.loads(config.read_text()))
    result = {"scope": "native --bg then TTY attach; no platform adapter or foreground adoption", "auth_source": args.auth_source}
    attached = None
    master = slave = None

    def cli(args, tag):
        completed = subprocess.run([CLAUDE, *args], env=env, cwd=cwd, capture_output=True, text=True, timeout=35)
        write(root / (tag + ".stdout"), completed.stdout)
        write(root / (tag + ".stderr"), completed.stderr)
        assert completed.returncode == 0, tag + " exit " + str(completed.returncode)
        return completed.stdout

    try:
        cli(["--bg", "NETCAP_A_TURN_1", "--settings", str(config), "--setting-sources", "user,project,local"], "launch")
        # A first-run native prompt can block before the model request. Capture
        # it through the same attach surface instead of assuming startup worked.
        if args.auth_source == "helper":
            wait_for(provider.waiting.is_set, "original main request", timeout=25)
        else:
            time.sleep(2)
        cli(["logs", json.loads(cli(["agents", "--json"], "initial"))[0]["id"]], "initial-logs")
        before = json.loads(cli(["agents", "--json"], "before"))
        jobs = [item for item in before if item.get("cwd") == str(cwd) and item.get("kind") == "background"]
        assert len(jobs) == 1, "expected one owned background job"
        job = jobs[0]
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 36, 120, 0, 0))
        attached = subprocess.Popen([CLAUDE, "attach", job["id"]], env=env, cwd=cwd,
                                    stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
        output = bytearray()

        def drain(seconds):
            until = time.monotonic() + seconds
            while time.monotonic() < until:
                if select.select([master], [], [], .1)[0]:
                    try:
                        output.extend(os.read(master, 65536))
                    except OSError:
                        break

        drain(2)
        write(root / "attached-initial.ansi", output.decode(errors="replace"))
        during = json.loads(cli(["agents", "--json"], "during"))
        current = next(item for item in during if item.get("id") == job["id"])
        result["same_worker_while_attached"] = bool(job.get("pid")) and current.get("pid") == job["pid"]
        result["attach_process_alive"] = attached.poll() is None
        result["model_request_started"] = provider.waiting.is_set()
        provider.release.set()
        drain(3)
        logs = cli(["logs", job["id"]], "logs")
        # Terminal redraws insert ANSI controls and split/omit rendered spaces.
        rendered = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", logs + output.decode(errors="replace"))
        result["original_reply_observed"] = "fixtureA:NETCAP_A_TURN_1" in re.sub(r"\s+", "", rendered)
        result["attach_rendered_session"] = b"NETCAP_A_TURN_1" in output
        # Ctrl+Z is the documented detach action for an attached native TUI.
        os.write(master, b"\x1a")
        drain(1)
        after = json.loads(cli(["agents", "--json"], "after-detach"))
        survivor = next(item for item in after if item.get("id") == job["id"])
        result["same_worker_after_detach"] = survivor.get("pid") == job.get("pid")
        result["authentication_verified"] = bool(provider.requests) and all(r["auth_matches"] for r in provider.requests)
        result["request_paths"] = [r["path"] for r in provider.requests]
        result["single_primary_request"] = len([r for r in provider.requests if primary_request(r["body"])]) == 1
        result["auxiliary_request_count"] = len([r for r in provider.requests if not primary_request(r["body"])])
        result["job"] = {k: job.get(k) for k in ("id", "pid", "state", "kind")}
        write(root / "attach.ansi", output.decode(errors="replace"))
        result["execution"] = "completed"
        result["expectations_met"] = all(result[k] for k in ["same_worker_while_attached", "attach_process_alive",
            "model_request_started", "original_reply_observed", "attach_rendered_session", "same_worker_after_detach", "authentication_verified", "single_primary_request"])
    except Exception as error:
        result.update(execution="failed", error=str(error))
    finally:
        provider.release.set()
        stop(attached)
        if master is not None:
            os.close(master)
        if slave is not None:
            os.close(slave)
        # C_CONFIG is verified to be inside this freshly allocated fixture.
        assert Path(env["CLAUDE_CONFIG_DIR"]).is_relative_to(root)
        cleanup = subprocess.run([CLAUDE, "daemon", "stop", "--any"], env=env, cwd=cwd,
                                 capture_output=True, text=True, timeout=20)
        write(root / "cleanup.txt", cleanup.stdout + cleanup.stderr)
        result["owned_supervisor_cleanup_exit"] = cleanup.returncode
        provider.close()
        write(root / "result.json", result)
        print(json.dumps(result, ensure_ascii=False), flush=True)
    return int(result["execution"] != "completed" or not result.get("expectations_met") or result["owned_supervisor_cleanup_exit"] != 0)


if __name__ == "__main__":
    raise SystemExit(main())
