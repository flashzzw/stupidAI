#!/usr/bin/env python3
"""Four native execution configurations registered through production HTTP."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import copy
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import threading
import time
from urllib.error import HTTPError

from console_fixture import ConsoleFixture, Provider, CLI, SERVER_CLI, isolated_env, stop, wait_for


class ConcurrentProvider(Provider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.barrier = None
        self.overlap = []

    def gate(self, backend, payload):
        if self.barrier is not None:
            observation={'backend':backend, 'entered_at':time.time()}
            self.overlap.append(observation)
            self.barrier.wait(timeout=60)
            observation['released_at']=time.time()

    def responses(self, payload, answer, seq):
        self.gate('codex',payload)
        return super().responses(payload,answer,seq)

    def anthropic(self,payload,answer,seq):
        self.gate('claude',payload)
        return super().anthropic(payload,answer,seq)


class ProfilesFixture(ConsoleFixture):
    def __init__(self,binary):
        super().__init__(binary,ConcurrentProvider)
        self.providers={'A':self.provider,'B':ConcurrentProvider(self.root,'B')}
        self.profiles={};self.natives=[];self.logs=[];self.native_envs={}
        self.env.update(NETCAP_PROFILE_A='fixture-A',NETCAP_PROFILE_B='fixture-B',CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1',DISABLE_AUTOUPDATER='1',DISABLE_TELEMETRY='1',DISABLE_ERROR_REPORTING='1')
        for backend in ('codex','claude'):
            for letter in ('A','B'):
                id=f'{backend}-{letter.lower()}'
                root=self.root/id;root.mkdir()
                env=isolated_env(root);env.update({k:v for k,v in self.env.items() if k.startswith('NETCAP_PROFILE_') or k in ('CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC','DISABLE_AUTOUPDATER','DISABLE_TELEMETRY','DISABLE_ERROR_REPORTING')})
                self.native_envs[id]=env
                profile=Path(env['CODEX_HOME' if backend=='codex' else 'CLAUDE_CONFIG_DIR'])
                endpoint=self.providers[letter].url+('/v1' if backend=='codex' else '')
                instance=dict(id=id,name='同名开发配置',kind=backend,executable=CLI if backend=='codex' else '/home/zzw/.local/bin/claude',profile=str(profile),policy_revision='w0201-v1',env_allow=[f'NETCAP_PROFILE_{letter}'])
                if backend=='codex':
                    instance['socket']=str(root/'native.sock')
                    (profile/'config.toml').write_text(f'''model = "gpt-6-astra"
model_provider = "fixture"
approval_policy = "on-request"
sandbox_mode = "read-only"
[model_providers.fixture]
name = "Fixture"
base_url = "{endpoint}"
env_key = "NETCAP_PROFILE_{letter}"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = false
request_max_retries = 0
stream_max_retries = 0
[features]
apps = false
plugins = false
shell_snapshot = false
''')
                else:
                    settings=root/f'{id}-custom.json'
                    sender=root/'hook.py'
                    sender.write_text('import json,os,sys\nfrom pathlib import Path\nx=json.load(sys.stdin)\nwith Path('+repr(str(root/'hooks.jsonl'))+").open('a') as f:f.write(json.dumps({'session':x['session_id'],'marker':os.getenv('NETCAP_PROFILE_MARKER'),'state':os.getenv('CLAUDE_CONFIG_DIR')})+'\\n')\n")
                    settings.write_text(json.dumps({'env':{'NETCAP_PROFILE_MARKER':id},'hooks':{'SessionStart':[{'hooks':[{'type':'command','command':f'python3 {sender}'}]}]}}))
                    instance.update(settings_file=str(settings),bridge=str(Path('bridges/claude/dist/main.js').resolve()),node_executable=shutil.which('node'))
                    instance['env_allow']+=['CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC','DISABLE_AUTOUPDATER','DISABLE_TELEMETRY','DISABLE_ERROR_REPORTING']
                    (profile/'settings.json').write_text('{}')
                route=dict(id=id+'-route',instance_id=id,provider='fixture' if backend=='codex' else 'anthropic',endpoint=endpoint,model='gpt-6-astra' if backend=='codex' else 'claude-sonnet-4-6',credential_ref=f'env:NETCAP_PROFILE_{letter}',credential_revision='v1',config_revision='v1')
                self.profiles[id]={'instance':instance,'route':route}
        self.config['instances']=[];self.config['routes']=[]
        self.config['listen']='127.0.0.1:0'

    def start_native(self):
        for id,p in self.profiles.items():
            if p['instance']['kind']!='codex':continue
            log=(self.root/id/'native.log').open('a');self.logs.append(log)
            proc=subprocess.Popen([SERVER_CLI,'app-server','--listen','unix://'+p['instance']['socket']],env=self.native_envs[id],cwd=self.cwd,stdout=log,stderr=log,start_new_session=True)
            self.natives.append(proc)
            wait_for(lambda:Path(p['instance']['socket']).exists(),'owned '+id+' socket')

    def close(self):
        for p in getattr(self,'natives',[]):stop(p)
        for log in getattr(self,'logs',[]):log.close()
        if hasattr(self,'providers'):self.providers['B'].close()
        super().close()

    def completed(self,receipt):
        op=next(o for o in self.snapshot()['operations'] if o['id']==receipt['operation_id'])
        return op['status'] in ('succeeded','interrupted','failed','not_sent')


def rejected(f,body):
    try:f.api('/api/commands',body)
    except HTTPError as e:
        assert e.code==409
        return json.load(e)['error']
    raise AssertionError('expected explicit rejection')


def run(f):
    checks={}; sessions={}; receipts={}; originals={}
    for id,p in f.profiles.items():
        preview=f.api('/api/execution-profiles/preview',p)
        assert not preview['instance_status'].get('config_error'),preview
        assert not preview['route_status'].get('config_error'),preview
        assert not f.snapshot()['sessions']
        receipt=f.command('register-'+id,'register_execution',execution_profile=p)
        assert f.command('register-'+id,'register_execution',execution_profile=p)==receipt
        assert receipt['execution_profile_id']==id
    checks['four_profiles_registered_without_model_calls']=not any(p.requests for p in f.providers.values())
    assert len(f.snapshot()['instances'])==4
    for id in f.profiles:
        receipt=f.command('create-'+id,'create',project_id='fixture',route_id=id+'-route',title='同名会话',permission='read_only')
        sid=receipt['session_id'];sessions[id]=sid
        wait_for(lambda:f.session(sid)['status']=='idle' and f.completed(receipt),'created '+id,timeout=60)
        originals[id]=f.session(sid)
    checks['four_same_named_sessions_have_distinct_state_namespaces']=len({s['scope']['state_namespace'] for s in originals.values()})==4
    barrier=threading.Barrier(4)
    for p in f.providers.values():p.barrier=barrier
    def send(item,number):
        id,sid=item;letter=id[-1].upper()
        return id,f.command(f'send-{number}-{id}','send',session_id=sid,text=f'NETCAP_{letter}_TURN_{number} PROFILE_{id}')
    with ThreadPoolExecutor(max_workers=4) as pool:
        receipts=dict(pool.map(lambda item:send(item,1),sessions.items()))
    for id,r in receipts.items():wait_for(lambda:f.completed(r) and f.session(sessions[id])['status']=='idle','overlapping first turn '+id,timeout=90)
    for p in f.providers.values():
        p.barrier=None
        assert {x['backend'] for x in p.overlap}=={'codex','claude'},p.overlap
    checks['four_actual_model_requests_overlap_at_barrier']=all(len(p.overlap)==2 for p in f.providers.values())
    for id in ('claude-a','claude-b'):
        hooks=[json.loads(x) for x in (f.root/id/'hooks.jsonl').read_text().splitlines()]
        assert hooks and all(h['session']==originals[id]['scope']['native_id'] and h['marker']==id and h['state']==f.profiles[id]['instance']['profile'] for h in hooks)
    checks['custom_settings_and_state_directory_separately_effective']=True
    for p in f.providers.values():p.stream_delay=7
    with ThreadPoolExecutor(max_workers=4) as pool:receipts=dict(pool.map(lambda item:send(item,2),sessions.items()))
    wait_for(lambda:all(any(m['role']=='assistant' and m['status']=='inProgress' for m in f.session(sid)['view']['messages']) for sid in sessions.values()),'all four second turns streaming',timeout=40)
    stopped=f.command('stop-codex-a','interrupt',session_id=sessions['codex-a'])
    wait_for(lambda:f.completed(stopped),'one owned turn stopped',timeout=40)
    for id,r in receipts.items():wait_for(lambda:f.completed(r),'other turn completed '+id,timeout=40)
    statuses={id:next(o['status'] for o in f.snapshot()['operations'] if o['id']==r['operation_id']) for id,r in receipts.items()}
    assert statuses['codex-a']=='interrupted' and all(state=='succeeded' for id,state in statuses.items() if id!='codex-a'),statuses
    checks['stopping_one_does_not_interrupt_other_three']=True
    for p in f.providers.values():p.stream_delay=0
    resume=f.command('resume-codex-a','resume',session_id=sessions['codex-a'])
    wait_for(lambda:not f.session(sessions['codex-a'])['paused'],'resume stopped session')
    print('PASS first barrier and single interruption; restarting console',flush=True)
    restarted=time.time();f.restart_console()
    (f.root/'restart-snapshot.json').write_text(json.dumps(f.snapshot(),indent=2))
    wait_for(lambda:len(f.snapshot()['instances'])==4,'persistent execution registrations')
    from datetime import datetime
    def freshly_observed(sid):
        s=f.session(sid);e=s['view'].get('evidence',{})
        return s['connection']=='ready' and e.get('at') and datetime.fromisoformat(e['at'].replace('Z','+00:00')).timestamp()>restarted
    for id,sid in sessions.items():
        handoff=f.command('restart-handoff-'+id,'resume',session_id=sid)
        wait_for(lambda:f.completed(handoff) and not f.session(sid)['paused'],'fresh read-only handoff '+id,timeout=60)
        wait_for(lambda:freshly_observed(sid),'new native observation after restart '+id,timeout=60)
        assert f.session(sid)['scope']['native_id']==originals[id]['scope']['native_id']
    checks['registry_restart_restores_original_native_ids']=True
    with ThreadPoolExecutor(max_workers=4) as pool:receipts=dict(pool.map(lambda item:send(item,3),sessions.items()))
    for id,r in receipts.items():
        wait_for(lambda:f.completed(r) and f.session(sessions[id])['status']=='idle','original third turn '+id,timeout=60)
        assert f.session(sessions[id])['execution_snapshot']==originals[id]['execution_snapshot']
    checks['third_turn_uses_original_immutable_snapshot']=True
    for letter,provider in f.providers.items():
        for request in provider.requests:
            assert request['auth_matches']
            if 'count_tokens' in request['path']:continue
            body=json.dumps(request['body']);backend='codex' if 'responses' in request['path'] else 'claude'
            other_backend='claude' if backend=='codex' else 'codex'
            assert f'PROFILE_{other_backend}-' not in body
            assert f'NETCAP_{"B" if letter=="A" else "A"}_TURN' not in body
            assert request['body']['model']==f.profiles[f'{backend}-{letter.lower()}']['route']['model']
        assert not provider.errors,provider.errors
    checks['all_observed_endpoints_credentials_models_and_history_isolated']=True
    for id in ('codex-a','claude-a'):
        alias=copy.deepcopy(f.profiles[id]);aid=id+'-alias'
        link=f.root/aid;link.symlink_to(alias['instance']['profile'],target_is_directory=True)
        alias['instance'].update(id=aid,profile=str(link));alias['route'].update(id=aid+'-route',instance_id=aid)
        preview=f.api('/api/execution-profiles/preview',alias)
        assert preview['instance_status']['shared_state_with']==[id]
        f.command('register-'+aid,'register_execution',execution_profile=alias)
        statuses={i['id']:i for i in f.snapshot()['instances']}
        assert statuses[id]['shared_state_with']==[aid] and statuses[aid]['shared_state_with']==[id]
    checks['both_backend_symlink_aliases_mark_shared_in_production']=True
    alias_sessions={}
    for id in ('codex-a','claude-a'):
        aid=id+'-alias'
        created=f.command('create-'+aid,'create',project_id='fixture',route_id=aid+'-route',permission='read_only')
        sid=created['session_id']
        wait_for(lambda:f.completed(created),'create through symlink '+aid,timeout=60)
        sent=f.command('send-'+aid,'send',session_id=sid,text='ALIAS_ONLY_'+aid)
        wait_for(lambda:f.completed(sent) and f.session(sid)['status']=='idle','alias turn '+aid,timeout=60)
        alias_sessions[aid]=(sid,f.session(sid)['scope']['native_id'])
    f.restart_console()
    for aid,(sid,native) in alias_sessions.items():
        resumed=f.command('restore-'+aid,'resume',session_id=sid)
        wait_for(lambda:f.completed(resumed) and not f.session(sid)['paused'],'restore alias '+aid,timeout=60)
        assert f.session(sid)['scope']['native_id']==native
        sent=f.command('send-restored-'+aid,'send',session_id=sid,text='ALIAS_RESTORED_'+aid)
        wait_for(lambda:f.completed(sent) and f.session(sid)['status']=='idle','restored alias turn '+aid,timeout=60)
        assert f.session(sid)['scope']['state_namespace']==f.profiles[aid.removesuffix('-alias')]['instance']['profile']
    checks['symlink_bound_sessions_restore_original_ids_and_native_state']=True
    for aid,(sid,_) in alias_sessions.items():
        link=f.root/aid;link.unlink();other=aid.removesuffix('-a-alias')+'-b'
        link.symlink_to(f.profiles[other]['instance']['profile'],target_is_directory=True)
        before=sum(len(p.requests) for p in f.providers.values())
        rejected(f,dict(command_id='retarget-'+aid,kind='send',session_id=sid,text='MUST_NOT_SEND'))
        assert sum(len(p.requests) for p in f.providers.values())==before
    checks['retargeted_alias_cannot_send_into_another_native_state']=True
    collision=copy.deepcopy(f.profiles['claude-a']);collision['instance']['profile']=f.profiles['claude-b']['instance']['profile']
    reason=rejected(f,dict(command_id='conflicting-id',kind='register_execution',execution_profile=collision))
    assert 'ID 已登记' in reason
    checks['same_registered_id_cannot_rebind_native_state']=True
    missing=copy.deepcopy(f.profiles['claude-a']);missing['instance'].update(id='missing',settings_file=str(f.root/'missing.json'));missing['route'].update(id='missing-route',instance_id='missing')
    preview=f.api('/api/execution-profiles/preview',missing)
    assert 'settings' in preview['instance_status']['config_error']
    f.command('register-missing','register_execution',execution_profile=missing)
    before=sum(len(p.requests) for p in f.providers.values())
    reason=rejected(f,dict(command_id='create-missing',kind='create',project_id='fixture',route_id='missing-route',permission='read_only'))
    assert 'settings' in reason and sum(len(p.requests) for p in f.providers.values())==before
    checks['unavailable_registered_profile_explains_and_rejects_creation']=True
    assert all(checks.values()),checks
    result={'checks':checks,'sessions':{id:{'console_id':sid,'native_id':originals[id]['scope']['native_id'],'state_namespace':originals[id]['scope']['state_namespace']} for id,sid in sessions.items()},'overlap':{k:p.overlap for k,p in f.providers.items()},'limitations':['Complete inherited settings/permissions are W02.09. Native ID collision across separate state homes is tested separately.']}
    (f.root/'result.json').write_text(json.dumps(result,indent=2));(f.root/'snapshot.json').write_text(json.dumps(f.snapshot(),indent=2))
    print('PASS',len(checks),'production execution profile checks',flush=True)
    return sessions


def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--netcap',default='.dev/bin/netcap');parser.add_argument('--info');parser.add_argument('--hold',action='store_true');parser.add_argument('--empty',action='store_true');args=parser.parse_args()
    with ProfilesFixture(str(Path(args.netcap).resolve())) as f:
        print('Evidence directory:',f.root,flush=True)
        try:
            sessions={} if args.empty else run(f)
        except BaseException:
            (f.root/'failure-snapshot.json').write_text(json.dumps(f.snapshot(),indent=2))
            raise
        if args.info:Path(args.info).write_text(json.dumps(dict(f.info,root=str(f.root),harness_pid=os.getpid(),profiles=f.profiles,sessions=sessions)))
        if args.hold:
            stopped=False
            def finish(*_):
                nonlocal stopped;stopped=True
            signal.signal(signal.SIGTERM,finish);signal.signal(signal.SIGINT,finish)
            print('READY',f.info['url'],flush=True)
            while not stopped:
                assert f.console.poll() is None
                time.sleep(.1)


if __name__=='__main__':main()
