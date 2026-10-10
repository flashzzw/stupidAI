#!/usr/bin/env python3
"""Capability registry/fixed versions through production HTTP and native sessions.

References are passive here. Native loading is intentionally tested separately
by the CLI-specific selection suites.
"""
import argparse
import copy
import json
import os
from pathlib import Path
import signal
import time
from urllib.error import HTTPError

from execution_profiles_smoke import ProfilesFixture, wait_for, rejected


def asset_source(root,name,version):
    source=root/name;source.mkdir()
    (source/'SKILL.md').write_text(f'---\nname: shared-method\ndescription: CAPABILITY_MUST_NOT_AUTOLOAD_{version}\n---\nRead helper.py when explicitly loaded.\n')
    (source/'helper.py').write_text(f'print("CAPABILITY_SOURCE_{version}")\n');(source/'helper.py').chmod(0o700)
    return source


def fixture(binary):
    f=ProfilesFixture(binary)
    f.assets={}
    for version in ('v1','v2'):
        source=asset_source(f.root,'asset-'+version,version)
        f.assets[version]=dict(id='shared-method',name='共享工作方法',kind='skill',version=version,source=str(source),backends=['codex','claude'],management='managed')
    f.config['instances']=[f.profiles[id]['instance'] for id in ('codex-a','claude-b')]
    f.config['routes']=[f.profiles[id]['route'] for id in ('codex-a','claude-b')]
    return f


def change(f,key,action,id,asset=None):
    current=next((x for x in f.api('/api/capabilities') if x['asset']['id']==id),None)
    request=dict(action=action,asset_id=id,base_revision=current['revision'] if current else '')
    if asset is not None:request['asset']=asset
    preview=f.api('/api/capabilities/preview',request)
    request=preview['request'];request['preview_hash']=preview['preview_hash']
    result=f.command(key,'manage_capability',capability_change=request)
    assert f.command(key,'manage_capability',capability_change=request)==result
    return preview,result


