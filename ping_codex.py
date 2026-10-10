#!/usr/bin/env python3
"""临时会话执行 UTC 时间任务；持久会话每次查询一个城市，失败原地重试五次。"""

import argparse
import json
import math
import os
import shlex
import signal
import subprocess
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path

from weather import CITIES

PROMPT = "请执行 date -u +%FT%TZ，只返回命令输出。"
WEATHER_SCRIPT = Path(__file__).resolve().with_name("weather.py")
DEFAULT_LOG = Path(__file__).with_name("codex_success_sessions.jsonl")
SESSION_RETRIES = 5


def weather_prompt(city: str) -> str:
    command = shlex.join(["python3", str(WEATHER_SCRIPT), city])
    return (
        f"请直接使用 {WEATHER_SCRIPT} 查询{city}天气。用法：{command}。"
        "请执行一次并原样返回输出，失败则原样返回错误，不要另找方法或展开解释。"
    )


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def run_once(mode: str, prompt: str, timeout_s: int, thread_id: str | None = None) -> tuple[int, str, str]:
    cmd = ["codex", "exec"]
    if mode == "resume":
        cmd.append("resume")
    cmd += ["--skip-git-repo-check", "--json", "-c", 'approval_policy="never"']
    if mode == "probe":
        cmd += ["-c", 'sandbox_mode="read-only"', "--ephemeral"]
    else:
        # 天气脚本需要联网；只给本次调用启用网络，不修改用户的全局配置。
        cmd += ["-c", 'sandbox_mode="workspace-write"',
                "-c", "sandbox_workspace_write.network_access=true"]
    if mode == "resume":
        if not thread_id:
            raise ValueError("恢复会话必须提供 thread_id")
        cmd.append(thread_id)
    cmd.append(prompt)
    # 临时输出文件随调用关闭删除，不创建 Codex 持久会话。
    # 不使用 PIPE，避免后代进程持有管道而拖延超时清理。
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        try:
            proc = subprocess.Popen(
                cmd, stdin=subprocess.DEVNULL, stdout=out, stderr=err,
                cwd=WEATHER_SCRIPT.parent, start_new_session=True,
            )
        except OSError as exc:
            return 127, "", str(exc)
        timed_out = False
        try:
            code = proc.wait(timeout=timeout_s)
        except subprocess.TimeoutExpired:
            code, timed_out = 124, True
        finally:
            # 同时覆盖超时、Ctrl-C，以及主进程退出后遗留的子进程。
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            proc.wait(timeout=5)
        out.seek(0)
        err.seek(0)
        stdout = out.read().decode("utf-8", "replace")
        stderr = err.read().decode("utf-8", "replace")
        return code, stdout, stderr + ("\nTIMEOUT" if timed_out else "")


def read_events(stdout: str) -> list[dict]:
    events = []
    for line in stdout.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(event, dict):
            events.append(event)
    return events


def thread_id_from(events: list[dict]) -> str | None:
    for event in events:
        if (event.get("type") == "thread.started" and isinstance(event.get("thread_id"), str)
                and event["thread_id"].strip()):
            return event["thread_id"]
    return None


def error_text(events: list[dict], stderr: str) -> str:
    messages = []
    for event in events:
        if event.get("type") == "error":
            messages.append(str(event.get("message", "")))
        elif event.get("type") == "turn.failed":
            error = event.get("error")
            messages.append(str(error.get("message", "")) if isinstance(error, dict) else str(error))
    return "\n".join(messages + [stderr])


def final_message(events: list[dict]) -> str:
    for event in reversed(events):
        item = event.get("item")
        if event.get("type") == "item.completed" and isinstance(item, dict):
            if item.get("type") == "agent_message" and isinstance(item.get("text"), str):
                return item["text"]
    return ""


