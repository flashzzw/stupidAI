"""配置解析。默认 Codex 目录不受调用者的 CODEX_HOME 影响。"""

from __future__ import annotations

import math
import tomllib
from dataclasses import dataclass
from pathlib import Path

DEFAULT_HOME = Path('/home/zzw/.codex')
TAKEOVER_PROMPT = '如果原任务还有明确、已授权且无需补充信息的步骤，请继续；如果任务已完成或需要用户决定，请执行一次运行状态检查，只返回一行结果。'
PROJECT_ROOT = Path(__file__).resolve().parent.parent


@dataclass(frozen=True)
class Job:
    id: str
    file: Path
    cwd: Path
    token_budget: int | None = None


@dataclass(frozen=True)
class Config:
    home: Path
    jobs: tuple[Job, ...]
    thread_ids: tuple[str, ...]
    workspaces: tuple[Path, ...]
    takeover_enabled: bool
    takeover_after: float
    probe_interval: float
    probe_timeout: int
    session_retries: int
    keepalive_mode: str
    keepalive_interval: float
    poll_interval: float
    auto_start_daemon: bool

    @property
    def socket(self) -> Path:
        return self.home / 'app-server-control' / 'app-server-control.sock'

    @property
    def state_dir(self) -> Path:
        return self.home / 'netcap'

    @property
    def model_provider(self) -> str:
        file = self.home / 'config.toml'
        if not file.is_file():
            return 'openai'
        value = tomllib.loads(file.read_text(encoding='utf-8')).get('model_provider', 'openai')
        if not isinstance(value, str):
            raise ValueError('Codex 配置中的 model_provider 必须是字符串')
        return value


def _positive_number(value: object, name: str) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value <= 0:
        raise ValueError(f'{name} 必须是有限的正数')
    return float(value)


def _positive_int(value: object, name: str) -> int:
    if type(value) is not int or value <= 0:
        raise ValueError(f'{name} 必须是正整数')
    return value


def _path(value: str, base: Path) -> Path:
    path = Path(value).expanduser()
    return (path if path.is_absolute() else base / path).resolve()


def load_config(path: Path | None = None, home_override: Path | None = None) -> Config:
    config_path = path.resolve() if path is not None else None
    raw = tomllib.loads(config_path.read_text(encoding='utf-8')) if config_path else {}
    base = config_path.parent if config_path else PROJECT_ROOT
    home_value = str(home_override) if home_override is not None else raw.get('codex_home', str(DEFAULT_HOME))
    if not isinstance(home_value, str) or not home_value.strip():
        raise ValueError('codex_home 必须是目录路径')
    home = _path(home_value, base)
    take = raw.get('takeover', {})
    probe = raw.get('probe', {})
    keep = raw.get('keepalive', {})
    runtime = raw.get('runtime', {})
    if any(not isinstance(v, dict) for v in (take, probe, keep, runtime)):
        raise ValueError('配置段必须是 TOML table')
    configured_jobs = raw.get('goals', [])
    if not isinstance(configured_jobs, list):
        raise ValueError('goals 必须是任务列表')
    jobs = []
    ids = set()
    for item in configured_jobs:
        if not isinstance(item, dict) or any(not isinstance(item.get(key), str) or not item[key].strip() for key in ('id', 'file', 'cwd')):
            raise ValueError('每项 Goal 必须提供非空 id、file、cwd')
        jid = item['id'].strip()
        if jid in ids or not all(ch.isalnum() or ch in '-_' for ch in jid):
            raise ValueError(f'Goal id 重复或包含不支持的字符：{jid}')
        ids.add(jid)
        file = _path(item['file'], base)
        cwd = _path(item['cwd'], base)
        if not cwd.is_dir():
            raise ValueError(f'Goal 工作目录不存在：{cwd}')
        budget = item.get('token_budget')
        if budget is not None:
            budget = _positive_int(budget, f'{jid}.token_budget')
        jobs.append(Job(jid, file, cwd, budget))
    thread_ids = take.get('thread_ids', [])
    workspaces = take.get('workspaces', [])
    if not isinstance(thread_ids, list) or any(not isinstance(x, str) or not x.strip() for x in thread_ids):
        raise ValueError('takeover.thread_ids 必须是非空字符串列表')
    if not isinstance(workspaces, list) or any(not isinstance(x, str) or not x.strip() for x in workspaces):
        raise ValueError('takeover.workspaces 必须是目录列表')
    workspace_paths = tuple(_path(x, base) for x in workspaces)
    if any(not x.is_dir() for x in workspace_paths):
        raise ValueError('takeover.workspaces 中有不存在的目录')
    mode = keep.get('mode', 'status')
    if mode not in ('status', 'weather'):
        raise ValueError('keepalive.mode 只能是 status 或 weather')
    retries = probe.get('session_retries', 5)
    if type(retries) is not int or retries < 0:
        raise ValueError('probe.session_retries 必须是非负整数')
    for name, value in [('takeover.enabled', take.get('enabled', True)),
                        ('runtime.auto_start_daemon', runtime.get('auto_start_daemon', True))]:
        if type(value) is not bool:
            raise ValueError(f'{name} 必须是布尔值')
    return Config(
        home=home, jobs=tuple(jobs), thread_ids=tuple(thread_ids),
        workspaces=workspace_paths, takeover_enabled=take.get('enabled', True),
        takeover_after=_positive_number(take.get('takeover_after_seconds', 30), 'takeover_after_seconds'),
        probe_interval=_positive_number(probe.get('interval_seconds', 3), 'probe.interval_seconds'),
        probe_timeout=_positive_int(probe.get('timeout_seconds', 120), 'probe.timeout_seconds'),
        session_retries=retries, keepalive_mode=mode,
        keepalive_interval=_positive_number(keep.get('interval_seconds', 10), 'keepalive.interval_seconds'),
        poll_interval=_positive_number(runtime.get('poll_interval_seconds', 2), 'runtime.poll_interval_seconds'),
        auto_start_daemon=runtime.get('auto_start_daemon', True),
    )
