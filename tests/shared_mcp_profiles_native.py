#!/usr/bin/env python3
"""Run one shared MCP asset in four production profiles inside an owned namespace."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import threading
import time
from urllib.error import HTTPError

from console_fixture import Provider, SERVER_CLI, wait_for
from execution_config_probe import environment, verify_versions, write
from execution_profiles_smoke import ProfilesFixture
from mcp_binding_probe import ToolProvider
from native_mcp_selection_smoke import decoded_outputs
from runtime.common import stop

REPO = Path(__file__).resolve().parents[1]


class SharedProvider(Provider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.issued = set()
        self.barrier = None
        self.overlap = []
        self.tool_programs = []

    def target(self, backend, payload):
        history = payload.get('input' if backend == 'codex' else 'messages', [])
        matches = re.findall(r'NETCAP_([AB])_TURN_(\d+) PROFILE_((?:codex|claude)-[ab]) TOOL_([a-z]+)', json.dumps(history))
        if not matches:
            return None
        letter, turn, profile, server = matches[-1]
        assert letter == self.route and profile.startswith(backend)
        native = payload.get('client_metadata', {}).get('thread_id') if backend == 'codex' else json.dumps(payload.get('metadata'), sort_keys=True)
        key = (backend, native, turn, profile)
        if key in self.issued:
            return None
        self.issued.add(key)
        if self.barrier:
            row = {'profile': profile, 'turn': turn, 'entered_at': time.time()}
            self.overlap.append(row)
            self.barrier.wait(timeout=45)
            row['released_at'] = time.time()
        return server, profile, turn

    def responses(self, payload, answer, seq):
        target = self.target('codex', payload)
        if target is None:
            return Provider.responses(self, payload, answer, seq)
        server, profile, turn = target
        self.tool_programs.append({'backend': 'codex', 'profile': profile, 'turn': turn, 'server': server, 'call_id': f'call_{seq}'})
        name = 'mcp__' + server + '__whoami'
        code = 'text({mcp_tools:ALL_TOOLS.filter(t=>t.name.startsWith("mcp__")).map(t=>t.name)}); const available=ALL_TOOLS.some(t=>t.name===' + json.dumps(name) + '); text({server:' + json.dumps(server) + ',available}); if(available) text(await tools.' + name + '({}));'
        item = {'id': f'ctc_{seq}', 'type': 'custom_tool_call', 'call_id': f'call_{seq}', 'name': 'exec', 'namespace': 'functions', 'input': code, 'status': 'completed'}
        response = {'id': f'resp_{seq}', 'object': 'response', 'created_at': int(time.time()), 'status': 'completed', 'model': payload['model'],
            'output': [item], 'usage': {'input_tokens': 32, 'output_tokens': 16, 'total_tokens': 48}}
        return self.sse([('response.created', {'response': dict(response, status='in_progress', output=[])}),
            ('response.output_item.added', {'output_index': 0, 'item': dict(item, status='in_progress')}),
            ('response.output_item.done', {'output_index': 0, 'item': item}), ('response.completed', {'response': response})])

    def anthropic(self, payload, answer, seq):
        target = self.target('claude', payload)
        if target is None:
            return Provider.anthropic(self, payload, answer, seq)
        server, profile, turn = target
        selected = [tool for tool in payload.get('tools', []) if tool.get('name') == 'mcp__' + server + '__whoami']
        self.tool_programs.append({'backend': 'claude', 'profile': profile, 'turn': turn, 'server': server, 'call_id': f'tool_{seq}', 'available': bool(selected)})
        if not selected:
            return Provider.anthropic(self, payload, answer, seq)
        return ToolProvider.anthropic(self, {**payload, 'messages': [], 'tools': selected}, answer, seq)

    def returned(self, profile, turn):
        program = next(row for row in self.tool_programs if row['profile'] == profile and row['turn'] == str(turn))
        values = []
        if program['backend'] == 'codex':
            selected = [{'body': {'input': [item]}} for request in self.requests for item in request['body'].get('input', [])
                if item.get('type') == 'custom_tool_call_output' and item.get('call_id') == program['call_id']]
            containers = decoded_outputs(selected)
        else:
            containers = [block for request in self.requests for message in request['body'].get('messages', [])
                for block in message.get('content', []) if isinstance(block, dict) and block.get('type') == 'tool_result' and block.get('tool_use_id') == program['call_id']]
        for container in containers:
            content = container.get('content', [])
            if isinstance(content, str):
                content = [{'type': 'text', 'text': content}]
            for block in content:
                if isinstance(block, dict) and block.get('type') == 'text':
                    try: values.append(json.loads(block['text']))
                    except (ValueError, TypeError): pass
        return values


class SharedFixture(ProfilesFixture):
    def __init__(self, binary, provider_class=SharedProvider):
        super().__init__(binary)
        for provider in self.providers.values():
            provider.close()
        provider_root = self.root / 'joint-providers'
        provider_root.mkdir()
        self.providers = {letter: provider_class(provider_root, letter) for letter in ['A', 'B']}
        self.provider = self.providers['A']
        self.config['projects'] = []
        self.config['capability_assets'] = []
        self.assets = {}
        self.daemons = {}
        self.make_asset('shared', 'identity', ['codex', 'claude'])
        self.make_asset('unused-credentials', 'unavailable', ['codex'])
        unused = self.assets['unused-credentials'] / '.mcp.json'
        value = json.loads(unused.read_text())
        value['mcpServers']['unavailable']['env'] = {'MCP_TOKEN': '${UNCONFIGURED_MCP_TOKEN}'}
        write(unused, value)
        for id, profile in self.profiles.items():
            letter = id[-1].upper()
            backend = profile['instance']['kind']
            previous = profile['route']['endpoint']
            endpoint = self.providers[letter].url + ('/v1' if backend == 'codex' else '')
            profile['route']['endpoint'] = endpoint
            cwd = self.root / id / 'work'
            cwd.mkdir()
            subprocess.run(['git', 'init', '--quiet', str(cwd)], env=self.native_envs[id], check=True)
            self.config['projects'].append({'id': id, 'name': 'Joint ' + id, 'cwd': str(cwd)})
            log = self.root / 'calls' / id
            log.mkdir(parents=True)
            values = {'NETCAP_MCP_CONTEXT': id, 'NETCAP_MCP_SECRET': 'fixture-mcp-' + id, 'NETCAP_MCP_LOG': str(log)}
            profile['instance']['env_allow'] += list(values)
            if backend == 'codex':
                self.native_envs[id].update(values)
                config = Path(profile['instance']['profile']) / 'config.toml'
                config.write_text(config.read_text().replace(previous, endpoint))
            else:
                settings = Path(profile['instance']['settings_file'])
                data = json.loads(settings.read_text())
                data['env'].update(values)
                data['env']['ENABLE_TOOL_SEARCH'] = 'false'
                write(settings, data)
            self.make_asset('private-' + id, 'exclusive', [backend])

    def start_native(self):
        for id, profile in self.profiles.items():
            if profile['instance']['kind'] == 'codex':
                self.start_daemon(id)

    def start_daemon(self, id):
        previous = self.daemons.get(id)
        assert previous is None or previous.poll() is not None
        socket = Path(self.profiles[id]['instance']['socket'])
        if socket.exists():
            socket.unlink()
        log = (self.root / id / 'native.log').open('a')
        self.logs.append(log)
        process = subprocess.Popen([SERVER_CLI, 'app-server', '--listen', 'unix://' + str(socket)], env=self.native_envs[id],
            cwd=self.root / id / 'work', stdout=log, stderr=log, start_new_session=True)
        self.natives.append(process)
        self.daemons[id] = process
        wait_for(socket.exists, 'owned ' + id + ' socket')

    def make_asset(self, id, server, backends):
        root = self.root / 'assets' / id
        root.mkdir(parents=True)
        script = '''import json,os,sys
from pathlib import Path
label=LABEL
server=SERVER
for line in sys.stdin:
 request=json.loads(line)
 if 'id' not in request:continue
 method=request['method']
 if method=='initialize':result={'protocolVersion':request['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':label,'version':'v1'}}
 elif method=='tools/list':result={'tools':[{'name':'whoami','description':'Return the owned execution binding','inputSchema':{'type':'object','properties':{}}}]}
 elif method=='tools/call':
  profile=os.environ['MCP_CONTEXT']
  identity={'profile':profile,'cwd':os.getcwd(),'credential_matches':os.getenv('MCP_TOKEN')=='fixture-mcp-'+profile,'asset':label,'version':'v1'}
  with (Path(os.environ['MCP_LOG'])/(server+'.jsonl')).open('a') as output:output.write(json.dumps(identity)+'\\n')
  result={'content':[{'type':'text','text':json.dumps(identity)}]}
 else:result={}
 print(json.dumps({'jsonrpc':'2.0','id':request['id'],'result':result}),flush=True)
'''.replace('LABEL', repr(id)).replace('SERVER', repr(server))
        write(root / 'server.py', script)
        write(root / '.mcp.json', {'mcpServers': {server: {'command': 'python3', 'args': ['./server.py'], 'env': {
            'MCP_CONTEXT': '${NETCAP_MCP_CONTEXT}', 'MCP_TOKEN': '${NETCAP_MCP_SECRET}', 'MCP_LOG': '${NETCAP_MCP_LOG}'}}}})
        self.assets[id] = root
        self.config['capability_assets'].append({'id': id, 'name': id, 'kind': 'mcp', 'version': 'v1', 'source': str(root), 'backends': backends})

    def calls(self, profile, server):
        path = self.root / 'calls' / profile / (server + '.jsonl')
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def operation(self, receipt):
        return next(op for op in self.snapshot()['operations'] if op['id'] == receipt['operation_id'])

    def settle(self, receipt):
        def ready():
            op = self.operation(receipt)
            if op['status'] in ['failed', 'not_sent']:
                raise RuntimeError(op.get('error', op['status']))
            # An unknown observation does not terminate the original operation.
            return op if op['status'] in ['succeeded', 'interrupted'] else None
        return wait_for(ready, 'owned joint operation', timeout=60)


def exercise(f):
    checks, sessions, originals, frames = {}, {}, {}, {}
    initial_calls = {}
    finished = False

    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    def execution(id):
        return f.api('/api/sessions/' + sessions[id] + '/execution')

    def save_frame(name):
        frame = {'/api/workspace': f.api('/api/workspace')}
        for sid in sessions.values():
            for path in ['/api/sessions/' + sid, '/api/sessions/' + sid + '/execution']:
                frame[path] = f.api(path)
        frames[name] = frame

    def call_all(number, server, active=None, overlap=False):
        active = active or list(sessions)
        barrier = threading.Barrier(len(active)) if overlap else None
        for provider in f.providers.values():
            provider.barrier = barrier
        before = {id: len(f.calls(id, server)) for id in active}
        with ThreadPoolExecutor(max_workers=len(active)) as pool:
            receipts = dict(pool.map(lambda id: (id, f.command('send-' + str(number) + '-' + id, 'send', session_id=sessions[id],
                text=f'NETCAP_{id[-1].upper()}_TURN_{number} PROFILE_{id} TOOL_{server}')), active))
        pending = {}
        for id in active:
            pending[id] = wait_for(lambda: next((row for row in f.session(sessions[id])['view']['pending'] if row['status'] == 'pending'), None), 'approval ' + id, timeout=60)
        for provider in f.providers.values():
            provider.barrier = None
        check(str(number) + '_all_calls_wait_for_approval', all(len(f.calls(id, server)) == before[id] for id in active))
        for id, request in pending.items():
            receipt = f.command('approve-' + str(number) + '-' + id, 'respond', session_id=sessions[id], pending_id=request['id'], option_id='decision-0' if id.startswith('codex') else 'allow')
            f.settle(receipt)
        for id in active:
            f.settle(receipts[id])
            wait_for(lambda: f.session(sessions[id])['status'] == 'idle', 'idle ' + id)
            calls = f.calls(id, server)
            expected = {'profile': id, 'cwd': str(f.root / id / 'work'), 'credential_matches': True, 'asset': 'shared' if server == 'identity' else 'private-' + id, 'version': 'v1'}
            check(str(number) + '_' + id + '_one_bound_tool_result', len(calls) == before[id] + 1 and calls[-1] == expected)
            check(str(number) + '_' + id + '_operation_uses_current_snapshot', f.operation(receipts[id])['execution_snapshot_id'] == execution(id)['snapshot']['id'])
            check(str(number) + '_' + id + '_exact_result_returns_to_own_model', expected in f.providers[id[-1].upper()].returned(id, number))
        return receipts

    def apply_shared(id, mode, probe_other_profiles=False):
        before = execution(id)
        original = f.session(sessions[id])
        request = {'base_snapshot_id': before['snapshot']['id'], 'pending_change_id': '', 'selections': [{'asset_id': 'shared', 'mode': mode}]}
        preview = f.api('/api/sessions/' + sessions[id] + '/execution/preview', request)
        request['preview_hash'] = preview['preview_hash']
        request_count = sum(len(provider.requests) for provider in f.providers.values())
        f.command('stage-' + mode + '-' + id, 'stage_execution', session_id=sessions[id], execution_change=request)
        pending = execution(id)['pending_change']
        ref = {'base_snapshot_id': before['snapshot']['id'], 'pending_change_id': pending['id']}
        prepared = f.command('prepare-' + mode + '-' + id, 'prepare_execution', session_id=sessions[id], execution_change=ref)
        f.settle(prepared)
        label = mode + '_' + id
        check(label + '_prepare_preserves_snapshot_and_holds', execution(id)['snapshot'] == before['snapshot'] and f.session(sessions[id])['execution_hold'])
        check(label + '_prepare_does_not_call_model', sum(len(provider.requests) for provider in f.providers.values()) == request_count)
        if id.startswith('codex'):
            stop(f.daemons[id])
            if probe_other_profiles:
                call_all(5, 'exclusive', [name for name in sessions if name != id])
                check(label + '_other_three_call_while_one_daemon_is_stopped', f.daemons[id].poll() is not None)
            f.start_daemon(id)
            wait_for(lambda: f.session(sessions[id])['view'].get('native_status') == 'notLoaded' and f.session(sessions[id])['status'] == 'idle',
                'held original thread unloaded ' + id, timeout=60)
        applied = f.command('apply-' + mode + '-' + id, 'apply_execution', session_id=sessions[id], execution_change=ref)
        f.settle(applied)
        wait_for(lambda: execution(id)['snapshot']['id'] == pending['snapshot']['id'] and any(row.get('asset_id') == 'shared' and row.get('verified') and
            row['mode'] == mode for row in f.session(sessions[id])['view'].get('capability_evidence', [])), 'applied native selection ' + label, timeout=60)
        after = execution(id)
        check(label + '_promotes_exact_candidate', after['snapshot'] == pending['snapshot'] and 'pending_change' not in after)
        check(label + '_preserves_original_identity_and_messages', f.session(sessions[id])['scope']['native_id'] == original['scope']['native_id'] and f.session(sessions[id])['view']['messages'] == original['view']['messages'])
        check(label + '_preserves_previous_snapshot_in_history', before['snapshot'] in after['history'])
        check(label + '_private_selection_keeps_original_binding', next(row for row in after['snapshot']['mcp_selections'] if row['asset_id'] == 'private-' + id) ==
            next(row for row in before['snapshot']['mcp_selections'] if row['asset_id'] == 'private-' + id))
        check(label + '_command_retry_preserves_receipt', f.command('apply-' + mode + '-' + id, 'apply_execution', session_id=sessions[id], execution_change=ref) == applied)
        check(label + '_first_turn_keeps_initial_snapshot', f.operation(initial_calls[id])['execution_snapshot_id'] == originals[id]['execution_snapshot']['id'])
        print(id + ': shared ' + mode + ' applied', flush=True)

    def excluded_all(number):
        for id in sessions:
            before = len(f.calls(id, 'identity'))
            sent = f.command('send-' + str(number) + '-' + id, 'send', session_id=sessions[id], text=f'NETCAP_{id[-1].upper()}_TURN_{number} PROFILE_{id} TOOL_identity')
            f.settle(sent)
            provider = f.providers[id[-1].upper()]
            program = next(row for row in provider.tool_programs if row['profile'] == id and row['turn'] == str(number))
            if id.startswith('codex'):
                requests = [{'body': {'input': [item]}} for request in provider.requests for item in request['body'].get('input', [])
                    if item.get('type') == 'custom_tool_call_output' and item.get('call_id') == program['call_id']]
                absent = {'server': 'identity', 'available': False} in decoded_outputs(requests)
            else:
                absent = program['available'] is False
            check(id + '_disabled_shared_service_is_not_exposed', absent)
            check(id + '_disabled_shared_service_is_not_called', len(f.calls(id, 'identity')) == before)
            check(id + '_disabled_turn_uses_current_snapshot', f.operation(sent)['execution_snapshot_id'] == execution(id)['snapshot']['id'])

    try:
        for id, profile in f.profiles.items():
            f.command('register-' + id, 'register_execution', execution_profile=profile)
        check('four_registrations_do_not_call_models', not any(provider.requests for provider in f.providers.values()))
        for id in f.profiles:
            created = f.command('create-' + id, 'create', project_id=id, route_id=id + '-route', title='Shared MCP ' + id, permission='read_only',
                capability_selections=[{'asset_id': 'shared', 'mode': 'enabled'}, {'asset_id': 'private-' + id, 'mode': 'enabled'}])
            sessions[id] = created['session_id']
            f.settle(created)
            wait_for(lambda: f.session(sessions[id])['status'] == 'idle' and
                len([row for row in f.session(sessions[id])['view'].get('capability_evidence', []) if row.get('asset_id') and row.get('verified')]) == 2,
                'new shared execution and native inventory ' + id, timeout=60)
            originals[id] = f.session(sessions[id])
            evidence = originals[id]['view'].get('capability_evidence', [])
            check(id + '_two_explicit_services_verified', len([row for row in evidence if row.get('asset_id') and row.get('verified')]) == 2)
        check('four_independent_native_state_namespaces', len({row['scope']['state_namespace'] for row in originals.values()}) == 4)
        references = [next(ref for ref in row['execution_snapshot']['capability_refs'] if ref['asset']['id'] == 'shared') for row in originals.values()]
        check('all_four_use_the_same_registered_asset_and_source_version', all(ref == references[0] for ref in references))
        check('creation_does_not_call_models', not any(provider.requests for provider in f.providers.values()))
        save_frame('initial')
        initial_calls = call_all(1, 'identity', overlap=True)
        overlap = [row for provider in f.providers.values() for row in provider.overlap]
        check('four_native_model_requests_overlap', len(overlap) == 4 and {row['profile'] for row in overlap} == set(sessions) and max(row['entered_at'] for row in overlap) <= min(row['released_at'] for row in overlap))
        call_all(2, 'exclusive')
        save_frame('used')
        shared_script = f.assets['shared'] / 'server.py'
        body = shared_script.read_bytes()
        shared_script.write_bytes(body + b'\n# owned shared dependency drift\n')
        before = sum(len(provider.requests) for provider in f.providers.values())
        for id in sessions:
            check(id + '_shared_dependency_drift_visible', execution(id)['status'] == 'drifted')
            try:
                f.command('drift-shared-' + id, 'send', session_id=sessions[id], text='MUST_NOT_SEND')
            except HTTPError as error:
                check(id + '_shared_drift_refuses_input', error.code == 409)
            else:
                check(id + '_shared_drift_refuses_input', False)
        check('shared_drift_has_no_model_side_effect', sum(len(provider.requests) for provider in f.providers.values()) == before)
        save_frame('shared-drift')
        shared_script.write_bytes(body)
        private_script = f.assets['private-codex-a'] / 'server.py'
        body = private_script.read_bytes()
        private_script.write_bytes(body + b'\n# owned private dependency drift\n')
        for id in sessions:
            check(id + '_private_drift_has_exact_scope', execution(id)['status'] == ('drifted' if id == 'codex-a' else 'unchanged'))
        call_all(3, 'identity', [id for id in sessions if id != 'codex-a'])
        private_script.write_bytes(body)
        call_all(3, 'identity', ['codex-a'])
        f.restart_console()
        for id, sid in sessions.items():
            resumed = f.command('resume-' + id, 'resume', session_id=sid)
            f.settle(resumed)
        call_all(4, 'identity')
        for id, sid in sessions.items():
            restored = f.session(sid)
            check(id + '_restart_preserves_original_identity_and_snapshot', restored['scope']['native_id'] == originals[id]['scope']['native_id'] and restored['execution_snapshot'] == originals[id]['execution_snapshot'])
            encoded = json.dumps(execution(id))
            check(id + '_execution_does_not_export_credential_values', all('fixture-mcp-' + name not in encoded for name in sessions))
        save_frame('restored')
        for id in sessions:
            apply_shared(id, 'disabled', probe_other_profiles=id == 'codex-a')
        save_frame('shared-disabled')
        excluded_all(6)
        call_all(7, 'exclusive')
        for id in sessions:
            apply_shared(id, 'enabled')
        save_frame('shared-reenabled')
        call_all(8, 'identity', overlap=True)
        save_frame('reenabled-used')
        before = sum(len(provider.requests) for provider in f.providers.values())
        disabled = f.command('create-disabled-missing-credential', 'create', project_id='codex-a', route_id='codex-a-route', title='Excluded MCP without credentials', permission='read_only',
            capability_selections=[{'asset_id': 'unused-credentials', 'mode': 'disabled'}])
        f.settle(disabled)
        wait_for(lambda: any(row.get('asset_id') == 'unused-credentials' and row.get('verified') and row['runtime_status'] == 'disabled'
            for row in f.session(disabled['session_id'])['view'].get('capability_evidence', [])), 'disabled source without credential access')
        check('disabled_source_without_credentials_creates_no_model_request', sum(len(provider.requests) for provider in f.providers.values()) == before)
        sent = f.command('probe-disabled-source', 'send', session_id=disabled['session_id'], text='NETCAP_A_TURN_99 PROFILE_codex-a TOOL_unavailable')
        f.settle(sent)
        program = next(row for row in f.providers['A'].tool_programs if row['profile'] == 'codex-a' and row['turn'] == '99')
        requests = [{'body': {'input': [item]}} for request in f.providers['A'].requests for item in request['body'].get('input', [])
            if item.get('type') == 'custom_tool_call_output' and item.get('call_id') == program['call_id']]
        check('disabled_source_is_absent_from_native_tool_registry', {'server': 'unavailable', 'available': False} in decoded_outputs(requests))
        check('disabled_source_has_no_tool_call', not f.calls('codex-a', 'unavailable'))
        read = f.api('/api/sessions/' + disabled['session_id'] + '/execution')
        before = sum(len(provider.requests) for provider in f.providers.values())
        try:
            f.api('/api/sessions/' + disabled['session_id'] + '/execution/preview', {'base_snapshot_id': read['snapshot']['id'], 'pending_change_id': '',
                'selections': [{'asset_id': 'unused-credentials', 'mode': 'enabled'}]})
        except HTTPError as error:
            check('activation_without_credential_binding_is_rejected', error.code == 409)
        else:
            check('activation_without_credential_binding_is_rejected', False)
        check('rejected_activation_keeps_disabled_snapshot_without_model_requests', f.api('/api/sessions/' + disabled['session_id'] + '/execution')['snapshot'] == read['snapshot'] and
            sum(len(provider.requests) for provider in f.providers.values()) == before)
        for id in sessions:
            check(id + '_model_history_does_not_contain_other_profiles', all(not any('PROFILE_' + other in json.dumps(row['body']) for other in sessions if other != id)
                for row in f.providers[id[-1].upper()].requests if 'PROFILE_' + id in json.dumps(row['body'])))
        for letter, provider in f.providers.items():
            check(letter + '_all_model_requests_have_correct_credentials', bool(provider.requests) and all(row['auth_matches'] for row in provider.requests) and not provider.errors)
        finished = True
    finally:
        write(f.root / 'result.json', {'checks': checks, 'passed': finished and all(checks.values()), 'sessions': sessions, 'frames': frames,
            'provider_errors': {letter: provider.errors for letter, provider in f.providers.items()},
            'scope': 'production HTTP, one shared asset plus one private asset per execution, Codex A/B and Claude A/B, local scripted models and owned MCP processes'})
        write(f.root / 'snapshot.json', f.snapshot())
        write(f.root / 'tool-programs.json', {letter: provider.tool_programs for letter, provider in f.providers.items()})
    print(json.dumps({'passed': True, 'checks': len(checks)}), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--netcap', type=Path, required=True)
    parser.add_argument('--inside', type=Path)
    args = parser.parse_args()
    if args.inside:
        assert os.path.samefile(args.inside / 'policy', '/etc/claude-code')
        os.environ['TMPDIR'] = str(args.inside)
        tempfile.tempdir = None
        verify_versions(args.inside, ['codex', 'server', 'claude'])
        with SharedFixture(str(args.netcap)) as fixture:
            print('Fixture directory:', fixture.root, flush=True)
            exercise(fixture)
        return
    root = Path(tempfile.mkdtemp(prefix='netcap-shared-mcp-profiles-', dir='/var/tmp'))
    (root / 'policy').mkdir()
    registry = root / 'windows/Windows/System32/reg.exe'
    write(registry, '#!/usr/bin/python3\nimport sys\nsys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\nraise SystemExit(1)\n')
    registry.chmod(0o700)
    print('Evidence directory:', root, flush=True)
    command = ['bwrap', '--unshare-user', '--unshare-pid', '--unshare-net', '--die-with-parent', '--ro-bind', '/', '/',
        '--tmpfs', '/etc', '--ro-bind', str(root / 'policy'), '/etc/claude-code', '--ro-bind', str(root / 'policy'), '/etc/codex',
        '--bind', str(root), str(root), '--tmpfs', '/mnt', '--ro-bind', str(root / 'windows'), '/mnt/c',
        '--tmpfs', '/tmp', '--tmpfs', '/run', '--proc', '/proc', '--dev', '/dev', sys.executable, str(Path(__file__).resolve()),
        '--netcap', str(args.netcap.resolve()), '--inside', str(root)]
    process = subprocess.Popen(command, cwd=REPO, env=environment(root / 'environment'), text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        out, err = process.communicate(timeout=300)
        write(root / 'runner.stdout', out); write(root / 'runner.stderr', err)
        print(out, end=''); print(err, end='', file=sys.stderr)
    finally:
        stop(process)
    raise SystemExit(process.returncode)


if __name__ == '__main__':
    main()
