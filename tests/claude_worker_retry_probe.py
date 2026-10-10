#!/usr/bin/env python3
"""Expose native worker retry while the original tool lives; verify production guards.

The provider requests the same owned Bash action again if a recovered main
prompt has lost the tool call/result. It never approves the second request.
A successful diagnostic run is not successful native tool-output recovery.
"""
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import traceback
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from execution_config_probe import CLAUDE, Provider, stop, verify_versions, wait_for, write
from native_claude_lifecycle_probe import alive, crash_owned
from workbench_boundary_probe import ClaudeProfile

REPO = Path(__file__).resolve().parents[1]
MARKER = 'NETCAP_WORKER_RETRY'


def primary(payload):
    messages = payload.get('messages', [])
    if not messages or messages[-1].get('role') != 'user':
        return False
    content = messages[-1].get('content')
    parts = content if isinstance(content, list) else [{'text': content}]
    return any(isinstance(p.get('text'), str) and p['text'].strip() == MARKER for p in parts)


class RetryingProvider(Provider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.cwd, self.issued = root / 'work', []

    def anthropic(self, payload, answer, seq):
        if not primary(payload):
            return super().anthropic(payload, answer, seq)
        if len(self.issued) >= 2:
            raise AssertionError('unexpected third automatic main request')
        call_id = f'owned_retry_{seq}'
        self.issued.append(call_id)
        args = {'command': shlex.join(['/usr/bin/python3', str(self.cwd / 'owned_tool.py')]),
                'description': 'Run only the owned retry fixture', 'timeout': 180000}
        message = {'id': f'msg_{seq}', 'type': 'message', 'role': 'assistant', 'model': payload['model'],
                   'content': [], 'stop_reason': None, 'stop_sequence': None,
                   'usage': {'input_tokens': 32, 'output_tokens': 0}}
        return self.sse([('message_start', {'message': message}),
            ('content_block_start', {'index': 0, 'content_block': {'type': 'tool_use', 'id': call_id, 'name': 'Bash', 'input': {}}}),
            ('content_block_delta', {'index': 0, 'delta': {'type': 'input_json_delta', 'partial_json': json.dumps(args)}}),
            ('content_block_stop', {'index': 0}),
            ('message_delta', {'delta': {'stop_reason': 'tool_use', 'stop_sequence': None}, 'usage': {'output_tokens': 32}}),
            ('message_stop', {})])


class ObservationConsole:
    def __init__(self, profile):
        self.root = profile.root / 'application'
        self.root.mkdir()
        self.log = (self.root / 'console.log').open('w')
        config = {'version': 1, 'state_dir': str(self.root / 'state'), 'listen': '127.0.0.1:0',
            'instances': [{'id': 'native-claude', 'kind': 'claude', 'executable': CLAUDE,
                'profile': profile.env['CLAUDE_CONFIG_DIR'], 'bridge': str(REPO / 'bridges/claude/dist/main.js'),
                'node_executable': shutil.which('node'), 'version': '2.1.280/0.3.280', 'policy_revision': 'native-1'}],
            'routes': [{'id': 'unused', 'instance_id': 'native-claude', 'provider': 'anthropic',
                'endpoint': profile.provider.url, 'model': 'claude-sonnet-4-6',
                'credential_ref': 'env:NETCAP_UNUSED_KEY', 'credential_revision': '1', 'config_revision': '1'}],
            'projects': [{'id': 'native-project', 'name': '原生 worker 故障', 'cwd': str(profile.cwd)}]}
        write(self.root / 'console.json', config)
        self.process = subprocess.Popen([str(REPO / '.dev/bin/netcap'), '--config', str(self.root / 'console.json'), 'serve'],
            cwd=profile.cwd, env={**profile.env, 'NETCAP_UNUSED_KEY': 'fixture-A'},
            stdout=self.log, stderr=self.log, start_new_session=True)
        self.exchanges = []
        try:
            wait_for((self.root / 'state/connection.json').exists, 'production console ready')
            self.connection = json.loads((self.root / 'state/connection.json').read_text())
            self.token = Path(self.connection['token_file']).read_text().strip()
        except BaseException:
            self.close()
            raise

    def api(self, path, body=None, status=200):
        request = Request(self.connection['url'] + path, data=json.dumps(body).encode() if body is not None else None,
            headers={'Authorization': 'Bearer ' + self.token, 'Content-Type': 'application/json'})
        try:
            with urlopen(request, timeout=15) as response:
                result = {'status': response.status, 'body': json.load(response)}
        except HTTPError as error:
            result = {'status': error.code, 'body': json.loads(error.read())}
        self.exchanges.append({'path': path, 'request': body, 'response': result})
        write(self.root / 'http.json', self.exchanges)
        assert result['status'] == status, result
        return result['body']

    def close(self):
        stop(self.process)
        self.log.close()


def run(root):
    p = ClaudeProfile(root / 'profile', 'A', provider_factory=RetryingProvider)
    app = None
    checks, samples = {}, {}
    result = {'scope': 'real native worker retry, TTY approvals, surviving owned tool and production readonly observer',
              'checks': checks, 'samples': samples, 'goal_acceptance_met': False}
    tool_pid = None

    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    def roster(tag):
        rows = json.loads(p.cli(['agents', '--json', '--all'], tag))
        assert len(rows) == 1
        return rows[0]

    try:
        write(p.root / 'key.py', "from pathlib import Path\nwith Path(__file__).with_name('helper-calls.txt').open('a') as f:f.write('call\\n')\nprint('fixture-A')\n")
        write(p.cwd / 'owned_tool.py', '''import os,time
from pathlib import Path
root=Path(__file__).parent
with (root/'starts.txt').open('a') as f:f.write(str(os.getpid())+'\\n')
deadline=time.monotonic()+120
while not (root/'release').exists():
 if time.monotonic()>deadline:raise TimeoutError('fixture was not released')
 time.sleep(.05)
with (root/'completed.txt').open('a') as f:f.write(str(os.getpid())+'\\n')
print('ORIGINAL_RETRY_TOOL_OUTPUT',flush=True)
''')
        p.cli(['--bg', MARKER, '--permission-mode', 'default'], 'launch')
        pending = wait_for(lambda: (j if (j := roster('waiting-first')).get('waitingFor') == 'permission prompt' else None), 'first native approval')
        samples['original'] = pending
        check('first_request_has_no_side_effect', not (p.cwd / 'starts.txt').exists())
        tty = p.attach(pending['id'], 'first-approval')
        tty.until(lambda: 'Yes' in tty.text() and 'owned_tool.py' in tty.text(), 'first approval visible')
        tty.send('\r')
        tty.until(lambda: (p.cwd / 'starts.txt').exists(), 'first owned tool starts')
        tool_pid = int((p.cwd / 'starts.txt').read_text().strip())
        samples['original_tool_pid'] = tool_pid
        crash_owned(tty.proc.pid, p)
        tty.proc.wait(timeout=5)
        helper_before = (p.root / 'helper-calls.txt').read_bytes()
        app = ObservationConsole(p)
        preview = app.api('/api/native-sessions/preview', {'project_id': 'native-project', 'instance_id': 'native-claude', 'native_id': pending['sessionId']})
        bound = app.api('/api/native-sessions/bind', {'command_id': 'bind-native', 'project_id': 'native-project',
            'instance_id': 'native-claude', 'native_id': pending['sessionId'], 'preview_token': preview['binding_token']})
        sid = bound['session_id']
        result['session_id'], result['native_id'] = sid, pending['sessionId']
        samples['bound_before'] = app.api('/api/sessions/' + sid)
        samples['history_before_crash'] = app.api('/api/sessions/' + sid + '/native/history', {})
        history = samples['history_before_crash']
        check('original_authenticated_history_readable', history['read_only'] and history['session']['native_id'] == pending['sessionId'] and
              history['coverage'] == 'claude_main_transcript_entries' and bool(history['entries']))
        app.api('/api/commands', {'command_id': 'rejected-create', 'kind': 'create', 'project_id': 'native-project',
                'route_id': 'unused', 'permission': 'read_only'}, status=409)
        check('execution_auth_conflict_still_blocks_create', len(app.api('/api/snapshot')['sessions']) == 1)
        samples['observe_before_crash'] = app.api('/api/sessions/' + sid + '/native/observe', {})
        check('metadata_and_roster_reads_do_not_execute_auth_helper', (p.root / 'helper-calls.txt').read_bytes() == helper_before)
        samples['auth_helper_calls_before_metadata'] = helper_before.decode().splitlines()
        samples['auth_helper_calls_after_metadata'] = (p.root / 'helper-calls.txt').read_text().splitlines()
        count_before = len([r for r in p.provider.requests if primary(r['body'])])
        crash_owned(pending['pid'], p)
        wait_for(lambda: not alive(pending['pid']), 'original worker exits')
        retried = wait_for(lambda: (j if (j := roster('replacement')).get('pid') and j['pid'] != pending['pid'] and j.get('waitingFor') == 'permission prompt' else None),
                           'replacement requests permission for the retry', timeout=40)
        samples['replacement'] = retried
        mains = [r for r in p.provider.requests if primary(r['body'])]
        samples['main_requests_before_crash'] = count_before
        samples['main_requests_before_new_user_input'] = mains
        check('automatic_prompt_retry_without_user_input', count_before == 1 and len(mains) == 2)
        check('both_requests_keep_full_native_id', all(json.loads(r['body']['metadata']['user_id'])['session_id'] == pending['sessionId'] for r in mains))
        check('retry_omits_original_tool_call_and_result', not any(part.get('type') in ['tool_use', 'tool_result']
              for message in mains[1]['body']['messages'] if isinstance(message.get('content'), list) for part in message['content']))
        check('replacement_waits_for_new_permission', retried['waitingFor'] == 'permission prompt' and retried['status'] == 'waiting')
        check('original_tool_still_alive', alive(tool_pid) and (p.cwd / 'starts.txt').read_text().splitlines() == [str(tool_pid)])
        again = p.attach(pending['id'], 'retry-permission')
        again.until(lambda: 'Yes' in again.text() and 'owned_tool.py' in again.text(), 'second approval visible')
        samples['observe_after_crash'] = app.api('/api/sessions/' + sid + '/native/observe', {})
        observed = samples['observe_after_crash']
        check('production_tracks_same_id_and_replacement_declaration', observed['view']['native_id'] == pending['sessionId'] and
              observed['view']['agent_roster']['pid'] == retried['pid'] and observed['view']['agent_roster']['waiting_for'] == 'permission prompt')
        check('production_keeps_unknown_incomplete_occupancy', observed['status'] == 'sampled' and observed['read_only'] and
              observed['view']['status'] == 'unknown' and not observed['view']['evidence']['complete'] and not observed['view'].get('execution_clear', False))
        for kind in ['send', 'resume', 'takeover', 'interrupt', 'respond']:
            app.api('/api/commands', {'command_id': 'rejected-' + kind, 'kind': kind, 'session_id': sid, 'text': 'must not send'}, status=409)
        samples['bound_after'] = app.api('/api/sessions/' + sid)
        check('readonly_record_unchanged_and_no_operations', samples['bound_before']['session'] == samples['bound_after']['session'] and not samples['bound_after']['operations'])
        check('production_controls_do_not_send_model_input', len([r for r in p.provider.requests if primary(r['body'])]) == 2)
        check('second_tool_not_approved_or_started', (p.cwd / 'starts.txt').read_text().splitlines() == [str(tool_pid)])
        write(p.cwd / 'release', 'release original only')
        wait_for((p.cwd / 'completed.txt').exists, 'original tool completes on disk')
        wait_for(lambda: not alive(tool_pid), 'original tool exits')
        again.drain(.4)
        samples['after_original_completion'] = roster('after-original-completion')
        samples['observe_after_completion'] = app.api('/api/sessions/' + sid + '/native/observe', {})
        check('new_permission_still_pending_after_original_completion', samples['after_original_completion'].get('waitingFor') == 'permission prompt')
        matched = [part for row in p.provider.requests for message in row['body'].get('messages', [])
                   if isinstance(message.get('content'), list) for part in message['content']
                   if part.get('type') == 'tool_result' and part.get('tool_use_id') == p.provider.issued[0]]
        samples['original_result_blocks'] = matched
        result['tool_output_continuity'] = any('ORIGINAL_RETRY_TOOL_OUTPUT' in json.dumps(v) and not v.get('is_error') for v in matched)
        check('missing_original_result_observed', not result['tool_output_continuity'])
        check('only_original_tool_completed', (p.cwd / 'completed.txt').read_text().splitlines() == [str(tool_pid)])
        check('local_provider_has_no_errors', not p.provider.errors and all(r['auth_matches'] for r in p.provider.requests))
        result['completed'] = True
    except Exception as error:
        result['error'] = repr(error)
        write(root / 'error.txt', traceback.format_exc())
    finally:
        write(p.cwd / 'release', 'cleanup')
        if app:
            app.close()
            result['console_exit'] = app.process.returncode
        p.close()
        if tool_pid:
            wait_for(lambda: not alive(tool_pid), 'owned tool cleanup', timeout=5)
        check('owned_profile_stopped', p.cleaned)
        result['provider_requests'] = len(p.provider.requests)
        result['issued_tool_ids'] = p.provider.issued
        result['diagnostic_expectations_met'] = result.get('completed', False) and all(checks.values())
        write(root / 'result.json', result)
    return result


if __name__ == '__main__':
    root = Path(tempfile.mkdtemp(prefix='netcap-claude-worker-retry-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    value = run(root)
    print(json.dumps({k: value[k] for k in ['diagnostic_expectations_met', 'goal_acceptance_met', 'tool_output_continuity', 'checks', 'error'] if k in value}, ensure_ascii=False), flush=True)
    raise SystemExit(0 if value['diagnostic_expectations_met'] else 1)
