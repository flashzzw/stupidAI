"""顺序执行文档任务，并以 Codex 原生 Goal 状态作为完成依据。"""

from __future__ import annotations

import time
import uuid
from pathlib import Path
from typing import Callable

from .config import Config
from .probe import Probe
from .rpc import AppServer, RpcError
from .state import State
from .takeover import Takeover, _queue_items

RECOVERY_BACKOFF_BASE = 30
RECOVERY_BACKOFF_MAX = 300


def _same_project(first: str, second: str) -> bool:
    a, b = Path(first).resolve(), Path(second).resolve()
    return a == b or a in b.parents or b in a.parents


class Goals:
    def __init__(self, config: Config, state: State, rpc: AppServer, probe: Probe,
                 sender: Takeover | None = None):
        self.config, self.state, self.rpc, self.probe = config, state, rpc, probe
        self.sender = sender

    def on_event(self, event: dict) -> None:
        params = event.get('params') or {}
        thread_id = params.get('threadId')
        if not thread_id:
            return
        job = self.state.job_by_thread(thread_id)
        if not job:
            return
        method = event.get('method')
        if method == 'thread/goal/updated':
            goal = params.get('goal') or {}
            self.state.update_job(job['id'], goal_status=goal.get('status'))
        if method == 'turn/completed':
            turn = params.get('turn') or {}
            if turn.get('status') == 'failed':
                tries = job['tries'] + 1
                self.state.update_job(job['id'], status='reconnecting', tries=tries,
                                      pause_reason='供应商请求失败；等待探测与原会话恢复')
                error = turn.get('error') or {}
                detail = error.get('message', '') if isinstance(error, dict) else str(error)
                self.probe.mark_failure(f'Goal {job["id"]} 轮次失败，保留会话 {thread_id}：{detail}')
            elif turn.get('status') == 'completed':
                self.probe.mark_success()
                self.state.update_job(job['id'], tries=0)

    def _created_thread(self, job: dict) -> str | None:
        matches = []
        loaded_cursor = None
        while True:
            request = {'cursor': loaded_cursor} if loaded_cursor else {}
            page = self.rpc.call('thread/loaded/list', request)
            for thread_id in page.get('data', []):
                try:
                    thread = self.rpc.call('thread/read', {'threadId': thread_id})['thread']
                except RpcError:
                    continue
                if thread.get('threadSource') == f'netcap.goal.{job["batch"]}':
                    matches.append(thread_id)
            loaded_cursor = page.get('nextCursor')
            if not loaded_cursor:
                break
        cursor = None
        while True:
            params = {'sourceKinds': ['appServer'], 'limit': 100}
            if cursor:
                params['cursor'] = cursor
            page = self.rpc.call('thread/list', params)
            matches.extend(t['id'] for t in page.get('data', [])
                           if t.get('threadSource') == f'netcap.goal.{job["batch"]}'
                           and t['id'] not in matches)
            cursor = page.get('nextCursor')
            if not cursor:
                break
        if len(matches) > 1:
            self.state.update_job(job['id'], status='blocked',
                                  pause_reason='发现多个同批次会话，需要人工核对')
            return None
        return matches[0] if matches else None

    def _ensure_thread(self, job: dict) -> dict:
        if job['thread_id']:
            return job
        existing = self._created_thread(job)
        if existing:
            self.state.update_job(job['id'], thread_id=existing, status='ready')
            return self.state.job(job['id'])
        if self.state.job(job['id'])['status'] == 'blocked':
            return self.state.job(job['id'])
        created = self.rpc.call('thread/start', {
            'cwd': job['cwd'], 'approvalPolicy': 'never', 'sandbox': 'danger-full-access',
            'threadSource': f'netcap.goal.{job["batch"]}', 'ephemeral': False,
        })
        thread_id = created['thread']['id']
        self.state.update_job(job['id'], thread_id=thread_id, status='ready')
        self.state.log('job_thread', f'job={job["id"]} thread={thread_id}')
        return self.state.job(job['id'])

    def _objective(self, job: dict) -> str:
        return (f'阅读并完成 {job["snapshot"]} 中的任务。按文档的验收条件核实完成；'
                '无法继续时说明具体阻塞。该文档是本次执行的固定快照。')

    def _goal(self, thread_id: str) -> dict | None:
        return self.rpc.call('thread/goal/get', {'threadId': thread_id}).get('goal')

    def tick(self, human_conflict: Callable[[str], bool]) -> dict | None:
        job = self.state.current_job()
        if not job:
            return None
        if self.state.get('paused', '0') == '1':
            return job
        if human_conflict(job['cwd']):
            if job['status'] == 'running' and job['thread_id']:
                goal = self._goal(job['thread_id'])
                if goal and goal.get('status') == 'active':
                    result = self.rpc.call('thread/goal/set', {
                        'threadId': job['thread_id'], 'status': 'paused'})
                    self.state.update_job(job['id'], status='paused_by_guard',
                                          pause_reason='同一项目有人工作',
                                          paused_version=result['goal']['updatedAt'])
            return self.state.job(job['id'])
        if job['status'] == 'pending':
            if not self.probe.available and not self.probe.ensure(job['cwd']):
                return job
            job = self.state.snapshot_job(job, uuid.uuid4().hex)
        if job['status'] == 'preparing':
            job = self._ensure_thread(job)
        if job['status'] == 'blocked' and job['thread_id']:
            native = self._goal(job['thread_id'])
            if native and native.get('status') in ('active', 'complete') and native.get('objective') == self._objective(job):
                self.state.update_job(job['id'], status='running', pause_reason=None)
                job = self.state.job(job['id'])
        if job['status'] == 'blocked':
            return job
        if not job['thread_id']:
            return job
        thread_id = job['thread_id']
        if job['status'] in ('ready', 'running', 'reconnecting', 'paused_by_guard', 'retry_requested'):
            thread = self.rpc.call('thread/read', {'threadId': thread_id})['thread']
            goal = self._goal(thread_id)
            if job['status'] == 'ready':
                if not goal:
                    params = {'threadId': thread_id, 'objective': self._objective(job),
                              'status': 'paused'}
                    if job['budget']:
                        params['tokenBudget'] = job['budget']
                    goal = self.rpc.call('thread/goal/set', params)['goal']
                if goal.get('objective') != self._objective(job):
                    self.state.update_job(job['id'], status='blocked',
                                          pause_reason='会话中的 Goal 与任务快照不一致')
                    return self.state.job(job['id'])
                if goal['status'] == 'paused':
                    goal = self.rpc.call('thread/goal/set', {'threadId': thread_id,
                                                             'status': 'active'})['goal']
                self.state.update_job(job['id'], status='running',
                                      goal_status=goal['status'], pause_reason=None)
                return self.state.job(job['id'])
            if not goal:
                self.state.update_job(job['id'], status='blocked',
                                      pause_reason='原会话中找不到 Goal，停止以避免重做')
                return self.state.job(job['id'])
            if goal.get('objective') != self._objective(job):
                self.state.update_job(job['id'], status='blocked',
                                      pause_reason='原会话 Goal 已由其他客户端修改')
                return self.state.job(job['id'])
            if job['goal_status'] != goal['status']:
                self.state.update_job(job['id'], goal_status=goal['status'])
            if goal['status'] == 'complete':
                if thread.get('status', {}).get('type') == 'idle' and not _queue_items(self.rpc, thread_id):
                    self.state.update_job(job['id'], status='complete', pause_reason=None)
                    self.state.log('job_complete', f'job={job["id"]} thread={thread_id}')
                return self.state.job(job['id'])
            if goal['status'] in ('blocked', 'usageLimited', 'budgetLimited'):
                if job['status'] == 'retry_requested' and goal['status'] == 'blocked':
                    goal = self.rpc.call('thread/goal/set', {'threadId': thread_id,
                                                             'status': 'active'})['goal']
                    self.state.update_job(job['id'], status='running', pause_reason=None)
                else:
                    self.state.update_job(job['id'], status='blocked',
                                          pause_reason=f'Goal 状态：{goal["status"]}')
                return self.state.job(job['id'])
            if goal['status'] == 'paused':
                if (job['status'] == 'paused_by_guard' and
                        job['paused_version'] == goal.get('updatedAt')):
                    self.rpc.call('thread/goal/set', {'threadId': thread_id,
                                                       'status': 'active'})
                    self.state.update_job(job['id'], status='running', pause_reason=None,
                                          paused_version=None)
                elif job['status'] == 'retry_requested':
                    self.rpc.call('thread/goal/set', {'threadId': thread_id,
                                                       'status': 'active'})
                    self.state.update_job(job['id'], status='running', pause_reason=None)
                else:
                    self.state.update_job(job['id'], status='blocked',
                                          pause_reason='Goal 已暂停，等待显式恢复')
                return self.state.job(job['id'])
            if job['status'] == 'reconnecting':
                if job['tries'] > self.config.session_retries:
                    over = min(job['tries'] - self.config.session_retries, 5)
                    backoff = min(RECOVERY_BACKOFF_MAX, RECOVERY_BACKOFF_BASE * (2 ** over))
                    if time.time() - job['updated_at'] < backoff:
                        return job
                if not self.probe.available and not self.probe.ensure(job['cwd']):
                    return job
                if thread.get('status', {}).get('type') == 'notLoaded':
                    self.rpc.call('thread/resume', {'threadId': thread_id})
                    return self.state.job(job['id'])
                if thread.get('status', {}).get('type') == 'idle':
                    if _queue_items(self.rpc, thread_id):
                        return job
                    if self.sender is None or not self.sender.send(
                            thread_id, 'goal-recover',
                            '请继续当前会话已设定的 Goal，先核对已有进度，避免重复已完成的操作。'):
                        return self.state.job(job['id'])
                if job['tries'] > self.config.session_retries:
                    self.state.log('job_wait', f'job={job["id"]} 重试耗尽；原会话仍保留')
                self.state.update_job(job['id'], status='running', pause_reason=None)
            elif job['status'] == 'retry_requested':
                self.state.update_job(job['id'], status='running', pause_reason=None)
            elif (job['status'] == 'running' and thread.get('status', {}).get('type') == 'idle'
                  and time.time() - job['updated_at'] > 300 and not job['pause_reason']):
                self.state.update_job(job['id'], pause_reason='Goal 仍活跃但长时间空闲；请检查原生续行状态')
                self.state.log('job_wait', f'job={job["id"]} 活跃 Goal 长时间空闲')
        return self.state.job(job['id'])

    def retry(self, job_id: str) -> None:
        job = self.state.job(job_id)
        if not job or job['status'] not in ('blocked', 'reconnecting'):
            raise ValueError('仅可显式重试当前阻塞或断线任务')
        if self.state.current_job()['id'] != job_id:
            raise ValueError('必须先处理更早的任务')
        if job['goal_status'] in ('usageLimited', 'budgetLimited'):
            raise ValueError('Goal 达到限制，不能通过重试绕过')
        self.state.update_job(job_id, status='retry_requested', pause_reason=None)
