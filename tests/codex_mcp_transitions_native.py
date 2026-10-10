#!/usr/bin/env python3
"""Verify shared MCP versions through the real Application, HTTP and native CLI."""
import argparse
import copy
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from urllib.error import HTTPError
from uuid import uuid4

from execution_config_probe import CODEX, environment, verify_versions, write
from go_cli_smoke import Proxy
from runtime.common import stop, wait_for
from shared_mcp_profiles_native import REPO, SharedFixture


class TransitionFixture(SharedFixture):
    def api(self, path, body=None):
        try:
            return super().api(path, body)
        except HTTPError as error:
            detail = error.read().decode(errors='replace')
            with (self.root / 'http-errors.jsonl').open('a') as output:
                output.write(json.dumps({'path': path, 'status': error.code, 'detail': detail}) + '\n')
            error.add_note(detail)
            raise

    def __init__(self, binary):
        super().__init__(binary)
        self.versions = {}
        self.make_asset('added', 'added', ['codex'])
        self.make_asset('inherited', 'keep', ['codex'])
        for asset, server in [('shared', 'identity'), ('added', 'added'), ('inherited', 'keep')]:
            self.write_version(asset, server, 'v1', self.assets[asset])
        for version in ['v2', 'v3']:
            self.write_version('shared', 'identity', version, self.root / 'versions' / version)
        for profile, row in self.profiles.items():
            if not profile.startswith('codex'):
                continue
            path = Path(row['instance']['profile']) / 'config.toml'
            with path.open('a') as output:
                output.write('\n[mcp_servers.keep]\ncommand = "python3"\nargs = ' + json.dumps([str(self.assets['inherited'] / 'server.py')]) + '\n')
                output.write('cwd = ' + json.dumps(str(self.root / profile / 'work')) + '\n[mcp_servers.keep.env]\n')
                for key, value in {'MCP_CONTEXT': profile, 'MCP_TOKEN': 'fixture-mcp-' + profile, 'MCP_LOG': str(self.root / 'calls' / profile)}.items():
                    output.write(key + ' = ' + json.dumps(value) + '\n')

    def write_version(self, asset, server, version, root):
        root.mkdir(parents=True, exist_ok=True)
        marker = uuid4().hex
        script = '''import json,os,sys
from pathlib import Path
for line in sys.stdin:
 request=json.loads(line)
 if 'id' not in request:continue
 method=request['method']
 if method=='initialize':result={'protocolVersion':request['params']['protocolVersion'],'capabilities':{'tools':{},'experimental':{'original':{'fixture':'preserved'}}},'serverInfo':{'name':'owned-mcp','version':'unchanged'}}
 elif method=='tools/list':result={'tools':[{'name':'whoami','description':'Return the owned execution binding','inputSchema':{'type':'object','properties':{}}}]}
 elif method=='tools/call':
  profile=os.environ['MCP_CONTEXT']
  identity={'profile':profile,'cwd':os.getcwd(),'credential_matches':os.getenv('MCP_TOKEN')=='fixture-mcp-'+profile,'asset':ASSET,'version':VERSION,'marker':MARKER,'pid':os.getpid()}
  with (Path(os.environ['MCP_LOG'])/(SERVER+'.jsonl')).open('a') as output:output.write(json.dumps(identity)+'\\n')
  result={'content':[{'type':'text','text':json.dumps(identity)}],'_meta':{'original':'preserved'}}
 else:result={}
 print(json.dumps({'jsonrpc':'2.0','id':request['id'],'result':result}),flush=True)
'''
        for key, value in {'ASSET': asset, 'SERVER': server, 'VERSION': version, 'MARKER': marker}.items():
            script = script.replace(key, repr(value))
        write(root / 'server.py', script)
        write(root / '.mcp.json', {'mcpServers': {server: {'command': 'python3', 'args': ['./server.py'], 'env': {
            'MCP_CONTEXT': '${NETCAP_MCP_CONTEXT}', 'MCP_TOKEN': '${NETCAP_MCP_SECRET}', 'MCP_LOG': '${NETCAP_MCP_LOG}'}}}})
        self.versions[asset + '/' + version] = {'root': str(root), 'marker': marker}