def run(f):
    checks={};sessions={};snapshots={}
    p,r=change(f,'import-v1','register','shared-method',f.assets['v1'])
    checks['preview_register_and_retry_without_native_execution']=not f.snapshot()['sessions'] and not any(p.requests for p in f.providers.values())
    current=f.api('/api/capabilities')[0]
    managed_v1=Path(current['asset']['source'])
    assert managed_v1!=Path(f.assets['v1']['source']) and (managed_v1/'helper.py').read_text().strip()=='print("CAPABILITY_SOURCE_v1")'
    checks['managed_version_copies_scripts_and_preserves_original']=True
    for id in ('codex-a','claude-b'):
        r=f.command('create-'+id,'create',project_id='fixture',route_id=id+'-route',permission='read_only',capability_refs=['shared-method'])
        sid=r['session_id'];sessions[id]=sid
        wait_for(lambda:f.completed(r) and f.session(sid)['status']=='idle','native session '+id,timeout=60)
        snapshots[id]=f.session(sid)['execution_snapshot']
    checks['both_backends_reference_one_fixed_asset_independently']=len({f.session(sid)['scope']['state_namespace'] for sid in sessions.values()})==2 and all(s['capability_refs'][0]['source']['path']==str(managed_v1) for s in snapshots.values())
    for p in f.providers.values():p.stream_delay=5
    in_flight={id:f.command('first-'+id,'send',session_id=sid,text=f'NETCAP_{id[-1].upper()}_TURN_1') for id,sid in sessions.items()}
    wait_for(lambda:all(any(m['role']=='assistant' and m['status']=='inProgress' for m in f.session(sid)['view']['messages']) for sid in sessions.values()),'both native turns in flight',timeout=40)
    p,r=change(f,'update-v2','update','shared-method',f.assets['v2'])
    assert len(p['uses'])==2
    change(f,'disable-v2','disable','shared-method')
    change(f,'remove-v2','remove','shared-method')
    for id,r in in_flight.items():wait_for(lambda:f.completed(r) and f.session(sessions[id])['status']=='idle','original turn preserved '+id,timeout=40)
    for id,sid in sessions.items():assert f.session(sid)['execution_snapshot']==snapshots[id]
    checks['update_disable_remove_during_native_turns_preserve_old_snapshots']=True
    (Path(f.assets['v1']['source'])/'helper.py').write_text('changed original imported source\n')
    assert (managed_v1/'helper.py').read_text().strip()=='print("CAPABILITY_SOURCE_v1")'
    for id,sid in sessions.items():assert f.api(f'/api/sessions/{sid}/execution')['status']=='unchanged'
    checks['external_import_origin_change_does_not_mutate_fixed_source']=True
    removed=f.api('/api/capabilities')[0]
    assert removed['removed'] and len(removed['versions'])==2 and len(removed['uses'])==2
    assert not f.api('/api/workspace').get('capability_assets')
    checks['removed_entry_retains_both_versions_and_usage_history']=True
    rejected(f,dict(command_id='removed-create',kind='create',project_id='fixture',route_id='codex-a-route',permission='read_only',capability_refs=['shared-method']))
    checks['removed_asset_cannot_bind_new_session']=True
    for p in f.providers.values():p.stream_delay=0
    f.restart_console()
    for id,sid in sessions.items():
        r=f.command('resume-'+id,'resume',session_id=sid)
        wait_for(lambda:f.completed(r) and not f.session(sid)['paused'],'resume original '+id,timeout=60)
        r=f.command('second-'+id,'send',session_id=sid,text=f'NETCAP_{id[-1].upper()}_TURN_2')
        wait_for(lambda:f.completed(r) and f.session(sid)['status']=='idle','continue original '+id,timeout=60)
        assert f.session(sid)['execution_snapshot']==snapshots[id]
    checks['restart_and_next_native_turn_keep_old_version_and_id']=True
    external=copy.deepcopy(f.assets['v2']);external.update(id='external',management='',source=str(f.root/'external-source'))
    Path(external['source']).mkdir();(Path(external['source'])/'SKILL.md').write_text('external method\n');(Path(external['source'])/'auth.json').write_text('{"api_key":"LIBRARY_EXPORT_SECRET"}')
    change(f,'external','register','external',external)
    exported=f.api('/api/capabilities/export');encoded=json.dumps(exported)
    assert 'LIBRARY_EXPORT_SECRET' not in encoded and external['source'] not in encoded and 'session_id' not in encoded and len(exported['assets'])==1
    assert exported['assets'][0]['source_required']
    checks['catalog_export_excludes_files_credentials_paths_and_sessions']=True
    candidate=copy.deepcopy(external);candidate.update(id='unsafe-copy',management='managed')
    try:f.api('/api/capabilities/preview',dict(action='register',asset_id='unsafe-copy',base_revision='',asset=candidate))
    except HTTPError as e:assert e.code==409 and '账号' in json.load(e)['error']
    else:raise AssertionError('account state imported')
    checks['native_account_state_not_copied_as_managed_asset']=True
    for letter,p in f.providers.items():
        assert all(r['auth_matches'] and f'NETCAP_{"B" if letter=="A" else "A"}_TURN' not in json.dumps(r['body']) for r in p.requests)
        assert all('CAPABILITY_MUST_NOT_AUTOLOAD' not in json.dumps(r['body']) for r in p.requests)
        assert not p.errors
    checks['reference_does_not_install_assets_or_cross_model_credentials']=True
    assert all(checks.values())
    result=dict(checks=checks,expectations_met=True,sessions=sessions,scope='production catalog and references; fixed resources; real CLI/local model providers; native loading is not claimed')
    (f.root/'result.json').write_text(json.dumps(result,indent=2));(f.root/'snapshot.json').write_text(json.dumps(f.snapshot(),indent=2));(f.root/'library.json').write_text(json.dumps(f.api('/api/capabilities'),indent=2))
    print('PASS',len(checks),'capability library production checks',flush=True)


def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--netcap',default='.dev/bin/netcap');parser.add_argument('--hold',action='store_true');parser.add_argument('--info');args=parser.parse_args()
    with fixture(str(Path(args.netcap).resolve())) as f:
        print('Evidence directory:',f.root,flush=True)
        if not args.hold:run(f)
        else:
            info=dict(f.info,root=str(f.root),harness_pid=os.getpid(),assets=f.assets)
            if args.info:Path(args.info).write_text(json.dumps(info))
            finished=False
            def finish(*_):
                nonlocal finished;finished=True
            signal.signal(signal.SIGTERM,finish);signal.signal(signal.SIGINT,finish)
            print('READY',f.info['url'],flush=True)
            while not finished:
                assert f.console.poll() is None
                time.sleep(.1)


if __name__=='__main__':main()
