#!/usr/bin/env python3
"""Windows -> WSL owned Codex daemon/proxy control, with a local text provider.

Run in WSL. No user configuration, shared daemon, real model, installation or
distribution termination. This probes the host path, not production UI control.
"""
import base64
import hashlib
import json
import os
from pathlib import Path
import pwd
import select
import signal
import subprocess
import sys
import tempfile
import threading
import time

from execution_config_probe import CODEX, SERVER, environment, verify_versions, write
from go_cli_smoke import Proxy
from runtime.common import Provider, stop, wait_for
from wsl_host_probe import POWERSHELL

SCRIPT = Path(__file__).resolve()


def identity(pid=None):
    pid = pid or os.getpid()
    proc = Path('/proc') / str(pid)
    # comm may contain spaces/parentheses; fields after the final ')' are stable.
    stat = (proc / 'stat').read_text().rsplit(')', 1)[1].split()
    return {'pid': pid, 'start_ticks': stat[19], 'state': stat[0],
            'cwd': str((proc / 'cwd').resolve(strict=True)),
            'exe': str((proc / 'exe').resolve(strict=True)), 'uid': proc.stat().st_uid}


def alive(record):
    try:
        current = identity(record['pid'])
        return current['start_ticks'] == record['start_ticks'] and current['state'] != 'Z'
    except (FileNotFoundError, ProcessLookupError):
        return False


def receiver(mode, root, label):
    root = Path(root)
    env = environment(root / 'profile')
    env['NETCAP_FIXTURE_KEY'] = 'fixture-A'
    me = {**identity(), 'user': pwd.getpwuid(os.getuid()).pw_name,
          'distro': os.environ.get('WSL_DISTRO_NAME')}
    write(root / (label + '-identity.json'), me)
    sock = root / 'native.sock'
    if mode == '--proxy':
        os.execve(CODEX, [CODEX, 'app-server', 'proxy', '--sock', str(sock)], env)
    def terminate(_signum, _frame):
        raise SystemExit(143)
    signal.signal(signal.SIGTERM, terminate)
    native = None
    report = {'receiver': me}
    try:
        with (root / 'native.log').open('a') as log:
            native = subprocess.Popen([SERVER, 'app-server', '--listen', 'unix://' + str(sock)],
                                      env=env, stdout=log, stderr=log, start_new_session=True)
        wait_for(sock.exists, 'owned native socket')
        report['native'] = identity(native.pid)
        write(root / (label + '-native-identity.json'), report['native'])
        print(json.dumps(report), flush=True)
        # A bounded lease ensures an abandoned host cannot leave a test daemon.
        deadline = time.monotonic() + 240
        while time.monotonic() < deadline:
            if not select.select([sys.stdin], [], [], min(1, deadline - time.monotonic()))[0]:
                continue
            command = sys.stdin.readline()
            if not command or command.strip() == 'stop':
                break
            if command.strip() == 'status':
                print(json.dumps({'native': identity(native.pid), 'receiver': me}), flush=True)
    finally:
        stop(native)
        report['exit_code'] = native.returncode if native else None
        report['exited'] = native is not None and native.poll() is not None
        write(root / (label + '-cleanup.json'), report)


