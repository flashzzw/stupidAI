"""串行协调人工会话、Goal 和保活。"""

from __future__ import annotations

import shlex
import time
import uuid
from pathlib import Path

from weather import CITIES

from .config import Config, PROJECT_ROOT
from .goals import Goals, _same_project
from .probe import Probe
from .rpc import AppServer, RpcDisconnected, RpcError
from .state import State
from .takeover import Takeover, _queue_items


class Supervisor:
    def __init__(self, config: Config, state: State):
        self.config, self.state = config, state
        self.rpc = AppServer(config.home, config.socket)
        self.probe = Probe(config, state)
        self.takeover = Takeover(config, state, self.rpc)
        self.goals = Goals(config, state, self.rpc, self.probe, self.takeover)
        self.next_discovery = 0.0
        self.next_reconcile = 0.0

    def _human_conflict(self, cwd: str) -> bool:
        now = time.time()
        for watch in self.state.watched():
            if not watch['cwd'] or not _same_project(watch['cwd'], cwd):
                continue
            if now - watch['last_human'] < self.config.takeover_after:
                return True
            try:
                thread = self.rpc.call('thread/read', {'threadId': watch['thread_id']})['thread']
            except RpcError:
                continue
            if thread.get('status', {}).get('type') == 'active':
                return True
            if any(not self.takeover._owned(item.get('clientUserMessageId') or '')
                   for item in _queue_items(self.rpc, watch['thread_id'])):
                return True
        return False

    def _events(self) -> None:
        for event in self.rpc.drain_events():
            self.takeover.on_event(event)
            self.goals.on_event(event)
            params = event.get('params') or {}
            if event.get('method') == 'turn/completed':
                turn = params.get('turn') or {}
                if turn.get('status') == 'completed':
                    thread_id = params.get('threadId')
                    if self.state.job_by_thread(thread_id):
                        self.probe.mark_success()
                    else:
                        try:
                            thread = self.rpc.call('thread/read', {'threadId': thread_id})['thread']
                            if thread.get('modelProvider') == self.config.model_provider:
                                self.probe.mark_success()
                        except RpcError:
                            pass
                for send in self.state.recent_sends(params.get('threadId', '')):
                    if (send['turn_id'] == turn.get('id') and send['kind'].startswith('weather:')
                            and turn.get('status') == 'completed'):
                        city_idx = int(send['kind'].split(':')[1])
                        self.state.put('weather_index', str((city_idx + 1) % len(CITIES)))
                        self.state.put('weather_advanced_send', send['id'])

    def _paused(self) -> None:
        for watch in self.state.watched():
            self.takeover.cancel_pending(watch['thread_id'])
        job = self.state.current_job()
        if job and job['status'] == 'running' and job['thread_id']:
            goal = self.rpc.call('thread/goal/get', {'threadId': job['thread_id']}).get('goal')
            if goal and goal.get('status') == 'active':
                result = self.rpc.call('thread/goal/set', {'threadId': job['thread_id'],
                                                           'status': 'paused'})
                self.state.update_job(job['id'], status='paused_by_guard',
                                      pause_reason='托管器全局暂停',
                                      paused_version=result['goal']['updatedAt'])

    def _keepalive_thread(self) -> str | None:
        if self.config.jobs:
            rows = self.state.jobs()
            if any(row['status'] != 'complete' for row in rows if row['id'] in {j.id for j in self.config.jobs}):
                return None
            return next((row['thread_id'] for row in reversed(rows)
                         if row['id'] in {j.id for j in self.config.jobs} and row['thread_id']), None)
        thread_id = self.state.get('keepalive_thread')
        if thread_id:
            return thread_id
        batch = self.state.get('keepalive_batch')
        if not batch:
            batch = uuid.uuid4().hex
            self.state.put('keepalive_batch', batch)
        cursor = None
        found = []
        while True:
            params = {'sourceKinds': ['appServer'], 'limit': 100}
            if cursor:
                params['cursor'] = cursor
            page = self.rpc.call('thread/list', params)
            found.extend(x['id'] for x in page.get('data', [])
                         if x.get('threadSource') == f'netcap.keepalive.{batch}')
            cursor = page.get('nextCursor')
            if not cursor:
                break
        if len(found) > 1:
            self.state.log('keepalive', '发现多个同批次保活会话；停止以待核对')
            return None
        if found:
            thread_id = found[0]
        else:
            result = self.rpc.call('thread/start', {
                'cwd': str(PROJECT_ROOT), 'approvalPolicy': 'never', 'sandbox': 'read-only',
                'threadSource': f'netcap.keepalive.{batch}', 'ephemeral': False})
            thread_id = result['thread']['id']
        self.state.put('keepalive_thread', thread_id)
        return thread_id

    def _keepalive(self) -> None:
        if self.state.current_job() or self.state.get('paused', '0') == '1':
            return
        if not self.probe.available:
            cwd = str(self.config.jobs[-1].cwd if self.config.jobs else PROJECT_ROOT)
            self.probe.ensure(cwd)
            return
        thread_id = self._keepalive_thread()
        if not thread_id:
            return
        thread = self.rpc.call('thread/read', {'threadId': thread_id})['thread']
        if thread.get('status', {}).get('type') == 'notLoaded':
            self.rpc.call('thread/resume', {'threadId': thread_id})
            return
        if thread.get('status', {}).get('type') != 'idle' or _queue_items(self.rpc, thread_id):
            return
        if not self.state.watch_row(thread_id):
            self.state.watch(thread_id, thread.get('cwd'))
            self.state.update_watch(thread_id, initialized=1)
            return
        self.takeover.reconcile_sends(thread_id)
        watch = self.state.watch_row(thread_id)
        for send in self.state.recent_sends(thread_id):
            if (send['kind'].startswith('weather:') and send['status'] == 'done'
                    and self.state.get('weather_advanced_send') != send['id']):
                city_idx = int(send['kind'].split(':')[1])
                self.state.put('weather_index', str((city_idx + 1) % len(CITIES)))
                self.state.put('weather_advanced_send', send['id'])
                break
        if (time.time() - max(float(self.state.get('keepalive_at', '0')),
                              watch['last_human']) < self.config.keepalive_interval
                or self.state.unresolved_sends(thread_id)):
            return
        if self.config.keepalive_mode == 'weather':
            index = int(self.state.get('weather_index', '0')) % len(CITIES)
            city = CITIES[index]
            command = shlex.join(['python3', str(PROJECT_ROOT / 'weather.py'), city])
            prompt = f'请执行一次 {command} 并原样返回单行结果，失败则返回错误。'
            kind = f'weather:{index}'
        else:
            command = shlex.join(['python3', str(PROJECT_ROOT / 'runtime_status.py'),
                                   '--state-dir', str(self.config.state_dir),
                                   '--cwd', str(thread.get('cwd') or PROJECT_ROOT)])
            prompt = f'请执行一次 {command}，只返回脚本的一行输出。'
            kind = 'status'
        if self.takeover.send(thread_id, kind, prompt, watch['activity_version']):
            self.state.put('keepalive_at', str(time.time()))

    def _tick(self) -> None:
        self._events()
        self.probe.poll()
        # unwatch 由管理命令直接更新数据库；尚在服务端排队的自动消息必须撤销。
        for row in self.state.all('SELECT thread_id FROM watches WHERE enabled=0'):
            if self.state.unresolved_sends(row['thread_id']):
                self.takeover.cancel_pending(row['thread_id'])
        now = time.time()
        if now >= self.next_discovery:
            self.takeover.register_configured()
            self.next_discovery = now + 10
        if now >= self.next_reconcile:
            for send in self.state.unresolved_sends():
                self.takeover.reconcile_sends(send['thread_id'])
            self.next_reconcile = now + 10
        if self.state.get('paused', '0') == '1':
            self._paused()
            return
        current = self.goals.tick(self._human_conflict)
        watched = self.state.watched()
        provider_ready = self.probe.available
        if watched and not provider_ready:
            self.probe.ensure(watched[0]['cwd'] or str(PROJECT_ROOT))
        for watch in watched:
            if not self.config.takeover_enabled:
                break
            if self.state.job_by_thread(watch['thread_id']) or watch['thread_id'] == self.state.get('keepalive_thread'):
                continue
            if current and current.get('cwd') and watch['cwd'] and _same_project(current['cwd'], watch['cwd']):
                continue
            try:
                self.takeover.tick_watch(watch['thread_id'], allow_send=provider_ready)
            except RpcError as exc:
                self.state.log('watch_error', f'thread={watch["thread_id"]} {str(exc)[:180]}')
        self._keepalive()
        self.state.put('heartbeat_at', str(int(time.time())))

    def run(self) -> None:
        self.state.acquire_lock()
        try:
            self.state.sync_jobs()
            if self.state.get('provider_key') != self.config.model_provider:
                self.state.put('provider', 'unknown')
                self.state.put('provider_key', self.config.model_provider)
                self.state.put('provider_retry_at', '0')
            self.state.log('start', f'home={self.config.home} socket={self.config.socket}')
            while True:
                try:
                    if self.rpc.sock is None:
                        self.rpc.connect(auto_start=self.config.auto_start_daemon)
                        self.takeover.reset_subscriptions()
                        self.state.put('service', 'connected')
                        self.next_discovery = self.next_reconcile = 0
                        self.state.log('service', f'connected socket={self.config.socket}')
                    self._tick()
                    time.sleep(self.config.poll_interval)
                except RpcDisconnected as exc:
                    self.state.put('service', 'disconnected')
                    self.state.log('service', f'disconnected: {str(exc)[:200]}')
                    self.rpc.close()
                    time.sleep(self.config.probe_interval)
                except RpcError as exc:
                    self.state.log('rpc_error', str(exc)[:200])
                    time.sleep(self.config.poll_interval)
        finally:
            self.probe.close()
            self.rpc.close()
            self.state.close()
