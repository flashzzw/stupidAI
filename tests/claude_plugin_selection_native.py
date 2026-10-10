#!/usr/bin/env python3
"""Production Session plugin loading/restore without model input or MCP servers."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
from claude_plugin_contract_probe import plugin
from execution_config_probe import environment, verify_versions, write
from runtime.common import stop

root=Path(tempfile.mkdtemp(prefix='netcap-w0208-plugin-selection-'))
print('Evidence directory:',root,flush=True)
verify_versions(root,['claude']);env=environment(root);cwd=root/'work';cwd.mkdir()
source=root/'plugin';plugin(source,'workbench-plugin')
write(cwd/'.claude/skills/project/SKILL.md','---\nname: project-method\ndescription: preserve inherited\n---\n')
files=[]
for p in sorted(source.rglob('*')):
    item={'path':str(p.relative_to(source)),'kind':'directory' if p.is_dir() else 'file','mode':p.stat().st_mode&0o777}
    if p.is_file():item['sha256']=hashlib.sha256(p.read_bytes()).hexdigest()
    files.append(item)
selection={'asset_id':'plugin','name':'workbench-plugin','version':'1.0.0','mode':'enabled','source_path':str(source),'source_hash':'a'*64,'root_mode':source.stat().st_mode&0o777,'files':files,'components':[
    {'kind':'skill','name':'workbench-plugin-method','path':'skills/method/SKILL.md','native_name':'workbench-plugin:workbench-plugin-method','count':1},
    {'kind':'command','name':'action','path':'commands/action.md','native_name':'workbench-plugin:action','count':1},
    {'kind':'agent','name':'check','path':'agents/check.md','native_name':'workbench-plugin:workbench-plugin-checker','count':1},
    {'kind':'hook','name':'PreToolUse','path':'hooks/hooks.json','native_name':'PreToolUse','count':1}]}
write(root/'request.json',{'root':str(root),'cwd':str(cwd),'selection':selection})
process=subprocess.Popen(['node',str(Path(__file__).with_suffix('.mjs')),str(root/'request.json')],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
try:
    stdout,stderr=process.communicate(timeout=90);write(root/'stdout.log',stdout);write(root/'stderr.log',stderr)
finally:stop(process)
result=json.loads((root/'result.json').read_text()) if (root/'result.json').exists() else {}
print(json.dumps({'exit_code':process.returncode,'checks':result.get('checks')},indent=2))
raise SystemExit(process.returncode or int(not result or not all(result['checks'].values())))
