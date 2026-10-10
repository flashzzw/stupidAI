#!/usr/bin/env python3
"""只读、单次输出的本机运行状态简报。"""

from __future__ import annotations

import argparse
import os
import shutil
import sqlite3
from datetime import datetime, timezone
from pathlib import Path


def status_line(state_dir: Path, cwd: Path) -> str:
    when = datetime.now(timezone.utc).strftime('%H:%MZ')
    free_gb = shutil.disk_usage(cwd).free / 1024 ** 3
    try:
        load = f'{os.getloadavg()[0]:.1f}'
    except OSError:
        load = '未知'
    done = total = 0
    error = '无新增错误'
    database = state_dir / 'guard.sqlite3'
    if database.is_file():
        with sqlite3.connect(f'file:{database}?mode=ro', uri=True) as db:
            done, total = db.execute("SELECT COALESCE(SUM(status='complete'),0),COUNT(*) FROM jobs").fetchone()
            row = db.execute("SELECT detail FROM event_log WHERE topic IN "
                             "('provider','send_error','job_wait') ORDER BY at DESC LIMIT 1").fetchone()
            if row:
                error = '最近错误：' + ' '.join(str(row[0]).split())[:70]
    return f'{when}｜磁盘余量{free_gb:.1f}GB｜负载{load}｜任务{done}/{total}｜{error}'


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state-dir', type=Path, default=Path('/home/zzw/.codex/netcap'))
    parser.add_argument('--cwd', type=Path, default=Path.cwd())
    args = parser.parse_args()
    try:
        print(status_line(args.state_dir, args.cwd))
    except (OSError, sqlite3.Error, ValueError) as exc:
        print('运行状态检查失败：' + ' '.join(str(exc).split())[:120])
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
