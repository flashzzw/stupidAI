#!/usr/bin/env python3
"""管理 Codex 交互会话接管和顺序 Goal 任务。"""

from __future__ import annotations

import argparse
import json
import sys
import time
from pathlib import Path

from netcap.config import load_config
from netcap.state import State
from netcap.supervisor import Supervisor


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, help='TOML 配置文件；省略时使用默认参数')
    parser.add_argument('--codex-home', type=Path, help='覆盖默认 /home/zzw/.codex')
    commands = parser.add_subparsers(dest='command', required=True)
    for name in ('run', 'status', 'sessions', 'pause', 'resume', 'tasks'):
        commands.add_parser(name)
    commands.add_parser('watch').add_argument('thread_id')
    commands.add_parser('unwatch').add_argument('thread_id')
    commands.add_parser('retry').add_argument('task_id')
    commands.add_parser('reset').add_argument('task_id')
    args = parser.parse_args()
    try:
        config = load_config(args.config, args.codex_home)
        state = State(config)
        if args.command == 'run':
            Supervisor(config, state).run()
            return 0
        try:
            if args.command == 'status':
                print(json.dumps(state.summary(), ensure_ascii=False, indent=2))
            elif args.command == 'sessions':
                print(json.dumps(state.watched(), ensure_ascii=False, indent=2))
            elif args.command == 'tasks':
                print(json.dumps(state.jobs(), ensure_ascii=False, indent=2))
            elif args.command == 'watch':
                state.watch(args.thread_id)
                state.log('watch', f'thread={args.thread_id}')
            elif args.command == 'unwatch':
                state.unwatch(args.thread_id)
                state.log('unwatch', f'thread={args.thread_id}')
            elif args.command in ('pause', 'resume'):
                state.put('paused', '1' if args.command == 'pause' else '0')
                if args.command == 'resume' and state.get('provider') == 'configuration_error':
                    state.put('provider', 'unknown')
                    state.put('provider_retry_at', '0')
                state.log(args.command, f'home={config.home} at={int(time.time())}')
            elif args.command == 'retry':
                job = state.job(args.task_id)
                current = state.current_job()
                if not job or not current or current['id'] != job['id'] or job['status'] not in ('blocked', 'reconnecting'):
                    raise ValueError('只能重试当前阻塞或断线任务')
                if job['goal_status'] in ('budgetLimited', 'usageLimited'):
                    raise ValueError('Goal 达到预算或用量限制，不能通过重试绕过')
                state.update_job(job['id'], status='retry_requested', pause_reason=None)
                state.log('retry', f'job={job["id"]}')
            elif args.command == 'reset':
                job = state.job(args.task_id)
                if not job or job['status'] != 'complete' or state.current_job():
                    raise ValueError('仅在队列全部完成后重置已完成任务')
                if not Path(job['source']).is_file() or not Path(job['source']).read_text(encoding='utf-8').strip():
                    raise ValueError('任务原文档不存在或为空，无法重置')
                state.update_job(job['id'], status='pending', goal_status=None,
                                 thread_id=None, batch=None, snapshot=None, sha256=None,
                                 pause_reason=None, paused_version=None, tries=0)
                state.log('reset', f'job={job["id"]}')
        finally:
            state.close()
        return 0
    except KeyboardInterrupt:
        print('托管器已停止', file=sys.stderr)
        return 130
    except (OSError, ValueError, RuntimeError) as exc:
        print(f'错误：{exc}', file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
