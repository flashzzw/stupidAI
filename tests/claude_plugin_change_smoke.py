#!/usr/bin/env python3
"""Same-ID plugin changes with real tools/hooks and local scripted providers."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil

from claude_plugin_execution_smoke import PluginFixture, plugin_tool, result_texts, planner_checks
from console_fixture import wait_for
from execution_config_probe import write, verify_versions


def run(f, binary):
    checks, sessions, records = {}, {}, {}
    complete = False
    binary_hash = hashlib.sha256(binary.read_bytes()).hexdigest()

    def check(name, ok):
        checks[name] = bool(ok)
        assert ok, name

    def read(sid):
        return f.api(f'/api/sessions/{sid}/execution')

    def settle(receipt):
        def done():
            operation = next(o for o in f.snapshot()['operations'] if o['id'] == receipt['operation_id'])
            return operation if operation['status'] in ['succeeded', 'not_sent', 'failed'] else None
        result = wait_for(done, 'configuration operation', timeout=60)
        assert result['status'] == 'succeeded', result.get('error', result['status'])

    def change(letter, label, mode, version=None):
        sid = sessions[letter]
        before = read(sid)
        history = f.session(sid)['view']['messages']
        count = sum(len(p.requests) for p in f.providers.values())
        pick = {'asset_id': 'suite', 'mode': mode, **({'version': version} if version else {})}
        req = {'base_snapshot_id': before['snapshot']['id'], 'pending_change_id': '', 'selections': [pick]}
        preview = f.api(f'/api/sessions/{sid}/execution/preview', req)
        prefix = letter + '-' + label
        f.command(prefix + '-stage', 'stage_execution', session_id=sid, execution_change={**req, 'preview_hash': preview['preview_hash']})
        staged = read(sid)
        check(prefix + '_save_preserves_current', staged['snapshot'] == before['snapshot'])
        ref = {'base_snapshot_id': before['snapshot']['id'], 'pending_change_id': staged['pending_change']['id']}
        settle(f.command(prefix + '-prepare', 'prepare_execution', session_id=sid, execution_change=ref))
        settle(f.command(prefix + '-apply', 'apply_execution', session_id=sid, execution_change=ref))
        after = read(sid)
        check(prefix + '_exact_candidate_and_history', after['snapshot'] == staged['pending_change']['snapshot'] and after['history'] == before.get('history', []) + [before['snapshot']])
        check(prefix + '_same_id_no_model_replay', f.session(sid)['scope']['native_id'] == native[letter] and f.session(sid)['view']['messages'] == history and count == sum(len(p.requests) for p in f.providers.values()))
        check(prefix + '_remains_paused', f.session(sid)['paused'] and not after['hold'])
        records[prefix] = {'preview': preview, 'before': before, 'after': after}
        return after

    def call(letter, turn, version):
        sid = sessions[letter]
        before = len(f.calls(letter))
        sent = f.command(f'{letter}-send-{turn}', 'send', session_id=sid, text=f'NETCAP_{letter}_TURN_{turn}_PLUGIN_suite')
        pending = f.pending(sid)
        check(f'{letter}-{turn}_new_approval', pending['kind'] == plugin_tool('plugin-suite') and len(f.calls(letter)) == before)
        f.command(f'{letter}-allow-{turn}', 'respond', session_id=sid, pending_id=pending['id'], option_id='allow')
        f.idle(sid)
        expected = {**f.expected_identity(letter), 'plugin_version': version}
        check(f'{letter}-{turn}_exact_versioned_result', len(f.calls(letter)) == before + 1 and f.calls(letter)[-1] == expected and expected in result_texts(f.providers[letter].requests))
        operation = next(o for o in f.snapshot()['operations'] if o['id'] == sent['operation_id'])
        check(f'{letter}-{turn}_active_snapshot_recorded', operation['execution_snapshot_id'] == read(sid)['snapshot']['id'])
        trace = f.hooks(letter, native[letter])
        posts = [row for row in trace if row['hook_event_name'] == 'PostToolUse']
        selected = next(p for p in read(sid)['snapshot']['plugin_selections'] if p['asset_id'] == 'suite')
        plugin_root = Path(selected['source_path'])
        fixture_source = f.root / ('plugin-suite' if version == '1.0.0' else 'plugin-suite-v2')
        # Managed versions execute from the library's retained copy. Verify its
        # contents against the fixture version before checking the hook path.
        files = lambda root: {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest() for p in root.rglob('*') if p.is_file()}
        check(f'{letter}-{turn}_selected_package_matches_fixture_version', selected['version'] == version and files(plugin_root) == files(fixture_source))
        check(f'{letter}-{turn}_hook_uses_selected_version', len(posts) == before + 1 and posts[-1]['plugin_root'] == str(plugin_root) and posts[-1]['cwd'] == expected['cwd'])

    def excluded(label, turn):
        sid = sessions['A']
        count, hooks, start = len(f.calls('A')), len(f.hooks('A', native['A'])), len(f.providers['A'].requests)
        f.command(label + '-send', 'send', session_id=sid, text=f'NETCAP_A_TURN_{turn}_PLUGIN_suite')
        f.idle(sid)
        reqs = f.providers['A'].requests[start:]
        check(label + '_tool_absent_from_actual_requests', bool(reqs) and all(not any(t['name'] == plugin_tool('plugin-suite') for t in r['body']['tools']) for r in reqs))
        check(label + '_no_tool_or_plugin_hook', len(f.calls('A')) == count and len(f.hooks('A', native['A'])) == hooks)

    try:
        for letter in ['A', 'B']:
            profile = 'claude-' + letter.lower()
            receipt = f.command('create-' + letter, 'create', project_id=profile, route_id=profile + '-route', permission='read_only')
            sessions[letter] = receipt['session_id']
            f.idle(receipt['session_id'])
        native = {letter: f.session(sid)['scope']['native_id'] for letter, sid in sessions.items()}
        for letter in ['A', 'B']:
            change(letter, 'add', 'enabled', 'asset-v1')
            call(letter, 1, '1.0.0')
        change('A', 'disable', 'disabled')
        excluded('disabled', 2)
        change('A', 'enable', 'enabled')
        call('A', 3, '1.0.0')
        old = {letter: read(sid)['snapshot'] for letter, sid in sessions.items()}
        current = next(x for x in f.api('/api/capabilities') if x['asset']['id'] == 'suite')
        req = {'action': 'update', 'asset_id': 'suite', 'base_revision': current['revision'], 'asset': {**current['asset'], 'version': 'asset-v2', 'source': str(f.root / 'plugin-suite-v2'), 'management': 'managed'}}
        preview = f.api('/api/capabilities/preview', req)
        f.command('publish-v2', 'manage_capability', capability_change={**preview['request'], 'preview_hash': preview['preview_hash']})
        check('library_update_keeps_both_snapshots', all(read(sid)['snapshot'] == old[letter] for letter, sid in sessions.items()))
        change('A', 'upgrade', 'enabled', 'asset-v2')
        call('A', 4, '2.0.0')
        call('B', 2, '1.0.0')
        released = change('A', 'inherit', 'inherit')
        check('inherit_releases_explicit_dependency', not released['snapshot'].get('plugin_selections') and not released['snapshot']['capability_refs'])
        excluded('inherited', 5)
        saved = read(sessions['B'])['snapshot']
        f.restart_console()
        settle(f.command('resume-B', 'resume', session_id=sessions['B']))
        check('restart_same_id_snapshot', f.session(sessions['B'])['scope']['native_id'] == native['B'] and read(sessions['B'])['snapshot'] == saved)
        call('B', 3, '1.0.0')
        change('B', 'upgrade-after-restart', 'enabled', 'asset-v2')
        call('B', 4, '2.0.0')
        check('provider_auth_and_history_isolated', all(p.requests and not p.errors and all(r['auth_matches'] and 'NETCAP_' + ('B' if letter == 'A' else 'A') + '_TURN_' not in json.dumps(r['body']) for r in p.requests) for letter, p in f.providers.items()))
        check('same_binary_throughout', hashlib.sha256(binary.read_bytes()).hexdigest() == binary_hash)
        complete = True
    finally:
        write(f.root / 'snapshot.json', f.snapshot())
        write(f.root / 'plugin-change-records.json', records)
        write(f.root / 'result.json', {'scope': 'production Application/Runtime/Session, real plugin changes and tools/hooks with local providers', 'checks': checks, 'sessions': sessions, 'binary_sha256': binary_hash, 'expectations_met': complete and all(checks.values()), 'completed_matrix': complete})
    print(json.dumps({'checks': checks, 'expectations_met': complete}, indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--netcap', default='.dev/bin/netcap')
    parser.add_argument('--planner-only', action='store_true')
    args = parser.parse_args()
    if args.planner_only:
        print(json.dumps({'checks': planner_checks(), 'native_execution_verified': False}, indent=2))
        return
    binary = Path(args.netcap).resolve()
    with PluginFixture(str(binary)) as fixture:
        print('Evidence directory:', fixture.root, flush=True)
        source = fixture.root / 'plugin-suite'
        server = source / 'server.py'
        server.write_text(server.read_text().replace("identity={'profile':profile", "identity={'plugin_version':'1.0.0','profile':profile"))
        v2 = fixture.root / 'plugin-suite-v2'
        shutil.copytree(source, v2)
        write(v2 / '.claude-plugin/plugin.json', {'name': 'plugin-suite', 'version': '2.0.0'})
        (v2 / 'server.py').write_text((v2 / 'server.py').read_text().replace("'plugin_version':'1.0.0'", "'plugin_version':'2.0.0'"))
        verify_versions(fixture.root, ['claude'])
        fixture.register()
        run(fixture, binary)


if __name__ == '__main__':
    main()
