#!/usr/bin/env python3
"""Require portable hook discovery alongside two legacy controls.

This deliberately retains the full discovery requirement and exits nonzero
when a schema-bearing portable declaration is silently absent. Only isolated
native metadata reads are sent; no hook command, turn, MCP or Skill is used.
"""
import json
from pathlib import Path
import tempfile

from codex_hook_metadata_probe import profile
from codex_skill_selection_probe import Stdio
from execution_config_probe import write, verify_versions

root = Path(tempfile.mkdtemp(prefix='netcap-w0208-hook-portable-minimal-'))
print('Evidence directory:', root, flush=True)
verify_versions(root, ['server'])
packages = {}
layouts = ['legacy-default', 'legacy-custom', 'portable-default', 'portable-inline-array', 'portable-fallback']
for layout in layouts:
    name = 'fixture-' + layout
    p = root / 'source' / name
    packages[name] = p
    hook_path = 'hooks/hooks.json' if layout.endswith('default') else 'custom.json'
    identity = {'name': name, 'version': '1.0.0'}
    if layout.startswith('legacy') or layout.endswith('fallback'):
        write(p / '.codex-plugin/plugin.json', {**identity, **({'hooks': './' + hook_path} if hook_path == 'custom.json' else {})})
    if layout.startswith('portable'):
        manifest = {'$schema': 'https://agent-plugins.org/schemas/1.0.0/plugin.schema.json', **identity}
        if layout.endswith('array'):
            manifest['extensions'] = {'com.openai': {'hooks': ['./custom.json']}}
        write(p / 'plugin.json', manifest)
    write(p / hook_path, {'hooks': {'SessionStart': [{'hooks': [{'type': 'command', 'command': 'true', 'timeout': 1}]}]}})
group = root / 'native'
group.mkdir()
env, cwd, caches = profile(group, packages)
env['RUST_LOG'] = 'warn,codex_core_plugins::loader=debug'
client = Stdio(group, env, 'native')
try:
    result = client.call('hooks/list', {'cwds': [str(cwd)]})
finally:
    client.close()
seen = {h['pluginId'] for h in result['data'][0]['hooks']}
checks = {layout + '_hook_discovered': 'fixture-' + layout + '@owned' in seen for layout in layouts}
checks['no_warnings_or_errors'] = not result['data'][0]['errors'] and not result['data'][0]['warnings']
checks['no_execution_requested'] = [c['method'] for c in client.calls] == ['initialize', 'hooks/list']
checks['owned_server_exited_zero'] = client.proc.returncode == 0
write(root / 'result.json', {'root': str(root), 'checks': checks, 'passed': all(checks.values()), 'listing': result,
    'scope': 'minimal schema-bearing portable hooks versus legacy; no MCP/Skill/variables/async; no execution'})
print(json.dumps(checks, indent=2))
raise SystemExit(0 if all(checks.values()) else 1)
