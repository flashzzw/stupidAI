#!/usr/bin/env python3
"""Observe same-ID MCP definition transitions with owned native clients.

Passing checks validate the recorded experiment. Each step's target_applied
separately states whether the requested definition actually became callable.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
from uuid import uuid4

from execution_config_probe import CODEX, SERVER, environment, verify_versions, write
from go_cli_smoke import Proxy
from native_mcp_selection_smoke import decoded_outputs
from shared_mcp_profiles_native import SharedProvider
from runtime.common import stop, wait_for


def inside(root, netcap=None):
    assert os.path.samefile(root / 'policy', '/etc/codex')
    verify_versions(root, ['codex', 'server'])
    env = environment(root / 'execution')
    env['NETCAP_FIXTURE_KEY'] = 'fixture-A'
    cwd = root / 'work'
    cwd.mkdir()
    subprocess.run(['git', 'init', '--quiet', str(cwd)], env=env, check=True)
    provider = SharedProvider(root, 'A')
    profile = Path(env['CODEX_HOME'])
    events_file = root / 'mcp-lifecycle.jsonl'
    script = root / 'server.py'
    write(script, '''import json,os,sys,time,uuid
from pathlib import Path
instance=uuid.uuid4().hex
binding={'version':1,'digest':os.environ['MCP_MARKER']}
def record(kind):
 value={'kind':kind,'name':os.environ['MCP_NAME'],'version':os.environ['MCP_VERSION'],'marker':os.environ['MCP_MARKER'],'old_only':os.getenv('OLD_ONLY'),'instance':instance,'cwd':os.getcwd(),'pid':os.getpid(),'at':time.time()}
 with Path(os.environ['MCP_LOG']).open('a') as output:output.write(json.dumps(value)+'\\n')
 return value
for line in sys.stdin:
 request=json.loads(line)
 if 'id' not in request:continue
 method=request['method']
 if method=='initialize':
  record('initialize');result={'protocolVersion':request['params']['protocolVersion'],'capabilities':{'tools':{},'experimental':{'netcap/binding':binding}},'serverInfo':{'name':'owned-mcp','version':'unchanged'}}
 elif method=='tools/list':result={'tools':[{'name':'whoami','description':'Return this owned MCP process binding','inputSchema':{'type':'object','properties':{}}}]}
 elif method=='tools/call':result={'content':[{'type':'text','text':json.dumps(record('call'))}],'_meta':{'netcap/binding':binding}}
 else:result={}
 print(json.dumps({'jsonrpc':'2.0','id':request['id'],'result':result}),flush=True)
''')
    markers = {version: uuid4().hex for version in ['v1', 'v2', 'v3', 'keep']}
    bindings = {}

    def server(name, version, old=False, wrapped=True):
        values = {'MCP_NAME': name, 'MCP_VERSION': version, 'MCP_MARKER': markers[version], 'MCP_LOG': str(events_file)}
        if old:
            values['OLD_ONLY'] = 'original-env-entry'
        definition = {'command': 'python3', 'args': [str(script)], 'cwd': str(cwd), 'env': values, 'enabled': True}
        if netcap and wrapped:
            identity = {'version': 1, 'digest': '', 'asset_id': name, 'asset_version': version, 'server': name}
            context = hashlib.sha256(str(root).encode()).hexdigest()
            payload = {'identity': identity, 'context': context, 'cwd': str(cwd), 'command': definition['command'], 'args': definition['args'], 'environment': dict(sorted(values.items()))}
            digest = hashlib.sha256(b'netcap-mcp-stdio-v1\0' + json.dumps(payload, ensure_ascii=False, separators=(',', ':')).encode()).hexdigest()
            binding = {**identity, 'digest': digest, 'context': context, 'cwd': str(cwd), 'environment': sorted(values)}
            bindings[name + '/' + version] = binding
            definition.update(command=str(netcap), args=['__mcp-stdio-v1', json.dumps(binding), '--', 'python3', str(script)])
        return definition

    inherited = server('keep', 'keep', wrapped=False)
    config = f'''model = "gpt-6-astra"
model_provider = "fixture"
approval_policy = "on-request"
sandbox_mode = "read-only"
[model_providers.fixture]
name = "Fixture"
base_url = "{provider.url}/v1"
env_key = "NETCAP_FIXTURE_KEY"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = false
request_max_retries = 0
stream_max_retries = 0
[features]
apps = false
plugins = false
shell_snapshot = false
[mcp_servers.keep]
command = "python3"
args = {json.dumps([str(script)])}
cwd = {json.dumps(str(cwd))}
[mcp_servers.keep.env]
'''
    config += ''.join(key + ' = ' + json.dumps(value) + '\n' for key, value in inherited['env'].items())
    write(profile / 'config.toml', config)
    checks, replies, steps = {}, {}, {}
    clients, sessions = {}, {}
    native, finished, number = None, False, 0
    parameters = {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture', 'sandbox': 'read-only', 'approvalPolicy': 'on-request'}

    def check(label, value):
        checks[label] = bool(value)
        assert value, label

    def rpc(label, method, params, client='main'):
        row = {'client': client, 'method': method, 'params': params, 'started_at': time.time()}
        replies[label] = row
        try:
            row['result'] = clients[client].call(method, params)
            return row['result']
        except Exception as error:
            row['error'] = str(error)
            raise
        finally:
            row['finished_at'] = time.time()

    def overrides(definitions):
        return {'mcp_servers.' + name + '.' + key: value for name, definition in definitions.items() for key, value in definition.items()}

    def calls():
        return [json.loads(line) for line in events_file.read_text().splitlines() if json.loads(line)['kind'] == 'call'] if events_file.exists() else []

    def invoke(label, sid, name, wanted, client='main'):
        nonlocal number
        number += 1
        before = len(calls())
        receipt = rpc(label + '.send', 'turn/start', {'threadId': sid, 'input': [{'type': 'text', 'text': f'NETCAP_A_TURN_{number} PROFILE_codex-a TOOL_{name}'}]}, client)
        turn = receipt['turn']['id']
        row = {'number': number, 'native_id': sid, 'native_turn': turn, 'server': name, 'wanted': wanted, 'approvals': []}
        steps[label] = row
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            # Only one client responds, even if multiple subscribers observe it.
            for key, proxy in clients.items():
                for event in proxy.events:
                    params = event.get('params', {})
                    if row['approvals'] or event.get('method') != 'mcpServer/elicitation/request' or params.get('threadId') != sid or params.get('turnId') != turn:
                        continue
                    check(label + '_approval_precedes_tool', len(calls()) == before)
                    check(label + '_approval_has_exact_tool_origin', params['serverName'] == name and params.get('_meta', {}).get('codex_approval_kind') == 'mcp_tool_call')
                    proxy.send({'id': event['id'], 'result': {'action': 'accept', 'content': {}}})
                    row['approvals'].append({'client': key, 'request': event})
            try:
                history = rpc(label + '.history', 'thread/read', {'threadId': sid, 'includeTurns': True}, client)['thread']
            except RuntimeError as error:
                if 'failed to read session metadata' in str(error) and 'rollout' in str(error) and 'is empty' in str(error):
                    row.setdefault('observation_errors', []).append({'at': time.time(), 'error': str(error)})
                    time.sleep(.02)
                    continue
                raise
            current = next((value for value in history['turns'] if value['id'] == turn), None)
            status = current['status'] if current else 'unobserved'
            if not row.get('observations') or row['observations'][-1]['status'] != status:
                row.setdefault('observations', []).append({'status': status, 'at': time.time()})
            terminal = [event['params']['turn'] for proxy in clients.values() for event in proxy.events
                        if event.get('method') == 'turn/completed' and event.get('params', {}).get('threadId') == sid and event['params']['turn']['id'] == turn]
            if terminal:
                check(label + '_original_turn_completed', all(value['status'] == 'completed' for value in terminal))
                break
            for key in clients:
                if key != client:
                    rpc(label + '.other-observation', 'thread/read', {'threadId': sid, 'includeTurns': False}, key)
            time.sleep(.02)
        else:
            raise TimeoutError('same original native turn did not settle: ' + label)
        program = next(value for value in provider.tool_programs if value['turn'] == str(number))
        matching = [{'body': {'input': [item]}} for request in provider.requests for item in request['body'].get('input', [])
                    if item.get('type') == 'custom_tool_call_output' and item.get('call_id') == program['call_id']]
        decoded = decoded_outputs(matching)
        registries = [value['mcp_tools'] for value in decoded if 'mcp_tools' in value]
        check(label + '_tool_registry_was_observed', bool(registries) and all(value == registries[0] for value in registries))
        exposed = 'mcp__' + name + '__whoami' in registries[0]
        actual = calls()[before:]
        check(label + '_one_actual_call_per_exposed_tool', len(actual) == int(exposed) and len(row['approvals']) == int(exposed))
        if actual:
            check(label + '_result_returns_to_its_original_call', actual[0] in provider.returned('codex-a', number) and actual[0]['cwd'] == str(cwd) and actual[0]['name'] == name)
            inventory = rpc(label + '.inventory', 'mcpServerStatus/list', {'threadId': sid}, client)
            loaded = next(value for value in inventory['data'] if value['name'] == name)
            row['load_binding'] = loaded.get('serverCapabilities', {}).get('experimental', {}).get('netcap/binding')
            history = rpc(label + '.final-history', 'thread/read', {'threadId': sid, 'includeTurns': True}, client)['thread']
            actual_turn = next(value for value in history['turns'] if value['id'] == turn)
            row['result_bindings'] = [item.get('result', {}).get('_meta', {}).get('netcap/binding') for item in actual_turn['items'] if item.get('type') == 'mcpToolCall']
            if netcap and name != 'keep':
                expected = bindings[name + '/' + actual[0]['version']]
                matches = lambda value: isinstance(value, dict) and all(value.get(key) == expected[key] for key in ['version', 'digest', 'asset_id', 'asset_version', 'server']) and value.get('upstream', {}).get('digest') == actual[0]['marker']
                check(label + '_load_and_result_have_actual_forwarder_binding', matches(row['load_binding']) and len(row['result_bindings']) == 1 and matches(row['result_bindings'][0]))
                if wanted is not None:
                    check(label + '_binding_detects_whether_target_really_loaded', (row['load_binding']['digest'] == bindings[name + '/' + wanted]['digest']) == (actual[0]['version'] == wanted))
            else:
                check(label + '_native_preserves_mcp_load_and_result_metadata', row['load_binding'] == {'version': 1, 'digest': actual[0]['marker']} and row['result_bindings'] == [row['load_binding']])
        row.update(actual=actual, registry=registries[0], target_applied=(not exposed if wanted is None else bool(actual) and actual[0]['version'] == wanted and actual[0]['marker'] == markers[wanted]))
        print(label, 'wanted', wanted, 'actual', actual[0]['version'] if actual else None, flush=True)
        return row

    def transition(label, sid, definitions, detach, client='main'):
        before = rpc(label + '.before', 'thread/read', {'threadId': sid, 'includeTurns': True}, client)['thread']
        count = len(provider.requests)
        if detach:
            rpc(label + '.unsubscribe', 'thread/unsubscribe', {'threadId': sid}, client)
            rpc(label + '.after-unsubscribe', 'thread/read', {'threadId': sid, 'includeTurns': False}, client)
            rpc(label + '.loaded', 'thread/loaded/list', {}, client)
        resumed = rpc(label + '.resume', 'thread/resume', {**parameters, 'threadId': sid, 'excludeTurns': True, 'config': overrides(definitions)}, client)
        after = rpc(label + '.after', 'thread/read', {'threadId': sid, 'includeTurns': True}, client)['thread']
        rpc(label + '.inventory', 'mcpServerStatus/list', {'threadId': sid}, client)
        check(label + '_same_id_and_history', resumed['thread']['id'] == before['id'] == after['id'] == sid and before['turns'] == after['turns'])
        check(label + '_configuration_does_not_make_model_request', len(provider.requests) == count)

    try:
        socket = root / 'native.sock'
        with (root / 'native.log').open('w') as log:
            native = subprocess.Popen([SERVER, 'app-server', '--listen', 'unix://' + str(socket)], env=env, cwd=cwd, stdout=log, stderr=log, start_new_session=True)
        wait_for(socket.exists, 'owned MCP transition server')
        clients['main'] = Proxy(CODEX, socket, env)
        for label in ['target', 'control']:
            started = rpc(label + '.start', 'thread/start', {**parameters, 'historyMode': 'legacy', 'config': overrides({'identity': server('identity', 'v1', old=True)})})
            sessions[label] = started['thread']['id']
            invoke(label + '-baseline', sessions[label], 'identity', 'v1')
        sid = sessions['target']
        new = {'identity': server('identity', 'v2'), 'added': server('added', 'v2')}
        transition('hot', sid, new, False)
        invoke('hot-identity', sid, 'identity', 'v2')
        invoke('hot-added', sid, 'added', 'v2')
        transition('detached', sid, new, True)
        invoke('detached-identity', sid, 'identity', 'v2')
        invoke('detached-added', sid, 'added', 'v2')
        invoke('detached-inherited', sid, 'keep', 'keep')
        invoke('control-after-change', sessions['control'], 'identity', 'v1')
        clients['other'] = Proxy(CODEX, socket, env)
        rpc('other.subscribe', 'thread/resume', {**parameters, 'threadId': sid, 'excludeTurns': True}, 'other')
        transition('second-subscriber', sid, {'identity': server('identity', 'v3'), 'added': server('added', 'v3')}, True)
        invoke('second-subscriber-main', sid, 'identity', 'v3')
        invoke('second-subscriber-other', sid, 'identity', 'v3', 'other')
        rpc('other.unsubscribe', 'thread/unsubscribe', {'threadId': sid}, 'other')
        write(root / 'native-events-other.json', clients['other'].events)
        clients['other'].close()
        del clients['other']
        transition('release-omitted', sid, {}, True)
        invoke('release-identity', sid, 'identity', None)
        invoke('release-added', sid, 'added', None)
        invoke('release-inherited', sid, 'keep', 'keep')
        invoke('control-after-release', sessions['control'], 'identity', 'v1')
        check('profile_bytes_are_unchanged', (profile / 'config.toml').read_text() == config)
        check('every_request_uses_owned_authentication', all(value['auth_matches'] for value in provider.requests) and not provider.errors)
        finished = True
    finally:
        for key, client in clients.items():
            write(root / ('native-events-' + key + '.json'), client.events)
            client.close()
        stop(native)
        provider.close()
        write(root / 'result.json', {'passed': finished and all(checks.values()), 'checks': checks, 'replies': replies, 'steps': steps, 'sessions': sessions,
                                    'markers': markers, 'bindings': bindings, 'netcap': str(netcap) if netcap else None, 'model_requests': len(provider.requests), 'programs': provider.tool_programs,
                                    'scope': 'owned native MCP transition observations; target_applied is separate from harness checks'})
    print(json.dumps({'passed': True, 'checks': len(checks), 'observed_targets': {key: value['target_applied'] for key, value in steps.items()}}), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside', type=Path)
    parser.add_argument('--netcap', type=Path)
    args = parser.parse_args()
    if args.inside:
        return inside(args.inside, args.netcap)
    root = Path(tempfile.mkdtemp(prefix='netcap-codex-mcp-transition-', dir='/var/tmp'))
    (root / 'policy').mkdir()
    print('Evidence directory:', root, flush=True)
    command = ['bwrap', '--unshare-user', '--unshare-pid', '--unshare-net', '--die-with-parent', '--ro-bind', '/', '/',
               '--tmpfs', '/etc', '--ro-bind', str(root / 'policy'), '/etc/codex', '--bind', str(root), str(root),
               '--tmpfs', '/mnt', '--tmpfs', '/tmp', '--tmpfs', '/run', '--proc', '/proc', '--dev', '/dev',
               sys.executable, str(Path(__file__).resolve()), '--inside', str(root)]
    if args.netcap:
        command += ['--netcap', str(args.netcap.resolve())]
    process = subprocess.Popen(command, env=environment(root / 'environment'), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
    try:
        out, err = process.communicate(timeout=180)
        write(root / 'runner.stdout', out)
        write(root / 'runner.stderr', err)
        print(out, end='')
        print(err, end='', file=sys.stderr)
    finally:
        stop(process)
    raise SystemExit(process.returncode)


if __name__ == '__main__':
    main()
