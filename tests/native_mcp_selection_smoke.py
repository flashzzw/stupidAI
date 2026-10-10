#!/usr/bin/env python3
"""W02 production MCP selection and approval via HTTP, real Codex, local provider."""
import argparse
import json
import os
from pathlib import Path
import signal
import time
from urllib.error import HTTPError

from console_fixture import ConsoleFixture, wait_for
from codex_mcp_binding_probe import MCPProvider
from mcp_binding_probe import shared_definition


class SelectionProvider(MCPProvider):
    def responses(self, payload, answer, seq):
        # This native version omits tool schemas from Responses payloads. Ask
        # its actual exec registry whether the selected MCP is callable instead.
        response = super().responses(payload, answer, seq)
        original = json.dumps("text(await tools.mcp__identity__whoami({}));")
        guarded = json.dumps('const available=ALL_TOOLS.some(t=>t.name==="mcp__identity__whoami"); text({identityAvailable:available}); if(available) text(await tools.mcp__identity__whoami({}));')
        return response.replace(original, guarded)


def decoded_outputs(requests):
    outputs = []
    for request in requests:
        for item in request['body'].get('input', []):
            if item.get('type') != 'custom_tool_call_output': continue
            output = item.get('output')
            for block in output if isinstance(output, list) else [{'text': output}]:
                try:
                    value = json.loads(block.get('text', ''))
                    if isinstance(value, dict): outputs.append(value)
                except (ValueError, TypeError): pass
    return outputs


def fixture_for(binary):
    f = ConsoleFixture(binary, provider_class=SelectionProvider)
    definition = shared_definition(f.root)
    path = f.profile / 'config.toml'
    with path.open('a') as out:
        for name in ('identity', 'inherited'):
            out.write(f'''\n[mcp_servers.{name}]
command = {json.dumps(definition['command'])}
args = {json.dumps(definition['args'])}
cwd = {json.dumps(str(f.cwd))}
[mcp_servers.{name}.env]
NETCAP_MCP_PROFILE = "A"
NETCAP_MCP_KEY = "fixture-mcp-A"
NETCAP_MCP_LOG = {json.dumps(str(f.root / (name + '-calls.jsonl')))}
''')
        out.write('\n[mcp_servers.unavailable]\ncommand = "/bin/false"\nargs = []\nenabled = false\n')
    f.config['capability_assets'] = [dict(id=name, name='本地 MCP ' + name, kind='mcp', version='v1',
        source=str(path), backends=['codex'], codex_mcp=name) for name in ('identity', 'unavailable')]
    return f


