#!/usr/bin/env python3
"""Real Claude configuration reply loss, mid-apply crash and rollback."""
import argparse
import json
import os
from pathlib import Path
import signal
from urllib.error import HTTPError

from claude_execution_apply_smoke import fixture_for
from console_fixture import wait_for
from execution_config_probe import write


def run(case, resources=False, release=False):
    if resources:
        from claude_execution_resources_smoke import ResourceFixture
        f = ResourceFixture(str(Path('.dev/bin/netcap').resolve()))
    else:
        f = fixture_for(str(Path('.dev/bin/netcap').resolve()), 'http' if case == 'rollback' else 'stdio')
    faults = f.root / 'faults'
    if case != 'rollback':
        faults.mkdir()
        f.env['NETCAP_CLAUDE_FAULT_DIR'] = str(faults)
        profile = f.profiles['claude-a']['instance']
        profile['env_allow'].append('NETCAP_CLAUDE_FAULT_DIR')
        profile['bridge'] = str(Path('tests/fault_claude_bridge.mjs').resolve())
    with f:
        print('Evidence directory:', f.root, flush=True)
        f.register()
        sid = f.create_selected('fault', mode='disabled' if case == 'rollback' and not resources else 'enabled')
        native = f.session(sid)['scope']['native_id']
        def read(): return f.api(f'/api/sessions/{sid}/execution')
        def operation(receipt): return next(o for o in f.snapshot()['operations'] if o['id'] == receipt['operation_id'])
        def command(key, kind, **params):
            def attempt():
                try: return f.command(key, kind, session_id=sid, **params)
                except HTTPError as error:
                    body = json.load(error)
                    if error.code == 409 and '正在核对' in body.get('error',''): return None
                    raise RuntimeError(body) from error
            return wait_for(attempt, kind + ' accepted', timeout=20)
        original = read()['snapshot']
        if resources: f.publish_v2()
        selection = {'asset_id':'shared','mode':'enabled' if case == 'rollback' or resources else 'disabled'}
        if resources: selection['version'] = 'v2'
        if release: selection['mode'] = 'inherit'
        request = {'base_snapshot_id': original['id'], 'pending_change_id':'', 'selections':[selection]}
        preview = f.api(f'/api/sessions/{sid}/execution/preview',request)
        command('stage','stage_execution',execution_change={**request,'preview_hash':preview['preview_hash']})
        plan = read()['pending_change']
        ref = {'base_snapshot_id':original['id'],'pending_change_id':plan['id']}
        prep = command('prepare','prepare_execution',execution_change=ref)
        wait_for(lambda: operation(prep)['status']=='succeeded','prepared')
        if case=='rollback': f.remote.accept_auth=False
        else: write(faults/'armed.json',{'mode':'drop_verified' if case=='drop' else 'crash_applying'})
        applied = command('apply','apply_execution',execution_change=ref)
        checks = {}
        metadata = f.root/'console/runtimes/claude-a'/(native+'.json')
        if case=='rollback':
            wait_for(lambda:operation(applied)['status']=='not_sent','candidate failure rolled back',timeout=60)
            checks['failed_candidate_retains_original_snapshot'] = read()['snapshot']==original and read()['pending_change']['status']=='pending'
            checks['failed_candidate_has_durable_rollback'] = json.loads(metadata.read_text())['_configuration']['status']=='not_sent'
            checks['failed_candidate_does_not_call_model_or_tool'] = not f.provider.requests and not f.remote.calls
            checks['failure_keeps_automation_paused'] = f.session(sid)['paused'] and read()['hold']
            f.restart_console(crash=True)
            wait_for(lambda:f.session(sid)['connection']=='ready','rollback restored',timeout=60)
            f.remote.accept_auth=True
            prep2=command('prepare-retry','prepare_execution',execution_change=ref)
            wait_for(lambda:operation(prep2)['status']=='succeeded','fresh preparation')
            retry=command('apply-retry','apply_execution',execution_change=ref)
            wait_for(lambda:operation(retry)['status']=='succeeded','explicit new apply',timeout=60)
            checks['explicit_retry_promotes_same_saved_candidate'] = read()['snapshot']==plan['snapshot'] and read()['history']==[original] and f.session(sid)['scope']['native_id']==native
            f.command('send-after-retry','send',session_id=sid,text='NETCAP_A_TURN_1_TOOL_identity' if resources else 'NETCAP_A_TURN_1')
            pending=f.pending(sid)
            f.command('allow-after-retry','respond',session_id=sid,pending_id=pending['id'],option_id='allow')
            f.idle(sid)
            checks['retried_connection_uses_original_account'] = f.remote.calls==[{'profile':'A','workspace':str(f.root/'claude-a/work'),'credential_matches':True,'transport':'http'}] if resources else f.calls('A')==[f.expected_identity('A')]
        else:
            wait_for(lambda:(faults/'intercepted.json').exists(),'fault intercept',timeout=60)
            if case=='crash':
                owner=json.loads((faults/'owner.json').read_text())
                assert b'fault_claude_bridge.mjs' in Path(f"/proc/{owner['wrapper']}/cmdline").read_bytes()
                os.killpg(owner['wrapper'],signal.SIGKILL)
            wait_for(lambda:operation(applied)['status']=='uncertain','unverified reply',timeout=45)
            checks['unknown_keeps_old_and_pending_snapshots'] = read()['snapshot']==original and read()['pending_change']['snapshot']==plan['snapshot'] and read()['hold']
            f.command('manual-takeover','takeover',session_id=sid)
            f.restart_console(crash=True)
            wait_for(lambda:operation(applied)['status']=='uncertain','uncertainty persists after restart')
            if case=='drop':
                checks['native_success_is_durable_before_wire_reply'] = json.loads(metadata.read_text())['_configuration']['status']=='verified'
                if resources:
                    current = next(x for x in f.api('/api/capabilities') if x['asset']['id']=='shared')
                    disable = {'action':'disable','asset_id':'shared','base_revision':current['revision']}
                    preview = f.api('/api/capabilities/preview',disable)
                    f.command('disable-after-native-commit','manage_capability',capability_change={**preview['request'],'preview_hash':preview['preview_hash']})
                (faults/'armed.json').unlink()
                wait_for(lambda:operation(applied)['status']=='succeeded','durable success reconciled without apply',timeout=60)
                checks['late_proof_promotes_exact_candidate'] = read()['snapshot']==plan['snapshot'] and read()['history']==[original]
                checks['late_proof_does_not_regrant_control'] = f.session(sid)['control_mode']=='human_takeover' and f.session(sid)['paused']
            else:
                checks['crash_before_commit_remains_unknown'] = json.loads(metadata.read_text())['_configuration']['status']=='applying' and read()['snapshot']==original
                for kind in ['apply_execution','resume','send']:
                    try:
                        params={'execution_change':ref} if kind=='apply_execution' else {'text':'MUST_NOT_SEND'}
                        f.command('reject-'+kind,kind,session_id=sid,**params)
                        checks['unknown_blocks_'+kind]=False
                    except HTTPError as error: checks['unknown_blocks_'+kind]=error.code==409
            checks['no_replayed_application'] = len((faults/'applications.jsonl').read_text().splitlines())==1
            checks['fault_recovery_does_not_send_model_input'] = not f.provider.requests
            checks['original_native_id_preserved'] = f.session(sid)['scope']['native_id']==native
        result={'case':case,'selection':'resource_upgrade' if resources else 'inherit' if release else 'toggle','checks':checks,'expectations_met':all(checks.values()),'native_id':native}
        write(f.root/'result.json',result)
        print(json.dumps(result,ensure_ascii=False),flush=True)
        assert result['expectations_met'],checks


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--case',choices=['drop','crash','rollback'],required=True)
    modes=parser.add_mutually_exclusive_group()
    modes.add_argument('--resources',action='store_true')
    modes.add_argument('--release',action='store_true')
    args=parser.parse_args()
    if args.release and args.case=='rollback': parser.error('inherit does not introduce a failing candidate connection')
    run(args.case,args.resources,args.release)
