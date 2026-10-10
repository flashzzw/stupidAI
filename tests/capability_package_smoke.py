#!/usr/bin/env python3
"""Portable capability ZIPs across two isolated production consoles.

Native sessions validate independent references and immutable history only.
Skill/MCP/plugin loading remains the responsibility of selection suites.
"""
import argparse
import base64
import copy
import io
import json
import os
from pathlib import Path
import signal
import time
from urllib.error import HTTPError
from urllib.request import Request, urlopen
import zipfile

from capability_library_smoke import fixture, change
from execution_profiles_smoke import wait_for


def catalog(f, id):
    return next(x for x in f.api('/api/capabilities') if x['asset']['id']==id)


def export(f,id,version):
    item=catalog(f,id)
    request=dict(asset_id=id,revision=item['revision'],version=version)
    preview=f.api('/api/capabilities/package/export/preview',request)
    assert preview['diagnosis']['portable'],preview['diagnosis']
    request['preview_hash']=preview['preview_hash']
    token=Path(f.info['token_file']).read_text().strip()
    with urlopen(Request(f.info['url']+'/api/capabilities/package/export',data=json.dumps(request).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'}),timeout=30) as response:
        assert response.headers['Content-Type']=='application/zip'
        data=response.read()
    target=f.root/f'{id}-{version}.zip';target.write_bytes(data)
    return data


def import_package(f,id,data,action='register',key=None):
    r=dict(archive=base64.b64encode(data).decode(),asset_id=id,action=action,base_revision=catalog(f,id)['revision'] if action=='update' else '')
    preview=f.api('/api/capabilities/package/import/preview',r)
    r['preview_hash']=preview['preview_hash']
    command=dict(command_id=key or 'package-'+id+'-'+preview['asset']['version'],kind='import_capability_package',capability_package=r)
    receipt=f.api('/api/capabilities/package/import',command)
    assert f.api('/api/capabilities/package/import',command)==receipt
    return preview,command


def add_samples(f):
    assets=[f.assets['v1']]
    mcp=f.root/'shared-mcp';mcp.mkdir();(mcp/'scripts').mkdir()
    (mcp/'.mcp.json').write_text(json.dumps({'mcpServers':{'fixture':{'command':'python3','args':['./scripts/server.py'],'env':{'API_KEY':'${CAPABILITY_TEST_KEY}'}}}}))
    (mcp/'scripts/server.py').write_text('print("MCP_SOURCE_MUST_NOT_AUTOLOAD")\n');(mcp/'scripts/server.py').chmod(0o700)
    assets.append(dict(id='shared-mcp',name='共享 MCP 定义',kind='mcp',version='v1',source=str(mcp),backends=['codex','claude'],management='managed'))
    for backend in ('codex','claude'):
        source=f.root/(backend+'-plugin');source.mkdir();(source/('.'+backend+'-plugin')).mkdir();(source/'skills/method').mkdir(parents=True);(source/'hooks').mkdir();(source/'scripts').mkdir()
        (source/('.'+backend+'-plugin/plugin.json')).write_text(json.dumps({'name':backend+'-fixture','mcpServers':'./extra.json'}))
        (source/'skills/method/SKILL.md').write_text('---\nname: plugin-method\ndescription: PLUGIN_SOURCE_MUST_NOT_AUTOLOAD\n---\n[Script](../../scripts/tool.py)\n')
        (source/'scripts/tool.py').write_text('print("LOCAL_PLUGIN_SCRIPT")\n');(source/'scripts/tool.py').chmod(0o700)
        (source/'.mcp.json').write_text(json.dumps({'mcpServers':{'duplicate':{'command':'python3','args':['./scripts/tool.py']}}}))
        (source/'extra.json').write_text(json.dumps({'mcpServers':{'duplicate':{'url':'https://example.test/mcp'}}}))
        (source/'hooks/hooks.json').write_text(json.dumps({'hooks':{'SessionStart':[{'hooks':[{'type':'command','command':'python3 ./'+'scripts/tool.py'}]}]}}))
        assets.append(dict(id=backend+'-plugin',name=backend+' 专属插件',kind='cli_plugin',version='v1',source=str(source),backends=[backend],management='managed'))
    for asset in assets:change(f,'sample-'+asset['id'],'register',asset['id'],asset)
    return assets


def run(source,dest):
    checks={};assets=add_samples(source);packages={}
    for asset in assets:
        id=asset['id'];packages[id]=export(source,id,'v1');preview,_=import_package(dest,id,packages[id])
        assert preview['diagnosis']['native_status']=='unverified'
        imported=catalog(dest,id);assert imported['asset']['source']!=catalog(source,id)['asset']['source']
        with zipfile.ZipFile(io.BytesIO(packages[id])) as z:
            manifest=json.loads(z.read('netcap-capability.json'))
            assert str(source.root) not in json.dumps(manifest) and 'session_id' not in json.dumps(manifest)
            for e in manifest['entries']:
                path=Path(imported['asset']['source'])/e['path']
                if e['kind']=='file':assert path.read_bytes()==z.read('resources/'+e['path'])
                assert path.stat().st_mode&0o777==e['mode']
        if asset['kind']=='cli_plugin':
            diag=preview['diagnosis'];assert {'plugin','skill','mcp','hook'} <= {c['kind'] for c in diag['components']}
            assert any(i['code']=='duplicate_mcp' for i in diag['issues']) and not any(c['independent_switch'] for c in diag['components'])
    checks['shared_skill_mcp_and_both_plugins_roundtrip_bytes_and_modes']=True
    checks['manifest_excludes_source_paths_sessions_and_runtime_binding']=True
    checks['plugin_components_duplicate_mcp_and_whole_plugin_boundary_visible']=True
    assert catalog(dest,'shared-mcp')['diagnosis']['required_environment']==['CAPABILITY_TEST_KEY']
    checks['mcp_environment_reference_preserved_without_runtime_value']=True
    sessions={};before={}
    for backend,id in (('codex','codex-a'),('claude','claude-b')):
        r=dest.command('create-'+id,'create',project_id='fixture',route_id=id+'-route',permission='read_only',capability_refs=['shared-method','shared-mcp',backend+'-plugin'])
        sid=r['session_id'];sessions[id]=sid;wait_for(lambda:dest.completed(r) and dest.session(sid)['status']=='idle','native references '+id,timeout=60)
        before[id]=dest.session(sid)['execution_snapshot']
    assert len({dest.session(sid)['scope']['state_namespace'] for sid in sessions.values()})==2
    assert all(len(s['capability_refs'])==3 for s in before.values())
    checks['both_native_backends_independently_reference_imported_shared_and_exclusive_assets']=True
    for p in dest.providers.values():p.stream_delay=5
    turns={id:dest.command('turn-'+id,'send',session_id=sid,text=f'NETCAP_{id[-1].upper()}_TURN_1') for id,sid in sessions.items()}
    wait_for(lambda:all(any(m['role']=='assistant' and m['status']=='inProgress' for m in dest.session(sid)['view']['messages']) for sid in sessions.values()),'two turns active',timeout=45)
    change(source,'new-version','update','shared-method',source.assets['v2'])
    v2=export(source,'shared-method','v2');preview,_=import_package(dest,'shared-method',v2,'update')
    assert len(preview['uses'])==2
    change(dest,'disable','disable','shared-method');change(dest,'remove','remove','shared-method')
    for id,r in turns.items():wait_for(lambda:dest.completed(r) and dest.session(sessions[id])['status']=='idle','original turn '+id,timeout=45)
    assert all(dest.session(sessions[id])['execution_snapshot']==snapshot for id,snapshot in before.items())
    checks['import_update_disable_remove_during_turns_preserve_old_snapshots']=True
    old=export(dest,'shared-method','v1');assert old==packages['shared-method']
    checks['removed_historical_version_exports_original_bytes']=True
    for p in dest.providers.values():p.stream_delay=0
    dest.restart_console()
    for id,sid in sessions.items():
        r=dest.command('resume-'+id,'resume',session_id=sid);wait_for(lambda:dest.completed(r) and not dest.session(sid)['paused'],'resume '+id,timeout=60)
        r=dest.command('continued-'+id,'send',session_id=sid,text=f'NETCAP_{id[-1].upper()}_TURN_2');wait_for(lambda:dest.completed(r) and dest.session(sid)['status']=='idle','next turn '+id,timeout=60)
        assert dest.session(sid)['execution_snapshot']==before[id]
    checks['restart_and_native_next_turn_retain_original_id_and_pinned_versions']=True
    for file in ('auth.json','credential.py'):
        unsafe=copy.deepcopy(source.assets['v1']);unsafe.update(id='unsafe-'+file.replace('.','-'),source=str(source.root/('unsafe-'+file)),management='')
        path=Path(unsafe['source']);path.mkdir();(path/'SKILL.md').write_text('---\nname: unsafe\ndescription: fixture\n---\n');(path/file).write_text('secret = "PACKAGE_MUST_NOT_LEAK"')
        change(source,unsafe['id'],'register',unsafe['id'],unsafe)
        item=catalog(source,unsafe['id']);r=dict(asset_id=unsafe['id'],revision=item['revision'],version='v1')
        p=source.api('/api/capabilities/package/export/preview',r);assert not p['diagnosis']['portable'] and 'PACKAGE_MUST_NOT_LEAK' not in json.dumps(p)
        r['preview_hash']=p['preview_hash']
        try:source.api('/api/capabilities/package/export',r)
        except HTTPError as e:assert e.code==409 and 'PACKAGE_MUST_NOT_LEAK' not in e.read().decode()
        else:raise AssertionError('unsafe export accepted')
    checks['literal_credentials_and_native_account_state_refused_without_disclosure']=True
    for p in dest.providers.values():
        assert all(r['auth_matches'] for r in p.requests)
        assert all('MUST_NOT_AUTOLOAD' not in json.dumps(r['body']) for r in p.requests)
        assert not p.errors
    checks['passive_references_do_not_install_or_execute_bundled_components']=True
    result=dict(checks=checks,expectations_met=all(checks.values()),sessions=sessions,scope='production HTTP ZIPs, two isolated states, real CLI references and history; local model providers; no native capability loading claim')
    (dest.root/'package-result.json').write_text(json.dumps(result,indent=2));(dest.root/'package-library.json').write_text(json.dumps(dest.api('/api/capabilities'),indent=2))
    print('PASS',len(checks),'package production checks',flush=True)
    return dict(url=dest.info['url'],token_file=dest.info['token_file'],root=str(dest.root),source_root=str(source.root),packages={id:str(source.root/f'{id}-v1.zip') for id in packages})


def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--netcap',default='.dev/bin/netcap');parser.add_argument('--hold',action='store_true');parser.add_argument('--info');args=parser.parse_args()
    with fixture(str(Path(args.netcap).resolve())) as source,fixture(str(Path(args.netcap).resolve())) as dest:
        print('Evidence directories:',source.root,dest.root,flush=True)
        info=run(source,dest);info['harness_pid']=os.getpid()
        if args.info:Path(args.info).write_text(json.dumps(info))
        if args.hold:
            finished=False
            def finish(*_):
                nonlocal finished;finished=True
            signal.signal(signal.SIGTERM,finish);signal.signal(signal.SIGINT,finish)
            print('READY',info['url'],flush=True)
            while not finished:
                assert dest.console.poll() is None
                time.sleep(.1)


if __name__=='__main__':main()