def record_session(log_path: Path, thread_id: str, recorded_at: str) -> None:
    entry = json.dumps({"time_utc": recorded_at, "thread_id": thread_id}) + "\n"
    log_path.parent.mkdir(parents=True, exist_ok=True)
    with log_path.open("a", encoding="utf-8") as log:
        log.write(entry)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--interval", type=float, default=3.0, help="失败后的重试间隔（秒）")
    parser.add_argument("--weather-interval", type=float, default=10.0, help="成功完成后等待多少秒再查询下一个城市")
    parser.add_argument("--timeout", type=int, default=120, help="单次 Codex 调用超时（秒）")
    parser.add_argument("--max-tries", type=int, default=0, help="最多调用次数；0 表示持续运行")
    parser.add_argument("--log", type=Path, default=DEFAULT_LOG, help="成功创建的持久会话日志")
    args = parser.parse_args()
    if (any(not math.isfinite(v) or v < 0 for v in (args.interval, args.weather_interval))
            or args.timeout <= 0 or args.max_tries < 0):
        parser.error("间隔和最大次数不能为负，超时必须大于 0")

    mode = "probe"
    thread_id = None
    attempt = 0
    city_index = 0
    consecutive_failures = 0
    session_recorded = False
    while True:
        if args.max_tries and attempt >= args.max_tries:
            print(f"{utc_now()} stop max-tries={args.max_tries}", flush=True)
            return 2
        attempt += 1
        city = CITIES[city_index] if mode != "probe" else None
        prompt = weather_prompt(city) if city else PROMPT
        started = time.monotonic()
        code, stdout, stderr = run_once(mode, prompt, args.timeout, thread_id)
        elapsed = time.monotonic() - started
        events = read_events(stdout)
        completed = any(event.get("type") == "turn.completed" for event in events)
        failed = any(event.get("type") == "turn.failed" for event in events)
        new_thread_id = thread_id_from(events)
        answer = final_message(events).strip()
        success = code == 0 and completed and not failed and bool(answer)
        protocol_error = ""
        if mode == "persist":
            # 创建时即使失败，也可能已经落盘并返回 ID，必须保留用于原会话重试。
            thread_id = new_thread_id
            if not thread_id:
                success = False
                protocol_error = "未收到持久会话 ID"
        if mode == "resume" and new_thread_id and new_thread_id != thread_id:
            success = False
            protocol_error = "恢复返回的会话 ID 不匹配"
        details = error_text(events, stderr).strip()
        if protocol_error:
            details = f"{protocol_error}; {details}"
        if code == 124:
            details = f"TIMEOUT after {args.timeout}s; {details}"
        summary = "" if success else " detail=" + " ".join(
            (details or stdout.strip() or "未收到成功完成事件或最终响应").split()
        )[:500]
        print(
            f"{utc_now()} try={attempt} mode={mode} exit={code} "
            f"success={success} elapsed_s={elapsed:.1f} city={city or '-'} "
            f"thread_id={thread_id or '-'} "
            f"retry={consecutive_failures}/{SESSION_RETRIES}{summary}", flush=True,
        )

        if success:
            consecutive_failures = 0
            print(answer, flush=True)
            if mode == "probe":
                mode = "persist"
                continue
            # 包括 persist 失败、之后 resume 首次成功的情况；每个会话只记录一次。
            if not session_recorded:
                recorded_at = utc_now()
                record_session(args.log, thread_id, recorded_at)
                session_recorded = True
                print(f"{recorded_at} saved thread_id={thread_id} log={args.log}", flush=True)
            mode = "resume"
            city_index = (city_index + 1) % len(CITIES)
            if args.max_tries and attempt >= args.max_tries:
                continue
            time.sleep(args.weather_interval)
            continue

        if mode != "probe" and thread_id:
            consecutive_failures += 1
            if consecutive_failures <= SESSION_RETRIES:
                mode = "resume"
                print(f"{utc_now()} retry same thread_id={thread_id} "
                      f"retry={consecutive_failures}/{SESSION_RETRIES} city={city}", flush=True)
            else:
                # 首次失败 + 五次重试均失败，下一次从临时探测开始。
                print(f"{utc_now()} retries exhausted; abandon thread_id={thread_id}; "
                      "return to ephemeral probing", flush=True)
                mode, thread_id = "probe", None
                consecutive_failures, session_recorded = 0, False
        else:
            mode, thread_id = "probe", None
            consecutive_failures, session_recorded = 0, False
        if args.max_tries and attempt >= args.max_tries:
            continue
        time.sleep(args.interval)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        print(f"\n{utc_now()} stopped by user", flush=True)
        raise SystemExit(130)
    except (OSError, subprocess.TimeoutExpired) as exc:
        print(f"{utc_now()} stopped due to local process/log error: {exc}", flush=True)
        raise SystemExit(1)
