#!/usr/bin/env python3
"""Native helper invocation/trust, without a model prompt or listening server.

The unreachable loopback URL deliberately cannot prove authenticated requests.
Use claude_remote_mcp_smoke.py --headers-helper for the full protocol path.
"""
import json
from pathlib import Path
import shutil
import subprocess
import tempfile

from console_fixture import stop
from execution_config_probe import environment, write, verify_versions


def main():
    root=Path(tempfile.mkdtemp(prefix='netcap-claude-helper-'))
    print('Evidence directory:',root,flush=True)
    verify_versions(root,['claude'])
    script=root/'shared-helper.py'
    script.write_text('''import json,os
from pathlib import Path
p=os.environ['NETCAP_HELPER_PROFILE']
Path(os.environ['NETCAP_HELPER_MARKER']).write_text(json.dumps({'cwd':os.getcwd(),'profile':p,'config_dir':os.environ['CLAUDE_CONFIG_DIR'],'credential_present':'NETCAP_HELPER_KEY' in os.environ,'key_matches':os.environ.get('NETCAP_HELPER_KEY')=='fixture-'+p}))
print(json.dumps({'Authorization':'Bearer '+os.environ.get('NETCAP_HELPER_KEY','')}))
''')
    definitions={}
    for transport in ('http','sse'):
        path=root/(transport+'.json')
        write(path,{'mcpServers':{'identity':{'type':transport,'url':'http://127.0.0.1:1/'+transport,'headersHelper':'/usr/bin/python3 "$NETCAP_HELPER_SCRIPT"'}}})
        definitions[transport]=path
    cases=[]
    for transport in ('http','sse'):
        for letter in ('A','B'):
            for mode in ('enabled','disabled'):cases.append((transport,letter,mode,'dynamic','none',False))
        for trust in ('none','parent','exact'):cases.append((transport,'A','enabled','project',trust,False))
    cases.append(('http','A','enabled','dynamic','none',True))
    results={};checks={}
    for transport,letter,mode,source,trust,relative in cases:
        key='-'.join((transport,letter,mode,source,trust))+('-relative' if relative else '')
        folder=root/key;folder.mkdir();env=environment(folder);cwd=folder/'work';cwd.mkdir()
        # Give native trust a real project root; non-repo temp paths may key
        # workspace trust at /tmp rather than the supplied cwd.
        subprocess.run(['git','init','--quiet',str(cwd)],env=env,check=True)
        marker=folder/'invocation.json';settings=folder/'settings.json';definition=definitions[transport]
        write(settings,{'enableAllProjectMcpServers':True,'env':{'NETCAP_HELPER_SCRIPT':str(script),'NETCAP_HELPER_MARKER':str(marker),'NETCAP_HELPER_PROFILE':letter,'NETCAP_HELPER_KEY':'fixture-'+letter}})
        if source=='project':shutil.copyfile(definition,cwd/'.mcp.json')
        if trust!='none':
            trusted=cwd if trust=='exact' else cwd.parent
            write(Path(env['CLAUDE_CONFIG_DIR'])/'.claude.json',{'projects':{str(trusted):{'hasTrustDialogAccepted':True}}})
        if relative:
            shutil.copyfile(script,cwd/'project-helper.py');definition=folder/'relative.json'
            write(definition,{'mcpServers':{'identity':{'type':transport,'url':'http://127.0.0.1:1/http','headersHelper':'/usr/bin/python3 ./project-helper.py'}}})
        write(folder/'input.json',{'cwd':str(cwd),'settings':str(settings),'definition':str(definition),'marker':str(marker),'source':source,'mode':mode,'output':str(folder/'result.json')})
        proc=None
        try:
            proc=subprocess.Popen(['node',str(Path('tests/claude_mcp_helper_probe.mjs').resolve()),str(folder/'input.json')],env=env,cwd=cwd,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
            stdout,stderr=proc.communicate(timeout=30)
            (folder/'stderr.log').write_text(stderr)
            assert proc.returncode==0,'native probe failed; see isolated stderr'
            result=json.loads((folder/'result.json').read_text())
        finally:stop(proc)
        expected=mode=='enabled' and (source=='dynamic' or trust=='exact')
        checks[key+'_invocation']=result['helper_ran']==expected
        checks[key+'_source']=not result['statuses'] if mode=='disabled' else any(s['name']=='identity' and s['source']==source for s in result['statuses'])
        checks[key+'_no_model_input']=result['model_results']==0
        if source=='project':checks[key+'_trust_rule']=result['trust_warning']==(trust!='exact')
        if result['helper_ran']:
            invocation=json.loads(marker.read_text());result['invocation']=invocation
            checks[key+'_binding']=invocation=={'cwd':str(cwd),'profile':letter,'config_dir':env['CLAUDE_CONFIG_DIR'],'credential_present':source=='dynamic','key_matches':source=='dynamic'}
        results[key]=result
    result={'checks':checks,'expectations_met':all(checks.values()),'cases':results,'scope':'real CLI helper invocation and source trust only; endpoint unreachable; no authenticated request or model call proven'}
    write(root/'result.json',result);print(json.dumps(result,ensure_ascii=False),flush=True)
    assert result['expectations_met'],'native helper boundary mismatch'


if __name__=='__main__':main()