def run(binary, hold=False, info_file=None):
    with fixture_for(binary) as f:
        print('Evidence directory:', f.root, flush=True)
        if hold:
            if info_file:
                Path(info_file).write_text(json.dumps({**f.info, 'root': str(f.root), 'harness_pid': os.getpid()}))
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
            return
        profile = (f.profile / 'config.toml').read_bytes()
        checks, sessions, snapshots = {}, {}, {}
        log = f.root / 'identity-calls.jsonl'

        def create(mode, asset='identity'):
            r = f.command('create-' + mode + '-' + asset, 'create', project_id='fixture', route_id='route-a',
                title=mode + '-' + asset, permission='read_only', capability_selections=[{'asset_id': asset, 'mode': mode}])
            sid = r['session_id']
            wait_for(lambda: f.session(sid)['view'].get('capability_evidence'), 'native MCP evidence ' + mode, timeout=45)
            return sid

        for mode in ('enabled', 'disabled'):
            sid = sessions[mode] = create(mode)
            observed = wait_for(lambda: next((e for e in f.session(sid)['view'].get('capability_evidence', [])
                if e['server'] == 'identity' and e['verified']), None), mode + ' matches native selection', timeout=45)
            checks[mode + '_verified'] = observed['runtime_status'] == ('connected' if mode == 'enabled' else 'disabled')
            snapshots[mode] = f.api(f'/api/sessions/{sid}/execution')['snapshot']
            checks[mode + '_automatically_records_reference'] = snapshots[mode]['capability_refs'][0]['asset']['id'] == 'identity'
            checks[mode + '_other_servers_inherit'] = any(e['server'] == 'inherited' and e['mode'] == 'inherit' and e['runtime_status'] == 'connected' for e in f.session(sid)['view']['capability_evidence'])

        native_ids = {mode: f.session(sid)['scope']['native_id'] for mode, sid in sessions.items()}
        enabled = sessions['enabled']
        f.command('enabled-turn', 'send', session_id=enabled, text='NETCAP_A_TURN_1')
        pending = wait_for(lambda: next((p for p in f.session(enabled)['view']['pending'] if p['status'] == 'pending'), None), 'native MCP approval', timeout=45)
        (f.root / 'pending.json').write_text(json.dumps(pending, indent=2))
        checks['native_approval_visible_before_tool_executes'] = pending['kind'] == 'mcpServer/elicitation/request' and pending['response_kind'] == 'approval' and not log.exists()
        answer = dict(session_id=enabled, pending_id=pending['id'], option_id='decision-0')
        receipt = f.command('mcp-approve', 'respond', **answer)
        checks['approval_retry_is_idempotent'] = f.command('mcp-approve', 'respond', **answer) == receipt
        wait_for(lambda: f.session(enabled)['status'] == 'idle', 'approved MCP turn finishes', timeout=45)
        expected = {'profile': 'A', 'cwd': str(f.cwd), 'credential_matches': True}
        checks['tool_runs_once_with_bound_identity'] = [json.loads(line) for line in log.read_text().splitlines()] == [expected]
        evidence = f.api(f'/api/sessions/{enabled}/execution')['capability_evidence']
        checks['successful_call_evidence'] = any(e['server'] == 'identity' and e.get('used_tools') == ['whoami'] for e in evidence)
        returned = []
        for output in decoded_outputs(f.provider.requests):
            for content in output.get('content', []):
                if content.get('type') == 'text':
                    try: returned.append(json.loads(content['text']))
                    except (ValueError, TypeError): pass
        checks['exact_mcp_result_returns_to_model'] = expected in returned
        try:
            f.command('mcp-approve-late', 'respond', **answer)
            checks['late_approval_rejected'] = False
        except HTTPError as e:
            checks['late_approval_rejected'] = e.code == 409

        # A previous allow must not silently grant the next call in this session.
        wait_for(lambda: all(o['status'] in ('succeeded', 'not_sent', 'failed') for o in f.snapshot()['operations'] if o['session_id'] == enabled), 'original turn and approval operations settle', timeout=45)
        f.provider.issued.discard(native_ids['enabled'])
        before_repeat = log.read_bytes()
        f.command('enabled-repeat', 'send', session_id=enabled, text='NETCAP_A_TURN_2')
        repeated = wait_for(lambda: next((p for p in f.session(enabled)['view']['pending'] if p['status'] == 'pending'), None), 'same-session second call approval', timeout=45)
        checks['allow_is_single_call_not_session_grant'] = repeated['id'] != pending['id'] and log.read_bytes() == before_repeat
        f.command('repeat-deny', 'respond', session_id=enabled, pending_id=repeated['id'], option_id='decision-1')
        wait_for(lambda: f.session(enabled)['status'] == 'idle', 'second call denial finishes', timeout=45)

        def send(mode, number):
            sid = sessions[mode]
            receipt = f.command(mode + '-turn-' + str(number), 'send', session_id=sid, text=f'NETCAP_A_TURN_{number}')
            wait_for(lambda: f.session(sid)['status'] == 'idle' and next(o for o in f.snapshot()['operations'] if o['id'] == receipt['operation_id'])['status'] == 'succeeded', mode + ' turn finishes', timeout=45)

        send('disabled', 1)
        disabled_requests = [r for r in f.provider.requests if r['body'].get('client_metadata', {}).get('thread_id') == native_ids['disabled']]
        checks['disabled_tool_absent_from_native_exec_registry'] = {'identityAvailable': False} in decoded_outputs(disabled_requests)
        original_log = log.read_bytes()
        f.restart_console(crash=True)
        f.restart_native()
        for mode, sid in sessions.items():
            wait_for(lambda: f.session(sid)['connection'] == 'ready' and f.session(sid)['status'] == 'idle', 'reconnect ' + mode, timeout=45)
            send(mode, 3)
            checks[mode + '_restart_preserves_snapshot_id_and_selection'] = f.api(f'/api/sessions/{sid}/execution')['snapshot'] == snapshots[mode] and f.session(sid)['scope']['native_id'] == native_ids[mode]
            checks[mode + '_restart_native_match'] = any(e['server'] == 'identity' and e['verified'] for e in f.session(sid)['view']['capability_evidence'])
        checks['excluded_and_resumed_turns_do_not_call_tool'] = log.read_bytes() == original_log

        denied = f.command('create-denied', 'create', project_id='fixture', route_id='route-a', permission='read_only', capability_selections=[{'asset_id':'identity','mode':'enabled'}])['session_id']
        wait_for(lambda: f.session(denied)['status'] == 'idle' and any(e['verified'] for e in f.session(denied)['view'].get('capability_evidence', [])), 'denial session ready')
        f.command('denied-turn', 'send', session_id=denied, text='NETCAP_A_TURN_1')
        pending = wait_for(lambda: next((p for p in f.session(denied)['view']['pending'] if p['status'] == 'pending'), None), 'denial approval', timeout=45)
        f.command('mcp-deny', 'respond', session_id=denied, pending_id=pending['id'], option_id='decision-1')
        wait_for(lambda: f.session(denied)['status'] == 'idle', 'denied turn finishes', timeout=45)
        checks['denial_prevents_tool_call'] = log.read_bytes() == original_log
        checks['denied_call_not_recorded_as_success'] = not any(e.get('used_tools') for e in f.session(denied)['view']['capability_evidence'])

        broken = create('enabled', 'unavailable')
        wait_for(lambda: f.session(broken)['status'] == 'unknown', 'unavailable MCP not verified', timeout=45)
        count = len(f.provider.requests)
        for kind in ('send', 'resume'):
            try:
                r = f.command('unavailable-' + kind, kind, session_id=broken, text='must not reach model')
                wait_for(lambda: next(o for o in f.snapshot()['operations'] if o['id'] == r['operation_id'])['status'] in ('not_sent', 'failed'), 'unavailable operation denied')
                checks['unavailable_blocks_' + kind] = True
            except HTTPError as e:
                checks['unavailable_blocks_' + kind] = e.code == 409
        checks['unavailable_no_model_request'] = len(f.provider.requests) == count
        checks['profile_not_modified'] = (f.profile / 'config.toml').read_bytes() == profile
        checks['snapshot_and_evidence_do_not_copy_mcp_secret'] = all('fixture-mcp-A' not in json.dumps(f.api(f'/api/sessions/{sid}/execution')) for sid in sessions.values())
        checks['model_auth_matches'] = bool(f.provider.requests) and all(r['auth_matches'] for r in f.provider.requests)
        checks['no_provider_errors'] = not f.provider.errors
        result = {'scope': 'real Codex, production HTTP, local provider; per-thread stdio MCP selection and one-call native approvals', 'checks': checks, 'native_ids': native_ids, 'expectations_met': all(checks.values())}
        (f.root / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2))
        print(json.dumps(result, ensure_ascii=False), flush=True)
        assert all(checks.values()), checks


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--netcap', default='.dev/bin/netcap')
    parser.add_argument('--hold', action='store_true')
    parser.add_argument('--info-file')
    args = parser.parse_args()
    run(str(Path(args.netcap).resolve()), args.hold, args.info_file)


if __name__ == '__main__': main()
