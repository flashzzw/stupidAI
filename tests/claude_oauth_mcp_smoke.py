#!/usr/bin/env python3
"""Production console with native OAuth storage, refresh, revocation and recovery."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import signal
import threading
import time
from urllib.error import HTTPError

from claude_mcp_selection_smoke import MCPFixture
from claude_mcp_oauth_probe import login
from console_fixture import wait_for
from execution_config_probe import write, verify_versions
from oauth_mcp_fixture import OAuthMCP


class OAuthFixture(MCPFixture):
    def __init__(self,binary,transport='http'):
        super().__init__(binary)
        self.oauth=OAuthMCP(self.root,transport);self.oauth.ttl=3600
        self.definition=self.root/'shared-mcp/.mcp.json'
        write(self.definition,{'mcpServers':{'identity':{'type':transport,'url':self.oauth.endpoint,'headers':{'X-Netcap-Workspace':'${NETCAP_MCP_WORKSPACE}'}}}})
        for id,profile in self.profiles.items():
            settings=Path(profile['instance']['settings_file']);data=json.loads(settings.read_text())
            data['env']['NETCAP_MCP_WORKSPACE']=str(self.root/id/'work');write(settings,data)
            sidecar=self.root/id/'oauth-sidecar';sidecar.mkdir();write(sidecar/'.mcp.json',json.loads(self.definition.read_text()))
    def authenticate(self,letter,plan=None):
        id='claude-'+letter.lower();profile=self.profiles[id]['instance']
        result=login(self.root/id,self.native_envs[id],self.root/id/'oauth-sidecar',Path(profile['settings_file']),self.definition,self.oauth,letter,terminal=True,command=plan['command'] if plan else None)
        assert result['exit_code']==0,'native login failed; see sanitized fixture output'
    def calls(self,letter):
        with self.oauth.lock:return [dict(c) for c in self.oauth.calls if c['profile']==letter]
    def expected_identity(self,letter):return {'profile':letter,'workspace':str(self.root/('claude-'+letter.lower())/'work'),'oauth':True,'transport':self.oauth.transport}
    def expire(self,letter):
        with self.oauth.lock:self.oauth.tokens={k:(v[0],0 if v[0]==letter else v[1]) for k,v in self.oauth.tokens.items()}
    def close(self):
        super().close();self.oauth.close()


def run(f,recover_without_restart=False):
    checks={}
    def check(name,value):checks[name]=bool(value);assert value,name
    def operation(receipt):return next(o for o in f.snapshot()['operations'] if o['id']==receipt['operation_id'])
    def command(key,kind,**params):
        def attempt():
            try:return f.command(key,kind,**params)
            except HTTPError as e:
                body=json.load(e)
                if e.code==409 and '正在核对' in body.get('error',''):return None
                raise RuntimeError(body) from e
        return wait_for(attempt,kind+' admission',timeout=20)
    def no_auth(key,letter):
        before=sum(len(p.requests) for p in f.providers.values())
        sid=f.create_selected(key,letter,success=False)
        op=next(o for o in f.snapshot()['operations'] if o['session_id']==sid and o['intent']['kind']=='create')
        check(key+'_blocks_before_input',op['status']=='not_sent' and sum(len(p.requests) for p in f.providers.values())==before and not f.calls(letter))
        check(key+'_explains_oauth', 'OAuth' in op.get('error','') and 'identity' in op.get('error',''))
        return sid
    def prepare(letter,sid,key):
        before=f.session(sid);events=list(f.oauth.events);requests=sum(len(p.requests) for p in f.providers.values())
        request={'snapshot_id':before['execution_snapshot']['id'],'server':'identity'}
        receipt=command(key,'prepare_mcp_login',session_id=sid,mcp_login=request)
        check(key+'_idempotent',f.command(key,'prepare_mcp_login',session_id=sid,mcp_login=request)==receipt)
        plan=receipt['mcp_login']
        check(key+'_no_native_side_effect',before==f.session(sid) and f.oauth.events==events and sum(len(p.requests) for p in f.providers.values())==requests)
        check(key+'_isolated_and_single_definition',Path(plan['directory']).is_relative_to(f.root/'console/mcp-login') and set(json.loads((Path(plan['directory'])/'.mcp.json').read_text())['mcpServers'])=={'identity'})
        check(key+'_correct_profile_and_snapshot',plan['profile']==f.profiles['claude-'+letter.lower()]['instance']['profile'] and plan['snapshot_id']==request['snapshot_id'])
        return plan
    failed=no_auth('missing-A','A')
    f.authenticate('A',prepare('A',failed,'prepare-A'))
    failed_b=no_auth('B-not-authenticated-by-A','B')
    f.authenticate('B',prepare('B',failed_b,'prepare-B'))
    sessions={letter:f.create_selected('authenticated-'+letter,letter) for letter in ('A','B')}
    native={letter:f.session(sid)['scope']['native_id'] for letter,sid in sessions.items()}
    snapshots={letter:f.api(f'/api/sessions/{sid}/execution')['snapshot'] for letter,sid in sessions.items()}
    def call(letter,turn,allow=True):
        sid=sessions[letter];count=len(f.calls(letter));key=letter+str(turn)
        sent=command(key+'send','send',session_id=sid,text=f'NETCAP_{letter}_TURN_{turn}')
        pending=f.pending(sid)
        check(key+'_waits_for_permission',len(f.calls(letter))==count)
        answer=command(key+'answer','respond',session_id=sid,pending_id=pending['id'],option_id='allow' if allow else 'deny')
        check(key+'_duplicate_reply_idempotent',f.command(key+'answer','respond',session_id=sid,pending_id=pending['id'],option_id='allow' if allow else 'deny')==answer)
        f.idle(sid)
        check(key+'_authenticated_call_count',len(f.calls(letter))==count+int(allow))
        check(key+'_version_and_id',f.session(sid)['scope']['native_id']==native[letter] and operation(sent)['execution_snapshot_id']==snapshots[letter]['id'])
        if allow:check(key+'_profile_binding',f.calls(letter)[-1]==f.expected_identity(letter))
    barrier=threading.Barrier(2)
    for p in f.providers.values():p.barrier=barrier
    with ThreadPoolExecutor(max_workers=2) as pool:list(pool.map(lambda letter:call(letter,1),('A','B')))
    check('both_oauth_profiles_have_overlapping_turns',all(len(p.overlap)==1 for p in f.providers.values()))
    for p in f.providers.values():p.barrier=None
    f.expire('A');f.expire('B');f.restart_console(crash=True)
    for letter,sid in sessions.items():
        resumed=command('resume-'+letter,'resume',session_id=sid)
        wait_for(lambda:f.completed(resumed) and f.session(sid)['status']=='idle','original OAuth session restored',timeout=60)
        call(letter,2)
    check('refresh_for_each_native_profile',all(any(e['kind']=='refresh_token' and e['profile']==p for e in f.oauth.events) for p in ('A','B')))
    for letter in ('A','B'):call(letter,3,allow=False)
    f.oauth.reject_refresh=True;f.expire('A');f.restart_console(crash=True)
    resumed=command('resume-revoked','resume',session_id=sessions['A'])
    wait_for(lambda:f.completed(resumed),'revoked original session baseline',timeout=60)
    before=(len(f.provider.requests),len(f.calls('A')))
    rejected=command('revoked-input','send',session_id=sessions['A'],text='REVOKED_MUST_NOT_REACH_MODEL')
    wait_for(lambda:f.completed(rejected),'revoked token blocked',timeout=60)
    check('revocation_blocks_restore_input',operation(rejected)['status']=='not_sent' and before==(len(f.provider.requests),len(f.calls('A'))))
    check('revocation_explained', 'OAuth' in operation(rejected).get('error',''))
    check('revocation_keeps_original_identity',f.session(sessions['A'])['scope']['native_id']==native['A'])
    resumed=command('resume-unaffected-B','resume',session_id=sessions['B'])
    wait_for(lambda:f.completed(resumed) and f.session(sessions['B'])['status']=='idle','other profile remains available',timeout=60)
    call('B',4)
    check('other_profile_keeps_its_authorization',sum(e['kind']=='authorize' and e['profile']=='B' for e in f.oauth.events)==1)
    def last_query_pid(letter):
        journal=f.root/('console/runtimes/claude-'+letter.lower())/(native[letter]+'.jsonl')
        initialized=[json.loads(line) for line in journal.read_text().splitlines() if json.loads(line).get('kind')=='initialized']
        return initialized[-1]['pid']
    console_pid=f.console.pid
    sibling_pid=last_query_pid('B') if recover_without_restart else None
    f.oauth.reject_refresh=False;f.authenticate('A',prepare('A',sessions['A'],'prepare-reauth-A'))
    if not recover_without_restart:f.restart_console(crash=True)
    resumed=command('resume-reauthenticated','resume',session_id=sessions['A'])
    wait_for(lambda:f.completed(resumed) and f.session(sessions['A'])['status']=='idle','reauthenticated original session',timeout=60)
    call('A',4)
    if recover_without_restart:
        check('reauthentication_keeps_console_process',f.console.pid==console_pid and f.console.poll() is None)
        check('reauthentication_keeps_sibling_query',last_query_pid('B')==sibling_pid)
        call('B',5)
    check('prepared_command_survives_restart',len(f.api(f'/api/sessions/{sessions["A"]}/execution').get('mcp_logins',[]))==1)
    serialized=json.dumps(f.snapshot())+(f.root/'console/runtimes/claude-a'/(native['A']+'.json')).read_text()
    check('workbench_records_have_no_oauth_tokens',all(token not in serialized for token in list(f.oauth.tokens)+list(f.oauth.refresh)))
    check('model_credentials_and_rules_retained',all(not p.errors and all(r['auth_matches'] for r in p.requests) for p in f.providers.values()))
    def returned(provider):
        out=[]
        for request in provider.requests:
            for message in request['body'].get('messages',[]):
                for block in message.get('content',[]) if isinstance(message.get('content'),list) else []:
                    if block.get('type')=='tool_result':
                        for part in block.get('content',[]) if isinstance(block.get('content'),list) else []:
                            if part.get('type')=='text':
                                try:out.append(json.loads(part['text']))
                                except ValueError:pass
        return out
    check('all_tool_results_return_to_own_model',all(f.expected_identity(letter) in returned(p) for letter,p in f.providers.items()))
    result={'transport':f.oauth.transport,'recover_without_restart':recover_without_restart,'checks':checks,'expectations_met':all(checks.values()),'native_ids':native,'auth_events':f.oauth.events,'calls':f.oauth.calls}
    write(f.root/'result.json',result);print(json.dumps(result,ensure_ascii=False),flush=True)


def main():
    parser=argparse.ArgumentParser();parser.add_argument('--hold',action='store_true');parser.add_argument('--info-file');parser.add_argument('--transport',choices=['http','sse'],default='http');parser.add_argument('--recover-without-restart',action='store_true');args=parser.parse_args()
    with OAuthFixture(str(Path('.dev/bin/netcap').resolve()),args.transport) as f:
        print('Evidence directory:',f.root,flush=True);verify_versions(f.root,['claude']);f.register()
        if not args.hold:run(f,args.recover_without_restart);return
        f.authenticate('A')
        if args.info_file:write(Path(args.info_file),{**f.info,'root':str(f.root),'harness_pid':os.getpid()})
        stopped=False
        def finish(*_):
            nonlocal stopped
            stopped=True
        signal.signal(signal.SIGTERM,finish);signal.signal(signal.SIGINT,finish)
        print('READY',f.info['url'],flush=True)
        while not stopped:
            assert f.console.poll() is None
            time.sleep(.2)


if __name__=='__main__':main()
