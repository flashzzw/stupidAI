"""人工会话接管与可撤销的自动消息。"""

from __future__ import annotations

import time
import uuid
from pathlib import Path

from .config import Config, PROJECT_ROOT, TAKEOVER_PROMPT
from .rpc import AppServer, RpcDisconnected, RpcError
from .state import State


def _queue_items(rpc: AppServer, thread_id: str) -> list[dict]:
    items = []
    cursor = None
    while True:
        params = {'threadId': thread_id}
        if cursor:
            params['cursor'] = cursor
        result = rpc.call('thread/queue/list', params)
        items.extend(result.get('data', []))
        cursor = result.get('nextCursor')
        if not cursor:
            return items


def _recent_turns(rpc: AppServer, thread_id: str, limit: int = 12) -> list[dict]:
    result = rpc.call('thread/turns/list', {'threadId': thread_id, 'limit': limit,
                                           'sortDirection': 'desc', 'itemsView': 'full'})
    return result.get('data', [])


def _is_idle(thread: dict) -> bool:
    return thread.get('status', {}).get('type') == 'idle'


class Takeover:
    def __init__(self, config: Config, state: State, rpc: AppServer):
        self.config = config
        self.state = state
        self.rpc = rpc
        self.subscribed: set[str] = set()

    def reset_subscriptions(self) -> None:
        self.subscribed.clear()

    def _owned(self, client_id: str) -> bool:
        return bool(client_id and self.state.one('SELECT id FROM sends WHERE id=?', (client_id,)))

    def register_configured(self) -> None:
        if not self.config.takeover_enabled:
            return
        for thread_id in self.config.thread_ids:
            self.state.watch(thread_id)
        if not self.config.workspaces:
            return
        cursor = None
        while True:
            request = {'cursor': cursor} if cursor else {}
            result = self.rpc.call('thread/loaded/list', request)
            for thread_id in result.get('data', []):
                if self.state.job_by_thread(thread_id):
                    continue
                try:
                    thread = self.rpc.call('thread/read', {'threadId': thread_id})['thread']
                except RpcError:
                    continue
                if thread.get('parentThreadId') or str(thread.get('threadSource') or '').startswith('netcap.'):
                    continue
                cwd = Path(thread.get('cwd') or '/').resolve()
                if any(cwd == base or base in cwd.parents for base in self.config.workspaces):
                    self.state.watch(thread_id, str(cwd))
            cursor = result.get('nextCursor')
            if not cursor:
                break

    def _record_user_item(self, thread_id: str, item: dict) -> None:
        if item.get('type') != 'userMessage' or not item.get('id'):
            return
        client_id = item.get('clientId') or ''
        if not self._owned(client_id):
            if self.state.human(thread_id, item['id']):
                self.cancel_pending(thread_id)
        else:
            self.state.seen(thread_id, item['id'])

    def on_event(self, event: dict) -> None:
        method = event.get('method')
        params = event.get('params') or {}
        thread_id = params.get('threadId')
        if not thread_id:
            return
        if method in ('item/started', 'item/completed') and self.state.watch_row(thread_id):
            self._record_user_item(thread_id, params.get('item') or {})
        if method == 'turn/completed':
            turn = params.get('turn') or {}
            if self.state.watch_row(thread_id):
                self.state.update_watch(thread_id, last_completed=time.time())
            for send in self.state.unresolved_sends(thread_id):
                if send['turn_id'] == turn.get('id'):
                    self.state.update_send(send['id'], status='done')

    def cancel_pending(self, thread_id: str) -> None:
        for send in self.state.unresolved_sends(thread_id):
            if send['status'] == 'started':
                continue
            if send['queue_id']:
                try:
                    self.rpc.call('thread/queue/delete', {'threadId': thread_id,
                                                           'queuedSubmissionId': send['queue_id']})
                except (RpcError, RpcDisconnected):
                    self.state.update_send(send['id'], status='uncertain')
                    continue
            self.state.update_send(send['id'], status='cancelled')

    def reconcile_sends(self, thread_id: str) -> None:
        unresolved = self.state.unresolved_sends(thread_id)
        if not unresolved:
            return
        queued = {x.get('clientUserMessageId'): x for x in _queue_items(self.rpc, thread_id)}
        turns = []
        cursor = None
        oldest_send = min(send['created_at'] for send in unresolved)
        for _ in range(20):
            request = {'threadId': thread_id, 'limit': 50,
                       'sortDirection': 'desc', 'itemsView': 'full'}
            if cursor:
                request['cursor'] = cursor
            page = self.rpc.call('thread/turns/list', request)
            batch = page.get('data', [])
            turns.extend(batch)
            cursor = page.get('nextCursor')
            if not cursor or not batch or (batch[-1].get('startedAt') or 0) < oldest_send - 60:
                break
        by_client = {}
        for turn in turns:
            for item in turn.get('items', []):
                if item.get('type') == 'userMessage' and item.get('clientId'):
                    by_client[item['clientId']] = turn
        for send in unresolved:
            if send['id'] in by_client:
                turn = by_client[send['id']]
                self.state.update_send(send['id'], status='done' if turn.get('status') != 'inProgress' else 'started',
                                       turn_id=turn.get('id'))
                watch = self.state.watch_row(thread_id)
                if watch:
                    fields = {'last_auto': max(watch['last_auto'], send['created_at'])}
                    if send['kind'] == 'init':
                        fields['initialized'] = 1
                    self.state.update_watch(thread_id, **fields)
            elif send['id'] in queued:
                self.state.update_send(send['id'], status='queued',
                                       queue_id=queued[send['id']]['id'])
            else:
                # 请求可能已经生效但历史分页尚未覆盖它。保持阻塞以避免重复发送。
                self.state.update_send(send['id'], status='uncertain')

    def _started_automatically(self, thread_id: str, send_id: str, kind: str) -> bool:
        for turn in _recent_turns(self.rpc, thread_id):
            if any(item.get('type') == 'userMessage' and item.get('clientId') == send_id
                   for item in turn.get('items', [])):
                self.state.update_send(send_id, status='started' if turn.get('status') == 'inProgress' else 'done',
                                       turn_id=turn.get('id'))
                watch = self.state.watch_row(thread_id)
                if watch:
                    fields = {'last_auto': time.time()}
                    if kind == 'init':
                        fields['initialized'] = 1
                    self.state.update_watch(thread_id, **fields)
                self.state.log('send', f'kind={kind} thread={thread_id} id={send_id} auto_started=1')
                return True
        return False

    def send(self, thread_id: str, kind: str, prompt: str, activity_version: int = 0) -> bool:
        if self.state.unresolved_sends(thread_id):
            return False
        send_id = 'netcap:' + uuid.uuid4().hex
        self.state.send(send_id, thread_id, kind, prompt, activity_version)
        try:
            watch = self.state.watch_row(thread_id)
            if (self.state.get('paused', '0') == '1'
                    or (watch and watch['activity_version'] != activity_version)
                    or _queue_items(self.rpc, thread_id)
                    or not _is_idle(self.rpc.call('thread/read', {'threadId': thread_id})['thread'])):
                self.state.update_send(send_id, status='cancelled')
                return False
            result = self.rpc.call('thread/queue/add', {
                'threadId': thread_id, 'clientUserMessageId': send_id,
                'input': [{'type': 'text', 'text': prompt}]})
            queue_id = result['queuedSubmission']['id']
            self.state.update_send(send_id, status='queued', queue_id=queue_id)
            queue = _queue_items(self.rpc, thread_id)
            own = next((i for i in queue if i.get('id') == queue_id), None)
            watch = self.state.watch_row(thread_id)
            if not own:
                if self._started_automatically(thread_id, send_id, kind):
                    return True
                self.state.update_send(send_id, status='uncertain')
                return False
            if (len(queue) != 1
                    or (watch and watch['activity_version'] != activity_version)
                    or not _is_idle(self.rpc.call('thread/read', {'threadId': thread_id})['thread'])):
                if self._started_automatically(thread_id, send_id, kind):
                    return True
                self.cancel_pending(thread_id)
                return False
            try:
                result = self.rpc.call('thread/queue/start', {
                    'threadId': thread_id, 'queuedSubmissionId': queue_id})
            except RpcError:
                if self._started_automatically(thread_id, send_id, kind):
                    return True
                self.cancel_pending(thread_id)
                return False
            self.state.update_send(send_id, status='started', turn_id=result['turn']['id'])
            if watch:
                fields = {'last_auto': time.time()}
                if kind == 'init':
                    fields['initialized'] = 1
                self.state.update_watch(thread_id, **fields)
            self.state.log('send', f'kind={kind} thread={thread_id} id={send_id}')
            return True
        except RpcError as exc:
            self.state.log('send_error', f'thread={thread_id} kind={kind} {exc}')
            self.cancel_pending(thread_id)
            return False
        except RpcDisconnected:
            self.state.update_send(send_id, status='uncertain')
            raise

    def tick_watch(self, thread_id: str, allow_send: bool = True) -> None:
        watch = self.state.watch_row(thread_id)
        if not watch:
            return
        thread = self.rpc.call('thread/read', {'threadId': thread_id})['thread']
        if thread.get('parentThreadId'):
            self.state.unwatch(thread_id)
            return
        if thread_id not in self.subscribed:
            try:
                self.rpc.call('thread/resume', {'threadId': thread_id})
            except RpcError as exc:
                if 'no rollout found' in str(exc):
                    return
                raise
            self.subscribed.add(thread_id)
            self.state.update_watch(thread_id, last_completed=time.time())
            return
        if not watch['cwd']:
            self.state.update_watch(thread_id, cwd=thread.get('cwd'))
        for turn in reversed(_recent_turns(self.rpc, thread_id)):
            for item in turn.get('items', []):
                self._record_user_item(thread_id, item)
        queue = _queue_items(self.rpc, thread_id)
        for item in queue:
            if not self._owned(item.get('clientUserMessageId') or ''):
                if self.state.human(thread_id, 'queue:' + item['id']):
                    self.cancel_pending(thread_id)
        self.reconcile_sends(thread_id)
        watch = self.state.watch_row(thread_id)
        if not allow_send or self.state.get('paused', '0') == '1' or not _is_idle(thread) or queue:
            return
        goal = self.rpc.call('thread/goal/get', {'threadId': thread_id}).get('goal')
        if goal and goal.get('status') != 'complete':
            return
        if self.state.unresolved_sends(thread_id):
            return
        now = time.time()
        if not watch['initialized']:
            cmd = f'python3 {PROJECT_ROOT / "runtime_status.py"} --state-dir {self.config.state_dir}'
            prompt = ('以后当收到“运行状态检查”请求时，请执行一次以下只读命令并只返回一行输出：'
                      f'{cmd}。这条消息仅登记检查方式。')
            self.send(thread_id, 'init', prompt, watch['activity_version'])
            return
        recent = self.state.recent_sends(thread_id)
        last_kind = recent[0]['kind'] if recent else ''
        wait = (self.config.keepalive_interval if last_kind == 'takeover'
                else self.config.takeover_after)
        if now - max(watch['last_human'], watch['last_completed'], watch['last_auto']) < wait:
            return
        # 项目占用由 supervisor 在调用前检查。
        self.send(thread_id, 'takeover', TAKEOVER_PROMPT, watch['activity_version'])
