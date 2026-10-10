#!/usr/bin/env python3
"""Real Windows-launched production serve, Unix peer observation and tool recovery.

All profiles, servers, HTTP clients and tool side effects belong to this probe.
The Windows byte transport is shared with wsl_live_control_probe.py.
"""
import argparse
import json
import os
from pathlib import Path
import select
import shlex
import signal
import subprocess
import sys
import tempfile
import time
import traceback
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from execution_config_probe import CODEX, SERVER, environment, verify_versions, write
from runtime.common import Provider, stop, wait_for
from wsl_live_control_probe import WindowsHost, WindowsProxy, identity, alive

SCRIPT = Path(__file__).resolve()


def owned_tool_identity(native, reported, script):
    """Bind a sandbox PID to one host descendant; never signal the inner PID."""
    assert alive(native), 'native process identity changed'
    pending, seen, matches = [native['pid']], set(), []
    while pending:
        pid = pending.pop()
        if pid in seen:
            continue
        seen.add(pid)
        proc = Path('/proc') / str(pid)
        for path in proc.glob('task/*/children'):
            try:
                pending.extend(int(p) for p in path.read_text().split())
            except (FileNotFoundError, ProcessLookupError):
                pass
        try:
            argv = proc.joinpath('cmdline').read_bytes().split(b'\x00')
            if argv[:2] != [b'/usr/bin/python3', os.fsencode(script)]:
                continue
            host = identity(pid)
            namespace = os.readlink(proc / 'ns/pid')
            nsids = next(line.split()[1:] for line in proc.joinpath('status').read_text().splitlines() if line.startswith('NSpid:'))
            if (int(nsids[-1]) == reported['pid'] and namespace == reported['pid_namespace'] and
                    all(host[key] == reported[key] for key in ['start_ticks', 'cwd', 'uid'])):
                matches.append({'host': host, 'reported': reported, 'pid_namespace': namespace,
                                'namespace_pids': list(map(int, nsids)), 'native_parent': native['pid']})
        except (FileNotFoundError, ProcessLookupError):
            pass
    assert len(matches) == 1, 'owned tool host identity is absent or ambiguous'
    return matches[0]


def child(root, role):
    root = Path(root)
    config = json.loads((root / 'probe.json').read_text())
    env = environment(root / 'profile')
    env['NETCAP_FIXTURE_KEY'] = 'fixture-A'
    env['WSL_DISTRO_NAME'] = os.environ['WSL_DISTRO_NAME']
    proc = None
    generation = 0

    def terminate(_signum, _frame):
        raise SystemExit(143)
    signal.signal(signal.SIGTERM, terminate)
    signal.signal(signal.SIGHUP, terminate)

    def start():
        nonlocal proc, generation
        assert proc is None or proc.poll() is not None
        generation += 1
        if role == 'native':
            argv = [SERVER, 'app-server', '--listen', 'unix://' + str(root / 'native.sock')]
            ready = root / 'native.sock'
        else:
            argv = [config['binary'], '--config', str(root / 'console.json'), 'serve']
            ready = root / 'console/connection.json'
            ready.unlink(missing_ok=True)
        with (root / f'{role}-{generation}.log').open('w') as log:
            proc = subprocess.Popen(argv, cwd=config['cwd'], env=env, stdout=log, stderr=log, start_new_session=True)
        write(root / f'{role}-{generation}-identity.json', identity(proc.pid))
        wait_for(ready.exists, role + ' startup')
        value = identity(proc.pid)
        # Replace the exec-race sample with the verified running image.
        write(root / f'{role}-{generation}-identity.json', value)
        return {'process': value, 'receiver': identity(), 'generation': generation}

    write(root / f'{role}-receiver-identity.json', identity())
    try:
        print(json.dumps(start()), flush=True)
        deadline = time.monotonic() + 240
        while time.monotonic() < deadline:
            if not select.select([sys.stdin], [], [], 1)[0]:
                continue
            line = sys.stdin.readline()
            if not line:
                break
            request = json.loads(line)
            if request['op'] == 'stop':
                break
            if request['op'] == 'crash':
                os.killpg(proc.pid, signal.SIGKILL)
                proc.wait(timeout=5)
                result = {'exit_code': proc.returncode, 'exited': True}
            elif request['op'] == 'start':
                result = start()
            elif request['op'] == 'status':
                result = {'process': identity(proc.pid), 'generation': generation}
            else:
                raise ValueError('unknown child control')
            print(json.dumps(result), flush=True)
    finally:
        stop(proc)
        write(root / f'{role}-cleanup.json', {'exited': proc is not None and proc.poll() is not None,
                                            'exit_code': proc.returncode if proc else None})