class WindowsHost:
    def __init__(self, root):
        self.root, self.records, self.buffer = root, [], bytearray()
        self.child_buffers = {}
        self.log = (root / 'host-stderr.txt').open('wb')
        encoded = base64.b64encode(SCRIPT.with_name('wsl_live_control.ps1').read_text().encode('utf-16le')).decode()
        self.proc = subprocess.Popen([str(POWERSHELL), '-NoLogo', '-NoProfile', '-NonInteractive',
                                      '-EncodedCommand', encoded], stdin=subprocess.PIPE,
                                     stdout=subprocess.PIPE, stderr=self.log)
        try:
            self.info = self.line()
            assert self.info['ready'] and self.info['platform'] == 'Win32NT'
        except BaseException:
            self.close()
            raise

    def line(self):
        deadline = time.monotonic() + 25
        while b'\n' not in self.buffer:
            remaining = deadline - time.monotonic()
            if remaining <= 0 or not select.select([self.proc.stdout], [], [], remaining)[0]:
                raise TimeoutError('Windows host response timed out')
            data = os.read(self.proc.stdout.fileno(), 65536)
            if not data:
                raise EOFError('Windows host closed; see host-stderr.txt')
            self.buffer.extend(data)
        line, _, rest = self.buffer.partition(b'\n')
        self.buffer[:] = rest
        return json.loads(line.decode('utf-8-sig'))

    def action(self, op, name, **params):
        request = {'op': op, 'name': name, **params}
        self.proc.stdin.write(json.dumps(request).encode() + b'\n')
        self.proc.stdin.flush()
        result = self.line()
        self.records.append({'request': request, 'response': result})
        write(self.root / 'host-transcript.json', {'host': self.info, 'records': self.records})
        if not result['ok']:
            raise RuntimeError(result['error'])
        return result['result']

    def start(self, name, arguments):
        return self.action('start', name, arguments=subprocess.list2cmdline(arguments))

    def write(self, name, value):
        result = self.action('write', name, data=base64.b64encode(value).decode())
        assert result['written'] == len(value)

    def read(self, name):
        result = self.action('read', name)
        if result.get('timeout'):
            raise TimeoutError(name + ' Windows pipe timed out')
        if result['eof']:
            raise EOFError(name + ' Windows pipe closed')
        return base64.b64decode(result['data'], validate=True)

    def child_line(self, name):
        buffer = self.child_buffers.setdefault(name, bytearray())
        while b'\n' not in buffer:
            buffer.extend(self.read(name))
        line, _, rest = buffer.partition(b'\n')
        buffer[:] = rest
        return json.loads(line)

    def close(self):
        if self.proc.poll() is None:
            self.proc.stdin.close()
            try:
                self.proc.wait(timeout=20)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=5)
        self.proc.stdout.close()
        self.log.close()
        return {'exit_code': self.proc.returncode, 'exited': self.proc.poll() is not None}


