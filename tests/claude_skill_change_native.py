#!/usr/bin/env python3
"""Same-ID Skill configuration through the real production Session and CLI."""
from pathlib import Path
import json
import subprocess
import tempfile
from execution_config_probe import environment, verify_versions, write
from runtime.common import stop

root = Path(tempfile.mkdtemp(prefix='netcap-w0207-skill-change-'))
print('Evidence directory:', root, flush=True)
verify_versions(root, ['claude'])
env = environment(root)
cwd = root / 'work'
cwd.mkdir()
source = root / 'natural/SKILL.md'
write(source, '---\nname: natural-method\ndescription: natural fixture\n---\nPRIVATE_METHOD_BODY\n')
entry = Path(env['CLAUDE_CONFIG_DIR']) / 'skills/natural-key'
entry.parent.mkdir(parents=True)
entry.symlink_to(source.parent)
write(cwd / '.claude/skills/inherited/SKILL.md', '---\nname: inherited-method\ndescription: keep inherited\n---\n')
for version in ('v1', 'v2'):
    write(root / version / 'SKILL.md', f'---\nname: external-method\ndescription: external {version} fixture\n---\nPRIVATE_{version}_BODY\n')
write(root / 'collision/SKILL.md', '---\nname: inherited-method\ndescription: collision fixture\n---\n')
write(root / 'request.json', {'root': str(root), 'cwd': str(cwd), 'source': str(source), 'entry': str(entry / 'SKILL.md')})
proc = subprocess.Popen(['node', str(Path(__file__).with_suffix('.mjs')), str(root / 'request.json')], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
try:
    stdout, stderr = proc.communicate(timeout=120)
    write(root / 'stdout.log', stdout)
    write(root / 'stderr.log', stderr)
finally:
    stop(proc)
result = json.loads((root / 'result.json').read_text()) if (root / 'result.json').exists() else {}
print(json.dumps({'exit_code': proc.returncode, 'checks': result.get('checks')}, indent=2))
raise SystemExit(proc.returncode or int(not result or not all(result['checks'].values())))