def api_relay(root):
    root = Path(root)
    write(root / 'http-receiver-identity.json', identity())
    print(json.dumps({'ready': True, 'process': identity()}), flush=True)
    for line in sys.stdin:
        command = json.loads(line)
        connection = json.loads((root / 'console/connection.json').read_text())
        token = Path(connection['token_file']).read_text().strip()
        body = json.dumps(command['body']).encode() if 'body' in command else None
        request = Request(connection['url'] + command['path'], data=body,
                          headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token})
        try:
            with urlopen(request, timeout=20) as response:
                result = {'status': response.status, 'body': json.load(response)}
        except HTTPError as error:
            result = {'status': error.code, 'body': json.loads(error.read())}
        print(json.dumps(result), flush=True)


class ToolProvider(Provider):
    def __init__(self, root, expected):
        self.cwd = Path(expected['cwd'])
        self.expected, self.issued, self.returned = expected, set(), []
        super().__init__(root, 'A')

    def responses(self, payload, answer, seq):
        sid = payload.get('client_metadata', {}).get('thread_id')
        users = [m for m in payload.get('input', []) if m.get('role') == 'user']
        prompt = ''.join(c.get('text', '') for c in users[-1].get('content', [])) if users else ''
        if prompt != 'NETCAP_HOST_TOOL':
            return super().responses(payload, answer, seq)
        if sid in self.issued:
            outputs = [m for m in payload['input'] if m.get('type') == 'custom_tool_call_output']
            for output in outputs:
                blocks = output.get('output', [])
                if isinstance(blocks, str):
                    blocks = [{'text': blocks}]
                for block in blocks:
                    text = block.get('text', '')
                    try:
                        value = json.loads(text)
                    except (ValueError, TypeError):
                        continue
                    if value == self.expected:
                        self.returned.append({'sid': sid, 'value': value})
            assert self.returned, 'native exact tool result did not reach provider'
            return super().responses(payload, 'HOST_TOOL_CONFIRMED', seq)
        self.issued.add(sid)
        command = shlex.join(['/usr/bin/python3', str(self.cwd / 'owned_tool.py')])
        code = 'const r = await tools.exec_command(' + json.dumps({'cmd': command, 'login': False, 'yield_time_ms': 30000}) + '); if(r.exit_code !== 0) throw Error("owned tool did not complete"); text(JSON.parse(r.output));'
        item = {'id': f'ctc_{seq}', 'type': 'custom_tool_call', 'call_id': f'call_{seq}',
                'name': 'exec', 'namespace': 'functions', 'input': code, 'status': 'completed'}
        response = {'id': f'resp_{seq}', 'object': 'response', 'created_at': int(time.time()), 'status': 'completed',
                    'model': payload['model'], 'output': [item],
                    'usage': {'input_tokens': 32, 'output_tokens': 16, 'total_tokens': 48}}
        return self.sse([('response.created', {'response': dict(response, status='in_progress', output=[])}),
                         ('response.output_item.added', {'output_index': 0, 'item': dict(item, status='in_progress')}),
                         ('response.output_item.done', {'output_index': 0, 'item': item}),
                         ('response.completed', {'response': response})])


