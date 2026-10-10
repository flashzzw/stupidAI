#!/usr/bin/env python3
"""Real same-ID resource addition, pinned upgrade and inheritance with local MCPs."""
import argparse
import json
import os
from pathlib import Path
import re
import signal
import time
from urllib.error import HTTPError

from claude_mcp_selection_smoke import MCPFixture, MCPProvider
from console_fixture import Provider, wait_for
from execution_config_probe import write, verify_versions
from mcp_binding_probe import ToolProvider
from remote_mcp_fixture import RemoteMCP


class ResourceProvider(MCPProvider):
    def anthropic(self, payload, answer, seq):
        markers = re.findall(r'NETCAP_[AB]_TURN_\d+_TOOL_[a-z]+', json.dumps(payload.get('messages', [])))
        marker = markers[-1] if markers else ''
        key = (json.dumps(payload.get('metadata', {}), sort_keys=True), marker)
        if not marker or key in self.issued:
            return Provider.anthropic(self, payload, answer, seq)
        self.issued.add(key)
        target = 'mcp__' + marker.split('_TOOL_')[1] + '__whoami'
        return ToolProvider.anthropic(self, {**payload, 'messages': [], 'tools': [t for t in payload.get('tools', []) if t.get('name') == target]}, answer, seq)


class ResourceFixture(MCPFixture):
    def __init__(self, binary):
        super().__init__(binary)
        for p in self.providers.values(): p.close()
        provider_root = self.root / 'resource-providers'
        provider_root.mkdir()
        self.providers = {letter: ResourceProvider(provider_root, letter) for letter in ('A', 'B')}
        self.provider = self.providers['A']
        self.remote = RemoteMCP(self.root)
        shared = json.loads((self.root / 'shared-mcp/.mcp.json').read_text())
        shared['mcpServers']['companion'] = shared['mcpServers']['identity']
        write(self.root / 'shared-mcp/.mcp.json', shared)
        def remote(transport):
            return {'type': transport, 'url': self.remote.url + '/' + transport, 'headers': {
                'Authorization': 'Bearer ${NETCAP_MCP_KEY}', 'X-Netcap-Profile': '${NETCAP_MCP_PROFILE}', 'X-Netcap-Workspace': '${NETCAP_MCP_WORKSPACE}'}}
        write(self.root / 'additional.json', {'mcpServers': {'remotehttp': remote('http'), 'remotesse': remote('sse')}})
        self.config['capability_assets'].append(dict(id='additional', name='远程组合 MCP', kind='mcp', version='r1', source=str(self.root / 'additional.json'), backends=['claude']))
        write(self.root / 'shared-v2.json', {'mcpServers': {'identity': remote('http'), 'successor': remote('sse')}})
        for id, profile in self.profiles.items():
            letter = id[-1].upper()
            profile['route']['endpoint'] = self.providers[letter].url
            settings = Path(profile['instance']['settings_file'])
            data = json.loads(settings.read_text())
            data['env']['NETCAP_MCP_WORKSPACE'] = str(self.root / id / 'work')
            data['permissions'] = {'ask': ['mcp__identity__*']}
            write(settings, data)
            project = self.root / id / 'work/.mcp.json'
            inherited = json.loads(project.read_text())
            definition = json.loads(json.dumps(shared['mcpServers']['identity']))
            definition['args'] = [str(self.root / 'shared-mcp/server.py')]
            definition['env']['MCP_LOG'] = str(self.root / (letter + '-restored-calls.jsonl'))
            inherited['mcpServers']['identity'] = definition
            write(project, inherited)

    def close(self):
        super().close()
        self.remote.close()

    def publish_v2(self):
        current = next(x for x in self.api('/api/capabilities') if x['asset']['id'] == 'shared')
        request = {'action': 'update', 'asset_id': 'shared', 'base_revision': current['revision'],
            'asset': {**current['asset'], 'version': 'v2', 'source': str(self.root / 'shared-v2.json'), 'management': 'managed'}}
        preview = self.api('/api/capabilities/preview', request)
        self.command('publish-v2', 'manage_capability', capability_change={**preview['request'], 'preview_hash': preview['preview_hash']})


