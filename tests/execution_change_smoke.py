#!/usr/bin/env python3
"""W02.04 pending change via production HTTP, real Codex, local provider."""
import argparse
import json
from pathlib import Path
from urllib.error import HTTPError

from console_fixture import wait_for
from native_mcp_selection_smoke import fixture_for


def run(binary):
    with fixture_for(binary) as f:
        print('Evidence directory:', f.root, flush=True)
        checks = {}
        created = f.command('create', 'create', project_id='fixture', route_id='route-a', permission='read_only',
                            capability_selections=[{'asset_id': 'identity', 'mode': 'enabled'}])
        sid = created['session_id']
        wait_for(lambda: f.session(sid)['status'] == 'idle', 'original session ready', timeout=45)
        before = f.api(f'/api/sessions/{sid}/execution')['snapshot']
        native = f.session(sid)['scope']['native_id']
        request = {'base_snapshot_id': before['id'], 'pending_change_id': '',
                   'selections': [{'asset_id': 'identity', 'mode': 'disabled'}]}
        preview = f.api(f'/api/sessions/{sid}/execution/preview', request)
        checks['preview_has_exact_change'] = preview['changes'] == [{'asset_id': 'identity', 'server': 'identity', 'before': 'enabled', 'after': 'disabled'}]
        checks['preview_has_no_pending_side_effect'] = 'pending_change' not in f.api(f'/api/sessions/{sid}/execution')
        request['preview_hash'] = preview['preview_hash']
        f.command('original-turn', 'send', session_id=sid, text='NETCAP_A_TURN_1')
        pending = wait_for(lambda: next((p for p in f.session(sid)['view']['pending'] if p['status'] == 'pending'), None), 'native approval', timeout=45)
        generation = f.session(sid)['generation']
        receipt = f.command('stage', 'stage_execution', session_id=sid, execution_change=request)
        checks['stage_is_pending_not_native_operation'] = receipt['status'] == 'pending' and 'operation_id' not in receipt
        checks['stage_retry_same_receipt'] = f.command('stage', 'stage_execution', session_id=sid, execution_change=request) == receipt
        saved = f.api(f'/api/sessions/{sid}/execution')
        plan = saved['pending_change']
        checks['candidate_has_linked_new_version'] = plan['snapshot']['revision'] == 2 and plan['snapshot']['previous_snapshot_id'] == before['id'] and plan['snapshot']['id'] != before['id']
        checks['old_snapshot_unchanged'] = saved['snapshot'] == before
        current = f.session(sid)
        checks['native_approval_and_generation_preserved'] = current['status'] == 'waiting_approval' and current['generation'] == generation and any(p['id'] == pending['id'] and p['status'] == 'pending' for p in current['view']['pending'])
        checks['native_selection_still_enabled'] = any(e['server'] == 'identity' and e['runtime_status'] == 'connected' for e in saved['capability_evidence'])
        try:
            f.command('stale-stage', 'stage_execution', session_id=sid, execution_change=request)
            checks['stale_tab_rejected'] = False
        except HTTPError as error:
            checks['stale_tab_rejected'] = error.code == 409
        f.command('approve-old-turn', 'respond', session_id=sid, pending_id=pending['id'], option_id='decision-0')
        wait_for(lambda: f.session(sid)['status'] == 'idle', 'original approved turn completes', timeout=45)
        expected = {'profile': 'A', 'cwd': str(f.cwd), 'credential_matches': True}
        calls = f.root / 'identity-calls.jsonl'
        checks['old_approval_executes_original_tool_once'] = [json.loads(line) for line in calls.read_text().splitlines()] == [expected]
        f.restart_console(crash=True)
        f.restart_native()
        wait_for(lambda: f.session(sid)['connection'] == 'ready' and f.session(sid)['status'] == 'idle', 'same thread reconnects', timeout=45)
        restored = f.api(f'/api/sessions/{sid}/execution')
        checks['crash_preserves_pending_and_effective_snapshots'] = restored['snapshot'] == before and restored['pending_change'] == plan
        checks['ordinary_reconnect_uses_old_binding'] = f.session(sid)['scope']['native_id'] == native and any(e['server'] == 'identity' and e['runtime_status'] == 'connected' and e['mode'] == 'enabled' and e['verified'] for e in restored['capability_evidence'])
        next_turn = f.command('next-turn', 'send', session_id=sid, text='NETCAP_A_TURN_2')
        wait_for(lambda: next(o for o in f.snapshot()['operations'] if o['id'] == next_turn['operation_id'])['status'] == 'succeeded', 'next original-version turn', timeout=45)
        operations = [o for o in f.snapshot()['operations'] if o['session_id'] == sid]
        checks['all_native_operations_retain_original_snapshot'] = all(o['execution_snapshot_id'] == before['id'] and o['intent']['scope']['mcp_selections'][0]['mode'] == 'enabled' for o in operations)
        discard = {'base_snapshot_id': before['id'], 'pending_change_id': plan['id']}
        receipt = f.command('discard', 'discard_execution', session_id=sid, execution_change=discard)
        checks['discard_is_idempotent'] = receipt['status'] == 'discarded' and f.command('discard', 'discard_execution', session_id=sid, execution_change=discard) == receipt
        final = f.api(f'/api/sessions/{sid}/execution')
        checks['discard_keeps_original_execution'] = final['snapshot'] == before and 'pending_change' not in final
        checks['no_source_or_credential_copied'] = 'fixture-mcp-A' not in json.dumps(saved) and 'fixture-A' not in json.dumps(saved)
        checks['provider_auth_matches_and_no_errors'] = bool(f.provider.requests) and all(r['auth_matches'] for r in f.provider.requests) and not f.provider.errors
        result = {'scope': 'production pending-only change regression; explicit native apply is covered separately', 'checks': checks,
                  'native_id': native, 'expectations_met': all(checks.values())}
        for name, data in [('result', result), ('preview', preview), ('saved', saved), ('restored', restored)]:
            (f.root / (name + '.json')).write_text(json.dumps(data, ensure_ascii=False, indent=2))
        print(json.dumps(result, ensure_ascii=False), flush=True)
        assert result['expectations_met'], checks


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--netcap', default='.dev/bin/netcap')
    args = parser.parse_args()
    run(str(Path(args.netcap).resolve()))