def run(root, binary):
    checks, samples, calls = {}, {}, []
    result = {'root': str(root), 'checks': checks, 'samples': samples, 'native_calls': calls,
              'scope': 'Windows-launched production Application/SQLite/Runtime/Handler, Unix peer and an owned tool; local provider only'}
    host = provider = external = None
    windows_children = []
    cwd = root / "工作目录 with 'quote"
    cwd.mkdir()
    expected = {'tag': 'NETCAP_HOST_TOOL_RESULT', 'cwd': str(cwd), 'uid': os.getuid(), 'value': '中文完整结果'}
    write(cwd / 'expected.json', expected)
    write(cwd / 'owned_tool.py', '''import json,os,time
from pathlib import Path
root=Path(__file__).parent
with (root/'starts.jsonl').open('a') as f:
 f.write(json.dumps({'pid':os.getpid(),'cwd':os.getcwd(),'uid':os.getuid(),
 'pid_namespace':os.readlink('/proc/self/ns/pid'),
 'start_ticks':Path('/proc/self/stat').read_text().rsplit(')',1)[1].split()[19]})+'\\n')
deadline=time.monotonic()+60
while not (root/'release').exists():
 if time.monotonic()>deadline: raise TimeoutError('owned release missing')
 time.sleep(.05)
result=json.loads((root/'expected.json').read_text())
assert result['cwd']==os.getcwd() and result['uid']==os.getuid()
print(json.dumps(result),flush=True)
''')

    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    try:
        verify_versions(root, ['codex', 'server'])
        provider = ToolProvider(root, expected)
        env = environment(root / 'profile')
        profile = Path(env['CODEX_HOME'])
        write(profile / 'config.toml', f'''model="gpt-6-astra"
model_provider="fixture"
approval_policy="on-request"
sandbox_mode="read-only"
[model_providers.fixture]
name="Owned Windows fixture"
base_url={json.dumps(provider.url + '/v1')}
env_key="NETCAP_FIXTURE_KEY"
wire_api="responses"
requires_openai_auth=false
supports_websockets=false
request_max_retries=0
stream_max_retries=0
[features]
apps=false
plugins=false
shell_snapshot=false
''')
        write(root / 'probe.json', {'binary': str(binary), 'cwd': str(cwd)})
        write(root / 'console.json', {'version': 1, 'state_dir': str(root / 'console'), 'listen': '127.0.0.1:0',
            'instances': [{'id': 'codex-host', 'kind': 'codex', 'executable': CODEX, 'profile': str(profile),
                           'socket': str(root / 'native.sock'), 'env_allow': ['NETCAP_FIXTURE_KEY'],
                           'version': '0.157.1/0.158.0', 'policy_revision': 'host-1'}],
            'routes': [{'id': 'route-a', 'instance_id': 'codex-host', 'provider': 'fixture', 'endpoint': provider.url + '/v1',
                        'model': 'gpt-6-astra', 'credential_ref': 'env:NETCAP_FIXTURE_KEY', 'credential_revision': '1', 'config_revision': '1'}],
            'projects': [{'id': 'fixture', 'name': 'Windows 宿主验收', 'cwd': str(cwd)}]})
        original_config = (profile / 'config.toml').read_bytes()
        host = WindowsHost(root)

        def launch(script, *args):
            return ['--distribution', os.environ['WSL_DISTRO_NAME'], '--user', __import__('pwd').getpwuid(os.getuid()).pw_name,
                    '--cd', str(cwd), '--exec', '/usr/bin/python3', str(script), *map(str, args)]

        def control(name, op):
            host.write(name, json.dumps({'op': op}).encode() + b'\n')
            return host.child_line(name)

        def api(path, body=None, status=200):
            command = {'path': path}
            if body is not None:
                command['body'] = body
            host.write('http', json.dumps(command).encode() + b'\n')
            response = host.child_line('http')
            if response['status'] != status:
                raise AssertionError(f'HTTP {path}: {response}')
            return response['body']

        def command(key, kind, status=202, **values):
            return api('/api/commands', {'command_id': key, 'kind': kind, **values}, status)

        def detail(sid):
            return api('/api/sessions/' + sid)

        def op(sid, oid):
            return next(o for o in detail(sid)['operations'] if o['id'] == oid)

        def terminal(sid, receipt):
            def ready():
                value = op(sid, receipt['operation_id'])
                return value if value['status'] in ['succeeded', 'not_sent', 'failed', 'interrupted', 'uncertain'] else None
            value = wait_for(ready, 'production operation terminal', timeout=40)
            assert value['status'] == 'succeeded', value
            return value

        host.start('native', launch(SCRIPT, '--child', root, 'native'))
        samples['native'] = host.child_line('native')
        host.start('console', launch(SCRIPT, '--child', root, 'console'))
        samples['console_initial'] = host.child_line('console')
        host.start('http', launch(SCRIPT, '--api', root))
        samples['http'] = host.child_line('http')
        result['host'] = host.info
        check('windows_launched_native_and_production_binary', samples['native']['process']['exe'] == SERVER and
              samples['console_initial']['process']['exe'] == str(binary))
        check('production_special_cwd_and_uid', all(samples[k]['process']['cwd'] == str(cwd) and
              samples[k]['process']['uid'] == os.getuid() for k in ['native', 'console_initial']))
        proxy_script = SCRIPT.with_name('wsl_live_control_probe.py')
        external = WindowsProxy(host, 'external', launch(proxy_script, '--proxy', root, 'external'), calls)
        seed = external.call('thread/start', {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture',
            'sandbox': 'read-only', 'approvalPolicy': 'on-request', 'historyMode': 'legacy'})
        external_id = seed['thread']['id']
        turn = external.call('turn/start', {'threadId': external_id, 'input': [{'type': 'text', 'text': 'NETCAP_A_TURN_1'}]})['turn']['id']
        def seeded():
            value = external.call('thread/read', {'threadId': external_id, 'includeTurns': True})['thread']
            return value if any(t['id'] == turn and t['status'] == 'completed' for t in value['turns']) else None
        samples['external_native_history'] = wait_for(seeded, 'external seed')
        preview = api('/api/native-sessions/preview', {'project_id': 'fixture', 'instance_id': 'codex-host', 'native_id': external_id})
        samples['external_preview'] = preview
        binding = api('/api/native-sessions/bind', {'command_id': 'bind-external', 'project_id': 'fixture', 'instance_id': 'codex-host',
                      'native_id': external_id, 'preview_token': preview['binding_token']})
        bound_id = binding['session_id']
        before = detail(bound_id)
        observed = api('/api/sessions/' + bound_id + '/native/observe', {})
        samples['external_observed'] = observed
        after = detail(bound_id)
        samples['external_bound'] = after
        check('external_original_identity_readonly', after['session']['scope']['native_id'] == external_id and
              after['session']['control_mode'] == 'native_readonly' and after['session']['paused'] and
              after['session']['scope']['route']['id'] == '' and not after['operations'])
        check('external_observation_does_not_mutate_session', before['session'] == after['session'])
        check('external_real_observation_complete', observed['status'] == 'observed' and observed['view']['evidence']['complete'] and
              observed['view']['native_id'] == external_id)
        executor = observed['view']['executor']
        samples['executor'] = executor
        check('executor_is_real_unix_peer', executor['source'] == 'codex.unix_peer' and
              executor['pid'] == samples['native']['process']['pid'] and executor['executable'] == SERVER and
              executor['uid'] == str(os.getuid()) and executor['wsl_distro'] == os.environ['WSL_DISTRO_NAME'])
        for kind in ['send', 'resume', 'takeover', 'interrupt']:
            command('reject-external-' + kind, kind, session_id=bound_id, text='must not send', status=409)
        check('external_commands_rejected_without_request', len(provider.requests) == 1 and not detail(bound_id)['operations'])
        created = command('create-managed', 'create', project_id='fixture', route_id='route-a', permission='workspace', title='宿主工具恢复')
        sid = created['session_id']
        terminal(sid, created)
        managed = detail(sid)
        result['managed_id'], result['external_id'] = sid, bound_id
        native_id = managed['session']['scope']['native_id']
        result['native_id'] = native_id
        samples['managed_before_tool'] = managed
        check('managed_create_has_no_model_input', len(provider.requests) == 1)
        sent = command('send-owned-tool', 'send', session_id=sid, text='NETCAP_HOST_TOOL')
        wait_for(lambda: (cwd / 'starts.jsonl').exists(), 'actual native tool starts', timeout=25)
        starts = [json.loads(l) for l in (cwd / 'starts.jsonl').read_text().splitlines()]
        samples['tool_identity_binding'] = owned_tool_identity(samples['native']['process'], starts[0], cwd / 'owned_tool.py')
        tool_identity = samples['tool_identity_binding']['host']
        check('tool_host_pid_bound_to_sandbox_namespace', tool_identity['pid'] == samples['tool_identity_binding']['namespace_pids'][0] and
              starts[0]['pid'] == samples['tool_identity_binding']['namespace_pids'][-1])
        write(root / 'tool-identity.json', tool_identity)
        def tool_running():
            value = detail(sid)
            operation = next(o for o in value['operations'] if o['id'] == sent['operation_id'])
            return value if operation['status'] == 'running' else None
        samples['tool_running'] = wait_for(tool_running, 'published running operation', timeout=15)
        operation = next(o for o in samples['tool_running']['operations'] if o['id'] == sent['operation_id'])
        check('native_tool_running_once', len(starts) == 1 and alive(tool_identity) and operation['status'] == 'running')
        original_receipt = operation['receipt'].copy()
        original_snapshot = samples['tool_running']['session']['execution_snapshot']
        original_turn = original_receipt['native_turn']
        check('owned_operation_bound_to_original_turn', original_receipt['native_id'] == native_id and bool(original_turn))
        command('manual-before-crash', 'takeover', session_id=sid)
        samples['before_crash'] = detail(sid)
        count = len(provider.requests)
        samples['console_crash'] = control('console', 'crash')
        check('actual_go_sigkill', samples['console_crash']['exit_code'] == -9 and not alive(samples['console_initial']['process']))
        samples['during_console_crash'] = {'tool': identity(tool_identity['pid']),
                                           'native': identity(samples['native']['process']['pid'])}
        check('tool_and_backend_survive_go_crash', alive(tool_identity) and alive(samples['native']['process']))
        samples['console_restarted'] = control('console', 'start')
        def recovered():
            value = detail(sid)
            operation = next(o for o in value['operations'] if o['id'] == sent['operation_id'])
            return value if value['session']['connection'] == 'ready' and operation['status'] == 'running' else None
        samples['recovered_running'] = wait_for(recovered, 'same tool operation after Go restart', timeout=20)
        samples['tool_after_reconnect'] = identity(tool_identity['pid'])
        check('native_tool_host_identity_stable_after_restart', all(samples['tool_after_reconnect'][key] == tool_identity[key]
              for key in ['pid', 'start_ticks', 'cwd', 'exe', 'uid']))
        recovered_op = next(o for o in samples['recovered_running']['operations'] if o['id'] == sent['operation_id'])
        check('restart_preserves_receipt_and_snapshot', recovered_op['receipt'] == original_receipt and
              samples['recovered_running']['session']['execution_snapshot'] == original_snapshot)
        check('restart_preserves_manual_pause', samples['recovered_running']['session']['paused'] and
              samples['recovered_running']['session']['control_mode'] == 'human_takeover')
        check('restart_no_tool_or_input_replay', len(provider.requests) == count and len((cwd / 'starts.jsonl').read_text().splitlines()) == 1)
        command('reject-resume-running', 'resume', session_id=sid, status=409)
        write(cwd / 'release', 'release owned tool\n')
        samples['tool_completed_operation'] = terminal(sid, sent)
        samples['tool_completed_session'] = detail(sid)
        check('tool_result_returned_to_original_model_context', provider.returned == [{'sid': native_id, 'value': expected}])
        check('original_operation_finished', samples['tool_completed_operation']['receipt'] == original_receipt and
              samples['tool_completed_operation']['execution_snapshot_id'] == original_snapshot['id'])
        check('tool_executed_once_and_exited', len((cwd / 'starts.jsonl').read_text().splitlines()) == 1 and not alive(tool_identity))
        check('completion_keeps_manual_takeover', samples['tool_completed_session']['session']['paused'] and
              samples['tool_completed_session']['session']['control_mode'] == 'human_takeover')
        resumed = command('resume-after-tool', 'resume', session_id=sid)
        samples['handoff_operation'] = terminal(sid, resumed)
        check('handoff_duplicate_idempotent', command('resume-after-tool', 'resume', session_id=sid) == resumed)
        samples['after_handoff'] = detail(sid)
        check('verified_idle_handoff_returns_managed_control', samples['after_handoff']['session']['control_mode'] == 'managed' and
              not samples['after_handoff']['session']['paused'])
        count = len(provider.requests)
        next_turn = command('next-owned-input', 'send', session_id=sid, text='NETCAP_A_TURN_3')
        terminal(sid, next_turn)
        samples['final_managed'] = detail(sid)
        samples['final_external'] = detail(bound_id)
        check('next_input_same_native_id', samples['final_managed']['session']['scope']['native_id'] == native_id and len(provider.requests) == count + 1)
        check('external_binding_survives_production_restart', samples['final_external']['session']['native_binding'] == after['session']['native_binding'] and
              samples['final_external']['session']['control_mode'] == 'native_readonly')
        check('profile_and_route_auth_unchanged', (profile / 'config.toml').read_bytes() == original_config and
              all(r['auth_matches'] for r in provider.requests) and not provider.errors)
        windows_children.append(external.close())
        external = None
        for name in ['console', 'native']:
            host.write(name, b'{"op":"stop"}\n')
            windows_children.append(host.action('stop', name, kill=False))
        windows_children.append(host.action('stop', 'http', kill=False))
        check('owned_windows_children_exited', len(windows_children) == 4 and
              all(p['exited'] and p['stderr_complete'] and p['exit_code'] == 0 for p in windows_children))
        result['completed'] = True
    except Exception as error:
        result['error'] = repr(error)
        write(root / 'error.txt', traceback.format_exc())
    finally:
        write(cwd / 'release', 'cleanup owned tool\n')
        if host:
            result['host_cleanup'] = host.close()
        cleanup = []
        for path in root.glob('*-identity.json'):
            value = json.loads(path.read_text())
            if alive(value):
                os.kill(value['pid'], signal.SIGTERM)
                wait_for(lambda: not alive(value), 'owned process cleanup', timeout=6)
            cleanup.append({'identity': value, 'exited': not alive(value)})
        if provider:
            provider.close()
            result['provider_count'] = len(provider.requests)
            result['provider_errors'] = provider.errors
            result['returned_tool_results'] = provider.returned
        result['cleanup'], result['windows_children'] = cleanup, windows_children
        checks['owned_processes_exited'] = bool(cleanup) and all(v['exited'] for v in cleanup)
        checks['windows_host_exited'] = bool(host) and result['host_cleanup'] == {'exited': True, 'exit_code': 0}
        result['passed'] = result.get('completed', False) and all(checks.values())
        write(root / 'result.json', result)
    return result


if __name__ == '__main__':
    if sys.argv[1:2] == ['--child']:
        child(*sys.argv[2:])
    elif sys.argv[1:2] == ['--api']:
        api_relay(sys.argv[2])
    else:
        parser = argparse.ArgumentParser(description=__doc__)
        parser.add_argument('--netcap', type=Path, default=SCRIPT.parent.parent / '.dev/bin/netcap')
        args = parser.parse_args()
        root = Path(tempfile.mkdtemp(prefix='netcap-wsl-application-'))
        print('Evidence directory:', root, flush=True)
        result = run(root, args.netcap.resolve())
        print(json.dumps({k: result[k] for k in ['root', 'passed', 'checks', 'error'] if k in result}, ensure_ascii=False), flush=True)
        raise SystemExit(0 if result['passed'] else 1)
