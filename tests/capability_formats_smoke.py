#!/usr/bin/env python3
"""Production portable manifests and binary assets on isolated consoles."""
import argparse
import base64
import io
import json
import os
from pathlib import Path
import random
import signal
import time
import zipfile
from urllib.error import HTTPError

from capability_library_smoke import fixture,change
from capability_package_smoke import run as package_regression,export,import_package,catalog
from execution_profiles_smoke import wait_for

PNG=base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aL9sAAAAASUVORK5CYII=')

def sample(f):
    root=f.root/'portable-dual';root.mkdir()
    files={
      'plugin.json':{'name':'portable-dual','extensions':{'com.openai':{'hooks':'./hooks/selected.json','interface':{'logo':'./assets/icon.png'},'apps':'./.app.json'}}},
      '.codex-plugin/plugin.json':{'name':'ignored-overlay','hooks':'./hooks/missing-ignored.json','skills':'./missing-ignored-skills'},
      '.claude-plugin/plugin.json':{'name':'portable-dual','hooks':'./hooks/selected.json'},
      'mcp.json':{'mcpServers':{'codex-definition':{'type':'stdio','command':'python3','args':['./scripts/mcp.py'],'env':{'API_KEY':'${CAPABILITY_FORMAT_TOKEN}'}}}},
      '.mcp.json':{'mcpServers':{'claude-definition':{'command':'python3','args':['./scripts/mcp.py'],'env':{'API_KEY':'${CAPABILITY_FORMAT_TOKEN}'}}}},
      '.app.json':{'apps':{'fixture':{'id':'plugin_asdk_app_fixture'}}},
      'hooks/selected.json':{'hooks':{'SessionStart':[{'hooks':[{'type':'command','command':'python3 ./scripts/hook.py ${CLAUDE_PLUGIN_DATA}'}]}]}},
      'hooks/hooks.json':{'hooks':{'Stop':[{'hooks':[{'type':'command','command':'python3 ./scripts/hook.py ${CLAUDE_PLUGIN_DATA}'}]}]}},
      'skills/method/SKILL.md':'---\nname: portable-method\ndescription: FORMAT_MUST_NOT_AUTOLOAD\n---\n[Icon](../../assets/icon.png)\n',
      'scripts/mcp.py':'print("FORMAT_MUST_NOT_AUTOLOAD")\n',
      'scripts/hook.py':'raise RuntimeError("FORMAT_MUST_NOT_EXECUTE")\n',
      'scripts/module.wasm':b'\x00asm\x01\x00\x00\x00',
      'assets/icon.png':PNG,
      'assets/model-fixture.bin':random.Random(20).randbytes(1500000),
    }
    nested=io.BytesIO()
    with zipfile.ZipFile(nested,'w') as z:z.writestr('example.bin',b'\x00\x01\xff')
    files['assets/examples.zip']=nested.getvalue()
    for p,value in files.items():
        target=root/p;target.parent.mkdir(parents=True,exist_ok=True)
        if isinstance(value,bytes):target.write_bytes(value)
        else:target.write_text(value if isinstance(value,str) else json.dumps(value))
        target.chmod(0o700 if p.startswith('scripts/') else 0o600)
    return dict(id='portable-dual',name='通用插件与二进制资源',kind='cli_plugin',version='v1',source=str(root),backends=['codex','claude'],management='managed')

