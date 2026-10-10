#!/usr/bin/env python3
import json
from pathlib import Path
import subprocess
from claude_mcp_selection_smoke import MCPFixture
from console_fixture import stop
from execution_config_probe import write


def main():
    f=MCPFixture(str(Path('.dev/bin/netcap').resolve()))
    proc=None
    try:
        print('Evidence directory:',f.root,flush=True)
        profile=f.profiles['claude-a']['instance']
        definition=json.loads((f.root/'shared-mcp/.mcp.json').read_text())['mcpServers']['identity']
        definition['args']=[str(f.root/'shared-mcp/server.py')]
        write(f.root/'restart-input.json',{'cwd':str(f.root/'claude-a/work'),'settings':profile['settings_file'],'endpoint':f.provider.url,'servers':{'identity':definition},'output':str(f.root/'restart-result.json')})
        proc=subprocess.Popen(['node',str(Path('tests/claude_mcp_restart_probe.mjs').resolve()),str(f.root/'restart-input.json')],env=f.native_envs['claude-a'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
        _,stderr=proc.communicate(timeout=90)
        (f.root/'restart-stderr.log').write_text(stderr)
        assert proc.returncode==0,'native restart probe failed'
        result=json.loads((f.root/'restart-result.json').read_text())
        result['calls']=f.calls('A')
        result['model_auth_matches']=all(r['auth_matches'] for r in f.provider.requests)
        write(f.root/'result.json',result)
        print(json.dumps(result),flush=True)
    finally:
        stop(proc);f.close()


if __name__=='__main__':main()
