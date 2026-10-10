"""用独立临时进程探测供应商，主循环只轮询进程状态。"""

from __future__ import annotations

import json
import os
import re
import signal
import subprocess
import tempfile
import time

from .config import Config
from .state import State

PROBE_PROMPT = '请执行 date -u +%FT%TZ，只返回命令输出。'
RETRY_AFTER = re.compile(r'\bretry[- ]after\s*[:=]?\s*(\d+(?:\.\d+)?)\s*(?:s|sec|seconds)?\b', re.I)
CONFIGURATION_ERROR = re.compile(r'\b(?:401|403|unauthorized|invalid[_ -]?api[_ -]?key|authentication failed|unknown model|invalid model)\b', re.I)


class Probe:
    def __init__(self, config: Config, state: State):
        self.config, self.state = config, state
        try:
            self.next_at = float(state.get('provider_retry_at', '0'))
        except ValueError:
            self.next_at = 0.0
        self.proc: subprocess.Popen | None = None
        self.out = None
        self.err = None
        self.started = 0.0

    @property
    def available(self) -> bool:
        try:
            updated = float(self.state.get('provider_at'))
        except ValueError:
            return False
        return (self.state.get('provider') == 'available' and
                time.time() - updated <= max(30.0, 2 * self.config.probe_interval))

    def _stop_process(self) -> None:
        if self.proc is not None:
            try:
                os.killpg(self.proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            self.proc.wait()
            self.proc = None
        if self.out is not None:
            self.out.close()
            self.out = None
        if self.err is not None:
            self.err.close()
            self.err = None

    def close(self) -> None:
        self._stop_process()

    def mark_success(self) -> None:
        if self.proc is not None:
            self._stop_process()
        self.state.put('provider', 'available')
        self.state.put('provider_at', str(int(time.time())))
        self.next_at = time.time() + self.config.probe_interval
        self.state.put('provider_retry_at', str(self.next_at))

    def mark_failure(self, reason: str) -> None:
        configuration_error = bool(CONFIGURATION_ERROR.search(reason))
        self.state.put('provider', 'configuration_error' if configuration_error else 'unavailable')
        self.state.put('provider_at', str(int(time.time())))
        self.state.log('provider', reason[:220])
        retry = RETRY_AFTER.search(reason)
        delay = max(self.config.probe_interval, min(3600.0, float(retry.group(1)))) if retry else self.config.probe_interval
        self.next_at = float('inf') if configuration_error else time.time() + delay
        self.state.put('provider_retry_at', str(self.next_at))

    def _start(self, cwd: str) -> None:
        command = ['codex', 'exec', '--skip-git-repo-check', '--json', '--ephemeral',
                   '-c', 'approval_policy="never"', '-c', 'sandbox_mode="read-only"',
                   PROBE_PROMPT]
        env = {**os.environ, 'CODEX_HOME': str(self.config.home)}
        self.out = tempfile.TemporaryFile()
        self.err = tempfile.TemporaryFile()
        try:
            self.proc = subprocess.Popen(command, cwd=cwd, env=env,
                                         stdin=subprocess.DEVNULL, stdout=self.out,
                                         stderr=self.err, start_new_session=True)
            self.started = time.monotonic()
        except OSError as exc:
            self._stop_process()
            self.mark_failure(f'探测启动失败：{exc}')

    def _finish_if_ready(self) -> None:
        if self.proc is None:
            return
        code = self.proc.poll()
        if code is None and time.monotonic() - self.started < self.config.probe_timeout:
            return
        if code is None:
            code = 124
        # 只清理本次短探测拥有的进程组；Goal 轮次由共享服务管理。
        try:
            os.killpg(self.proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        self.proc.wait()
        self.proc = None
        self.out.seek(0)
        self.err.seek(0)
        events = []
        for line in self.out:
            try:
                item = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(item, dict):
                events.append(item)
        stderr = self.err.read().decode('utf-8', 'replace')
        self._stop_process()
        answer = any(e.get('type') == 'item.completed' and
                     isinstance(e.get('item'), dict) and
                     e['item'].get('type') == 'agent_message' and
                     bool(e['item'].get('text')) for e in events)
        good = (code == 0 and answer and
                any(e.get('type') == 'turn.completed' for e in events) and
                not any(e.get('type') == 'turn.failed' for e in events))
        if good:
            self.mark_success()
            return
        errors = []
        for event in events:
            if event.get('type') == 'error':
                errors.append(str(event.get('message', '')))
            elif event.get('type') == 'turn.failed':
                error = event.get('error')
                errors.append(str(error.get('message', '')) if isinstance(error, dict) else str(error))
        detail = (errors or [stderr])[-1][:180]
        self.mark_failure(f'探测失败 exit={code}: {detail}')

    def poll(self) -> None:
        self._finish_if_ready()

    def ensure(self, cwd: str) -> bool:
        self.poll()
        try:
            persisted = float(self.state.get('provider_retry_at', '0'))
        except ValueError:
            persisted = 0.0
        if persisted != self.next_at:
            self.next_at = persisted
        if self.available:
            return True
        if self.proc is not None or self.state.get('provider') == 'configuration_error':
            return False
        if time.time() < self.next_at:
            return False
        self._start(cwd)
        return False

    def check(self, cwd: str) -> bool:
        """显式单次检查，供命令行或离线实验使用；调度器使用 ensure。"""
        self.ensure(cwd)
        while self.proc is not None:
            time.sleep(0.05)
            self.poll()
        return self.available
