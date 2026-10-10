#!/usr/bin/env python3
"""Production Skill delivery/restore with real CLI, no model or HTTP listener."""
from pathlib import Path
import json
import subprocess
import tempfile
from execution_config_probe import environment, verify_versions, write
from runtime.common import stop

root = Path(tempfile.mkdtemp(prefix='netcap-w0207-skill-resources-'))
print('Evidence directory:', root, flush=True)
verify_versions(root, ['claude'])
env = environment(root)
cwd = root / 'work'
cwd.mkdir()
write(root / 'shared/SKILL.md', '---\nname: external-method\ndescription: selected external method\n---\nPRIVATE_BODY\n')
write(root / 'single/SKILL.md', '---\nname: single-method\ndescription: single file method\n---\nPRIVATE_BODY\n')
write(cwd / '.claude/skills/project/SKILL.md', '---\nname: project-method\ndescription: inherited project method\n---\n')
write(root / 'request.json', {'root': str(root), 'cwd': str(cwd)})
proc = subprocess.Popen(['node', str(Path(__file__).with_suffix('.mjs')), str(root / 'request.json')], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
try:
    stdout, stderr = proc.communicate(timeout=90)
    write(root / 'stdout.log', stdout)
    write(root / 'stderr.log', stderr)
finally:
    stop(proc)
result = json.loads((root / 'result.json').read_text()) if (root / 'result.json').exists() else {}
print(json.dumps({'exit_code': proc.returncode, 'checks': result.get('checks')}, indent=2))
raise SystemExit(proc.returncode or int(not result or not all(result['checks'].values())))