def run(source,dest):
    info=package_regression(source,dest)
    asset=sample(source);change(source,'portable-register','register',asset['id'],asset)
    data=export(source,asset['id'],'v1');assert len(data)>1<<20
    preview,_=import_package(dest,asset['id'],data)
    item=catalog(dest,asset['id']);diag=item['diagnosis'];checks={}
    assert {r['media_type'] for r in diag['resources']} >= {'image/png','application/wasm','application/zip','application/octet-stream'}
    assert any(r['inspection']=='members_scanned' for r in diag['resources'])
    checks['binary_and_nested_archive_types_visible']=True
    sources={s['backend']:s for s in diag['plugin_sources']}
    assert sources['codex']['identity']=='plugin.json' and sources['codex']['settings']=='plugin.json#extensions.com.openai'
    assert sources['claude']['identity']=='.claude-plugin/plugin.json'
    assert diag['required_environment']==['CAPABILITY_FORMAT_TOKEN']
    assert any(i['code']=='runtime_binding' and i['path'].startswith('hooks/') for i in diag['issues'])
    components=diag['components']
    assert {c['name'] for c in components if c['kind']=='mcp' and c['backend']=='codex'}=={'codex-definition'}
    assert {c['name'] for c in components if c['kind']=='mcp' and c['backend']=='claude'}=={'claude-definition'}
    assert {c['name'] for c in components if c['kind']=='hook' and c['backend']=='codex'}=={'SessionStart'}
    assert {c['name'] for c in components if c['kind']=='hook' and c['backend']=='claude'}=={'SessionStart','Stop'}
    checks['per_backend_canonical_sources_and_hook_rules_visible']=True
    for p in Path(asset['source']).rglob('*'):
        if p.is_file():
            imported=Path(item['asset']['source'])/p.relative_to(asset['source'])
            assert imported.read_bytes()==p.read_bytes() and imported.stat().st_mode&0o777==p.stat().st_mode&0o777
    checks['larger_than_one_mib_archive_roundtrip_bytes_and_permissions']=True
    sessions={};snapshots={}
    for id in ('codex-a','claude-b'):
        r=dest.command('format-create-'+id,'create',project_id='fixture',route_id=id+'-route',permission='read_only',capability_refs=[asset['id']])
        sid=r['session_id'];sessions[id]=sid;wait_for(lambda:dest.completed(r) and dest.session(sid)['status']=='idle','format session '+id,timeout=60)
        snapshots[id]=dest.session(sid)['execution_snapshot']
        r=dest.command('format-turn-'+id,'send',session_id=sid,text=f'NETCAP_{id[-1].upper()}_TURN_3')
        wait_for(lambda:dest.completed(r) and dest.session(sid)['status']=='idle','format turn '+id,timeout=60)
    checks['both_native_sessions_reference_large_binary_asset_independently']=len({dest.session(sid)['scope']['state_namespace'] for sid in sessions.values()})==2
    dest.restart_console()
    for id,sid in sessions.items():
        r=dest.command('format-resume-'+id,'resume',session_id=sid);wait_for(lambda:dest.completed(r) and not dest.session(sid)['paused'],'format resume '+id,timeout=60)
        assert dest.session(sid)['execution_snapshot']==snapshots[id]
    assert not any('FORMAT_MUST_NOT_AUTOLOAD' in json.dumps(r['body']) for p in dest.providers.values() for r in p.requests)
    checks['restart_preserves_sources_without_implicit_install_or_execution']=True
    # A renamed nested archive cannot carry native account state through the new binary path.
    bad=source.root/'nested-account';bad.mkdir();(bad/'SKILL.md').write_text('---\nname: fixture\ndescription: fixture\n---\n')
    with zipfile.ZipFile(bad/'assets.bin','w') as z:z.writestr('sessions/history.json','private fixture state')
    candidate=dict(id='nested-account',name='拒绝账号状态样本',kind='skill',version='v1',source=str(bad),backends=['codex'],management='')
    change(source,'bad-register','register',candidate['id'],candidate)
    item=catalog(source,candidate['id']);request=dict(asset_id=candidate['id'],revision=item['revision'],version='v1')
    p=source.api('/api/capabilities/package/export/preview',request);assert not p['diagnosis']['portable'];request['preview_hash']=p['preview_hash']
    try:source.api('/api/capabilities/package/export',request)
    except HTTPError as e:assert e.code==409 and 'private fixture state' not in e.read().decode()
    else:raise AssertionError('nested native state exported')
    checks['renamed_nested_archive_account_state_rejected']=True
    result=dict(checks=checks,expectations_met=all(checks.values()),archive_bytes=len(data),sessions=sessions,scope='production HTTP, binary resource bytes, per-backend static diagnosis, real native passive references and restart; local model providers')
    (dest.root/'formats-result.json').write_text(json.dumps(result,indent=2));(dest.root/'formats-library.json').write_text(json.dumps(dest.api('/api/capabilities'),indent=2))
    info.update(root=str(dest.root),source_root=str(source.root),url=dest.info['url'],token_file=dest.info['token_file']);info['packages'][asset['id']]=str(source.root/(asset['id']+'-v1.zip'))
    print('PASS',len(checks),'formats production checks',flush=True)
    return info

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--netcap',default='.dev/bin/netcap');parser.add_argument('--hold',action='store_true');parser.add_argument('--info');args=parser.parse_args()
    with fixture(str(Path(args.netcap).resolve())) as source,fixture(str(Path(args.netcap).resolve())) as dest:
        print('Evidence directories:',source.root,dest.root,flush=True);info=run(source,dest);info['harness_pid']=os.getpid()
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
