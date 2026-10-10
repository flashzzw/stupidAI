#!/usr/bin/env python3
"""Probe the fixed native CLI's headless MCP OAuth, using only loopback accounts."""
import json
import os
import pty
import fcntl
import struct
import termios
from pathlib import Path
import re
import select
import subprocess
import tempfile
import time
from urllib.error import HTTPError
from urllib.request import HTTPRedirectHandler, Request, build_opener

from console_fixture import stop
from execution_config_probe import environment, write, verify_versions
from oauth_mcp_fixture import OAuthMCP
from claude_mcp_selection_smoke import MCPProvider


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, *_): return None


def terminal_text(value):
    return re.sub(r'\x1b\[[0-9;?]*[a-zA-Z]','',re.sub(r'\x1b\][^\x07]*\x07','',value.decode(errors='replace')))


def login(root, env, cwd, settings, definition, server, profile, terminal=False, command=None):
    args = ['/home/zzw/.local/bin/claude','--mcp-config',str(definition),'--settings',str(settings),'mcp','login','identity','--no-browser']
    if command is not None: args=['bash','-c',command]
    master,slave = pty.openpty() if terminal else (None,None)
    if terminal: fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',24,4096,0,0))
    proc = subprocess.Popen(args, env=env, cwd=cwd, stdin=slave if terminal else subprocess.PIPE,stdout=slave if terminal else subprocess.PIPE,stderr=subprocess.STDOUT,start_new_session=True)
    if terminal: os.close(slave)
    reader=master if terminal else proc.stdout.fileno()
    output=b'';submitted=False;authorized=False
    try:
        deadline=time.monotonic()+30
        while time.monotonic()<deadline:
            if select.select([reader],[],[],.1)[0]:
                try: chunk=os.read(reader,65536)
                except OSError: chunk=b''
                output+=chunk
            clean=terminal_text(output)
            match=re.search(re.escape(server.url+'/authorize')+r'[^\s]+',clean)
            if match and not submitted:
                request=Request(match.group(0),headers={'X-Fixture-Account':profile})
                try: build_opener(NoRedirect).open(request,timeout=10)
                except HTTPError as response:
                    assert response.code==302
                    redirect=response.headers['Location']
                else: raise AssertionError('authorization did not redirect')
                authorized=True
                if terminal: os.write(master,(redirect+'\n').encode())
                else: proc.stdin.write((redirect+'\n').encode());proc.stdin.flush()
                submitted=True
            if proc.poll() is not None:
                if not terminal: output+=proc.stdout.read()
                break
        # Keep authorization URL and code out of report/log artifacts.
        clean=re.sub(r'https?://[^\s]+','<redacted-url>',terminal_text(output))
        (root/'login-output.txt').write_text(clean)
        return {'exit_code':proc.poll(),'authorization_completed':authorized,'redirect_submitted':submitted,'output':clean}
    finally:
        stop(proc)
        if terminal: os.close(master)


