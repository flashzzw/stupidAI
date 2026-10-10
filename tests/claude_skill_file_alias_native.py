#!/usr/bin/env python3
"""Native Skill file identity boundaries; isolated CLI, unavailable local provider."""
import json
from pathlib import Path
import subprocess
import tempfile
from execution_config_probe import environment, verify_versions, write
from runtime.common import stop

root=Path(tempfile.mkdtemp(prefix='netcap-w0207-file-alias-'))
print('Evidence directory:',root,flush=True)
verify_versions(root,['claude'])
cases={}
for kind in ('natural','external'):
    for mode in ('enabled','disabled'):
        case=root/(kind+'-'+mode)
        case.mkdir()
        env=environment(case)
        env['CLAUDE_CONFIG_DIR']=str(case/'profile')
        cwd=case/'work';cwd.mkdir()
        write(case/'source/SKILL.md','---\nname: alias-method\ndescription: file identity fixture\n---\nPRIVATE_BODY\n')
        write(case/'request.json',{'root':str(case),'cwd':str(cwd),'kind':kind,'mode':mode})
        proc=subprocess.Popen(['node',str(Path(__file__).with_suffix('.mjs')),str(case/'request.json')],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
        try:
            stdout,stderr=proc.communicate(timeout=45)
            write(case/'stdout.log',stdout);write(case/'stderr.log',stderr)
        finally:
            stop(proc)
        result=json.loads((case/'result.json').read_text()) if (case/'result.json').exists() else {}
        cases[case.name]={'exit_code':proc.returncode,'checks':result.get('checks',{}),'passed':result.get('passed',False)}
result={'root':str(root),'cases':cases,'passed':all(c['exit_code']==0 and c['passed'] for c in cases.values()),'scope':'four isolated real Session/SDK/CLI cases; no listener or real provider'}
write(root/'result.json',result)
print(json.dumps(result,indent=2))
raise SystemExit(0 if result['passed'] else 1)
