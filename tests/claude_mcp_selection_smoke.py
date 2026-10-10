#!/usr/bin/env python3
"""Production Claude MCP selection, single-call permission and A/B bindings.

Only owned CLI processes, isolated state, local MCPs and synthetic providers.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import re
import signal
import threading
import time
from urllib.error import HTTPError

from console_fixture import Provider, wait_for
from execution_profiles_smoke import ProfilesFixture
from execution_config_probe import write, verify_versions
from mcp_binding_probe import ToolProvider


class MCPProvider(ToolProvider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.issued = set()
        self.barrier = None
        self.overlap = []

    def anthropic(self, payload, answer, seq):
        markers = re.findall(r'NETCAP_[AB]_TURN_\d+', json.dumps(payload.get('messages', [])))
        marker = markers[-1] if markers else ''
        key = (json.dumps(payload.get('metadata', {}), sort_keys=True), marker)
        if not marker or key in self.issued:
            return Provider.anthropic(self, payload, answer, seq)
        self.issued.add(key)
        if self.barrier is not None:
            self.overlap.append(marker)
            self.barrier.wait(timeout=45)
        # The base provider checks all history for tool results. Here each fresh
        # marker asks once, so an earlier allow cannot hide permission reuse.
        current = {**payload, 'messages': []}
        current['tools'] = [t for t in payload.get('tools', []) if t.get('name') == 'mcp__identity__whoami']
        return ToolProvider.anthropic(self, current, answer, seq)


class MCPFixture(ProfilesFixture):
    scope = 'real Claude 2.1.280 / SDK 0.3.280, production HTTP, local providers and stdio MCP; startup selection and same-ID restore'
    def __init__(self, binary):
        super().__init__(binary)
        for p in self.providers.values(): p.close()
        provider_root = self.root / 'mcp-providers'
        provider_root.mkdir()
        self.providers = {letter: MCPProvider(provider_root, letter) for letter in ('A', 'B')}
        self.provider = self.providers['A']
        self.profiles = {k: v for k, v in self.profiles.items() if k.startswith('claude-')}
        self.config['projects'] = []
        self.sources = []
        asset = self.root / 'shared-mcp'
        asset.mkdir()
        server = asset / 'server.py'
        write(server, '''import json,os,sys
from pathlib import Path
profile=os.environ['MCP_PROFILE']
name=os.environ['MCP_NAME']
for line in sys.stdin:
 r=json.loads(line)
 if 'id' not in r:continue
 method=r['method']
 if method=='initialize':out={'protocolVersion':r['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':name,'version':'1'}}
 elif method=='tools/list':out={'tools':[{'name':'whoami' if name=='identity' else 'ping','description':'Local fixture identity','inputSchema':{'type':'object','properties':{}}}]}
 elif method=='tools/call':
  identity={'profile':profile,'cwd':os.getcwd(),'credential_matches':os.getenv('MCP_KEY')=='fixture-mcp-'+profile}
  with Path(os.environ['MCP_LOG']).open('a') as f:f.write(json.dumps(identity)+'\\n')
  out={'content':[{'type':'text','text':json.dumps(identity)}]}
 else:out={}
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':out}),flush=True)
''')
        definition = {'command': 'python3', 'args': ['./server.py'], 'env': {'MCP_PROFILE': '${NETCAP_MCP_PROFILE}',
            'MCP_NAME': 'identity', 'MCP_KEY': '${NETCAP_MCP_KEY}', 'MCP_LOG': '${NETCAP_MCP_LOG}'}}
        write(asset / '.mcp.json', {'mcpServers': {'identity': definition}})
        self.sources += [asset / '.mcp.json', server]
        self.config['capability_assets'] = [dict(id='shared', name='共享身份 MCP', kind='mcp', version='v1', source=str(asset), backends=['claude', 'codex'])]
        for name, definition in [('unavailable', {'command': '/bin/false'}), ('inherited', {'command': '/bin/false'})]:
            path = self.root / (name + '.json')
            write(path, {'mcpServers': {name: definition}})
            self.config['capability_assets'].append(dict(id=name, name=name, kind='mcp', version='v1', source=str(path), backends=['claude']))
        for id, profile in self.profiles.items():
            letter = id[-1].upper()
            profile['route']['endpoint'] = self.providers[letter].url
            cwd = self.root / id / 'work'
            cwd.mkdir()
            self.config['projects'].append(dict(id=id, name='MCP 项目 ' + letter, cwd=str(cwd)))
            settings = Path(profile['instance']['settings_file'])
            data = json.loads(settings.read_text())
            data['env'].update(NETCAP_MCP_PROFILE=letter, NETCAP_MCP_KEY='fixture-mcp-' + letter,
                NETCAP_MCP_LOG=str(self.root / (letter + '-calls.jsonl')), ENABLE_TOOL_SEARCH='false')
            data['enableAllProjectMcpServers'] = True
            write(settings, data)
            write(cwd / 'CLAUDE.md', 'MCP_PROJECT_RULE_' + letter + '_MUST_REMAIN\n')
            write(cwd / '.mcp.json', {'mcpServers': {'inherited': {'command': 'python3', 'args': [str(server)],
                'env': {'MCP_NAME': 'inherited', 'MCP_PROFILE': letter, 'MCP_KEY': '${NETCAP_MCP_KEY}', 'MCP_LOG': str(self.root / (letter + '-inherited-calls.jsonl'))}}}})
            self.sources += [settings, cwd / 'CLAUDE.md', cwd / '.mcp.json', Path(profile['instance']['profile']) / 'settings.json']

    def register(self):
        for id, profile in self.profiles.items():
            self.command('register-' + id, 'register_execution', execution_profile=profile)

    def create_selected(self, key, letter='A', mode='enabled', asset='shared', success=True):
        id = 'claude-' + letter.lower()
        receipt = self.command('create-' + key, 'create', project_id=id, route_id=id + '-route', title=key,
            permission='read_only', capability_selections=[{'asset_id': asset, 'mode': mode}])
        sid = receipt['session_id']
        wait_for(lambda: self.completed(receipt), 'creation ' + key, timeout=60)
        if success:
            wait_for(lambda: self.session(sid)['status'] == 'idle' and any(e.get('verified') and e.get('asset_id') == asset
                for e in self.session(sid)['view'].get('capability_evidence', [])), 'native selection ' + key, timeout=45)
        return sid

    def pending(self, sid):
        return wait_for(lambda: next((p for p in self.session(sid)['view']['pending'] if p['status'] == 'pending'), None), 'MCP approval', timeout=45)

    def idle(self, sid):
        return wait_for(lambda: self.session(sid)['status'] == 'idle' and all(o['status'] in ('succeeded', 'not_sent', 'failed', 'interrupted')
            for o in self.snapshot()['operations'] if o['session_id'] == sid), 'settled original turn', timeout=45)

    def calls(self, letter):
        path = self.root / (letter + '-calls.jsonl')
        return [json.loads(x) for x in path.read_text().splitlines()] if path.exists() else []

    def expected_identity(self, letter):
        return {'profile': letter, 'cwd': str(self.root / ('claude-' + letter.lower()) / 'work'), 'credential_matches': True}

    def additional_checks(self):
        return {}


def run(f):
    checks = {}
    originals = {path: path.read_bytes() for path in f.sources}
    sessions = {letter: f.create_selected('enabled-' + letter, letter) for letter in ('A', 'B')}
    sessions['disabled'] = f.create_selected('disabled', mode='disabled')
    native = {key: f.session(sid)['scope']['native_id'] for key, sid in sessions.items()}
    snapshots = {key: f.api(f'/api/sessions/{sid}/execution')['snapshot'] for key, sid in sessions.items()}
    checks['create_does_not_call_provider'] = not any(p.requests for p in f.providers.values())
    checks['same_asset_independent_state_namespaces'] = len({f.session(sid)['scope']['state_namespace'] for sid in (sessions['A'], sessions['B'])}) == 2
    checks['selections_record_shared_reference'] = all(s['capability_refs'][0]['asset']['id'] == 'shared' for s in snapshots.values())
    checks['inherited_servers_preserved'] = all(any(e['server'] == 'inherited' and e['mode'] == 'inherit' and e['runtime_status'] == 'connected'
        for e in f.session(sid)['view']['capability_evidence']) for sid in sessions.values())
    barrier = threading.Barrier(2)
    for p in f.providers.values(): p.barrier = barrier
    def send(letter):
        return f.command('send-first-' + letter, 'send', session_id=sessions[letter], text=f'NETCAP_{letter}_TURN_1')
    with ThreadPoolExecutor(max_workers=2) as pool: list(pool.map(send, ('A', 'B')))
    pendings = {letter: f.pending(sessions[letter]) for letter in ('A', 'B')}
    checks['two_native_requests_overlap'] = all(len(p.overlap) == 1 for p in f.providers.values())
    checks['approval_precedes_actual_tool'] = not f.calls('A') and not f.calls('B')
    for letter, pending in pendings.items():
        f.providers[letter].barrier = None
        params = dict(session_id=sessions[letter], pending_id=pending['id'], option_id='allow')
        receipt = f.command('allow-first-' + letter, 'respond', **params)
        checks['approval_idempotent_' + letter] = f.command('allow-first-' + letter, 'respond', **params) == receipt
    for letter in ('A', 'B'):
        f.idle(sessions[letter])
        expected = f.expected_identity(letter)
        checks['bound_tool_result_' + letter] = f.calls(letter) == [expected]
        returned = []
        for request in f.providers[letter].requests:
            for message in request['body'].get('messages', []):
                for block in message.get('content', []) if isinstance(message.get('content'), list) else []:
                    if block.get('type') == 'tool_result':
                        for part in block.get('content', []) if isinstance(block.get('content'), list) else []:
                            if part.get('type') == 'text':
                                try: returned.append(json.loads(part['text']))
                                except ValueError: pass
        checks['exact_result_returns_to_model_' + letter] = expected in returned
        checks['successful_usage_visible_' + letter] = any(e.get('used_tools') == ['whoami'] for e in f.session(sessions[letter])['view']['capability_evidence'])
    print('PASS actual A/B MCP calls and one-call approval', flush=True)
    before = {letter: f.calls(letter) for letter in ('A', 'B')}
    f.command('send-repeat', 'send', session_id=sessions['A'], text='NETCAP_A_TURN_2')
    pending = f.pending(sessions['A'])
    checks['allow_does_not_grant_next_call'] = pending['id'] != pendings['A']['id'] and f.calls('A') == before['A']
    f.command('deny-repeat', 'respond', session_id=sessions['A'], pending_id=pending['id'], option_id='deny')
    f.idle(sessions['A'])
    checks['denial_does_not_invoke'] = f.calls('A') == before['A']
    start = len(f.provider.requests)
    f.command('send-disabled', 'send', session_id=sessions['disabled'], text='NETCAP_A_TURN_3')
    f.idle(sessions['disabled'])
    checks['disabled_absent_from_model_tools'] = all(not any(t.get('name') == 'mcp__identity__whoami' for t in r['body'].get('tools', [])) for r in f.provider.requests[start:])
    checks['disabled_does_not_invoke'] = f.calls('A') == before['A']
    checks['disabled_does_not_claim_usage'] = not any(e.get('used_tools') for e in f.session(sessions['disabled'])['view']['capability_evidence'])
    denied = f.create_selected('denied')
    f.command('send-denied', 'send', session_id=denied, text='NETCAP_A_TURN_6')
    pending = f.pending(denied)
    f.command('deny-first', 'respond', session_id=denied, pending_id=pending['id'], option_id='deny')
    f.idle(denied)
    checks['denied_call_does_not_claim_success'] = not any(e.get('used_tools') for e in f.session(denied)['view']['capability_evidence']) and f.calls('A') == before['A']
    f.restart_console()
    for key, sid in sessions.items():
        receipt = f.command('resume-' + key, 'resume', session_id=sid)
        wait_for(lambda: f.completed(receipt), 'restore ' + key, timeout=60)
        checks['original_id_and_snapshot_' + key] = f.session(sid)['scope']['native_id'] == native[key] and f.api(f'/api/sessions/{sid}/execution')['snapshot'] == snapshots[key]
        if key in ('A', 'B'):
            checks['restored_historical_usage_' + key] = any(e.get('used_tools') == ['whoami'] for e in f.session(sid)['view']['capability_evidence'])
    f.command('send-restored', 'send', session_id=sessions['B'], text='NETCAP_B_TURN_4')
    pending = f.pending(sessions['B'])
    checks['restored_call_requires_new_permission'] = f.calls('B') == before['B']
    f.command('allow-restored', 'respond', session_id=sessions['B'], pending_id=pending['id'], option_id='allow')
    f.idle(sessions['B'])
    checks['restored_binding_still_correct'] = f.calls('B') == before['B'] * 2
    f.command('send-restored-disabled', 'send', session_id=sessions['disabled'], text='NETCAP_A_TURN_5')
    f.idle(sessions['disabled'])
    checks['restored_disabled_still_verified'] = any(e['server'] == 'identity' and e.get('verified') and e['runtime_status'] == 'disabled' for e in f.session(sessions['disabled'])['view']['capability_evidence'])
    print('PASS restart original IDs, bindings and disabled choice', flush=True)
    for asset, mode in [('unavailable', 'enabled'), ('inherited', 'disabled')]:
        count = sum(len(p.requests) for p in f.providers.values())
        sid = f.create_selected(asset, asset=asset, mode=mode, success=False)
        try:
            receipt = f.command('blocked-' + asset, 'send', session_id=sid, text='MUST_NOT_REACH_MODEL')
            wait_for(lambda: f.completed(receipt), 'blocked input ' + asset, timeout=45)
            status = next(o['status'] for o in f.snapshot()['operations'] if o['id'] == receipt['operation_id'])
            checks[asset + '_blocks_input'] = status in ('not_sent', 'failed')
        except HTTPError as error:
            checks[asset + '_blocks_input'] = error.code == 409
        checks[asset + '_no_model_request'] = sum(len(p.requests) for p in f.providers.values()) == count
    checks['existing_sources_unchanged'] = all(path.read_bytes() == data for path, data in originals.items())
    checks['project_rules_preserved'] = all(any('MCP_PROJECT_RULE_' + letter + '_MUST_REMAIN' in json.dumps(r['body']) for r in p.requests) for letter, p in f.providers.items())
    checks['model_auth_and_history_isolated'] = all(p.requests and all(r['auth_matches'] and ('NETCAP_' + ('B' if letter == 'A' else 'A') + '_TURN') not in json.dumps(r['body']) for r in p.requests) for letter, p in f.providers.items())
    public = json.dumps(f.snapshot()) + ''.join(json.dumps(f.api(f'/api/sessions/{sid}/execution')) for sid in sessions.values())
    persistent = ''.join(path.read_text(errors='replace') for path in (f.root / 'console').rglob('*') if path.is_file())
    checks['no_mcp_secrets_in_snapshot_state_or_metadata'] = all('fixture-mcp-' + letter not in public + persistent for letter in ('A', 'B'))
    checks['no_provider_errors'] = all(not p.errors for p in f.providers.values())
    checks.update(f.additional_checks())
    # Drift is last: all existing sessions share this pinned source.
    source = f.root / 'shared-mcp/.mcp.json'
    source.write_text(source.read_text() + '\n')
    count = sum(len(p.requests) for p in f.providers.values())
    try:
        f.command('source-drift', 'send', session_id=sessions['B'], text='DRIFT_MUST_NOT_REACH_MODEL')
        checks['source_drift_rejected'] = False
    except HTTPError as error:
        checks['source_drift_rejected'] = error.code == 409
    checks['source_drift_no_model_request'] = sum(len(p.requests) for p in f.providers.values()) == count
    result = {'scope': f.scope,
        'checks': checks, 'native_ids': native, 'expectations_met': all(checks.values())}
    write(f.root / 'snapshot.json', f.snapshot())
    write(f.root / 'result.json', result)
    print(json.dumps(result, ensure_ascii=False), flush=True)
    assert result['expectations_met'], checks


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--netcap', default='.dev/bin/netcap')
    parser.add_argument('--hold', action='store_true')
    parser.add_argument('--info-file')
    args = parser.parse_args()
    with MCPFixture(str(Path(args.netcap).resolve())) as f:
        print('Evidence directory:', f.root, flush=True)
        verify_versions(f.root, ['claude'])
        f.register()
        if not args.hold:
            run(f)
            return
        if args.info_file:
            write(Path(args.info_file), {**f.info, 'root': str(f.root), 'harness_pid': os.getpid()})
        stopped = False
        def finish(*_):
            nonlocal stopped
            stopped = True
        signal.signal(signal.SIGTERM, finish)
        signal.signal(signal.SIGINT, finish)
        print('READY', f.info['url'], flush=True)
        while not stopped:
            assert f.console.poll() is None
            time.sleep(.2)


if __name__ == '__main__': main()