def main():
    root=Path(tempfile.mkdtemp(prefix='netcap-claude-oauth-'));print('Evidence directory:',root,flush=True)
    verify_versions(root,['claude'])
    server=OAuthMCP(root)
    providers=[]
    try:
        server.ttl=3600
        definition=root/'mcp.json';write(definition,{'mcpServers':{'identity':{'type':'http','url':server.url+'/mcp','headers':{'X-Netcap-Workspace':'${NETCAP_MCP_WORKSPACE}'}}}})
        cases={};checks={};phases={}
        for profile in ('A','B'):
            case=root/profile;case.mkdir();env=environment(case);cwd=case/'work';cwd.mkdir();settings=case/'settings.json'
            write(settings,{'enableAllProjectMcpServers':True,'env':{'NETCAP_MCP_WORKSPACE':str(cwd)}})
            sidecar=case/'auth-sidecar';sidecar.mkdir();write(sidecar/'.mcp.json',json.loads(definition.read_text()))
            provider=MCPProvider(case,profile);providers.append(provider)
            cases[profile]=(case,env,cwd,settings,sidecar,provider)
        def native(profile,key,status,turn=None,session=None):
            case,env,cwd,settings,sidecar,provider=cases[profile]
            config={'cwd':str(cwd),'settings':str(settings),'endpoint':provider.url,'profile':profile,'servers':json.loads(definition.read_text())['mcpServers'],
                'expected_status':status,'output':str(case/(key+'.json')),'turn':turn,'session':session,'resume':bool(session)}
            write(case/'input.json',config)
            proc=subprocess.Popen(['node',str(Path('tests/claude_mcp_oauth_probe.mjs').resolve()),str(case/'input.json')],env=env,cwd=cwd,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
            try:
                _,error=proc.communicate(timeout=50)
                (case/(key+'-stderr.txt')).write_bytes(error)
                assert proc.returncode==0,'native '+profile+'/'+key+' failed; see isolated stderr'
                phases[profile+'-'+key]=json.loads((case/(key+'.json')).read_text())
                return phases[profile+'-'+key]
            finally:stop(proc)
        native('A','before','needs-auth');native('B','before','needs-auth')
        case,env,cwd,settings,sidecar,_=cases['A']
        direct=login(case,env,cwd,settings,definition,server,'A')
        staged=login(case,env,sidecar,settings,definition,server,'A')
        tty=login(case,env,sidecar,settings,definition,server,'A',terminal=True)
        checks['headless_pty_login_succeeded']=tty['exit_code']==0
        first=native('A','called','connected',turn=1)
        native('B','still-unauthed','needs-auth')
        case,env,cwd,settings,sidecar,_=cases['B']
        b=login(case,env,sidecar,settings,definition,server,'B',terminal=True)
        checks['second_account_login_succeeded']=b['exit_code']==0
        second=native('B','called','connected',turn=1)
        with server.lock:
            server.tokens={key:(value[0],0) for key,value in server.tokens.items()}
        native('A','refreshed','connected',turn=2,session=first['session'])
        native('B','refreshed','connected',turn=2,session=second['session'])
        checks['refresh_used_both_profiles']=all(any(e['kind']=='refresh_token' and e['profile']==p for e in server.events) for p in ('A','B'))
        checks['four_authenticated_calls']=len(server.calls)==4
        checks['authenticated_account_and_workspace']=all(c=={'profile':p,'workspace':str(cases[p][2]),'oauth':True,'transport':'http'} for c,p in zip(server.calls,['A','B','A','B']))
        checks['same_id_after_refresh']=phases['A-refreshed']['session']==first['session'] and phases['B-refreshed']['session']==second['session']
        checks['single_call_approval']=all(v['permissions']==['mcp__identity__whoami'] for k,v in phases.items() if k.endswith(('called','refreshed')))
        checks['model_accounts_isolated']=all(p.requests and all(r['auth_matches'] for r in p.requests) and not p.errors for p in providers)
        case,env,cwd,settings,sidecar,_=cases['A']
        completed=subprocess.run(['/home/zzw/.local/bin/claude','--settings',str(settings),'mcp','logout','identity'],env=env,cwd=sidecar,capture_output=True,text=True,timeout=20)
        checks['native_logout_succeeded']=completed.returncode==0
        native('A','after-logout','needs-auth',session=first['session'])
        native('B','after-other-logout','connected',session=second['session'])
        checks['logout_does_not_clear_other_profile']=phases['A-after-logout']['inventory'][0]['status']=='needs-auth' and phases['B-after-other-logout']['inventory'][0]['status']=='connected'
        checks['native_credential_files_private']=all((Path(env['CLAUDE_CONFIG_DIR'])/'.credentials.json').stat().st_mode & 0o077 == 0 for _,env,_,_,_,_ in cases.values())
        result={'checks':checks,'expectations_met':all(checks.values()),'direct_dynamic':direct,'isolated_project_definition':staged,'pty_sidecar':tty,'phases':phases,'auth_events':server.events,'calls':server.calls}
        write(root/'result.json',result);print(json.dumps(result),flush=True)
        assert result['expectations_met']
    finally:
        for p in providers:p.close()
        server.close()


if __name__=='__main__':main()