def exercise(f):
    checks, sessions, frames, operations, transitions, raw = {}, {}, {}, {}, {}, {}
    number, observer, finished = 0, None, False

    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    def profile(label):
        return label if label in f.profiles else 'codex-a'

    def execution(label):
        return f.api('/api/sessions/' + sessions[label] + '/execution')

    def frame(label):
        print('Verified phase:', label, flush=True)
        data = {'/api/workspace': f.api('/api/workspace'), '/api/capabilities': f.api('/api/capabilities')}
        for sid in sessions.values():
            for path in ['/api/sessions/' + sid, '/api/sessions/' + sid + '/execution']:
                data[path] = f.api(path)
        frames[label] = data
        write(f.root / 'frames.json', frames)

    def binding(snapshot, asset):
        selection = next(row for row in snapshot.get('mcp_selections', []) if row['asset_id'] == asset)
        return selection.get('definition', {}).get('binding')

    def actual(label, asset):
        return next((row for row in f.session(sessions[label])['view'].get('capability_evidence', [])
            if row.get('asset_id') == asset and row.get('mode') != 'history'), None)

    def stamp(value):
        return {key: value[key] for key in ['version', 'digest', 'asset_id', 'asset_version', 'server']}

    def settled(receipt):
        value = f.settle(receipt)
        operations[receipt['operation_id']] = copy.deepcopy(value)
        return value

    def invoke(label, server, asset, version):
        nonlocal number
        number += 1
        execution_profile = profile(label)
        before = len(f.calls(execution_profile, server))
        receipt = f.command('input-' + str(number), 'send', session_id=sessions[label],
            text=f'NETCAP_{execution_profile[-1].upper()}_TURN_{number} PROFILE_{execution_profile} TOOL_{server}')
        def approval_ready():
            operation = f.operation(receipt)
            if operation['status'] in ['not_sent', 'failed', 'interrupted']:
                raise RuntimeError(operation.get('error', operation['status']))
            return next((row for row in f.session(sessions[label])['view']['pending'] if row['status'] == 'pending'), None)
        pending = wait_for(approval_ready, 'single approval ' + label, timeout=45)
        check(f'{number}_{label}_waits_for_approval', len(f.calls(execution_profile, server)) == before)
        approval = f.command('approval-' + str(number), 'respond', session_id=sessions[label], pending_id=pending['id'],
            option_id='decision-0' if execution_profile.startswith('codex') else 'allow')
        settled(approval)
        settled(receipt)
        wait_for(lambda: f.session(sessions[label])['status'] == 'idle', 'original input idle', timeout=30)
        calls = f.calls(execution_profile, server)
        check(f'{number}_{label}_one_actual_call', len(calls) == before + 1)
        result = calls[-1]
        check(f'{number}_{label}_correct_version_and_identity', result['profile'] == execution_profile and result['asset'] == asset and result['version'] == version and result['credential_matches'] and result['cwd'] == str(f.root / execution_profile / 'work'))
        if asset + '/' + version in f.versions:
            check(f'{number}_{label}_actual_process_marker', result['marker'] == f.versions[asset + '/' + version]['marker'])
        check(f'{number}_{label}_exact_result_returns_to_model', result in f.providers[execution_profile[-1].upper()].returned(execution_profile, number))
        snapshot = execution(label)['snapshot']
        check(f'{number}_{label}_input_keeps_its_snapshot', f.operation(receipt)['execution_snapshot_id'] == snapshot['id'])
        if execution_profile.startswith('codex') and asset != 'inherited':
            expected = stamp(binding(snapshot, asset))
            evidence = wait_for(lambda: actual(label, asset), 'bound usage evidence')
            check(f'{number}_{label}_loaded_binding_matches', evidence.get('verified') and evidence.get('binding') == expected)
            check(f'{number}_{label}_successful_call_has_same_binding', evidence.get('used_tools') == ['whoami'])
        return result

    def register(version):
        current = next(row for row in f.api('/api/capabilities') if row['asset']['id'] == 'shared')
        asset = {**current['asset'], 'source': f.versions['shared/' + version]['root'], 'version': version}
        request = {'action': 'update', 'asset_id': 'shared', 'base_revision': current['revision'], 'asset': asset}
        preview = f.api('/api/capabilities/preview', request)
        request['preview_hash'] = preview['preview_hash']
        receipt = f.command('register-' + version, 'manage_capability', capability_change=request)
        check(version + '_registry_retry_is_idempotent', receipt == f.command('register-' + version, 'manage_capability', capability_change=request))

    def apply(label, tag, picks, unknown=False):
        before = execution(label)
        old_session = f.session(sessions[label])
        request = {'base_snapshot_id': before['snapshot']['id'], 'pending_change_id': '', 'selections': picks}
        preview = f.api('/api/sessions/' + sessions[label] + '/execution/preview', request)
        request['preview_hash'] = preview['preview_hash']
        calls = sum(len(provider.requests) for provider in f.providers.values())
        f.command('stage-' + tag, 'stage_execution', session_id=sessions[label], execution_change=request)
        pending = execution(label)['pending_change']
        ref = {'base_snapshot_id': before['snapshot']['id'], 'pending_change_id': pending['id']}
        check(tag + '_stage_preserves_active_snapshot', execution(label)['snapshot'] == before['snapshot'])
        settled(f.command('prepare-' + tag, 'prepare_execution', session_id=sessions[label], execution_change=ref))
        check(tag + '_prepare_holds_original', execution(label)['snapshot'] == before['snapshot'] and execution(label)['hold'])
        receipt = f.command('apply-' + tag, 'apply_execution', session_id=sessions[label], execution_change=ref)
        if unknown:
            wait_for(lambda: f.operation(receipt)['status'] == 'uncertain', 'same native change remains uncertain', timeout=45)
            after = execution(label)
            check(tag + '_old_snapshot_and_candidate_retained', after['snapshot'] == before['snapshot'] and after['pending_change']['snapshot'] == pending['snapshot'] and after['hold'])
        else:
            settled(receipt)
            wait_for(lambda: execution(label)['snapshot']['id'] == pending['snapshot']['id'], 'candidate promotion', timeout=30)
            after = execution(label)
            check(tag + '_promotes_exact_candidate', after['snapshot'] == pending['snapshot'] and 'pending_change' not in after)
            check(tag + '_retains_original_snapshot', before['snapshot'] in after['history'])
        current = f.session(sessions[label])
        check(tag + '_same_id_and_history', current['scope']['native_id'] == old_session['scope']['native_id'] and current['view']['messages'] == old_session['view']['messages'])
        check(tag + '_no_model_input_during_change', sum(len(provider.requests) for provider in f.providers.values()) == calls)
        check(tag + '_same_command_reuses_receipt', receipt == f.command('apply-' + tag, 'apply_execution', session_id=sessions[label], execution_change=ref))
        touched = {pick['asset_id'] for pick in picks}
        for old in before['snapshot'].get('mcp_selections', []):
            if old['asset_id'] not in touched:
                check(tag + '_preserves_' + old['asset_id'], old in pending['snapshot'].get('mcp_selections', []))
        transitions[tag] = {'label': label, 'before': before, 'preview': preview, 'pending': pending, 'receipt': receipt, 'request': ref, 'after': after}
        return transitions[tag]

    try:
        for name, execution_profile in f.profiles.items():
            f.command('register-' + name, 'register_execution', execution_profile=execution_profile)
        check('four_registrations_do_not_call_models', not any(provider.requests for provider in f.providers.values()))
        for label in [*f.profiles, 'peer', 'last']:
            execution_profile = profile(label)
            picks = [{'asset_id': 'added', 'mode': 'enabled'}] if label == 'last' else [
                {'asset_id': 'shared', 'mode': 'enabled'}, {'asset_id': 'private-' + execution_profile, 'mode': 'enabled'}]
            receipt = f.command('create-' + label, 'create', project_id=execution_profile, route_id=execution_profile + '-route',
                title='MCP transition ' + label, permission='read_only', capability_selections=picks)
            sessions[label] = receipt['session_id']
            settled(receipt)
            wait_for(lambda: f.session(sessions[label])['status'] == 'idle', 'created ' + label, timeout=45)
            wait_for(lambda: all((actual(label, pick['asset_id']) or {}).get('verified') for pick in picks), 'loaded selected MCPs ' + label, timeout=30)
        for label in [*f.profiles, 'peer']:
            invoke(label, 'identity', 'shared', 'v1')
        invoke('last', 'added', 'added', 'v1')
        check('same_daemon_sessions_have_distinct_launch_bindings', binding(execution('codex-a')['snapshot'], 'shared')['digest'] != binding(execution('peer')['snapshot'], 'shared')['digest'])
        frame('initial')

        apply('codex-a', 'added', [{'asset_id': 'added', 'mode': 'enabled', 'version': 'v1'}])
        invoke('codex-a', 'added', 'added', 'v1')
        frame('added')
        register('v2')
        frame('v2_registered')
        apply('codex-a', 'version', [{'asset_id': 'shared', 'mode': 'enabled', 'version': 'v2'}])
        invoke('codex-a', 'identity', 'shared', 'v2')
        for label in ['peer', 'codex-b', 'claude-a', 'claude-b']:
            invoke(label, 'identity', 'shared', 'v1')
        invoke('codex-a', 'keep', 'inherited', 'v1')
        invoke('codex-a', 'exclusive', 'private-codex-a', 'v1')
        rows = f.session(sessions['codex-a'])['view']['capability_evidence']
        check('v1_call_stays_in_versioned_history', any(row.get('mode') == 'history' and row.get('binding', {}).get('asset_version') == 'v1' and row.get('used_tools') == ['whoami'] for row in rows))
        check('v2_current_call_is_separate', actual('codex-a', 'shared')['binding']['asset_version'] == 'v2')
        frame('v2')

        source = Path(f.versions['shared/v2']['root']) / 'server.py'
        original_bytes = source.read_bytes()
        count = sum(len(provider.requests) for provider in f.providers.values())
        source.write_bytes(original_bytes + b'\n# Owned version drift\n')
        try:
            for label in [*f.profiles, 'peer']:
                check('v2_drift_isolated_' + label, execution(label)['status'] == ('drifted' if label == 'codex-a' else 'unchanged'))
            try:
                f.command('blocked-source-drift', 'send', session_id=sessions['codex-a'], text='Do not use a changed resource version.')
            except HTTPError as error:
                check('v2_drift_blocks_native_input', error.code == 409)
            else:
                raise AssertionError('changed source admitted an input')
            check('v2_drift_makes_no_model_request', sum(len(provider.requests) for provider in f.providers.values()) == count)
        finally:
            source.write_bytes(original_bytes)
        check('restored_v2_retains_original_binding', execution('codex-a')['status'] == 'unchanged')

        native_id = f.session(sessions['codex-a'])['scope']['native_id']
        observer = Proxy(CODEX, Path(f.profiles['codex-a']['instance']['socket']), f.native_envs['codex-a'])
        raw['before_second_subscriber'] = observer.call('thread/read', {'threadId': native_id, 'includeTurns': True})
        raw['second_subscription'] = observer.call('thread/resume', {'threadId': native_id, 'excludeTurns': True, 'cwd': str(f.root / 'codex-a/work'),
            'model': 'gpt-6-astra', 'modelProvider': 'fixture', 'sandbox': 'read-only', 'approvalPolicy': 'on-request'})
        register('v3')
        stale = apply('codex-a', 'second_subscriber', [{'asset_id': 'shared', 'mode': 'enabled', 'version': 'v3'}], unknown=True)
        raw['stale_inventory'] = observer.call('mcpServerStatus/list', {'threadId': native_id})
        loaded = next(row for row in raw['stale_inventory']['data'] if row['name'] == 'identity')
        check('second_subscriber_native_still_has_v2', loaded['serverCapabilities']['experimental']['netcap/binding'] == stamp(binding(stale['before']['snapshot'], 'shared')))
        raw['stale_history'] = observer.call('thread/read', {'threadId': native_id, 'includeTurns': True})
        check('second_subscriber_did_not_replay_history', raw['stale_history']['thread']['turns'] == raw['before_second_subscriber']['thread']['turns'])
        count = sum(len(provider.requests) for provider in f.providers.values())
        try:
            f.command('blocked-input', 'send', session_id=sessions['codex-a'], text='Do not send while a configuration version is unknown.')
        except HTTPError as error:
            check('unknown_change_blocks_input', error.code == 409)
        else:
            raise AssertionError('unknown configuration accepted a new input')
        check('blocked_input_did_not_call_model', sum(len(provider.requests) for provider in f.providers.values()) == count)
        frame('stale')
        f.restart_console()
        wait_for(lambda: f.session(sessions['codex-a'])['status'] == 'unknown', 'unknown change after restart', timeout=45)
        restarted = execution('codex-a')
        check('restart_keeps_unknown_candidate_and_original', restarted['snapshot'] == stale['before']['snapshot'] and restarted['pending_change']['snapshot'] == stale['pending']['snapshot'])
        check('restart_does_not_replay_input', sum(len(provider.requests) for provider in f.providers.values()) == count)
        frame('restarted_stale')
        raw['unsubscribe_second'] = observer.call('thread/unsubscribe', {'threadId': native_id})
        raw['second_events'] = observer.events
        observer.close()
        observer = None

        stop(f.daemons['codex-a'])
        for label in ['codex-b', 'claude-a', 'claude-b']:
            invoke(label, 'exclusive', 'private-' + label, 'v1')
        check('other_three_profiles_continue_while_owned_daemon_stopped', f.daemons['codex-a'].poll() is not None)
        f.start_daemon('codex-a')
        wait_for(lambda: f.session(sessions['codex-a'])['view'].get('native_status') == 'notLoaded', 'unknown original remains unloaded', timeout=45)
        recovery = {**stale['request'], 'operation_id': stale['receipt']['operation_id']}
        report = f.api('/api/sessions/' + sessions['codex-a'] + '/execution/recovery-check', recovery)
        raw['recovery_check'] = report
        check('unloaded_original_has_explicit_recovery_evidence', report['satisfied'])
        count = sum(len(provider.requests) for provider in f.providers.values())
        recovered = f.command('recover-original', 'recover_execution', session_id=sessions['codex-a'], execution_recovery=recovery)
        settled(recovered)
        wait_for(lambda: 'pending_change' not in execution('codex-a'), 'original version recovery', timeout=45)
        check('recovery_keeps_original_snapshot', execution('codex-a')['snapshot'] == stale['before']['snapshot'])
        check('recovery_does_not_replay_model_input', sum(len(provider.requests) for provider in f.providers.values()) == count)
        check('recovery_keeps_native_id', f.session(sessions['codex-a'])['scope']['native_id'] == native_id)
        frame('recovered')
        # Recovery preserves the user's pause. Explicit resume admits the next
        # new input only after the original configuration has been established.
        settled(f.command('resume-recovered', 'resume', session_id=sessions['codex-a']))
        invoke('codex-a', 'identity', 'shared', 'v2')

        apply('codex-a', 'inherit', [{'asset_id': 'shared', 'mode': 'inherit'}, {'asset_id': 'added', 'mode': 'inherit'}])
        check('released_shared_services_absent', all(row['server'] not in ['identity', 'added'] for row in f.session(sessions['codex-a'])['view']['capability_evidence'] if row.get('mode') != 'history'))
        invoke('codex-a', 'keep', 'inherited', 'v1')
        invoke('codex-a', 'exclusive', 'private-codex-a', 'v1')
        invoke('peer', 'identity', 'shared', 'v1')
        frame('inherit')
        apply('last', 'last_inherit', [{'asset_id': 'added', 'mode': 'inherit'}])
        check('empty_target_removes_last_explicit_binding', not execution('last')['snapshot'].get('mcp_selections'))
        observer = Proxy(CODEX, Path(f.profiles['codex-a']['instance']['socket']), f.native_envs['codex-a'])
        for label in ['codex-a', 'peer', 'last']:
            sid = f.session(sessions[label])['scope']['native_id']
            raw[label + '_final_history'] = observer.call('thread/read', {'threadId': sid, 'includeTurns': True})
            raw[label + '_final_inventory'] = observer.call('mcpServerStatus/list', {'threadId': sid})
        check('last_inherit_native_inventory_preserves_only_inherited_keep', [row['name'] for row in raw['last_final_inventory']['data']] == ['keep'])
        frame('final')
        check('local_providers_have_no_protocol_errors', all(not provider.errors for provider in f.providers.values()))
        finished = True
    finally:
        if observer:
            observer.close()
        write(f.root / 'native-readbacks.json', raw)
        write(f.root / 'result.json', {'passed': finished and all(checks.values()), 'checks': checks, 'sessions': sessions, 'frames': frames,
            'transitions': transitions, 'operations': operations, 'versions': f.versions,
            'scope': 'real Application/HTTP/native CLI; four profiles, same-daemon peer, exact MCP load/call binding; local scripted providers only'})
        write(f.root / 'snapshot.json', f.snapshot())
        write(f.root / 'tool-programs.json', {key: provider.tool_programs for key, provider in f.providers.items()})
    print(json.dumps({'passed': True, 'checks': len(checks)}), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--netcap', type=Path, required=True)
    parser.add_argument('--inside', type=Path)
    args = parser.parse_args()
    if args.inside:
        assert os.path.samefile(args.inside / 'policy', '/etc/claude-code') and os.path.samefile(args.inside / 'policy', '/etc/codex')
        os.environ['TMPDIR'] = str(args.inside)
        tempfile.tempdir = None
        verify_versions(args.inside, ['codex', 'server', 'claude'])
        with TransitionFixture(str(args.netcap)) as fixture:
            print('Fixture directory:', fixture.root, flush=True)
            exercise(fixture)
        return
    root = Path(tempfile.mkdtemp(prefix='netcap-mcp-transitions-', dir='/var/tmp'))
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
