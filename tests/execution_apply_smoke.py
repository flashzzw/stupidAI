#!/usr/bin/env python3
"""W02.04 explicit same-ID apply, using owned daemon restart and local provider."""
import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
from urllib.error import HTTPError

from console_fixture import wait_for
from native_mcp_selection_smoke import fixture_for, decoded_outputs


def run(binary, fault=False, unload_after_drop=False):
    fixture = fixture_for(binary)
    faults = fixture.root / 'faults'
    if fault:
        faults.mkdir()
        fixture.env['NETCAP_FAULT_DIR'] = str(faults)
        fixture.config['instances'][0]['env_allow'].append('NETCAP_FAULT_DIR')
        fixture.config['instances'][0]['executable'] = str(Path(__file__).with_name('fault_codex_proxy.py').resolve())
    with fixture as f:
        print('Evidence directory:', f.root, flush=True)
        checks = {}
        sid = f.command('create', 'create', project_id='fixture', route_id='route-a', permission='read_only', capability_selections=[{'asset_id': 'identity', 'mode': 'enabled'}])['session_id']
        wait_for(lambda: f.session(sid)['status'] == 'idle', 'original idle', timeout=45)

        def operation(receipt):
            return next(o for o in f.snapshot()['operations'] if o['id'] == receipt['operation_id'])

        def settled(receipt):
            return wait_for(lambda: (o if o['status'] in ('succeeded', 'not_sent', 'failed') else None)
                            if (o := operation(receipt)) else None, 'operation settles', timeout=45)

        def command(key, kind, **values):
            def attempt():
                try: return f.command(key, kind, session_id=sid, **values)
                except HTTPError as error:
                    body = json.load(error)
                    if error.code == 409 and '正在核对' in body.get('error', ''): return None
                    raise RuntimeError(body) from error
            return wait_for(attempt, kind + ' accepted', timeout=15)

        first = settled(f.command('first-turn', 'send', session_id=sid, text='NETCAP_A_TURN_0'))
        before = f.api(f'/api/sessions/{sid}/execution')['snapshot']
        original = f.session(sid)
        request = dict(base_snapshot_id=before['id'], pending_change_id='', selections=[dict(asset_id='identity', mode='disabled')])
        preview = f.api(f'/api/sessions/{sid}/execution/preview', request)
        f.command('stage', 'stage_execution', session_id=sid, execution_change=dict(request, preview_hash=preview['preview_hash']))
        plan = f.api(f'/api/sessions/{sid}/execution')['pending_change']
        ref = dict(base_snapshot_id=before['id'], pending_change_id=plan['id'])
        prep = command('prepare', 'prepare_execution', execution_change=ref)
        prepared = settled(prep)
        checks['preparation_is_read_only_and_has_idle_baseline'] = prepared['status'] == 'succeeded' and bool(prepared['receipt'].get('configuration_baseline')) and len(f.provider.requests) == 1
        checks['prepare_preserves_original_snapshot_and_holds'] = f.api(f'/api/sessions/{sid}/execution')['snapshot'] == before and f.session(sid)['execution_hold']
        for kind in ('send', 'resume'):
            try:
                f.command('held-' + kind, kind, session_id=sid, text='must not send')
                checks['hold_blocks_' + kind] = False
            except HTTPError as error:
                checks['hold_blocks_' + kind] = error.code == 409
        loaded = settled(command('loaded-apply', 'apply_execution', execution_change=ref))
        checks['loaded_apply_rejected_without_change'] = loaded['status'] == 'not_sent' and f.api(f'/api/sessions/{sid}/execution')['snapshot'] == before
        f.restart_console(crash=True)
        wait_for(lambda: f.session(sid)['connection'] == 'ready', 'hold restored')
        f.restart_native()
        unloaded = wait_for(lambda: (s if s['view'].get('native_status') == 'notLoaded' and s['status'] == 'idle' else None)
                            if (s := f.session(sid)) else None, 'unloaded, held original thread', timeout=45)
        checks['restart_does_not_autoload_old_config'] = unloaded['execution_hold'] and unloaded['scope']['native_id'] == original['scope']['native_id']
        if fault:
            (faults/'armed.json').write_text(json.dumps({'method':'thread/resume','mode':'drop','next':{'method':'thread/read','mode':'partial'}}))
        apply = command('apply', 'apply_execution', execution_change=ref)
        if fault:
            wait_for(lambda: (faults/'dropped.json').exists(), 'actual native apply receipt dropped')
            wait_for(lambda: operation(apply)['status'] == 'uncertain', 'partial history keeps apply unknown')
            f.restart_console(crash=True)
            wait_for(lambda: f.session(sid)['status'] == 'unknown', 'uncertainty survives console crash')
            checks['lost_apply_retains_old_and_pending_snapshots'] = f.api(f'/api/sessions/{sid}/execution')['snapshot'] == before and f.api(f'/api/sessions/{sid}/execution')['pending_change']['snapshot'] == plan['snapshot']
            if unload_after_drop:
                f.restart_native()
            (faults/'armed.json').unlink()
            if unload_after_drop:
                wait_for(lambda: f.session(sid)['view'].get('native_status') == 'notLoaded', 'unloaded uncertain version observed', timeout=45)
                checks['unloaded_after_lost_receipt_remains_unknown'] = operation(apply)['status'] == 'uncertain' and f.session(sid)['status'] == 'unknown' and f.session(sid)['execution_hold']
                checks['no_replay_when_actual_version_unavailable'] = sum(json.loads(line)['method']=='thread/resume' for line in (faults/'calls.jsonl').read_text().splitlines()) == 1
                try:
                    f.command('reject-unknown', 'apply_execution', session_id=sid, execution_change=ref)
                    checks['unknown_apply_blocks_new_operation'] = False
                except HTTPError as error:
                    checks['unknown_apply_blocks_new_operation'] = error.code == 409
                result = dict(scope='lost native apply receipt + console crash + daemon restart; preserve unknown without replay', checks=checks, expectations_met=all(checks.values()))
                (f.root/'result.json').write_text(json.dumps(result,ensure_ascii=False,indent=2))
                print(json.dumps(result,ensure_ascii=False),flush=True)
                assert result['expectations_met'], checks
                return
        applied = settled(apply)
        if fault:
            checks['lost_receipt_crash_reconciles_without_reapply'] = sum(json.loads(line)['method']=='thread/resume' for line in (faults/'calls.jsonl').read_text().splitlines()) == 1
        checks['explicit_apply_succeeded'] = applied['status'] == 'succeeded'
        read = f.api(f'/api/sessions/{sid}/execution')
        checks['candidate_promoted_unchanged'] = read['snapshot'] == plan['snapshot'] and read['history'] == [before] and 'pending_change' not in read and not read['hold']
        checks['same_native_id_and_history'] = f.session(sid)['scope']['native_id'] == original['scope']['native_id'] and f.session(sid)['view']['messages'] == original['view']['messages']
        checks['native_selection_verified_disabled'] = any(e['server'] == 'identity' and e['mode'] == 'disabled' and e['runtime_status'] == 'disabled' and e['verified'] for e in read['capability_evidence'])
        checks['apply_retry_same_operation'] = f.command('apply', 'apply_execution', session_id=sid, execution_change=ref) == apply
        checks['old_operation_retains_version'] = operation({'operation_id': first['id']})['execution_snapshot_id'] == before['id']
        next_turn = settled(f.command('new-version-turn', 'send', session_id=sid, text='NETCAP_A_TURN_1'))
        checks['next_turn_uses_new_version'] = next_turn['status'] == 'succeeded' and next_turn['execution_snapshot_id'] == plan['snapshot']['id']
        checks['new_version_excludes_actual_tool'] = {'identityAvailable': False} in decoded_outputs(f.provider.requests) and not (f.root/'identity-calls.jsonl').exists()
        f.restart_console(crash=True)
        f.restart_native()
        restarted_at=datetime.now(timezone.utc)
        def fresh_loaded():
            s=f.session(sid)
            observed=s['view'].get('evidence',{}).get('at','')
            return s if observed and datetime.fromisoformat(observed.replace('Z','+00:00'))>restarted_at and s['connection']=='ready' and s['status']=='idle' and s['view'].get('native_status')=='idle' else None
        wait_for(fresh_loaded, 'applied version restored and freshly loaded', timeout=45)
        restored = f.api(f'/api/sessions/{sid}/execution')
        checks['restart_retains_applied_history_and_selection'] = restored['snapshot'] == read['snapshot'] and restored['history'] == [before] and any(e['server'] == 'identity' and e['verified'] and e['runtime_status'] == 'disabled' for e in restored['capability_evidence'])
        second_request = dict(base_snapshot_id=restored['snapshot']['id'], pending_change_id='', selections=[dict(asset_id='identity',mode='enabled')])
        second_preview = f.api(f'/api/sessions/{sid}/execution/preview',second_request)
        f.command('stage-enable','stage_execution',session_id=sid,execution_change=dict(second_request,preview_hash=second_preview['preview_hash']))
        second_plan=f.api(f'/api/sessions/{sid}/execution')['pending_change']
        second_ref=dict(base_snapshot_id=restored['snapshot']['id'],pending_change_id=second_plan['id'])
        second_prepared=settled(command('prepare-enable','prepare_execution',execution_change=second_ref))
        assert second_prepared['status']=='succeeded',second_prepared.get('error')
        f.restart_native()
        wait_for(lambda:f.session(sid)['view'].get('native_status')=='notLoaded' and f.session(sid)['status']=='idle','unloaded before re-enable',timeout=45)
        second_apply=settled(command('apply-enable','apply_execution',execution_change=second_ref))
        third=f.api(f'/api/sessions/{sid}/execution')
        checks['reenable_promotes_version_three_with_both_old_versions'] = second_apply['status']=='succeeded' and third['snapshot']==second_plan['snapshot'] and third['snapshot']['revision']==3 and third['history']==[before,restored['snapshot']]
        f.provider.issued.discard(original['scope']['native_id'])
        third_turn=f.command('version-three-turn','send',session_id=sid,text='NETCAP_A_TURN_3')
        pending=wait_for(lambda:next((p for p in f.session(sid)['view']['pending'] if p['status']=='pending'),None),'re-enabled native call approval',timeout=45)
        f.command('approve-reenabled','respond',session_id=sid,pending_id=pending['id'],option_id='decision-0')
        third_done=settled(third_turn)
        expected={'profile':'A','cwd':str(f.cwd),'credential_matches':True}
        checks['reenabled_version_executes_real_tool_once_after_approval'] = third_done['status']=='succeeded' and third_done['execution_snapshot_id']==third['snapshot']['id'] and [json.loads(line) for line in (f.root/'identity-calls.jsonl').read_text().splitlines()]==[expected]
        checks['local_provider_bound_without_errors'] = all(r['auth_matches'] for r in f.provider.requests) and not f.provider.errors
        result = dict(scope='explicit production same-ID configuration apply; real Codex, local provider, owned daemon restart', checks=checks, expectations_met=all(checks.values()), native_id=original['scope']['native_id'])
        for name, data in [('result', result), ('before', before), ('applied', read), ('restored', restored), ('reenabled',third), ('operations', f.snapshot()['operations'])]:
            (f.root/(name+'.json')).write_text(json.dumps(data,ensure_ascii=False,indent=2))
        print(json.dumps(result,ensure_ascii=False),flush=True)
        assert result['expectations_met'], checks


if __name__ == '__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--netcap',default='.dev/bin/netcap')
    parser.add_argument('--fault',action='store_true')
    parser.add_argument('--unload-after-drop',action='store_true')
    args=parser.parse_args()
    run(str(Path(args.netcap).resolve()),args.fault or args.unload_after_drop,args.unload_after_drop)
