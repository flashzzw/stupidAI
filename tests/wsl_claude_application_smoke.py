#!/usr/bin/env python3
"""Real Windows control of owned production Claude sessions and in-flight tools.

The Go/SDK processes use a private bwrap mount/user namespace and isolated
profiles. Loopback networking is shared with the owned scripted providers.
Only the Windows-launched relay sends production HTTP commands.
"""
import argparse
import json
import os
from pathlib import Path
import pwd
import re
import shlex
import shutil
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import time
import traceback

from claude_skill_method_probe import SkillProvider, strings
from execution_config_probe import CLAUDE, environment, verify_versions, write
from runtime.common import wait_for
from wsl_application_smoke import child, api_relay, owned_tool_identity
from wsl_live_control_probe import WindowsHost, identity, alive

SCRIPT = Path(__file__).resolve()
REPO = SCRIPT.parents[1]


def owned_bridge_identity(runtime_root):
    directory = os.open(runtime_root, os.O_RDONLY)
    try:
        with socket.socket(socket.AF_UNIX) as peer:
            peer.connect('/proc/self/fd/' + str(directory) + '/controller.sock')
            pid, uid, _ = struct.unpack('3i', peer.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
    finally:
        os.close(directory)
    record = identity(pid)
    args = (Path('/proc') / str(pid) / 'cmdline').read_bytes().decode().rstrip('\0').split('\0')
    assert uid == os.getuid() == record['uid'] and str(runtime_root) in args
    assert str(REPO / 'bridges/claude/dist/main.js') in args and '--persistent-control' in args
    return {**record, 'args': args, 'socket_inode': (runtime_root / 'controller.sock').stat().st_ino}


class HostProvider(SkillProvider):
    def __init__(self, root, route, expected):
        self.expected, self.returned = expected, []
        super().__init__(root, route)

    def plan(self, payload):
        messages = payload.get('messages', [])
        phases = [(i, re.search(r'NETCAP_CLAUDE_HOST=([a-z-]+) ROUTE=([AB])', strings(m.get('content'))))
                  for i, m in enumerate(messages) if m.get('role') == 'user']
        phases = [(i, m.group(1), m.group(2)) for i, m in phases if m]
        if not phases:
            return {'text': 'No owned host task.'}
        start, phase, route = phases[-1]
        assert route == self.route, 'input reached another route'
        if phase != 'tool':
            return {'text': 'HOST_' + route + '_' + phase + '_DONE'}
        calls, results = {}, {}
        for message in messages[start:]:
            for block in message.get('content', []) if isinstance(message.get('content'), list) else []:
                if block.get('type') == 'tool_use':
                    calls[block['id']] = block
                if block.get('type') == 'tool_result':
                    results[block['tool_use_id']] = block
        if not calls:
            return {'tool': 'Bash', 'input': {'command': shlex.join(['/usr/bin/python3', str(Path(self.expected['cwd']) / 'owned_tool.py')]),
                                            'timeout': 120000, 'description': 'Read an owned host fixture and await its release'}}
        assert len(calls) == len(results) == 1 and set(calls) == set(results), 'original tool/result correlation missing'
        result = next(iter(results.values()))
        text = strings(result.get('content'))
        values = []
        for line in text.splitlines():
            try:
                values.append(json.loads(line))
            except ValueError:
                pass
        if not result.get('is_error') and self.expected in values:
            self.returned.append({'tool_use_id': result['tool_use_id'], 'value': self.expected})
            return {'text': 'HOST_ORIGINAL_TOOL_CONFIRMED'}
        return {'text': 'HOST_ORIGINAL_TOOL_RESULT_UNAVAILABLE'}


def run(root, binary, mode):
    checks, samples, frames = {}, {}, {}
    result = {'root': str(root), 'mode': mode, 'checks': checks, 'samples': samples, 'frames': frames,
              'scope': 'real Windows pipes -> WSL relay -> production Go/SDK/CLI; owned bwrap filesystem and local providers'}
    host, providers, windows_children = None, {}, []
    tool = None
    cwd = root / "Claude 工作目录 with 'quote"
    cwd.mkdir()
    expected = {'tag': 'OWNED_CLAUDE_HOST_RESULT', 'cwd': str(cwd), 'uid': os.getuid(), 'value': '中文完整结果'}
    write(cwd / 'expected.json', expected)
    write(cwd / 'owned_tool.py', '''import json,os,time
from pathlib import Path
root=Path(__file__).parent
with (root/'starts.jsonl').open('a') as f:
 f.write(json.dumps({'pid':os.getpid(),'cwd':os.getcwd(),'uid':os.getuid(),
 'pid_namespace':os.readlink('/proc/self/ns/pid'),
 'start_ticks':Path('/proc/self/stat').read_text().rsplit(')',1)[1].split()[19]})+'\\n')
deadline=time.monotonic()+110
while not (root/'release').exists():
 if time.monotonic()>deadline: raise TimeoutError('owned release missing')
 time.sleep(.05)
value=json.loads((root/'expected.json').read_text())
assert value['cwd']==os.getcwd() and value['uid']==os.getuid()
print(json.dumps(value,ensure_ascii=False),flush=True)
(root/'finished.json').write_text(json.dumps(value,ensure_ascii=False))
''')

    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    try:
        verify_versions(root, ['claude'])
        subprocess.run(['git', 'init', '--quiet', str(cwd)], env=environment(root / 'git'), check=True)
        profiles, instances, routes = {}, [], []
        for label in ['A', 'B']:
            profile = Path(environment(root / ('profile-' + label))['CLAUDE_CONFIG_DIR'])
            providers[label] = HostProvider(root, label, expected)
            wrapper = root / ('node-' + label)
            write(wrapper, '#!/bin/sh\nexport NETCAP_SETTINGS_WIRE_MODE=session\nexport NETCAP_SETTINGS_WIRE=' + shlex.quote(str(root / ('wire-' + label + '.jsonl'))) + '\nexec ' + shlex.quote(shutil.which('node')) + ' --import ' + shlex.quote(str(REPO / 'tests/claude_settings_wire_capture.mjs')) + ' "$@"\n')
            wrapper.chmod(0o700)
            profiles[label] = str(profile)
            instances.append({'id': label, 'kind': 'claude', 'executable': CLAUDE, 'profile': str(profile),
                              'bridge': str(REPO / 'bridges/claude/dist/main.js'), 'node_executable': str(wrapper),
                              'version': '2.1.280/0.3.280', 'policy_revision': 'owned-host-1',
                              'env_allow': ['XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME',
                                            'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC', 'DISABLE_AUTOUPDATER', 'DISABLE_TELEMETRY', 'DISABLE_ERROR_REPORTING']})
            routes.append({'id': label, 'instance_id': label, 'provider': 'anthropic', 'endpoint': providers[label].url,
                           'model': 'claude-sonnet-4-6', 'credential_ref': 'env:NETCAP_HOST_' + label, 'credential_revision': '1', 'config_revision': '1'})
        write(root / 'probe.json', {'binary': str(binary), 'cwd': str(cwd)})
        write(root / 'console.json', {'version': 1, 'state_dir': str(root / 'console'), 'listen': '127.0.0.1:0',
              'instances': instances, 'routes': routes, 'projects': [{'id': 'owned', 'name': 'Windows Claude fixture', 'cwd': str(cwd)}]})
        write(root / 'policy/managed-settings.json', {})
        registry = root / 'windows/Windows/System32/reg.exe'
        write(registry, '#!/usr/bin/python3\nimport sys\nsys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\nraise SystemExit(1)\n')
        registry.chmod(0o700)
        host = WindowsHost(root)
        result['host'] = host.info
        target, user = os.environ['WSL_DISTRO_NAME'], pwd.getpwuid(os.getuid()).pw_name
        base = ['--distribution', target, '--user', user, '--cd', str(cwd), '--exec']
        sandbox = ['bwrap', '--unshare-user', '--die-with-parent', '--ro-bind', '/', '/', '--tmpfs', '/etc',
                   '--ro-bind', '/etc/passwd', '/etc/passwd', '--ro-bind', '/etc/group', '/etc/group',
                   '--ro-bind', str(root / 'policy'), '/etc/claude-code', '--bind', str(root), str(root),
                   '--tmpfs', '/tmp', '--tmpfs', '/run', '--tmpfs', '/mnt', '--ro-bind', str(root / 'windows'), '/mnt/c',
                   '--proc', '/proc', '--dev', '/dev']
        samples['console_launch'] = host.start('console', base + sandbox + ['/usr/bin/python3', str(SCRIPT), '--child', str(root)])
        samples['console_initial'] = host.child_line('console')
        current_go = samples['console_initial']['process']
        check('real_windows_started_original_go_binary', samples['console_initial']['process']['exe'] == str(binary) and host.info['platform'] == 'Win32NT')
        check('go_cwd_and_user_preserved', samples['console_initial']['process']['cwd'] == str(cwd) and samples['console_initial']['process']['uid'] == os.getuid())
        isolation = json.loads((root / 'sandbox.json').read_text())
        check('native_uses_owned_policy_mount', isolation['owned_policy'] and isolation['mnt_namespace'] != os.readlink('/proc/self/ns/mnt'))
        relay = 'http-1'

        def connect(name):
            host.start(name, base + ['/usr/bin/python3', str(SCRIPT), '--api', str(root)])
            samples[name] = host.child_line(name)

        def api(path, body=None, status=200):
            command = {'path': path}
            if body is not None:
                command['body'] = body
            host.write(relay, json.dumps(command).encode() + b'\n')
            response = host.child_line(relay)
            assert response['status'] == status, (path, response)
            return response['body']

        def command(key, kind, status=202, **fields):
            return api('/api/commands', {'command_id': key, 'kind': kind, **fields}, status)

        def detail(sid):
            return api('/api/sessions/' + sid)

        def save(name, sid):
            value = detail(sid)
            frames[name] = {'/api/workspace': api('/api/workspace'), '/api/sessions/' + sid: value}
            return value

        def operation(sid, oid):
            return next(op for op in detail(sid)['operations'] if op['id'] == oid)

        def terminal(sid, receipt):
            # A disconnected controller or an old bridge's cleanup lock can
            # temporarily leave the operation uncertain. It is not a terminal
            # result and must not cause a replay or premature failure verdict.
            value = wait_for(lambda: (op if (op := operation(sid, receipt['operation_id']))['status'] in ['succeeded', 'not_sent', 'failed', 'interrupted'] else None), 'original operation terminal', timeout=25)
            assert value['status'] == 'succeeded', {k: value.get(k) for k in ['id', 'status', 'error']}
            return value

        def control(op):
            host.write('console', json.dumps({'op': op}).encode() + b'\n')
            return host.child_line('console')

        connect(relay)
        sessions = {}
        for label in ['A', 'B']:
            receipt = command('create-' + label, 'create', project_id='owned', route_id=label, permission='read_only', title='Windows Claude ' + label)
            sessions[label] = receipt['session_id']
            wait_for(lambda: detail(sessions[label])['session']['status'] == 'idle', 'Claude ' + label + ' ready')
        result['sessions'] = sessions
        first, sibling = detail(sessions['A'])['session'], detail(sessions['B'])['session']
        native = first['scope']['native_id']
        samples['original_bridges'] = {}
        samples['original_native_processes'] = {}
        for label in ['A', 'B']:
            bridge = owned_bridge_identity(root / 'console/runtimes' / label)
            samples['original_bridges'][label] = bridge
            write(root / ('bridge-' + label + '-identity.json'), bridge)
            sid = detail(sessions[label])['session']['scope']['native_id']
            entries = [json.loads(line) for line in (root / 'console/runtimes' / label / (sid + '.jsonl')).read_text().splitlines()]
            native_process = identity(next(e['pid'] for e in entries if e['kind'] == 'initialized'))
            samples['original_native_processes'][label] = native_process
            write(root / ('cli-' + label + '-identity.json'), native_process)
        check('profiles_and_native_ids_are_distinct', native != sibling['scope']['native_id'] and first['scope']['state_namespace'] != sibling['scope']['state_namespace'])
        check('creation_does_not_send_model_input', all(not p.requests for p in providers.values()))
        sent = command('original-tool-input', 'send', session_id=first['id'], text='NETCAP_CLAUDE_HOST=tool ROUTE=A')
        pending = wait_for(lambda: next((p for p in detail(first['id'])['session']['view']['pending'] if p['status'] == 'pending'), None), 'original Bash approval')
        check('original_tool_needs_explicit_approval', pending['kind'] == 'Bash' and pending['native_turn'] == sent['operation_id'] and not (cwd / 'starts.jsonl').exists())
        samples['original_pending'] = pending
        save('pending', first['id'])
        if mode == 'pending-go-crash':
            command('manual-handoff', 'takeover', session_id=first['id'])
            samples['pending_go_crash'] = control('crash')
            check('actual_go_sigkill_while_approval_pending', samples['pending_go_crash']['exit_code'] == -9 and not alive(current_go))
            samples['pending_console_restarted'] = control('start')
            current_go = samples['pending_console_restarted']['process']
            save('pending-restoring', first['id'])
            restored = wait_for(lambda: next((p for p in detail(first['id'])['session']['view']['pending'] if p['id'] == pending['id'] and p['status'] == 'pending'), None), 'original pending approval after Go reconciliation')
            restored_frame = save('pending-after-go-restart', first['id'])
            check('pending_restart_keeps_original_request_and_snapshot', all(restored[k] == pending[k] for k in ['id', 'native_id', 'native_turn', 'connection_id', 'request_id', 'status']) and restored_frame['session']['execution_snapshot'] == first['execution_snapshot'])
            check('pending_restart_keeps_manual_control', restored_frame['session']['paused'] and restored_frame['session']['control_mode'] == 'human_takeover')
            check('pending_restart_does_not_replay_or_execute', len(providers['A'].requests) == 1 and not (cwd / 'starts.jsonl').exists())
            check('pending_restart_keeps_native_processes', all(alive(p) for p in samples['original_bridges'].values()) and all(alive(p) for p in samples['original_native_processes'].values()))
        allowed = command('allow-original-tool', 'respond', session_id=first['id'], pending_id=pending['id'], option_id='allow')
        approval = wait_for(lambda: (op if (op := operation(first['id'], allowed['operation_id']))['status'] not in ['dispatching', 'acknowledged', 'prepared'] else None), 'submitted original response')
        samples['submitted_response'] = approval
        check('submitted_response_stays_in_flight_until_tool_finishes', approval['status'] in ['running', 'succeeded'])
        wait_for(lambda: (cwd / 'starts.jsonl').exists(), 'original tool starts')
        starts = [json.loads(s) for s in (cwd / 'starts.jsonl').read_text().splitlines()]
        journal = root / 'console/runtimes/A' / (native + '.jsonl')
        entries = [json.loads(s) for s in journal.read_text().splitlines()]
        native_pid = next(e['pid'] for e in entries if e['kind'] == 'initialized')
        cli = identity(native_pid)
        samples['tool_binding'] = owned_tool_identity(cli, starts[0], cwd / 'owned_tool.py')
        tool = samples['tool_binding']['host']
        write(root / 'tool-identity.json', tool)
        write(root / 'cli-identity.json', cli)
        wait_for(lambda: operation(first['id'], sent['operation_id'])['status'] == 'running', 'published original input running')
        before = save('running', first['id'])
        original_op = next(op for op in before['operations'] if op['id'] == sent['operation_id'])
        check('one_owned_tool_is_running', len(starts) == 1 and alive(tool) and alive(cli))
        windows_children.append(host.action('stop', relay, kill=True))
        check('original_windows_relay_actually_exited', windows_children[-1]['exited'] and windows_children[-1]['exit_code'] == -1)
        check('relay_loss_keeps_go_cli_and_tool_alive', alive(current_go) and alive(cli) and alive(tool))
        relay = 'http-2'
        connect(relay)
        after = save('reconnected', first['id'])
        after_op = next(op for op in after['operations'] if op['id'] == sent['operation_id'])
        check('reconnect_keeps_original_id_receipt_and_snapshot', after['session']['scope']['native_id'] == native and after_op['receipt'] == original_op['receipt'] and after['session']['execution_snapshot'] == first['execution_snapshot'])
        check('reconnect_does_not_replay_input_or_tool', len(providers['A'].requests) == 1 and len((cwd / 'starts.jsonl').read_text().splitlines()) == 1 and after_op['status'] == 'running')
        command('manual-handoff', 'takeover', session_id=first['id'])
        samples['manual_handoff'] = save('manual', first['id'])
        if mode == 'go-crash':
            samples['go_crash'] = control('crash')
            check('actual_go_sigkill', samples['go_crash']['exit_code'] == -9 and not alive(samples['console_initial']['process']))
            samples['console_restarted'] = control('start')
            time.sleep(1)
            samples['after_go_restart'] = save('after-go-restart', first['id'])
            samples['original_processes_after_go_restart'] = {'cli_alive': alive(cli), 'tool_alive': alive(tool)}
            check('go_restart_preserves_original_cli_and_tool', alive(cli) and alive(tool))
            samples['reconnected_bridges'] = {label: owned_bridge_identity(root / 'console/runtimes' / label) for label in ['A', 'B']}
            check('go_restart_reconnects_original_bridge_processes', all(
                all(samples['reconnected_bridges'][label][key] == bridge[key] for key in ['pid', 'start_ticks', 'uid', 'exe', 'cwd', 'args', 'socket_inode'])
                for label, bridge in samples['original_bridges'].items()))
        command('reject-resume-running', 'resume', session_id=first['id'], status=409)
        write(cwd / 'release', 'release original owned tool\n')
        completed = terminal(first['id'], sent)
        samples['completed_response'] = terminal(first['id'], allowed)
        final = save('finished', first['id'])
        check('original_tool_output_returns_to_original_context', len(providers['A'].returned) == 1 and providers['A'].returned[0]['value'] == expected)
        check('original_input_completes_with_same_receipt_and_snapshot', completed['receipt'] == original_op['receipt'] and completed['execution_snapshot_id'] == first['execution_snapshot']['id'])
        check('tool_runs_once_and_has_complete_output', len((cwd / 'starts.jsonl').read_text().splitlines()) == 1 and json.loads((cwd / 'finished.json').read_text()) == expected)
        check('completion_keeps_manual_control', final['session']['paused'] and final['session']['control_mode'] == 'human_takeover')
        resumed = command('resume-idle-original', 'resume', session_id=first['id'])
        terminal(first['id'], resumed)
        check('explicit_handoff_is_idempotent', command('resume-idle-original', 'resume', session_id=first['id']) == resumed)
        next_input = command('next-original-input', 'send', session_id=first['id'], text='NETCAP_CLAUDE_HOST=next ROUTE=A')
        terminal(first['id'], next_input)
        next_frame = save('next-input', first['id'])
        check('next_explicit_input_keeps_native_id', next_frame['session']['scope']['native_id'] == native and len(providers['A'].requests) == 3)
        check('sibling_profile_has_no_model_input_or_state_change', not providers['B'].requests and detail(sibling['id'])['session']['execution_snapshot'] == sibling['execution_snapshot'])
        check('local_route_authentication_is_correct', all(not p.errors and all(r['auth_matches'] for r in p.requests) for p in providers.values()))
        host.write('console', b'{"op":"stop"}\n')
        windows_children.append(host.action('stop', 'console', kill=False))
        windows_children.append(host.action('stop', relay, kill=False))
        check('owned_windows_children_exit', len(windows_children) == 3 and all(p['exited'] and p['stderr_complete'] for p in windows_children))
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
            record = json.loads(path.read_text())
            if alive(record):
                os.kill(record['pid'], signal.SIGTERM)
                wait_for(lambda: not alive(record), 'owned process cleanup', timeout=8)
            cleanup.append({'identity': record, 'exited': not alive(record)})
        result['cleanup'], result['windows_children'] = cleanup, windows_children
        for p in providers.values():
            p.close()
        result['provider_counts'] = {label: len(p.requests) for label, p in providers.items()}
        result['provider_errors'] = {label: p.errors for label, p in providers.items()}
        checks['owned_processes_exited'] = bool(cleanup) and all(p['exited'] for p in cleanup)
        checks['windows_host_exited'] = bool(host) and result['host_cleanup'] == {'exit_code': 0, 'exited': True}
        result['passed'] = bool(result.get('completed')) and all(checks.values())
        write(root / 'result.json', result)
    return result


if __name__ == '__main__':
    if sys.argv[1:2] == ['--child']:
        root = Path(sys.argv[2])
        write(root / 'sandbox.json', {'pid': os.getpid(), 'uid': os.getuid(), 'mnt_namespace': os.readlink('/proc/self/ns/mnt'),
                                     'owned_policy': os.path.samefile(root / 'policy', '/etc/claude-code')})
        child(root, 'console', {'NETCAP_HOST_A': 'fixture-A', 'NETCAP_HOST_B': 'fixture-B'})
    elif sys.argv[1:2] == ['--api']:
        api_relay(sys.argv[2])
    else:
        parser = argparse.ArgumentParser(description=__doc__)
        parser.add_argument('--netcap', type=Path, default=REPO / '.dev/bin/netcap')
        parser.add_argument('--case', choices=['host-disconnect', 'go-crash', 'pending-go-crash'], default='host-disconnect')
        args = parser.parse_args()
        root = Path(tempfile.mkdtemp(prefix='netcap-wsl-claude-', dir='/var/tmp'))
        print('Evidence directory:', root, flush=True)
        result = run(root, args.netcap.resolve(), args.case)
        print(json.dumps({k: result[k] for k in ['root', 'passed', 'checks', 'error'] if k in result}, ensure_ascii=False), flush=True)
        raise SystemExit(0 if result['passed'] else 1)