def run(f):
    checks = {}
    def check(name, value):
        checks[name] = bool(value)
        assert value, name
    sessions = {}
    for letter in ('A', 'B'):
        receipt = f.command('empty-' + letter, 'create', project_id='claude-' + letter.lower(), route_id='claude-' + letter.lower() + '-route', permission='read_only')
        sid = receipt['session_id']; sessions[letter] = sid
        f.idle(sid)
    originals = {letter: f.session(sid)['scope']['native_id'] for letter, sid in sessions.items()}
    def read(sid): return f.api(f'/api/sessions/{sid}/execution')
    def operation(receipt): return next(o for o in f.snapshot()['operations'] if o['id'] == receipt['operation_id'])
    def command(sid, key, kind, **params):
        def attempt():
            try: return f.command(key, kind, session_id=sid, **params)
            except HTTPError as error:
                body = json.load(error)
                if error.code == 409 and '正在核对' in body.get('error', ''): return None
                raise RuntimeError(body) from error
        return wait_for(attempt, kind + ' accepted', timeout=20)
    def settle(receipt):
        return wait_for(lambda: (o if o['status'] in ('succeeded', 'not_sent', 'failed') else None) if (o := operation(receipt)) else None, 'operation settles', timeout=60)
    def change(letter, key, picks):
        sid = sessions[letter]; before = read(sid); native = f.session(sid)['scope']['native_id']
        messages = f.session(sid)['view']['messages']; count = sum(len(p.requests) for p in f.providers.values())
        request = dict(base_snapshot_id=before['snapshot']['id'], pending_change_id='', selections=picks)
        preview = f.api(f'/api/sessions/{sid}/execution/preview', request)
        command(sid, key+'-stage', 'stage_execution', execution_change={**request, 'preview_hash': preview['preview_hash']})
        staged = read(sid); candidate = staged['pending_change']['snapshot']
        check(key+'_save_preserves_current', staged['snapshot'] == before['snapshot'])
        ref = dict(base_snapshot_id=before['snapshot']['id'], pending_change_id=staged['pending_change']['id'])
        assert settle(command(sid, key+'-prepare', 'prepare_execution', execution_change=ref))['status'] == 'succeeded'
        assert settle(command(sid, key+'-apply', 'apply_execution', execution_change=ref))['status'] == 'succeeded'
        after = read(sid)
        check(key+'_exact_candidate_and_history', after['snapshot'] == candidate and after['history'] == before.get('history', []) + [before['snapshot']])
        check(key+'_same_id_history_no_input', f.session(sid)['scope']['native_id'] == native and f.session(sid)['view']['messages'] == messages and sum(len(p.requests) for p in f.providers.values()) == count)
        check(key+'_pause_and_verified_selections', f.session(sid)['paused'] and not after['hold'] and all(e['verified'] for e in after.get('capability_evidence', []) if e.get('asset_id')))
        write(f.root / (key + '.json'), after)
        return before, after, preview
    def call(letter, number, server, transport=None, inherited=False):
        sid = sessions[letter]; key = letter+'-call-'+str(number)
        count = len(f.remote.calls) if transport else len(f.calls(letter))
        receipt = command(sid, key, 'send', text=f'NETCAP_{letter}_TURN_{number}_TOOL_{server}')
        pending = f.pending(sid)
        check(key+'_requested_tool', pending['title'].find(server) >= 0 or server in json.dumps(pending))
        f.command(key+'-allow', 'respond', session_id=sid, pending_id=pending['id'], option_id='allow')
        f.idle(sid)
        check(key+'_current_version', operation(receipt)['execution_snapshot_id'] == read(sid)['snapshot']['id'])
        if transport:
            expected = {'profile':letter,'workspace':str(f.root / ('claude-'+letter.lower()) / 'work'),'credential_matches':True,'transport':transport}
            check(key+'_bound_result', len(f.remote.calls) == count + 1 and f.remote.calls[-1] == expected)
        elif inherited:
            returned = [json.loads(l) for l in (f.root / (letter+'-restored-calls.jsonl')).read_text().splitlines()]
            expected = f.expected_identity(letter)
            check(key+'_inherited_source_used', returned[-1] == expected and len(f.calls(letter)) == count)
        else:
            expected = f.expected_identity(letter)
            check(key+'_bound_result', len(f.calls(letter)) == count + 1 and f.calls(letter)[-1] == expected)
        check(key+'_result_back_to_model', json.dumps(expected, sort_keys=True) in json.dumps([json.loads(t['text']) for r in f.providers[letter].requests for m in r['body'].get('messages', []) if isinstance(m.get('content'), list) for b in m['content'] if b.get('type') == 'tool_result' and isinstance(b.get('content'), list) for t in b['content'] if isinstance(t, dict) and t.get('type') == 'text' and t['text'].startswith('{')], sort_keys=True))
    for letter in ('A', 'B'):
        _, added, _ = change(letter, letter+'-add', [dict(asset_id='shared', mode='enabled', version='v1'), dict(asset_id='additional', mode='enabled', version='r1')])
        check(letter+'_four_services_two_assets', len(added['snapshot']['mcp_selections']) == 4 and len(added['snapshot']['capability_refs']) == 2)
        call(letter, 1, 'identity')
        call(letter, 2, 'remotehttp', 'http')
        call(letter, 3, 'remotesse', 'sse')
    f.publish_v2()
    for letter in ('A', 'B'):
        sid = sessions[letter]
        check(letter+'_library_update_does_not_upgrade', next(r['asset']['version'] for r in read(sid)['snapshot']['capability_refs'] if r['asset']['id'] == 'shared') == 'v1')
        call(letter, 4, 'identity')
        _, upgraded, preview = change(letter, letter+'-upgrade', [dict(asset_id='shared', mode='enabled', version='v2')])
        check(letter+'_resource_diff_is_explicit', any(c.get('before_version') == 'v1' and c.get('after_version') == 'v2' for c in preview['changes']))
        check(letter+'_retired_server_absent', not any(e['server'] == 'companion' for e in upgraded['capability_evidence']))
        check(letter+'_other_asset_stays_pinned', next(r['asset']['version'] for r in upgraded['snapshot']['capability_refs'] if r['asset']['id'] == 'additional') == 'r1')
        call(letter, 5, 'identity', 'http')
        _, restored, _ = change(letter, letter+'-inherit', [dict(asset_id='shared', mode='inherit'), dict(asset_id='additional', mode='inherit')])
        check(letter+'_explicit_dependencies_released', not restored['snapshot'].get('mcp_selections') and not restored['snapshot']['capability_refs'])
        check(letter+'_inherited_source_visible', any(e['server'] == 'identity' and e['mode'] == 'inherit' and e['runtime_status'] == 'connected' and 'project' in e['detail'] for e in restored['capability_evidence']))
        call(letter, 6, 'identity', inherited=True)
    snapshots = {letter: read(sid)['snapshot'] for letter, sid in sessions.items()}
    f.restart_console()
    for letter, sid in sessions.items():
        receipt = command(sid, letter+'-restore', 'resume')
        settle(receipt)
        call(letter, 7, 'identity', inherited=True)
        check(letter+'_restart_keeps_id_version', f.session(sid)['scope']['native_id'] == originals[letter] and read(sid)['snapshot'] == snapshots[letter])
    check('local_provider_accounts_isolated', all(not p.errors and all(r['auth_matches'] for r in p.requests) for p in f.providers.values()))
    result = dict(checks=checks, expectations_met=all(checks.values()), native_ids=originals)
    write(f.root/'result.json', result); print(json.dumps(result), flush=True)


def main():
    parser=argparse.ArgumentParser(); parser.add_argument('--hold',action='store_true'); parser.add_argument('--info-file'); args=parser.parse_args()
    with ResourceFixture(str(Path('.dev/bin/netcap').resolve())) as f:
        print('Evidence directory:',f.root,flush=True)
        verify_versions(f.root,['claude']); f.register()
        if not args.hold: run(f); return
        if args.info_file: write(Path(args.info_file),{**f.info,'root':str(f.root),'harness_pid':os.getpid()})
        stopped=False
        def finish(*_):
            nonlocal stopped
            stopped=True
        signal.signal(signal.SIGTERM,finish); signal.signal(signal.SIGINT,finish)
        print('READY',f.info['url'],flush=True)
        while not stopped:
            assert f.console.poll() is None
            time.sleep(.2)


if __name__=='__main__': main()
