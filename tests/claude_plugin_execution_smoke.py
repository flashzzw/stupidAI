#!/usr/bin/env python3
"""Production plugin MCP permission, hook payload/failure and original-ID probe.

Local scripted providers and fixture-owned plugin processes only. Run the full
probe in the WSL terminal with working local IPC/listeners; --planner-only
checks only the scripted provider and makes no native-execution claim.
"""
import argparse
import json
from pathlib import Path
import re
import time
from urllib.error import HTTPError

from claude_mcp_selection_smoke import MCPFixture, MCPProvider
from console_fixture import Provider, wait_for
from execution_config_probe import write, verify_versions
from mcp_binding_probe import ToolProvider


def plugin_tool(name):
    return 'mcp__plugin_' + name + '_identity__whoami'


class PluginProvider(MCPProvider):
    def anthropic(self, payload, answer, seq):
        markers = re.findall(r'NETCAP_[AB]_TURN_\d+_PLUGIN_(?:suite|fail)', json.dumps(payload.get('messages', [])))
        marker = markers[-1] if markers else ''
        key = (json.dumps(payload.get('metadata', {}), sort_keys=True), marker)
        if not marker or key in self.issued:
            return Provider.anthropic(self, payload, answer, seq)
        self.issued.add(key)
        target = plugin_tool('plugin-' + marker.rsplit('_', 1)[1])
        return ToolProvider.anthropic(self, {**payload, 'messages': [], 'tools': [t for t in payload.get('tools', []) if t.get('name') == target]}, answer, seq)


