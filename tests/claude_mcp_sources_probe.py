#!/usr/bin/env python3
"""Native MCP source precedence, strict mode and restrictive policy startup."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile

from console_fixture import stop
from execution_config_probe import environment,write,verify_versions


def main():
    root=Path(tempfile.mkdtemp(prefix='netcap-claude-sources-'))
    print('Evidence directory:',root,flush=True);verify_versions(root,['claude'])
    server=root/'server.py'
    server.write_text('''import json,sys
from pathlib import Path
tag=sys.argv[1]
trace=Path(sys.argv[2]).with_name('startup.jsonl')
def record(kind):
 with trace.open('a') as f:f.write(json.dumps({'kind':kind,'origin':tag})+'\\n')
record('started')
for line in sys.stdin:
 r=json.loads(line)
 if 'id' not in r:continue
 method=r['method']
 record(method)
 if method=='initialize':out={'protocolVersion':r['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':tag,'version':'1'}}
 elif method=='tools/list':out={'tools':[{'name':'from_'+tag,'description':'Fixture origin','inputSchema':{'type':'object','properties':{}}}]}
 elif method=='tools/call':
  with Path(sys.argv[2]).open('a') as f:f.write(tag+'\\n')
  out={'content':[{'type':'text','text':tag}]}
 else:out={}
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':out}),flush=True)
record('eof')
''')
    cases=[
        dict(name='user',layers=['user']),
        dict(name='project_over_user',layers=['user','project']),
        dict(name='local_over_project',layers=['user','project','local']),
        dict(name='dynamic_over_all',layers=['user','project','local'],mode='enabled'),
        dict(name='disabled_with_inheritance',layers=['user','project','local'],mode='disabled'),
        dict(name='disabled_without_inheritance',layers=[],mode='disabled'),
        dict(name='strict_dynamic',layers=['user','project','local'],mode='enabled',strict=True),
        dict(name='strict_empty',layers=['user','project','local'],strict=True),
        dict(name='user_deny_dynamic',layers=['user','project'],mode='enabled',user={'deniedMcpServers':[{'serverName':'identity'}]}),
        dict(name='project_deny_flag_allow',layers=['user','project'],mode='enabled',project={'deniedMcpServers':[{'serverName':'identity'}]},flag={'allowedMcpServers':[{'serverName':'identity'}]}),
        dict(name='user_allowlist',layers=['user','project'],mode='enabled',user={'allowedMcpServers':[{'serverName':'only_user'}]}),
        dict(name='merged_allowlists',layers=['user','project'],mode='enabled',user={'allowedMcpServers':[{'serverName':'identity'}]},project={'allowedMcpServers':[{'serverName':'only_project'}]}),
        dict(name='host_managed_deny',layers=['user','project'],mode='enabled',managed={'deniedMcpServers':[{'serverName':'identity'}]}),
        dict(name='user_permission_deny_flag_ask',layers=['user','project'],mode='enabled',user={'permissions':{'deny':['mcp__identity__*']}},ask=True),
        dict(name='host_permission_deny_flag_ask',layers=['user','project'],mode='enabled',managed={'permissions':{'deny':['mcp__identity__*']}},ask=True),
        dict(name='plugin_with_dynamic',layers=['user','project'],mode='enabled',plugin=True),
    ]
    expected={
        'user':{'identity':'user','only_user':'user'},
        'project_over_user':{'identity':'project','only_project':'project','only_user':'user'},
        'local_over_project':{'identity':'local','only_local':'local','only_project':'project','only_user':'user'},
        'dynamic_over_all':{'identity':'dynamic','only_local':'local','only_project':'project','only_user':'user'},
        'disabled_with_inheritance':{'identity':'local','only_local':'local','only_project':'project','only_user':'user'},
        'disabled_without_inheritance':{},'strict_dynamic':{'identity':'dynamic'},'strict_empty':{},
        'user_deny_dynamic':{'only_project':'project','only_user':'user'},'project_deny_flag_allow':{},
        'user_allowlist':{'only_user':'user'},'merged_allowlists':{'identity':'dynamic','only_project':'project'},
        'host_managed_deny':{'only_project':'project','only_user':'user'},
        'user_permission_deny_flag_ask':{'identity':'dynamic','only_project':'project','only_user':'user'},
        'host_permission_deny_flag_ask':{'identity':'dynamic','only_project':'project','only_user':'user'},
        'plugin_with_dynamic':{'identity':'dynamic','only_project':'project','only_user':'user','plugin:source-fixture:identity':'plugin'},
    }
    results={};checks={};connections={}
    for case in cases:
        folder=root/case['name'];folder.mkdir();env=environment(folder);cwd=folder/'work';cwd.mkdir()
        subprocess.run(['git','init','--quiet',str(cwd)],env=env,check=True)
        def definition(tag):return {'command':sys.executable,'args':[str(server),tag,str(folder/'tool-calls.log')]}
        native={'projects':{str(cwd):{'hasTrustDialogAccepted':True}}}
        for layer in case['layers']:
            servers={'identity':definition(layer),'only_'+layer:definition(layer)}
            if layer=='user':native['mcpServers']=servers
            elif layer=='local':native['projects'][str(cwd)]['mcpServers']=servers
            else:write(cwd/'.mcp.json',{'mcpServers':servers})
        write(Path(env['CLAUDE_CONFIG_DIR'])/'.claude.json',native)
        for layer,path in [('user',Path(env['CLAUDE_CONFIG_DIR'])/'settings.json'),('project',cwd/'.claude/settings.json')]:write(path,case.get(layer,{}))
        settings=folder/'custom.json';write(settings,{'enableAllProjectMcpServers':True,**case.get('flag',{})})
        source=folder/'selection.json';write(source,{'mcpServers':{'identity':definition('dynamic')}})
        config={'cwd':str(cwd),'settings':str(settings),'output':str(folder/'result.json'),'strict':case.get('strict',False),'ask':case.get('ask',False),'selections':[]}
        if 'mode' in case:config['selections']=[{'asset_id':'shared','server':'identity','mode':case['mode'],'source_path':str(source)}]
        if 'managed' in case:config['managed']=case['managed']
        if case.get('plugin'):
            plugin=folder/'plugin';write(plugin/'.claude-plugin/plugin.json',{'name':'source-fixture','version':'1.0.0'})
            write(plugin/'.mcp.json',{'mcpServers':{'identity':definition('plugin')}})
            config['plugins']=[{'type':'local','path':str(plugin)}]
        write(folder/'input.json',config)
        proc=subprocess.Popen(['node',str(Path('tests/claude_mcp_sources_probe.mjs').resolve()),str(folder/'input.json')],env=env,cwd=cwd,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
        try:
            _,stderr=proc.communicate(timeout=30);(folder/'stderr.log').write_text(stderr)
            assert proc.returncode==0,'native source probe failed; see isolated stderr'
        finally:stop(proc)
        result=json.loads((folder/'result.json').read_text());result['tool_calls']=int((folder/'tool-calls.log').exists());results[case['name']]=result
        trace=folder/'startup.jsonl';result['process_events']=[json.loads(line) for line in trace.read_text().splitlines()] if trace.exists() else []
        name=case['name'];inventory=result['inventory'];servers=inventory['servers']
        checks[name+'_sources']=inventory['available'] and {s['name']:s['source'] for s in servers}==expected[name]
        checks[name+'_no_input']=result['model_results']==0 and result['tool_calls']==0
        if 'mode' in case:
            checks[name+'_diagnostic']=not result['verification_error'] if result['selection_matches'] else 'identity' in result['verification_error']
        if expected[name]:connections[name]=all(s['status']=='connected' and s['tools']==['from_'+expected[name][s['name']]] for s in servers) and len(servers)==len(expected[name])
    result={'checks':checks,'source_expectations_met':all(checks.values()),'connection_checks':connections,'connections_verified':all(connections.values()),'expectations_met':all(checks.values()) and all(connections.values()),'cases':results}
    write(root/'result.json',result);print(json.dumps(result,ensure_ascii=False),flush=True)
    return 0 if result['expectations_met'] else 1


if __name__=='__main__':raise SystemExit(main())