class WindowsProxy(Proxy):
    """Reuse verified native WebSocket RPC framing over real Windows byte pipes."""
    def __init__(self, host, name, arguments, calls):
        self.host, self.name, self.calls = host, name, calls
        self.buffer, self.seq, self.events = bytearray(), 0, []
        self.launched = host.start(name, arguments)
        # Proxy.send uses this pipe facade; all bytes still cross Windows.
        class Pipe:
            def write(_, data):
                host.write(name, data)

            def flush(_):
                pass
        self.proc = type('RemotePipe', (), {'stdin': Pipe()})()
        key = base64.b64encode(os.urandom(16)).decode()
        self.proc.stdin.write(('GET / HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\n'
            'Connection: Upgrade\r\nSec-WebSocket-Key: ' + key +
            '\r\nSec-WebSocket-Version: 13\r\n\r\n').encode())
        header = b''
        while not header.endswith(b'\r\n\r\n'):
            header += self.read(1)
            assert len(header) < 16384
        expected = base64.b64encode(hashlib.sha1((key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest())
        assert header.startswith(b'HTTP/1.1 101 ') and expected in header
        self.initialized = self.call('initialize', {'clientInfo': {'name': 'netcap_windows_probe', 'version': '1'},
                                                   'capabilities': {'experimentalApi': True}})
        self.send({'method': 'initialized'})

    def read(self, size):
        while len(self.buffer) < size:
            self.buffer.extend(self.host.read(self.name))
        value = bytes(self.buffer[:size])
        del self.buffer[:size]
        return value

    def call(self, method, params):
        record = {'connection': self.name, 'method': method, 'params': params}
        self.calls.append(record)
        result = super().call(method, params)
        record['result'] = result
        return result

    def close(self, kill=False):
        return self.host.action('stop', self.name, kill=kill)


class HeldProvider(Provider):
    def __init__(self, root):
        self.waiting, self.release = threading.Event(), threading.Event()
        super().__init__(root, 'A')

    def responses(self, payload, answer, seq):
        if answer == 'fixture A: NETCAP_A_TURN_2':
            self.waiting.set()
            if not self.release.wait(90):
                raise TimeoutError('held fixture was not released')
        return super().responses(payload, answer, seq)


def run(root):
    checks, calls, samples = {}, [], {}
    report = {'root': str(root), 'scope': 'real Windows host / owned Codex Unix proxy / local text-only provider; no production Application or UI',
              'checks': checks, 'calls': calls, 'samples': samples, 'passed': False}
    host = provider = client = None
    stopped = []

    def check(name, condition):
        checks[name] = bool(condition)
        assert condition, name

    try:
        distro = os.environ['WSL_DISTRO_NAME']
        user = pwd.getpwuid(os.getuid()).pw_name
        verify_versions(root, ['codex', 'server'])
        cwd = root / "含空格 project 'quote"
        cwd.mkdir()
        env = environment(root / 'profile')
        provider = HeldProvider(root)
        config = Path(env['CODEX_HOME']) / 'config.toml'
        write(config, f'''model="gpt-6-astra"
model_provider="fixture"
approval_policy="on-request"
sandbox_mode="read-only"
[model_providers.fixture]
name="Owned host fixture"
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
        original_config = config.read_bytes()

        def arguments(mode, label):
            return ['--distribution', distro, '--user', user, '--cd', str(cwd), '--exec',
                    '/usr/bin/python3', str(SCRIPT), mode, str(root), label]

        host = WindowsHost(root)
        report['host'] = host.info
        host.start('inventory', ['--list', '--quiet'])
        inventory = bytearray()
        while True:
            try:
                inventory.extend(host.read('inventory'))
            except EOFError:
                break
        (root / 'distributions.raw').write_bytes(inventory)
        report['distributions'] = [s for s in inventory.decode('utf-16le').splitlines() if s]
        stopped.append(host.action('stop', 'inventory', kill=False))
        host.start('owner', arguments('--owner', 'owner'))
        owner = host.child_line('owner')
        samples['owner'] = owner
        check('windows_host_confirmed', host.info['platform'] == 'Win32NT')
        check('host_selected_identity_and_special_cwd', owner['receiver']['user'] == user and
              owner['receiver']['uid'] == os.getuid() and owner['receiver']['distro'] == distro and
              owner['receiver']['cwd'] == str(cwd) and owner['native']['cwd'] == str(cwd))
        check('host_launched_fixed_daemon', owner['native']['exe'] == SERVER and alive(owner['native']))
        client = WindowsProxy(host, 'initial', arguments('--proxy', 'initial'), calls)
        proxy_identity = identity(json.loads((root / 'initial-identity.json').read_text())['pid'])
        samples['initial_proxy'] = proxy_identity
        check('host_proxy_fixed_executable_and_identity', proxy_identity['exe'] == CODEX and
              proxy_identity['uid'] == os.getuid() and proxy_identity['cwd'] == str(cwd))
        check('isolated_native_home', client.initialized['codexHome'] == env['CODEX_HOME'])
        started = client.call('thread/start', {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture',
            'sandbox': 'read-only', 'approvalPolicy': 'on-request', 'historyMode': 'legacy'})
        sid = started['thread']['id']
        report['native_id'] = sid
        samples['started'] = started
        check('bound_start_settings', started['cwd'] == str(cwd) and started['modelProvider'] == 'fixture' and
              started['model'] == 'gpt-6-astra' and started['approvalPolicy'] == 'on-request' and
              started['sandbox']['type'] == 'readOnly')

        def send(number):
            return client.call('turn/start', {'threadId': sid, 'input': [{'type': 'text', 'text': f'NETCAP_A_TURN_{number}'}]})['turn']['id']

        def read():
            return client.call('thread/read', {'threadId': sid, 'includeTurns': True})['thread']

        def until(turn_id, status):
            def matches():
                history = read()
                return history if any(t['id'] == turn_id and t['status'] == status for t in history['turns']) else None
            return wait_for(matches, 'native turn ' + status, timeout=35)

        first = send(1)
        samples['completed_first'] = until(first, 'completed')
        first_turn = next(t for t in samples['completed_first']['turns'] if t['id'] == first)
        check('exact_first_answer', any(i.get('type') == 'agentMessage' and i.get('text') == 'fixture A: NETCAP_A_TURN_1' for i in first_turn['items']))
        second = send(2)
        check('second_provider_request_held', provider.waiting.wait(20) and not provider.release.is_set())
        samples['in_flight_before_disconnect'] = read()
        check('second_in_progress', any(t['id'] == second and t['status'] == 'inProgress' for t in samples['in_flight_before_disconnect']['turns']))
        initial_identity = json.loads((root / 'initial-identity.json').read_text())
        stopped.append(client.close(kill=True))
        client = None
        wait_for(lambda: not alive(initial_identity), 'Windows-killed proxy exits in Linux', timeout=15)
        check('abrupt_host_disconnect_closed_proxy', stopped[-1]['exited'] and not alive(initial_identity))
        host.write('owner', b'status\n')
        samples['after_disconnect_owner'] = host.child_line('owner')
        native_after = samples['after_disconnect_owner']['native']
        check('same_live_daemon_after_disconnect', all(native_after[k] == owner['native'][k] for k in ('pid', 'start_ticks', 'cwd', 'exe', 'uid')))
        before_requests = len(provider.requests)
        client = WindowsProxy(host, 'reconnected', arguments('--proxy', 'reconnected'), calls)
        samples['reconnected_proxy'] = identity(json.loads((root / 'reconnected-identity.json').read_text())['pid'])
        check('new_proxy_same_fixed_executable', samples['reconnected_proxy']['exe'] == CODEX and
              samples['reconnected_proxy']['pid'] != proxy_identity['pid'])
        samples['reconnected_read'] = read()
        check('reconnect_preserves_id_and_running_turn', samples['reconnected_read']['id'] == sid and
              any(t['id'] == second and t['status'] == 'inProgress' for t in samples['reconnected_read']['turns']))
        check('reconnect_does_not_send_or_replay', len(provider.requests) == before_requests == 2 and
              len([c for c in calls if c['method'] == 'turn/start']) == 2)
        client.call('turn/interrupt', {'threadId': sid, 'turnId': second})
        samples['interrupted'] = until(second, 'interrupted')
        check('original_turn_interrupted_from_new_host_connection', any(t['id'] == second and t['status'] == 'interrupted' for t in samples['interrupted']['turns']))
        provider.release.set()
        third = send(3)
        samples['completed_after_reconnect'] = until(third, 'completed')
        history = samples['completed_after_reconnect']
        check('three_original_turn_ids', [t['id'] for t in history['turns']] == [first, second, third])
        check('first_turn_preserved_exactly', history['turns'][0] == first_turn)
        check('exact_answer_after_reconnect', any(i.get('type') == 'agentMessage' and i.get('text') == 'fixture A: NETCAP_A_TURN_3' for i in history['turns'][2]['items']))
        check('interrupted_turn_not_reported_success', history['turns'][1]['status'] == 'interrupted' and
              not any(i.get('type') == 'agentMessage' for i in history['turns'][1]['items']))
        check('only_three_local_model_requests', len(provider.requests) == 3 and not provider.errors)
        check('all_requests_bound_to_original_thread', all(r['auth_matches'] and r['body']['model'] == 'gpt-6-astra' and
              r['body'].get('client_metadata', {}).get('thread_id') == sid for r in provider.requests))
        check('profile_bytes_unchanged', config.read_bytes() == original_config)
        stopped.append(client.close())
        client = None
        host.write('owner', b'stop\n')
        stopped.append(host.action('stop', 'owner', kill=False))
        check('owner_reaped_native_daemon', json.loads((root / 'owner-cleanup.json').read_text())['exited'] and not alive(owner['native']))
        check('native_socket_removed_on_exit', not (root / 'native.sock').exists())
        # A separate Windows launch now tests cold recovery, without input.
        host.start('cold-owner', arguments('--owner', 'cold-owner'))
        cold_owner = host.child_line('cold-owner')
        samples['cold_owner'] = cold_owner
        check('cold_restart_has_new_native_process', cold_owner['native']['pid'] != owner['native']['pid'] and
              cold_owner['native']['exe'] == SERVER and cold_owner['native']['cwd'] == str(cwd))
        client = WindowsProxy(host, 'cold', arguments('--proxy', 'cold'), calls)
        samples['cold_read_before_resume'] = read()
        check('cold_read_original_id_and_turns', samples['cold_read_before_resume']['id'] == sid and
              samples['cold_read_before_resume']['turns'] == history['turns'])
        restored = client.call('thread/resume', {'threadId': sid, 'cwd': str(cwd), 'model': 'gpt-6-astra',
            'modelProvider': 'fixture', 'sandbox': 'read-only', 'approvalPolicy': 'on-request'})
        samples['cold_resumed'] = restored
        check('cold_resume_preserves_identity_and_settings', restored['thread']['id'] == sid and
              all(restored[k] == started[k] for k in ('cwd', 'model', 'modelProvider', 'sandbox', 'approvalPolicy')))
        samples['cold_read_after_resume'] = read()
        check('cold_resume_preserves_all_turns', samples['cold_read_after_resume']['turns'] == history['turns'])
        check('cold_restart_resume_no_model_input', len(provider.requests) == 3 and
              len([c for c in calls if c['method'] == 'turn/start']) == 3 and
              len([c for c in calls if c['method'] == 'thread/start']) == 1)
        stopped.append(client.close())
        client = None
        host.write('cold-owner', b'stop\n')
        stopped.append(host.action('stop', 'cold-owner', kill=False))
        check('cold_owner_reaped_native_daemon', json.loads((root / 'cold-owner-cleanup.json').read_text())['exited'] and
              not alive(cold_owner['native']))
        check('all_windows_child_handles_exited', all(p['exited'] and p['stderr_complete'] for p in stopped))
        report['completed'] = True
    except Exception as error:
        report['error'] = repr(error)
    finally:
        if provider:
            provider.release.set()
        if host:
            report['host_cleanup'] = host.close()
        # Only recorded process identities created by this probe are eligible.
        cleanup = []
        for path in root.glob('*-identity.json'):
            record = json.loads(path.read_text())
            if alive(record):
                os.kill(record['pid'], signal.SIGTERM)
                try:
                    wait_for(lambda: not alive(record), 'owned receiver exit', timeout=5)
                except AssertionError:
                    if alive(record):
                        os.kill(record['pid'], signal.SIGKILL)
                    wait_for(lambda: not alive(record), 'owned receiver kill', timeout=5)
            cleanup.append({'identity': record, 'exited': not alive(record)})
        report['process_cleanup'] = cleanup
        report['windows_children'] = stopped
        if provider:
            provider.close()
            report['provider_requests'] = len(provider.requests)
            report['provider_errors'] = provider.errors
        checks['all_owned_linux_processes_exited'] = bool(cleanup) and all(c['exited'] for c in cleanup)
        checks['windows_host_exited'] = bool(host) and report['host_cleanup']['exited'] and report['host_cleanup']['exit_code'] == 0
        report['passed'] = report.get('completed', False) and all(checks.values())
        write(root / 'result.json', report)
    return report


if __name__ == '__main__':
    if sys.argv[1:2] in (['--owner'], ['--proxy']):
        receiver(*sys.argv[1:])
    else:
        root = Path(tempfile.mkdtemp(prefix='netcap-wsl-live-'))
        print('Evidence directory:', root, flush=True)
        report = run(root)
        print(json.dumps({k: report[k] for k in ('root', 'passed', 'checks', 'error') if k in report}, ensure_ascii=False), flush=True)
        raise SystemExit(0 if report['passed'] else 1)
