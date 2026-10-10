#!/usr/bin/env python3
"""Read fixed SDK MCP control behavior in an isolated native process."""
import json
from pathlib import Path
import subprocess
import tempfile
from mcp_binding_probe import ToolProvider
from execution_config_probe import environment,write,verify_versions
from console_fixture import stop

def main():
    root=Path(tempfile.mkdtemp(prefix='netcap-claude-mcp-control-'));print('Evidence directory:',root,flush=True)
    env=environment(root);verify_versions(root,['claude']);cwd=root/'work';cwd.mkdir()
    server=root/'server.py';write(server,'''import json,os,sys
from pathlib import Path
name=os.environ['MCP_NAME']
with Path(os.environ['MCP_LOG']).open('a') as f:f.write(json.dumps({'event':'start','name':name,'key_matches':os.getenv('MCP_KEY')=='fixture-mcp-A','cwd':os.getcwd()})+'\\n')
for line in sys.stdin:
 r=json.loads(line)
 if 'id' not in r:continue
 method=r['method']
 if method=='initialize':out={'protocolVersion':r['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':name,'version':'1'}}
 elif method=='tools/list':out={'tools':[{'name':'whoami' if name=='identity' else 'inherited_ping','description':'local fixture binding','inputSchema':{'type':'object','properties':{}}}]}
 elif method=='tools/call':
  out={'content':[{'type':'text','text':json.dumps({'name':name,'cwd':os.getcwd(),'key_matches':os.getenv('MCP_KEY')=='fixture-mcp-A'})}]}
  with Path(os.environ['MCP_LOG']).open('a') as f:f.write(json.dumps({'event':'call','name':name})+'\\n')
 else:out={}
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':out}),flush=True)
''')
    def definition(name):return {'command':'python3','args':[str(server)],'env':{'MCP_NAME':name,'MCP_LOG':str(root/'mcp.jsonl'),'MCP_KEY':'${NETCAP_MCP_KEY}'}}
    write(cwd/'.mcp.json',{'mcpServers':{'inherited':definition('inherited')}})
    write(cwd/'CLAUDE.md','MCP_PROJECT_RULE_MUST_REMAIN\n')
    settings=root/'custom.json';write(settings,{'env':{'NETCAP_MCP_KEY':'fixture-mcp-A'},'enableAllProjectMcpServers':True})
    provider=ToolProvider(root,'A')
    try:
        config={'cwd':str(cwd),'settings':str(settings),'servers':{'identity':definition('identity')},'endpoint':provider.url,'output':str(root/'result.json')};write(root/'input.json',config)
        before={str(p.relative_to(root)):p.read_bytes() for p in root.rglob('*.json') if p!=root/'input.json'}
        proc=subprocess.Popen(['node',str(Path('tests/claude_mcp_control_probe.mjs').resolve()),str(root/'input.json')],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
        try:
            _,stderr=proc.communicate(timeout=75)
            (root/'node.stderr').write_text(stderr)
            assert proc.returncode==0,'native control probe failed; see isolated stderr'
        finally:stop(proc)
        result=json.loads((root/'result.json').read_text());result['process_events']=[json.loads(x) for x in (root/'mcp.jsonl').read_text().splitlines()]
        result['config_files_changed']=[str(p.relative_to(root)) for p in root.rglob('*.json') if str(p.relative_to(root)) in before and p.read_bytes()!=before[str(p.relative_to(root))]]
        result['state_json_paths']=[str(p.relative_to(root)) for p in Path(env['CLAUDE_CONFIG_DIR']).rglob('*.json')]
        result['project_rule_preserved']=any('MCP_PROJECT_RULE_MUST_REMAIN' in json.dumps(r['body']) for r in provider.requests)
        result['provider_auth_matches']=all(r['auth_matches'] for r in provider.requests)
        write(root/'result.json',result);print(json.dumps(result),flush=True)
    finally:provider.close()

if __name__=='__main__':main()
