#!/usr/bin/env python3
"""Four production profiles combining shared/private skills, MCP and CLI plugins.

Native body transport and tools use a fixed local provider sequence. This does
not claim autonomous method adoption or a real-model Gate.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
from urllib.error import HTTPError
from uuid import uuid4

from console_fixture import Provider, wait_for
from execution_config_probe import environment, verify_versions, write
from mcp_binding_probe import ToolProvider
from native_mcp_selection_smoke import decoded_outputs
from shared_mcp_profiles_native import SharedFixture, SharedProvider
from runtime.common import stop

REPO = Path(__file__).resolve().parents[1]


class MixedProvider(SharedProvider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.aliases = {}
        self.skill_plans = {}
        self.skill_programs = []
        self.skill_flows = []

    def target(self, backend, payload):
        target = super().target(backend, payload)
        if target:
            server, profile, turn = target
            return self.aliases.get((profile, server), server), profile, turn
        return None

    def call_skill(self, payload, seq, name):
        message = {'id': f'msg_{seq}', 'type': 'message', 'role': 'assistant', 'model': payload['model'],
                   'content': [], 'stop_reason': None, 'stop_sequence': None, 'usage': {'input_tokens': 32, 'output_tokens': 0}}
        return self.sse([
            ('message_start', {'message': message}),
            ('content_block_start', {'index': 0, 'content_block': {'type': 'tool_use', 'id': f'skill_{seq}', 'name': 'Skill', 'input': {}}}),
            ('content_block_delta', {'index': 0, 'delta': {'type': 'input_json_delta', 'partial_json': json.dumps({'skill': name})}}),
            ('content_block_stop', {'index': 0}),
            ('message_delta', {'delta': {'stop_reason': 'tool_use', 'stop_sequence': None}, 'usage': {'output_tokens': 32}}),
            ('message_stop', {}),
        ])

    def call_mcp(self, payload, answer, seq, target):
        server, profile, turn = target
        selected = [tool for tool in payload.get('tools', []) if tool.get('name') == 'mcp__' + server + '__whoami']
        self.tool_programs.append({'backend': 'claude', 'profile': profile, 'turn': turn, 'server': server, 'call_id': f'tool_{seq}', 'available': bool(selected)})
        if not selected:
            return Provider.anthropic(self, payload, answer, seq)
        return ToolProvider.anthropic(self, {**payload, 'messages': [], 'tools': selected}, answer, seq)

    def anthropic(self, payload, answer, seq):
        target = self.target('claude', payload)
        if target:
            _, profile, turn = target
            name = self.skill_plans.get((profile, turn))
            if name:
                program = {'profile': profile, 'turn': turn, 'name': name, 'call_id': f'skill_{seq}', 'seq': seq}
                self.skill_programs.append(program)
                self.skill_flows.append({'call_id': program['call_id'], 'target': target, 'mcp_sent': False})
                return self.call_skill(payload, seq, name)
            return self.call_mcp(payload, answer, seq, target)
        returned_ids = {block.get('tool_use_id') for message in payload.get('messages', []) for block in message.get('content', [])
                        if isinstance(block, dict) and block.get('type') == 'tool_result'}
        for flow in self.skill_flows:
            if not flow['mcp_sent'] and flow['call_id'] in returned_ids:
                flow['mcp_sent'] = True
                return self.call_mcp(payload, answer, seq, flow['target'])
        return Provider.anthropic(self, payload, answer, seq)


class MixedFixture(SharedFixture):
    def __init__(self, binary):
        super().__init__(binary, MixedProvider)
        self.methods = {}
        self.make_method('shared-method', ['codex', 'claude'])
        for profile, value in self.profiles.items():
            backend = value['instance']['kind']
            if backend == 'codex':
                config = Path(value['instance']['profile']) / 'config.toml'
                config.write_text(config.read_text().replace('plugins = false', 'plugins = true\nplugin_sharing = false'))
            self.make_method('method-' + profile, [backend])
        for backend in ['codex', 'claude']:
            self.make_bundle(backend)

    def make_method(self, asset, backends, version='v1'):
        root = self.root / 'assets' / (asset if version == 'v1' else asset + '-' + version)
        body, reference = uuid4().hex, uuid4().hex
        write(root / 'SKILL.md', f'---\nname: {asset}\ndescription: Owned method {asset}\n---\nMIXED_BODY={body}\nRead [guide](references/guide.md) when using this method.\n')
        write(root / 'references/guide.md', 'MIXED_REFERENCE=' + reference + '\n')
        self.methods.setdefault(asset, {})[version] = {'source': str(root), 'body': body, 'reference': reference}
        definition = {'id': asset, 'name': asset, 'kind': 'skill', 'version': version, 'source': str(root), 'backends': backends}
        if version == 'v1':
            self.config['capability_assets'].append(definition)
        return definition

    def make_bundle(self, backend):
        asset = 'suite-' + backend
        root = self.root / 'assets' / asset
        write(root / ('.' + backend + '-plugin') / 'plugin.json', {'name': asset, 'version': '1.0.0'})
        body, reference = uuid4().hex, uuid4().hex
        write(root / 'skills/bundle-method/SKILL.md', f'---\nname: bundle-method\ndescription: Owned {backend} plugin method\n---\nMIXED_BODY={body}\nRead references/guide.md relative to this method.\n')
        write(root / 'skills/bundle-method/references/guide.md', 'MIXED_REFERENCE=' + reference + '\n')
        script = '''import json,os,sys
from pathlib import Path
for line in sys.stdin:
 request=json.loads(line)
 if 'id' not in request:continue
 method=request['method']
 if method=='initialize':result={'protocolVersion':request['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':ASSET,'version':'v1'}}
 elif method=='tools/list':result={'tools':[{'name':'whoami','description':'Return the owned mixed plugin binding','inputSchema':{'type':'object','properties':{}}}]}
 elif method=='tools/call':
  profile=os.environ['NETCAP_MCP_CONTEXT']
  identity={'profile':profile,'cwd':os.getcwd(),'credential_matches':os.getenv('NETCAP_MCP_SECRET')=='fixture-mcp-'+profile,'asset':ASSET,'version':'v1'}
  with (Path(os.environ['NETCAP_MCP_LOG'])/'bundle.jsonl').open('a') as output:output.write(json.dumps(identity)+'\\n')
  result={'content':[{'type':'text','text':json.dumps(identity)}]}
 else:result={}
 print(json.dumps({'jsonrpc':'2.0','id':request['id'],'result':result}),flush=True)
'''.replace('ASSET', repr(asset))
        config = {'command': 'python3', 'args': ['-u', '-c', script]}
        names = ['NETCAP_MCP_CONTEXT', 'NETCAP_MCP_SECRET', 'NETCAP_MCP_LOG']
        if backend == 'codex':
            config['env_vars'] = names
        else:
            config['env'] = {name: '${' + name + '}' for name in names}
        write(root / '.mcp.json', {'mcpServers': {'bundle': config}})
        self.methods[asset] = {'v1': {'source': str(root / 'skills/bundle-method'), 'body': body, 'reference': reference}}
        self.config['capability_assets'].append({'id': asset, 'name': asset, 'kind': 'cli_plugin', 'version': 'v1', 'source': str(root), 'backends': [backend]})

    def api(self, path, body=None):
        try:
            return super().api(path, body)
        except HTTPError as error:
            detail = json.load(error)
            write(self.root / 'last-http-error.json', {'path': path, 'status': error.code, 'response': detail})
            error.msg = str(detail)
            raise


def exercise(f):
    checks, sessions, frames, originals, previews, contexts, form_previews = {}, {}, {}, {}, {}, {}, {}
    approvals, observations, rounds = [], [], []
    finished = False

    def check(name, condition):
        checks[name] = bool(condition)
        assert condition, name

    def read(profile):
        return f.api('/api/sessions/' + sessions[profile] + '/execution')

    def frame(label):
        data = {'/api/workspace': f.api('/api/workspace'), '/api/capabilities': f.api('/api/capabilities')}
        for sid in sessions.values():
            for path in ['/api/sessions/' + sid, '/api/sessions/' + sid + '/execution']:
                data[path] = f.api(path)
        frames[label] = data

    def all_verified(profile):
        view = f.session(sessions[profile])['view']
        observed = view.get('plugin_evidence', []) + view.get('skill_evidence', []) + view.get('capability_evidence', [])
        ids = {'shared', 'private-' + profile, 'shared-method', 'method-' + profile, 'suite-' + profile.split('-')[0]}
        return f.session(sessions[profile])['status'] == 'idle' and all(any(row.get('asset_id') == asset and row.get('verified') for row in observed) for asset in ids)

    def method_name(profile, asset):
        snapshot = read(profile)['snapshot']
        if profile.startswith('claude') and asset.startswith(('shared-', 'method-')):
            return next(row['name'] for row in snapshot['skill_selections'] if row['asset_id'] == asset)
        pack = next(row for row in snapshot['plugin_selections'] if row['asset_id'] == asset)
        return next(row['native_name'] for row in pack['components'] if row['kind'] == 'skill')

    def execute_round(number, method, server, active=None, overlap=False):
        active = active or list(sessions)
        barrier = threading.Barrier(len(active)) if overlap else None
        before = {profile: len(f.calls(profile, server)) for profile in active}
        request_starts = {profile: len(f.providers[profile[-1].upper()].requests) for profile in active}
        selected = {}
        for profile in active:
            asset = method(profile)
            selected[profile] = asset
            provider = f.providers[profile[-1].upper()]
            provider.barrier = barrier
            if profile.startswith('claude'):
                provider.skill_plans[(profile, str(number))] = method_name(profile, asset)

        def send(profile):
            params = {'session_id': sessions[profile], 'text': f'NETCAP_{profile[-1].upper()}_TURN_{number} PROFILE_{profile} TOOL_{server}'}
            if profile.startswith('codex'):
                params['skill_input'] = {'snapshot_id': read(profile)['snapshot']['id'], 'items': [{'asset_id': selected[profile], 'name': method_name(profile, selected[profile])}]}
            return profile, f.command(f'round-{number}-{profile}', 'send', **params)

        with ThreadPoolExecutor(max_workers=len(active)) as pool:
            receipts = dict(pool.map(send, active))
        deadline = time.monotonic() + 90
        handled = set()
        statuses = {}
        while time.monotonic() < deadline:
            complete = True
            for profile, receipt in receipts.items():
                op = f.operation(receipt)
                if statuses.get(profile) != op['status']:
                    statuses[profile] = op['status']
                    observations.append({'profile': profile, 'round': number, 'operation_id': receipt['operation_id'],
                                         'status': op['status'], 'observed_at': time.time()})
                if op['status'] in ['not_sent', 'failed', 'interrupted']:
                    raise RuntimeError(json.dumps({'profile': profile, 'round': number, 'operation': op['status'], 'error': op.get('error')}))
                # Keep observing the same input through sampled unknown states;
                # the original deadline still bounds the test and no input retries.
                complete = complete and op['status'] == 'succeeded'
                for pending in f.session(sessions[profile])['view'].get('pending', []):
                    if pending['status'] != 'pending' or pending['id'] in handled:
                        continue
                    check(f'{number}_{profile}_approval_precedes_mcp_call_{len(handled)}', len(f.calls(profile, server)) == before[profile])
                    if pending['kind'] == 'Skill':
                        check(f'{number}_{profile}_skill_approval_is_selected_method', pending.get('request', {}).get('skill') == method_name(profile, selected[profile]))
                    elif profile.startswith('codex'):
                        request = pending.get('request', {})
                        expected_server = f.providers[profile[-1].upper()].aliases.get((profile, server), server)
                        check(f'{number}_{profile}_approval_is_exact_mcp_server', pending['kind'] == 'mcpServer/elicitation/request' and request.get('serverName') == expected_server and
                              request.get('_meta', {}).get('codex_approval_kind') == 'mcp_tool_call')
                    else:
                        wire_server = f.providers[profile[-1].upper()].aliases.get((profile, server), server)
                        check(f'{number}_{profile}_approval_is_exact_mcp_tool', pending['kind'] == 'mcp__' + wire_server + '__whoami')
                    reply = f.command(f'approve-{number}-{profile}-{len(handled)}', 'respond', session_id=sessions[profile], pending_id=pending['id'], option_id='decision-0' if profile.startswith('codex') else 'allow')
                    f.settle(reply)
                    approvals.append({'profile': profile, 'round': number, 'pending': pending, 'receipt': reply})
                    handled.add(pending['id'])
            if complete:
                break
            time.sleep(.03)
        else:
            raise TimeoutError('mixed native round did not finish: ' + json.dumps({'round': number, 'statuses': statuses}))
        for provider in f.providers.values():
            provider.barrier = None
        for profile, receipt in receipts.items():
            f.settle(receipt)
            wait_for(lambda: all_verified(profile), 'mixed capability inventory after round ' + str(number) + ' ' + profile, timeout=30)
            asset = selected[profile]
            snapshot = read(profile)['snapshot']
            version = next(row['asset']['version'] for row in snapshot['capability_refs'] if row['asset']['id'] == asset)
            rounds.append({'profile': profile, 'round': number, 'receipt': receipt, 'snapshot_id': snapshot['id'],
                           'asset': asset, 'version': version, 'server': server, 'method_name': method_name(profile, asset),
                           'request_start': request_starts[profile]})
            expected_asset = 'shared' if server == 'identity' else 'private-' + profile if server == 'exclusive' else 'suite-' + profile.split('-')[0]
            expected = {'profile': profile, 'cwd': str(f.root / profile / 'work'), 'credential_matches': True, 'asset': expected_asset, 'version': 'v1'}
            calls = f.calls(profile, server)
            check(f'{number}_{profile}_one_bound_actual_tool_call', len(calls) == before[profile] + 1 and calls[-1] == expected)
            check(f'{number}_{profile}_exact_tool_result_returns_to_model', expected in f.providers[profile[-1].upper()].returned(profile, number))
            check(f'{number}_{profile}_exactly_one_mcp_approval', len([row for row in approvals if row['profile'] == profile and row['round'] == number and row['pending']['kind'] != 'Skill']) == 1)
            check(f'{number}_{profile}_operation_uses_current_snapshot', f.operation(receipt)['execution_snapshot_id'] == read(profile)['snapshot']['id'])
            if profile.startswith('codex'):
                context = f.api('/api/sessions/' + sessions[profile] + '/skills/context', {})
                contexts[f'{number}/{profile}'] = context
                turn = f.operation(receipt)['receipt']['native_turn']
                evidence = next(row['input_evidence'] for row in context['frames'] if row['turn_id'] == turn)
                check(f'{number}_{profile}_body_belongs_to_original_input_and_snapshot', evidence['operation_id'] == receipt['operation_id'] and evidence['snapshot_id'] == read(profile)['snapshot']['id'] and
                      len(evidence['items']) == 1 and evidence['items'][0]['asset_id'] == asset and evidence['items'][0]['status'] == 'loaded_exact')
            provider = f.providers[profile[-1].upper()]
            body = f.methods[asset][version]['body']
            own = [request for request in provider.requests[request_starts[profile]:] if 'PROFILE_' + profile in json.dumps(request['body'])]
            program = next(row for row in provider.tool_programs if row['profile'] == profile and row['turn'] == str(number))
            if profile.startswith('codex'):
                outputs = [{'body': {'input': [item]}} for request in own for item in request['body'].get('input', [])
                           if item.get('type') == 'custom_tool_call_output' and item.get('call_id') == program['call_id']]
                registries = [row['mcp_tools'] for row in decoded_outputs(outputs) if 'mcp_tools' in row]
                check(f'{number}_{profile}_native_mcp_registry_is_observed', bool(registries))
            else:
                request = provider.requests[int(program['call_id'].split('_')[-1]) - 1]['body']
                registries = [[row['name'] for row in request['tools'] if row['name'].startswith('mcp__')]]
            names = {'mcp__identity__whoami', 'mcp__exclusive__whoami'}
            if next(row for row in snapshot['plugin_selections'] if row['asset_id'] == 'suite-' + profile.split('-')[0])['mode'] == 'enabled':
                names.add('mcp__' + provider.aliases[(profile, 'bundle')] + '__whoami')
            check(f'{number}_{profile}_only_enabled_mcp_tools_are_exposed', all(set(registry) == names for registry in registries))
            check(f'{number}_{profile}_selected_method_body_reaches_own_model', bool(own) and any(body in json.dumps(request['body']) for request in own))
            if asset == 'shared-method' and version == 'v1' and 'v2' in f.methods[asset]:
                check(f'{number}_{profile}_does_not_receive_unselected_method_version', all(f.methods[asset]['v2']['body'] not in json.dumps(request['body']) for request in own))
            if number <= 3 or version == 'v2' and number == 11:
                if profile.startswith('claude'):
                    program = next(row for row in provider.skill_programs if row['profile'] == profile and row['turn'] == str(number))
                    check(f'{number}_{profile}_body_was_not_in_pre_skill_request', body not in json.dumps(provider.requests[program['seq'] - 1]['body']))
        print('mixed round', number, 'passed', flush=True)

    def change(profile, label, selections):
        before = read(profile)
        ids = {row['asset_id'] for row in selections}
        request = {'base_snapshot_id': before['snapshot']['id'], 'pending_change_id': '', 'selections': selections}
        preview = f.api('/api/sessions/' + sessions[profile] + '/execution/preview', request)
        previews[label + '/' + profile] = preview
        check(label + '_' + profile + '_diff_only_contains_selected_resources', {row['asset_id'] for row in preview['changes']} == ids and len(preview['changes']) == len(ids))
        # Exercise the full form payload as well as the partial API patch. Both
        # must leave omitted/unchanged resource versions out of the difference.
        refs = {row['asset']['id']: row['asset'] for row in before['snapshot']['capability_refs']}
        picks = {}
        for row in before['snapshot'].get('mcp_selections', []) + before['snapshot'].get('skill_selections', []) + before['snapshot'].get('plugin_selections', []):
            asset = refs[row['asset_id']]
            picks[row['asset_id']] = {'asset_id': row['asset_id'], 'mode': row['mode']}
            if profile.startswith('claude') or asset['kind'] in ['skill', 'cli_plugin']:
                picks[row['asset_id']]['version'] = asset['version']
        for row in selections:
            picks[row['asset_id']].update(row)
        form_request = {**request, 'selections': list(picks.values()), 'adopt_local_settings': False,
                        'adopt_codex_hook_sources': False, 'adopt_codex_configuration_sources': False}
        form_preview = f.api('/api/sessions/' + sessions[profile] + '/execution/preview', form_request)
        form_previews[label + '/' + profile] = {'request': form_request, 'preview': form_preview}
        check(label + '_' + profile + '_full_form_has_the_same_exact_changes', form_preview['changes'] == preview['changes'])
        f.command('stage-' + label + '-' + profile, 'stage_execution', session_id=sessions[profile], execution_change={**request, 'preview_hash': preview['preview_hash']})
        pending = read(profile)['pending_change']
        if profile == 'codex-a' or label == 'v2':
            frame(label + '-staged-' + profile)
        ref = {'base_snapshot_id': before['snapshot']['id'], 'pending_change_id': pending['id']}
        prepared = f.command('prepare-' + label + '-' + profile, 'prepare_execution', session_id=sessions[profile], execution_change=ref)
        f.settle(prepared)
        applied = f.command('apply-' + label + '-' + profile, 'apply_execution', session_id=sessions[profile], execution_change=ref)
        f.settle(applied)
        wait_for(lambda: all_verified(profile), 'mixed change verified ' + label + ' ' + profile, timeout=60)
        after = read(profile)
        check(label + '_' + profile + '_promotes_exact_saved_candidate', after['snapshot'] == pending['snapshot'] and 'pending_change' not in after)
        check(label + '_' + profile + '_keeps_original_id_and_history', f.session(sessions[profile])['scope']['native_id'] == originals[profile]['scope']['native_id'] and before['snapshot'] in after['history'])
        by_name = lambda value: {row['asset_id']: row for row in value['mcp_selections']}
        check(label + '_' + profile + '_explicit_mcp_bindings_are_unchanged', by_name(before['snapshot']) == by_name(after['snapshot']))
        print('mixed change', profile, label, 'passed', flush=True)

    def dependency_drift(asset, affected):
        source = Path(f.methods[asset]['v1']['source']) / 'references/guide.md'
        content = source.read_bytes()
        before = sum(len(provider.requests) for provider in f.providers.values())
        source.write_bytes(content + b'\nOwned dependency changed.\n')
        try:
            for profile in sessions:
                expected = 'drifted' if profile in affected else 'unchanged'
                check(asset + '_' + profile + '_dependency_has_exact_impact', read(profile)['status'] == expected)
                if profile not in affected:
                    continue
                try:
                    f.command('drift-' + asset + '-' + profile, 'send', session_id=sessions[profile], text='MUST_NOT_SEND')
                except HTTPError as error:
                    check(asset + '_' + profile + '_changed_dependency_blocks_input', error.code == 409)
                else:
                    check(asset + '_' + profile + '_changed_dependency_blocks_input', False)
            check(asset + '_rejected_inputs_do_not_call_models', sum(len(provider.requests) for provider in f.providers.values()) == before)
            frame('drift-' + asset)
            unaffected = [profile for profile in sessions if profile not in affected]
            if unaffected:
                execute_round(8, lambda profile: 'method-' + profile, 'identity', active=unaffected)
        finally:
            source.write_bytes(content)
        check(asset + '_restored_dependency_clears_only_its_drift', all(read(profile)['status'] == 'unchanged' for profile in sessions))

    def register_method_version():
        before = {profile: read(profile)['snapshot'] for profile in sessions}
        request_count = sum(len(provider.requests) for provider in f.providers.values())
        definition = f.make_method('shared-method', ['codex', 'claude'], 'v2')
        current = next(row for row in f.api('/api/capabilities') if row['asset']['id'] == 'shared-method')
        request = {'action': 'update', 'asset_id': 'shared-method', 'base_revision': current['revision'], 'asset': definition}
        preview = f.api('/api/capabilities/preview', request)
        request = {**preview['request'], 'preview_hash': preview['preview_hash']}
        receipt = f.command('register-method-v2', 'manage_capability', capability_change=request)
        check('same_version_registration_reuses_receipt', f.command('register-method-v2', 'manage_capability', capability_change=request) == receipt)
        check('method_version_registration_has_no_model_request', sum(len(provider.requests) for provider in f.providers.values()) == request_count)
        for profile in sessions:
            check(profile + '_method_registration_keeps_pinned_snapshot', read(profile)['snapshot'] == before[profile] and read(profile)['status'] == 'unchanged')
        frame('registered-v2')

    def restart(label):
        snapshots = {profile: read(profile)['snapshot'] for profile in sessions}
        f.restart_console()
        for profile in sessions:
            restored = f.command('resume-' + label + '-' + profile, 'resume', session_id=sessions[profile])
            f.settle(restored)
            check(label + '_' + profile + '_restart_keeps_same_snapshot_and_id', read(profile)['snapshot'] == snapshots[profile] and
                  f.session(sessions[profile])['scope']['native_id'] == originals[profile]['scope']['native_id'])

    try:
        for profile, value in f.profiles.items():
            f.command('register-' + profile, 'register_execution', execution_profile=value)
        for profile in f.profiles:
            ids = ['shared', 'private-' + profile, 'shared-method', 'method-' + profile, 'suite-' + profile.split('-')[0]]
            created = f.command('create-' + profile, 'create', project_id=profile, route_id=profile + '-route', title='Mixed capabilities ' + profile,
                                permission='read_only', capability_selections=[{'asset_id': asset, 'mode': 'enabled'} for asset in ids])
            sessions[profile] = created['session_id']
            f.settle(created)
            wait_for(lambda: all_verified(profile), 'five mixed assets verified ' + profile, timeout=60)
            originals[profile] = f.session(sessions[profile])
            bundle = next(row['server'] for row in originals[profile]['view']['capability_evidence'] if row.get('asset_id') == 'suite-' + profile.split('-')[0])
            if profile.startswith('claude'):
                # The SDK status key is plugin:name:server; model tool names
                # use plugin_name_server, as recorded in the actual request.
                bundle = bundle.replace(':', '_')
            f.providers[profile[-1].upper()].aliases[(profile, 'bundle')] = bundle
            check(profile + '_five_assets_verified_before_input', len(read(profile)['snapshot']['capability_refs']) == 5)
        check('mixed_creation_does_not_call_models', not any(provider.requests for provider in f.providers.values()))
        frame('initial')
        execute_round(1, lambda _: 'shared-method', 'identity', overlap=True)
        overlaps = [row for provider in f.providers.values() for row in provider.overlap]
        check('four_actual_mixed_requests_overlap', len(overlaps) == 4 and max(row['entered_at'] for row in overlaps) <= min(row['released_at'] for row in overlaps))
        execute_round(2, lambda profile: 'method-' + profile, 'exclusive')
        execute_round(3, lambda profile: 'suite-' + profile.split('-')[0], 'bundle')
        frame('used')
        for profile in sessions:
            change(profile, 'disabled', [{'asset_id': asset, 'mode': 'disabled'} for asset in ['shared-method', 'suite-' + profile.split('-')[0]]])
        frame('disabled')
        execute_round(4, lambda profile: 'method-' + profile, 'identity')
        for profile in sessions:
            change(profile, 'enabled', [{'asset_id': asset, 'mode': 'enabled'} for asset in ['shared-method', 'suite-' + profile.split('-')[0]]])
        frame('reenabled')
        execute_round(5, lambda _: 'shared-method', 'identity')
        execute_round(6, lambda profile: 'suite-' + profile.split('-')[0], 'bundle')
        restart('mixed')
        execute_round(7, lambda _: 'shared-method', 'identity')
        frame('restored')
        dependency_drift('shared-method', set(sessions))
        dependency_drift('method-claude-a', {'claude-a'})
        execute_round(8, lambda profile: 'method-' + profile, 'identity', active=['claude-a'])
        register_method_version()
        execute_round(9, lambda _: 'shared-method', 'identity')
        upgraded = set()
        for number, target in [(10, 'codex-a'), (11, 'claude-b')]:
            before = {profile: read(profile)['snapshot'] for profile in sessions}
            change(target, 'v2', [{'asset_id': 'shared-method', 'mode': 'enabled', 'version': 'v2'}])
            upgraded.add(target)
            for profile in sessions:
                snapshot = read(profile)['snapshot']
                version = next(row['asset']['version'] for row in snapshot['capability_refs'] if row['asset']['id'] == 'shared-method')
                check(target + '_' + profile + '_only_explicit_selection_upgrades', version == ('v2' if profile in upgraded else 'v1') and (profile == target or snapshot == before[profile]))
            execute_round(number, lambda _: 'shared-method', 'identity')
            frame('v2-' + target)
        restart('versions')
        execute_round(12, lambda _: 'shared-method', 'identity')
        frame('versions-restored')
        for letter, provider in f.providers.items():
            check(letter + '_local_models_remain_authenticated', bool(provider.requests) and all(row['auth_matches'] for row in provider.requests) and not provider.errors)
        finished = True
    finally:
        write(f.root / 'mixed-result.json', {'passed': finished and all(checks.values()), 'checks': checks, 'sessions': sessions, 'frames': frames,
              'previews': previews, 'form_previews': form_previews, 'contexts': contexts, 'approvals': approvals, 'observations': observations, 'rounds': rounds, 'methods': f.methods, 'method_gate_verified': False,
              'provider_errors': {letter: provider.errors for letter, provider in f.providers.items()}})
        write(f.root / 'mixed-snapshot.json', f.snapshot())
        write(f.root / 'mixed-programs.json', {letter: {'skills': provider.skill_programs, 'mcp': provider.tool_programs} for letter, provider in f.providers.items()})
    print(json.dumps({'passed': True, 'checks': len(checks), 'method_gate_verified': False}), flush=True)


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
        with MixedFixture(str(args.netcap)) as fixture:
            print('Fixture directory:', fixture.root, flush=True)
            exercise(fixture)
        return
    root = Path(tempfile.mkdtemp(prefix='netcap-mixed-capability-profiles-', dir='/var/tmp'))
    (root / 'policy').mkdir()
    registry = root / 'windows/Windows/System32/reg.exe'
    write(registry, '#!/usr/bin/python3\nimport sys\nsys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\nraise SystemExit(1)\n')
    registry.chmod(0o700)
    print('Evidence directory:', root, flush=True)
    command = ['bwrap', '--unshare-user', '--unshare-pid', '--unshare-net', '--die-with-parent', '--ro-bind', '/', '/', '--tmpfs', '/etc',
        '--ro-bind', str(root / 'policy'), '/etc/claude-code', '--ro-bind', str(root / 'policy'), '/etc/codex', '--bind', str(root), str(root),
        '--tmpfs', '/mnt', '--ro-bind', str(root / 'windows'), '/mnt/c', '--tmpfs', '/tmp', '--tmpfs', '/run', '--proc', '/proc', '--dev', '/dev',
        sys.executable, str(Path(__file__).resolve()), '--netcap', str(args.netcap.resolve()), '--inside', str(root)]
    process = subprocess.Popen(command, cwd=REPO, env=environment(root / 'environment'), text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        out, err = process.communicate(timeout=360)
        write(root / 'runner.stdout', out)
        write(root / 'runner.stderr', err)
        print(out, end='')
        print(err, end='', file=sys.stderr)
    finally:
        stop(process)
    raise SystemExit(process.returncode)


if __name__ == '__main__':
    main()
