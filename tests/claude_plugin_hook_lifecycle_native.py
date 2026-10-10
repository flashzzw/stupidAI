#!/usr/bin/env python3
"""Observe actual plugin SessionStart hooks without model input or a listener."""
import hashlib
import json
from pathlib import Path
import shlex
import subprocess
import tempfile
from execution_config_probe import environment, verify_versions, write
from runtime.common import stop

root = Path(tempfile.mkdtemp(prefix='netcap-w0208-hook-lifecycle-'))
print('Evidence directory:', root, flush=True)
verify_versions(root, ['claude'])
env = environment(root)
cwd = root / 'work'
cwd.mkdir()
requests = []
for mode, code in [('success', 0), ('error', 2), ('disabled', 0)]:
    source = root / mode
    write(source / '.claude-plugin/plugin.json', {'name': 'hook-lifecycle', 'version': '1'})
    write(source / 'hook.py', '''import json,os,sys
from pathlib import Path
raw=sys.stdin.read()
body=json.loads(raw) if raw else {}
with Path(sys.argv[1]).open('a') as out:
    out.write(json.dumps({'stdin_bytes':len(raw.encode()),'event':body.get('hook_event_name'),'session_id':body.get('session_id'),'cwd':body.get('cwd'),'plugin_root':os.environ.get('CLAUDE_PLUGIN_ROOT'),'exit_code':int(sys.argv[2])})+'\\n')
print('FIXTURE_HOOK_OUTPUT')
print('FIXTURE_HOOK_STDERR',file=sys.stderr)
raise SystemExit(int(sys.argv[2]))
''')
    marker = root / (mode + '-trace.jsonl')
    command = f'python3 "${{CLAUDE_PLUGIN_ROOT}}/hook.py" {shlex.quote(str(marker))} {code}'
    write(source / 'hooks/hooks.json', {'hooks': {'SessionStart': [{'matcher': 'startup|resume', 'hooks': [{'type': 'command', 'command': command}]}]}})
    files = []
    for p in sorted(source.rglob('*')):
        entry = {'path': str(p.relative_to(source)), 'kind': 'directory' if p.is_dir() else 'file', 'mode': p.stat().st_mode & 0o777}
        if p.is_file():
            entry['sha256'] = hashlib.sha256(p.read_bytes()).hexdigest()
        files.append(entry)
    selection = {'asset_id': 'plugin', 'name': 'hook-lifecycle', 'version': '1', 'mode': 'disabled' if mode == 'disabled' else 'enabled', 'source_path': str(source), 'source_hash': 'a' * 64, 'root_mode': source.stat().st_mode & 0o777, 'files': files, 'components': [{'kind': 'hook', 'name': 'SessionStart', 'native_name': 'SessionStart', 'path': 'hooks/hooks.json', 'count': 1}]}
    requests.append({'mode': mode, 'selection': selection, 'marker': str(marker)})
write(root / 'request.json', {'root': str(root), 'cwd': str(cwd), 'cases': requests})
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
