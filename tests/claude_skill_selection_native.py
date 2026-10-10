#!/usr/bin/env python3
"""Native production Session Skill selection, without provider/model calls."""
from pathlib import Path
import json
import subprocess
import tempfile
from execution_config_probe import environment, verify_versions, write
from runtime.common import stop

root=Path(tempfile.mkdtemp(prefix='netcap-w0207-claude-selection-'))
print('Evidence directory:',root,flush=True)
verify_versions(root,['claude'])
env=environment(root)
cwd=root/'work';cwd.mkdir()
source=root/'shared/SKILL.md';write(source,'---\nname: displayed-method\ndescription: selected fixture method\n---\nPRIVATE_BODY\n')
entry=Path(env['CLAUDE_CONFIG_DIR'])/'skills/native-key';entry.parent.mkdir(parents=True);entry.symlink_to(source.parent)
write(cwd/'.claude/skills/project/SKILL.md','---\nname: project-method\ndescription: inherited project method\n---\n')
write(root/'request.json',{'root':str(root),'cwd':str(cwd),'source':str(source),'entry':str(entry/'SKILL.md')})
proc=subprocess.Popen(['node',str(Path(__file__).with_suffix('.mjs')),str(root/'request.json')],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
try:
    stdout,stderr=proc.communicate(timeout=50)
    write(root/'stdout.log',stdout);write(root/'stderr.log',stderr)
finally:
    stop(proc)
result=json.loads((root/'result.json').read_text()) if (root/'result.json').exists() else {}
print(json.dumps({'exit_code':proc.returncode,'checks':result.get('checks')},indent=2))
raise SystemExit(proc.returncode or int(not result or not all(result['checks'].values())))
