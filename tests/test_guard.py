"""离线状态机验证；协议联调使用临时 CODEX_HOME 和本机模拟供应商。"""

from __future__ import annotations

import os
import signal
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch

from netcap.config import DEFAULT_HOME, TAKEOVER_PROMPT, load_config
from netcap.goals import Goals
from netcap.probe import Probe
from netcap.rpc import AppServer
from netcap.state import State
from netcap.supervisor import Supervisor
from netcap.takeover import Takeover
from runtime_status import status_line


class FakeServer:
    def __init__(self, manual_queue_on_add: bool = False, auto_start_on_add: bool = False):
        self.threads = {}
        self.goals = {}
        self.queue = {}
        self.started = []
        self.prompt = None
        self.manual_queue_on_add = manual_queue_on_add
        self.auto_start_on_add = auto_start_on_add
        self.turns = {}
        self.counter = 0

    def call(self, method, params=None):
        params = params or {}
        tid = params.get('threadId')
        if method == 'thread/start':
            self.counter += 1
            tid = f'thread-{self.counter}'
            self.threads[tid] = {'id': tid, 'cwd': params['cwd'],
                                 'threadSource': params.get('threadSource'),
                                 'status': {'type': 'idle'}}
            return {'thread': self.threads[tid]}
        if method == 'thread/loaded/list':
            return {'data': list(self.threads)}
        if method == 'thread/list':
            return {'data': list(self.threads.values())}
        if method in ('thread/read', 'thread/resume'):
            return {'thread': self.threads[tid]}
        if method == 'thread/turns/list':
            return {'data': self.turns.get(tid, [])}
        if method == 'thread/goal/get':
            return {'goal': self.goals.get(tid)}
        if method == 'thread/goal/set':
            old = self.goals.get(tid, {})
            goal = {'objective': params.get('objective', old.get('objective')),
                    'status': params['status'], 'updatedAt': self.counter + 1}
            self.goals[tid] = goal
            return {'goal': goal}
        if method == 'thread/queue/list':
            return {'data': self.queue.get(tid, []).copy()}
        if method == 'thread/queue/add':
            self.counter += 1
            item = {'id': f'queue-{self.counter}', 'clientUserMessageId': params['clientUserMessageId']}
            self.queue.setdefault(tid, []).append(item)
            self.prompt = params['input'][0]['text']
            if self.auto_start_on_add:
                self.queue[tid].remove(item)
                self.threads[tid]['status'] = {'type': 'active', 'activeFlags': []}
                self.turns[tid] = [{'id': 'auto-turn', 'status': 'inProgress',
                                    'items': [{'type': 'userMessage', 'id': 'auto-item',
                                               'clientId': params['clientUserMessageId']}]}]
            if self.manual_queue_on_add:
                self.queue[tid].append({'id': 'manual-queue', 'clientUserMessageId': 'user'})
            return {'queuedSubmission': item}
        if method == 'thread/queue/delete':
            self.queue[tid] = [x for x in self.queue[tid]
                               if x['id'] != params['queuedSubmissionId']]
            return {}
        if method == 'thread/queue/start':
            self.started.append(params['queuedSubmissionId'])
            self.queue[tid] = [x for x in self.queue[tid]
                               if x['id'] != params['queuedSubmissionId']]
            return {'turn': {'id': 'turn-1'}}
        raise AssertionError(method)


class GuardTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='netcap-test-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config_file = self.root / 'guard.toml'
        self.config_file.write_text(f'codex_home = "{self.root / "home"}"\n')
        self.config = load_config(self.config_file)
        self.state = State(self.config)
        self.addCleanup(self.state.close)

    def test_default_home_is_independent_of_environment(self):
        with patch.dict(os.environ, {'CODEX_HOME': str(self.root / 'other')}):
            self.assertEqual(load_config().home, DEFAULT_HOME)

    def test_takeover_uses_exact_prompt(self):
        server = FakeServer()
        server.threads['manual'] = {'id': 'manual', 'cwd': str(self.root),
                                    'status': {'type': 'idle'}}
        self.state.watch('manual', str(self.root))
        self.state.update_watch('manual', initialized=1, last_human=time.time()-100,
                                last_completed=time.time()-100)
        takeover = Takeover(self.config, self.state, server)
        takeover.subscribed.add('manual')
        takeover.tick_watch('manual')
        self.assertEqual(server.prompt, TAKEOVER_PROMPT)
        self.assertEqual(len(server.started), 1)
        self.assertEqual(self.state.recent_sends('manual')[0]['kind'], 'takeover')

    def test_unavailable_provider_suppresses_automatic_reply(self):
        server = FakeServer()
        server.threads['manual'] = {'id': 'manual', 'cwd': str(self.root),
                                    'status': {'type': 'idle'}}
        self.state.watch('manual', str(self.root))
        self.state.update_watch('manual', initialized=1, last_human=time.time()-100,
                                last_completed=time.time()-100)
        takeover = Takeover(self.config, self.state, server)
        takeover.subscribed.add('manual')
        takeover.tick_watch('manual', allow_send=False)
        self.assertEqual(self.state.recent_sends('manual'), [])

    def test_human_message_wins_race_after_automatic_enqueue(self):
        server = FakeServer(manual_queue_on_add=True)
        server.threads['manual'] = {'id': 'manual', 'cwd': str(self.root),
                                    'status': {'type': 'idle'}}
        self.state.watch('manual', str(self.root))
        takeover = Takeover(self.config, self.state, server)
        self.assertFalse(takeover.send('manual', 'takeover', TAKEOVER_PROMPT, 0))
        self.assertEqual(server.started, [])
        self.assertEqual([x['id'] for x in server.queue['manual']], ['manual-queue'])
        self.assertEqual(self.state.recent_sends('manual')[0]['status'], 'cancelled')

    def test_queue_item_started_by_server_is_recorded_once(self):
        server = FakeServer(auto_start_on_add=True)
        server.threads['manual'] = {'id': 'manual', 'cwd': str(self.root),
                                    'status': {'type': 'idle'}}
        self.state.watch('manual', str(self.root))
        takeover = Takeover(self.config, self.state, server)
        self.assertTrue(takeover.send('manual', 'init', '只读检查说明', 0))
        self.assertEqual(server.started, [])
        self.assertEqual(self.state.recent_sends('manual')[0]['status'], 'started')
        self.assertEqual(self.state.watch_row('manual')['initialized'], 1)

    def test_goals_advance_only_after_native_completion(self):
        first = self.root / 'first.md'
        second = self.root / 'second.md'
        first.write_text('完成第一项。')
        second.write_text('完成第二项。')
        self.config_file.write_text(
            f'codex_home = "{self.root / "home"}"\n'
            f'[[goals]]\nid="first"\nfile="{first}"\ncwd="{self.root}"\n'
            f'[[goals]]\nid="second"\nfile="{second}"\ncwd="{self.root}"\n')
        config = load_config(self.config_file)
        state = State(config)
        self.addCleanup(state.close)
        state.sync_jobs()
        Probe(config, state).mark_success()
        server = FakeServer()
        goals = Goals(config, state, server, Probe(config, state))
        goals.tick(lambda _: False)
        first_job = state.job('first')
        self.assertEqual(first_job['status'], 'running')
        self.assertIsNone(state.job('second')['thread_id'])
        goals.tick(lambda _: False)
        self.assertIsNone(state.job('second')['thread_id'])
        server.goals[first_job['thread_id']]['status'] = 'complete'
        goals.tick(lambda _: False)
        self.assertEqual(state.job('first')['status'], 'complete')
        goals.tick(lambda _: False)
        self.assertEqual(state.job('second')['status'], 'running')

    def test_network_recovery_replies_once_in_original_goal_thread(self):
        source = self.root / 'goal.md'
        source.write_text('完成任务')
        self.config_file.write_text(
            f'codex_home="{self.config.home}"\n[[goals]]\nid="work"\n'
            f'file="{source}"\ncwd="{self.root}"\n')
        config = load_config(self.config_file)
        state = State(config)
        self.addCleanup(state.close)
        state.sync_jobs()
        probe = Probe(config, state)
        probe.mark_success()
        server = FakeServer()
        sender = Takeover(config, state, server)
        goals = Goals(config, state, server, probe, sender)
        goals.tick(lambda _: False)
        original = state.job('work')['thread_id']
        state.update_job('work', status='reconnecting', tries=1)
        goals.tick(lambda _: False)
        self.assertEqual(state.job('work')['thread_id'], original)
        self.assertEqual(state.job('work')['status'], 'running')
        self.assertEqual(state.recent_sends(original)[0]['kind'], 'goal-recover')
        goals.tick(lambda _: False)
        self.assertEqual(len(state.recent_sends(original)), 1)

    def test_probe_uses_selected_home_and_requires_success_events(self):
        from netcap.probe import Probe

        bin_dir = self.root / 'bin'
        bin_dir.mkdir()
        fake = bin_dir / 'codex'
        marker = self.root / 'probed-home'
        fake.write_text(
            '#!/usr/bin/env python3\n'
            'import json, os, pathlib\n'
            f'pathlib.Path({str(marker)!r}).write_text(os.environ["CODEX_HOME"])\n'
            'print(json.dumps({"type":"item.completed","item":{"type":"agent_message","text":os.environ["CODEX_HOME"]}}))\n'
            'print(json.dumps({"type":"turn.completed"}))\n')
        fake.chmod(0o755)
        with patch.dict(os.environ, {'PATH': str(bin_dir) + os.pathsep + os.environ['PATH'],
                                     'CODEX_HOME': str(self.root / 'wrong-home')}):
            self.assertTrue(Probe(self.config, self.state).check(str(self.root)))
        self.assertEqual(self.state.get('provider'), 'available')
        self.assertEqual(marker.read_text(), str(self.config.home))

    def test_probe_respects_retry_after_and_stops_on_auth_error(self):
        probe = Probe(self.config, self.state)
        probe.mark_failure('HTTP 429 Retry-After: 45')
        self.assertEqual(self.state.get('provider'), 'unavailable')
        self.assertGreater(probe.next_at - time.time(), 40)
        probe.mark_failure('HTTP 401 invalid_api_key sk-example-secret-value')
        self.assertEqual(self.state.get('provider'), 'configuration_error')
        self.assertEqual(probe.next_at, float('inf'))
        self.assertNotIn('sk-example-secret-value',
                         self.state.one('SELECT detail FROM event_log ORDER BY at DESC LIMIT 1')['detail'])
        self.state.put('provider', 'unknown')
        self.state.put('provider_retry_at', '0')
        with patch('netcap.probe.subprocess.Popen', side_effect=OSError('local test offline')):
            self.assertFalse(probe.check(str(self.root)))
        self.assertNotEqual(probe.next_at, float('inf'))

    def test_probe_does_not_block_session_loop(self):
        bin_dir = self.root / 'bin'
        bin_dir.mkdir()
        fake = bin_dir / 'codex'
        fake.write_text('#!/usr/bin/env python3\nimport time\ntime.sleep(10)\n')
        fake.chmod(0o755)
        probe = Probe(self.config, self.state)
        with patch.dict(os.environ, {'PATH': str(bin_dir) + os.pathsep + os.environ['PATH']}):
            started = time.monotonic()
            self.assertFalse(probe.ensure(str(self.root)))
            self.assertLess(time.monotonic() - started, 0.5)
            self.assertIsNotNone(probe.proc)
            probe.close()
            self.assertIsNone(probe.proc)

    def test_active_goal_uses_snapshot_after_source_is_removed(self):
        source = self.root / 'goal.md'
        source.write_text('完成原始任务')
        self.config_file.write_text(
            f'codex_home="{self.config.home}"\n[[goals]]\nid="work"\n'
            f'file="{source}"\ncwd="{self.root}"\n')
        config = load_config(self.config_file)
        state = State(config)
        self.addCleanup(state.close)
        state.sync_jobs()
        job = state.snapshot_job(state.job('work'), 'batch-1')
        source.unlink()
        reloaded = load_config(self.config_file)
        resumed = State(reloaded)
        self.addCleanup(resumed.close)
        resumed.sync_jobs()
        self.assertEqual(Path(job['snapshot']).read_text(), '完成原始任务')

    def test_removed_running_goal_is_rejected(self):
        source = self.root / 'goal.md'
        source.write_text('完成任务')
        self.config_file.write_text(
            f'codex_home="{self.config.home}"\n[[goals]]\nid="work"\n'
            f'file="{source}"\ncwd="{self.root}"\n')
        config = load_config(self.config_file)
        state = State(config)
        self.addCleanup(state.close)
        state.sync_jobs()
        state.update_job('work', status='running')
        self.config_file.write_text(f'codex_home="{self.config.home}"\n')
        removed = State(load_config(self.config_file))
        self.addCleanup(removed.close)
        with self.assertRaisesRegex(ValueError, '已从配置移除'):
            removed.sync_jobs()

    def test_reset_completed_goal_requires_explicit_command(self):
        source = self.root / 'goal.md'
        source.write_text('完成任务')
        self.config_file.write_text(
            f'codex_home="{self.config.home}"\n[[goals]]\nid="work"\n'
            f'file="{source}"\ncwd="{self.root}"\n')
        config = load_config(self.config_file)
        state = State(config)
        self.addCleanup(state.close)
        state.sync_jobs()
        state.update_job('work', status='complete', thread_id='old-thread', batch='old-batch')
        result = subprocess.run(
            [sys.executable, str(Path(__file__).resolve().parents[1] / 'codex_guard.py'),
             '--config', str(self.config_file), 'reset', 'work'],
            capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(state.job('work')['status'], 'pending')
        self.assertIsNone(state.job('work')['thread_id'])

    def test_status_script_and_keepalive_mode_switch(self):
        self.assertIn('任务0/0', status_line(self.config.state_dir, self.root))
        server = FakeServer()
        Probe(self.config, self.state).mark_success()
        supervisor = Supervisor(self.config, self.state)
        supervisor.rpc = server
        supervisor.takeover = Takeover(self.config, self.state, server)
        supervisor._keepalive()
        thread_id = self.state.get('keepalive_thread')
        self.assertTrue(thread_id)
        supervisor._keepalive()
        self.state.update_watch(thread_id, last_human=time.time()-100)
        supervisor._keepalive()
        self.assertEqual(self.state.recent_sends(thread_id)[0]['kind'], 'status')
        self.state.update_send(self.state.recent_sends(thread_id)[0]['id'], status='done')
        self.state.put('keepalive_at', '0')
        self.config_file.write_text(
            f'codex_home="{self.config.home}"\n[keepalive]\nmode="weather"\n')
        weather_config = load_config(self.config_file)
        supervisor.config = weather_config
        supervisor.takeover = Takeover(weather_config, self.state, server)
        supervisor._keepalive()
        self.assertEqual(self.state.recent_sends(thread_id)[0]['kind'], 'weather:0')

    def test_keepalive_waits_for_provider_recovery(self):
        server = FakeServer()
        supervisor = Supervisor(self.config, self.state)
        supervisor.rpc = server
        supervisor.takeover = Takeover(self.config, self.state, server)
        with patch.object(supervisor.probe, 'ensure', return_value=False) as ensure:
            supervisor._keepalive()
        ensure.assert_called_once()
        self.assertEqual(server.threads, {})

    def test_unwatch_removes_queued_auto_message_and_preserves_human_queue(self):
        server = FakeServer()
        server.threads['manual'] = {'id': 'manual', 'cwd': str(self.root),
                                    'status': {'type': 'idle'}}
        self.state.watch('manual', str(self.root))
        self.state.send('netcap:pending', 'manual', 'takeover', TAKEOVER_PROMPT, 0)
        server.queue['manual'] = [
            {'id': 'auto-queue', 'clientUserMessageId': 'netcap:pending'},
            {'id': 'manual-queue', 'clientUserMessageId': 'user'},
        ]
        self.state.update_send('netcap:pending', status='queued', queue_id='auto-queue')
        self.state.unwatch('manual')
        Takeover(self.config, self.state, server).cancel_pending('manual')
        self.assertEqual([x['id'] for x in server.queue['manual']], ['manual-queue'])
        self.assertEqual(self.state.recent_sends('manual')[0]['status'], 'cancelled')


@unittest.skipUnless(shutil.which('codex'), '需要本机 Codex CLI')
class AppServerProtocolTests(unittest.TestCase):
    def test_two_clients_share_goal_and_message_source(self):
        with tempfile.TemporaryDirectory(prefix='netcap-protocol-') as tmp:
            home = Path(tmp)
            (home / 'config.toml').write_text(
                'model="research-mock"\nmodel_provider="research"\n'
                '[model_providers.research]\nname="local research"\n'
                'base_url="http://127.0.0.1:9/v1"\nwire_api="responses"\n'
                'requires_openai_auth=false\n[features]\ngoals=true\n')
            env = {**os.environ, 'CODEX_HOME': str(home)}
            sock = home / 'shared.sock'

            def start_server():
                proc = subprocess.Popen(['codex', 'app-server', '--listen', 'unix://' + str(sock)],
                                        env=env, stdin=subprocess.DEVNULL,
                                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                        start_new_session=True)
                deadline = time.monotonic() + 5
                while not sock.exists() and time.monotonic() < deadline:
                    time.sleep(.05)
                self.assertTrue(sock.exists(), '模拟共享服务没有启动')
                return proc

            def stop_server(proc):
                if proc.poll() is not None:
                    return
                os.killpg(proc.pid, signal.SIGTERM)
                try:
                    proc.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    os.killpg(proc.pid, signal.SIGKILL)
                    proc.wait(timeout=3)

            proc = start_server()
            first, second = AppServer(home, sock), AppServer(home, sock)
            try:
                first.connect(False)
                second.connect(False)
                tid = first.call('thread/start', {'cwd': str(home), 'approvalPolicy': 'never',
                                                  'sandbox': 'danger-full-access',
                                                  'threadSource': 'netcap.goal.research'})['thread']['id']
                first.call('thread/goal/set', {'threadId': tid, 'objective': 'research only',
                                               'status': 'paused'})
                self.assertEqual(second.call('thread/resume', {'threadId': tid})['thread']['id'], tid)
                self.assertEqual(second.call('thread/goal/get', {'threadId': tid})['goal']['status'], 'paused')
                time.sleep(.1)
                self.assertTrue(any(e.get('method') == 'thread/goal/updated'
                                    for e in second.drain_events()))
                self.assertEqual(len(first.call('thread/list', {'sourceKinds': ['appServer']})['data']), 0)
                self.assertIn(tid, first.call('thread/loaded/list')['data'])
                first.close()
                second.close()
                stop_server(proc)
                if sock.exists():
                    sock.unlink()
                proc = start_server()
                third = AppServer(home, sock)
                try:
                    third.connect(False)
                    self.assertEqual(third.call('thread/goal/get', {'threadId': tid})['goal']['status'], 'paused')
                    self.assertEqual(third.call('thread/resume', {'threadId': tid})['thread']['id'], tid)
                    queued = third.call('thread/queue/add', {
                        'threadId': tid, 'clientUserMessageId': 'manual-research-message',
                        'input': [{'type': 'text', 'text': 'Local protocol research only.'}]})
                    waiting = third.call('thread/queue/list', {'threadId': tid})['data']
                    if any(item['id'] == queued['queuedSubmission']['id'] for item in waiting):
                        third.call('thread/queue/start', {
                            'threadId': tid,
                            'queuedSubmissionId': queued['queuedSubmission']['id']})
                    deadline = time.monotonic() + 5
                    found = False
                    while time.monotonic() < deadline:
                        turns = third.call('thread/turns/list', {
                            'threadId': tid, 'limit': 5, 'itemsView': 'full'})['data']
                        found = any(item.get('clientId') == 'manual-research-message'
                                    for turn in turns for item in turn.get('items', []))
                        if found:
                            break
                        time.sleep(.1)
                    self.assertTrue(found, '人工消息来源未出现在会话历史中')
                finally:
                    third.close()
            finally:
                first.close()
                second.close()
                stop_server(proc)


if __name__ == '__main__':
    unittest.main()