class PluginFixture(MCPFixture):
    scope = 'production Application/Runtime/Session; plugin MCP + PreToolUse/PostToolUse, local scripted providers and owned native CLI'

    def __init__(self, binary):
        super().__init__(binary)
        for provider in self.providers.values():
            provider.close()
        provider_root = self.root / 'plugin-providers'
        provider_root.mkdir()
        self.providers = {letter: PluginProvider(provider_root, letter) for letter in ('A', 'B')}
        self.provider = self.providers['A']
        self.config['capability_assets'] = []
        self.plugin_paths = {}
        for label in ['suite', 'fail']:
            source = self.root / ('plugin-' + label)
            self.plugin_paths[label] = source
            write(source / '.claude-plugin/plugin.json', {'name': 'plugin-' + label, 'version': '1.0.0'})
            write(source / 'server.py', (self.root / 'shared-mcp/server.py').read_text())
            definition = json.loads((self.root / 'shared-mcp/.mcp.json').read_text())
            definition['mcpServers']['identity']['args'] = ['${CLAUDE_PLUGIN_ROOT}/server.py']
            write(source / '.mcp.json', definition)
            write(source / 'hook.py', '''import json,os,sys
from pathlib import Path
body=json.load(sys.stdin)
code=int(sys.argv[1]) if body['hook_event_name']=='PreToolUse' else 0
row={key:body.get(key) for key in ['hook_event_name','session_id','cwd','tool_name','tool_use_id']}
row.update(plugin_root=os.environ.get('CLAUDE_PLUGIN_ROOT'),profile=os.environ.get('NETCAP_MCP_PROFILE'),exit_code=code)
with Path(os.environ['NETCAP_PLUGIN_HOOK_LOG']).open('a') as out:out.write(json.dumps(row)+'\\n')
if code:print('Fixture plugin intentionally blocked the tool',file=sys.stderr)
raise SystemExit(code)
''')
            command = 'python3 "${CLAUDE_PLUGIN_ROOT}/hook.py" ' + ('2' if label == 'fail' else '0')
            write(source / 'hooks/hooks.json', {'hooks': {event: [{'matcher': plugin_tool('plugin-' + label), 'hooks': [{'type': 'command', 'command': command}]}] for event in ['PreToolUse', 'PostToolUse']}})
            self.config['capability_assets'].append(dict(id=label, name='Plugin ' + label, kind='cli_plugin', version='asset-v1', source=str(source), backends=['claude']))
        for id, profile in self.profiles.items():
            letter = id[-1].upper()
            profile['route']['endpoint'] = self.providers[letter].url
            settings = Path(profile['instance']['settings_file'])
            data = json.loads(settings.read_text())
            data['env']['NETCAP_PLUGIN_HOOK_LOG'] = str(self.root / (letter + '-plugin-hooks.jsonl'))
            write(settings, data)

    def create_plugin(self, key, letter='A', mode='enabled', asset='suite'):
        profile = 'claude-' + letter.lower()
        receipt = self.command('create-' + key, 'create', project_id=profile, route_id=profile + '-route', title=key,
                               permission='read_only', capability_selections=[{'asset_id': asset, 'mode': mode}])
        sid = receipt['session_id']
        wait_for(lambda: self.completed(receipt), 'plugin creation', timeout=60)
        wait_for(lambda: self.session(sid)['status'] == 'idle' and any(e.get('verified') and e['asset_id'] == asset
                 for e in self.session(sid)['view'].get('plugin_evidence', [])), 'plugin loading', timeout=45)
        return sid

    def hooks(self, letter, sid=None):
        path = self.root / (letter + '-plugin-hooks.jsonl')
        rows = [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []
        return [row for row in rows if sid is None or row['session_id'] == sid]


def result_texts(requests):
    output = []
    for request in requests:
        for message in request['body'].get('messages', []):
            content = message.get('content', [])
            for block in content if isinstance(content, list) else []:
                if block.get('type') != 'tool_result':
                    continue
                parts = block.get('content', [])
                for part in parts if isinstance(parts, list) else []:
                    if part.get('type') == 'text':
                        try:
                            output.append(json.loads(part['text']))
                        except ValueError:
                            pass
    return output


def run(f):
    checks, sessions, reads = {}, {}, {}
    completed = False

    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    def send(sid, letter, turn, label='suite'):
        return f.command('send-' + letter + '-' + str(turn), 'send', session_id=sid, text=f'NETCAP_{letter}_TURN_{turn}_PLUGIN_{label}')

    try:
        for letter in ['A', 'B']:
            sessions[letter] = f.create_plugin('enabled-' + letter, letter)
        sessions['disabled'] = f.create_plugin('disabled', mode='disabled')
        native = {key: f.session(sid)['scope']['native_id'] for key, sid in sessions.items()}
        reads = {key: f.api(f'/api/sessions/{sid}/execution')['snapshot'] for key, sid in sessions.items()}
        check('creation_has_no_model_requests', not any(p.requests for p in f.providers.values()))
        check('profiles_keep_independent_namespaces', f.session(sessions['A'])['scope']['state_namespace'] != f.session(sessions['B'])['scope']['state_namespace'])
        for letter in ['A', 'B']:
            sid = sessions[letter]
            send(sid, letter, 1)
            pending = f.pending(sid)
            check(letter + '_plugin_tool_requires_approval', pending['kind'] == plugin_tool('plugin-suite') and not f.calls(letter))
            params = dict(session_id=sid, pending_id=pending['id'], option_id='allow')
            receipt = f.command('allow-' + letter, 'respond', **params)
            check(letter + '_approval_idempotent', f.command('allow-' + letter, 'respond', **params) == receipt)
            f.idle(sid)
            check(letter + '_exact_bound_call', f.calls(letter) == [f.expected_identity(letter)])
            check(letter + '_result_reaches_own_provider', f.expected_identity(letter) in result_texts(f.providers[letter].requests))
            trace = f.hooks(letter, native[letter])
            check(letter + '_both_tool_hook_payloads', {row['hook_event_name'] for row in trace} == {'PreToolUse', 'PostToolUse'})
            check(letter + '_hook_binding', all(row['cwd'] == f.expected_identity(letter)['cwd'] and row['profile'] == letter and row['plugin_root'] == str(f.plugin_paths['suite']) and row['tool_name'] == plugin_tool('plugin-suite') and row['tool_use_id'] for row in trace))
            events = f.session(sid)['view'].get('hook_executions', [])
            check(letter + '_tool_hook_results_visible', all(any(e['event'] == event and e['status'] == 'success' and e.get('operation') for e in events) for event in ['PreToolUse', 'PostToolUse']))
            check(letter + '_plugin_usage_visible', any(e.get('used_tools') == ['whoami'] and e.get('source') == 'plugin' for e in f.session(sid)['view'].get('capability_evidence', [])))
        before = {letter: f.calls(letter) for letter in ['A', 'B']}
        send(sessions['A'], 'A', 2)
        pending = f.pending(sessions['A'])
        check('next_call_requires_new_approval', f.calls('A') == before['A'])
        f.command('deny-repeat', 'respond', session_id=sessions['A'], pending_id=pending['id'], option_id='deny')
        f.idle(sessions['A'])
        check('denial_prevents_tool_call', f.calls('A') == before['A'])
        start = len(f.provider.requests)
        send(sessions['disabled'], 'A', 3)
        f.idle(sessions['disabled'])
        check('disabled_tool_absent', bool(f.provider.requests[start:]) and all(not any(t.get('name') == plugin_tool('plugin-suite') for t in r['body'].get('tools', [])) for r in f.provider.requests[start:]))
        check('disabled_plugin_does_not_run', f.calls('A') == before['A'] and not f.hooks('A', native['disabled']))
        sessions['fail'] = f.create_plugin('hook-failure', asset='fail')
        send(sessions['fail'], 'A', 4, 'fail')
        # If native permission still appears, allow only this fixture tool so
        # the hook assertion cannot pass merely because the harness denied it.
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            session = f.session(sessions['fail'])
            for pending in session['view'].get('pending', []):
                if pending['status'] == 'pending':
                    assert pending['kind'] == plugin_tool('plugin-fail')
                    f.command('allow-failure', 'respond', session_id=session['id'], pending_id=pending['id'], option_id='allow')
            if session['status'] == 'idle' and any(e['event'] == 'PreToolUse' and e['status'] == 'error' for e in session['view'].get('hook_executions', [])):
                break
            time.sleep(.03)
        f.idle(sessions['fail'])
        failed_id = f.session(sessions['fail'])['scope']['native_id']
        failed_trace = f.hooks('A', failed_id)
        check('failure_hook_received_actual_payload', any(row['hook_event_name'] == 'PreToolUse' and row['exit_code'] == 2 and row['tool_use_id'] and row['plugin_root'] == str(f.plugin_paths['fail']) and row['tool_name'] == plugin_tool('plugin-fail') for row in failed_trace))
        check('failure_hook_prevents_mcp_call', f.calls('A') == before['A'])
        check('failure_visible_with_native_exit', any(e['event'] == 'PreToolUse' and e['status'] == 'error' and e.get('exit_code') == 2 for e in f.session(sessions['fail'])['view'].get('hook_executions', [])))
        f.restart_console()
        for key in ['A', 'B', 'disabled']:
            sid = sessions[key]
            receipt = f.command('resume-' + key, 'resume', session_id=sid)
            wait_for(lambda: f.completed(receipt), 'original ID resume', timeout=60)
            check(key + '_restored_identity_and_version', f.session(sid)['scope']['native_id'] == native[key] and f.api(f'/api/sessions/{sid}/execution')['snapshot'] == reads[key])
        send(sessions['B'], 'B', 5)
        pending = f.pending(sessions['B'])
        check('restored_call_requires_approval', f.calls('B') == before['B'])
        f.command('allow-restored', 'respond', session_id=sessions['B'], pending_id=pending['id'], option_id='allow')
        f.idle(sessions['B'])
        check('restored_call_keeps_binding', f.calls('B') == before['B'] * 2)
        check('provider_auth_and_history_isolated', all(p.requests and all(r['auth_matches'] and ('NETCAP_' + ('B' if letter == 'A' else 'A') + '_TURN_') not in json.dumps(r['body']) for r in p.requests) and not p.errors for letter, p in f.providers.items()))
        source = f.plugin_paths['suite'] / '.mcp.json'
        source.write_text(source.read_text() + '\n')
        count = sum(len(p.requests) for p in f.providers.values())
        try:
            f.command('drift', 'send', session_id=sessions['B'], text='MUST_NOT_REACH_MODEL')
            check('source_drift_rejected', False)
        except HTTPError as error:
            check('source_drift_rejected', error.code == 409)
        check('source_drift_has_no_model_request', sum(len(p.requests) for p in f.providers.values()) == count)
        completed = True
    finally:
        write(f.root / 'snapshot.json', f.snapshot())
        write(f.root / 'result.json', {'scope': f.scope, 'checks': checks, 'sessions': sessions, 'expectations_met': completed and all(checks.values()), 'completed_matrix': completed})
    print(json.dumps({'checks': checks, 'expectations_met': all(checks.values())}, indent=2))


def planner_checks():
    provider = PluginProvider.__new__(PluginProvider)
    provider.issued = set()
    payload = {'model': 'fixture', 'metadata': {'user_id': 'fixture'}, 'messages': [{'role': 'user', 'content': 'NETCAP_A_TURN_1_PLUGIN_suite'}], 'tools': [{'name': plugin_tool('plugin-suite')}, {'name': 'mcp__inherited__ping'}]}
    first = provider.anthropic(payload, 'fixture', 1)
    repeat = provider.anthropic(payload, 'fixture', 2)
    next_turn = {**payload, 'messages': [{'role': 'user', 'content': 'NETCAP_A_TURN_2_PLUGIN_suite'}]}
    again = provider.anthropic(next_turn, 'fixture', 3)
    absent = {**payload, 'messages': [{'role': 'user', 'content': 'NETCAP_A_TURN_3_PLUGIN_suite'}], 'tools': [{'name': 'mcp__inherited__ping'}]}
    omitted = provider.anthropic(absent, 'fixture', 4)
    fail = {**payload, 'messages': [{'role': 'user', 'content': 'NETCAP_A_TURN_4_PLUGIN_fail'}], 'tools': [{'name': plugin_tool('plugin-fail')}]}
    blocked = provider.anthropic(fail, 'fixture', 5)
    checks = {'canonical_plugin_tool': '"name": "mcp__plugin_plugin-suite_identity__whoami"' in first,
              'inherited_tool_not_requested': '"name": "mcp__inherited__ping"' not in first,
              'response_does_not_repeat_call': '"type": "tool_use"' not in repeat,
              'new_turn_requests_again': '"type": "tool_use"' in again,
              'disabled_tool_not_invented': '"type": "tool_use"' not in omitted,
              'failure_package_is_distinct': '"name": "mcp__plugin_plugin-fail_identity__whoami"' in blocked}
    assert all(checks.values()), checks
    return checks


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--netcap', default='.dev/bin/netcap')
    parser.add_argument('--planner-only', action='store_true')
    args = parser.parse_args()
    if args.planner_only:
        print(json.dumps({'checks': planner_checks(), 'native_execution_verified': False}, indent=2))
        return
    with PluginFixture(str(Path(args.netcap).resolve())) as fixture:
        print('Evidence directory:', fixture.root, flush=True)
        verify_versions(fixture.root, ['claude'])
        fixture.register()
        run(fixture)


if __name__ == '__main__':
    main()
