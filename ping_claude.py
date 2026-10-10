#!/usr/bin/env python3
"""每隔固定秒数用 claude -p 发同一句。失败立刻杀掉本次进程，成功才退出。"""

import argparse
import json
import os
import signal
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

PROMPT = "hello, can i deng you"
SETTINGS = str(Path.home() / ".claude" / "settings.any.json")
FAIL_MARKS = (
    "[error] api error",
    "service unavailable",
    "overloaded_error",
    "status=429",
    "status=500",
    "status=502",
    "status=503",
    "status=504",
)
POLL_S = 0.2


def utc_now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def kill_group(proc: subprocess.Popen[str]) -> None:
    if proc.poll() is not None:
        return
    try:
        os.killpg(proc.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        proc.wait(timeout=2)
    except subprocess.TimeoutExpired:
        os.killpg(proc.pid, signal.SIGKILL)
        proc.wait(timeout=2)


def failure_line(debug_text: str) -> str | None:
    for line in debug_text.splitlines():
        folded = line.casefold()
        if "api error" in folded or any(mark in folded for mark in FAIL_MARKS[1:]):
            return line.strip()[:240]
    return None


def result_text(stdout: str) -> str:
    try:
        payload = json.loads(stdout)
    except json.JSONDecodeError:
        return stdout.strip().splitlines()[-1][:240] if stdout.strip() else "(empty)"
    if payload.get("is_error"):
        return str(payload.get("result") or payload.get("error") or "is_error")[:240]
    return str(payload.get("result") or payload.get("subtype") or "ok")[:240]


def is_success(code: int, stdout: str) -> bool:
    if code != 0 or not stdout.strip():
        return False
    try:
        payload = json.loads(stdout)
    except json.JSONDecodeError:
        return False
    return not payload.get("is_error") and bool(payload.get("result"))


def run_once(timeout_s: int, debug_path: Path) -> tuple[int, str, str]:
    debug_path.write_text("", encoding="utf-8")
    cmd = [
        "claude",
        "--dangerously-skip-permissions",
        "--settings",
        SETTINGS,
        "-p",
        "--output-format",
        "json",
        "--debug",
        "api",
        "--debug-file",
        str(debug_path),
        PROMPT,
    ]
    proc = subprocess.Popen(
        cmd,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        start_new_session=True,
    )
    deadline = time.monotonic() + timeout_s
    reason = ""
    while proc.poll() is None and time.monotonic() < deadline:
        hit = failure_line(debug_path.read_text(encoding="utf-8", errors="replace"))
        if hit:
            reason = hit
            kill_group(proc)
            break
        time.sleep(POLL_S)
    else:
        if proc.poll() is None:
            reason = "TIMEOUT"
            kill_group(proc)

    stdout, stderr = proc.communicate()
    if reason:
        stderr = f"{stderr}\n{reason}".strip()
    return proc.returncode if proc.returncode is not None else 1, stdout, stderr


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--interval", type=float, default=5.0)
    parser.add_argument("--timeout", type=int, default=20)
    parser.add_argument("--max-tries", type=int, default=0)
    args = parser.parse_args()
    debug_path = Path("/tmp/codex-netcap/claude-ping-debug.log")

    attempt = 0
    while True:
        attempt += 1
        if args.max_tries and attempt > args.max_tries:
            print(f"{utc_now()} stop max-tries={args.max_tries}", flush=True)
            return 2

        code, stdout, stderr = run_once(args.timeout, debug_path)
        ok = is_success(code, stdout)
        detail = result_text(stdout) if ok else (stderr.strip().splitlines() or ["(empty)"])[-1][:240]
        print(f"{utc_now()} try={attempt} exit={code} ok={ok} {detail}", flush=True)
        if ok:
            sys.stdout.write(stdout if stdout.endswith("\n") else stdout + "\n")
            return 0
        time.sleep(args.interval)


if __name__ == "__main__":
    raise SystemExit(main())
