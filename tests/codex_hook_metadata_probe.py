#!/usr/bin/env python3
"""Compare package hook metadata in isolated native profiles without execution.

The reference profile has a synthetic original thread and trusted hook state.
The inspection profile receives only copied plugin resources and generated
configuration. Its metadata must not be presented as the original policy.
"""
import hashlib
import argparse
import json
from pathlib import Path
import shutil
import tempfile

from codex_skill_selection_probe import Stdio
from execution_config_probe import environment, verify_versions, write


def files(root):
    return {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in root.rglob('*') if p.is_file()}


def definitions():
    return {'hooks': {
        'SessionStart': [{'hooks': [
            {'type': 'command', 'command': 'python3 ${PLUGIN_ROOT}/entry.py', 'timeout': 2},
            {'type': 'command', 'command': 'printf %s ${PLUGIN_DATA}', 'timeout': 3, 'async': True},
        ]}],
        'PreToolUse': [{'matcher': 'Read', 'hooks': [
            {'type': 'command', 'command': 'python3 ${CLAUDE_PLUGIN_ROOT}/entry.py', 'timeout': 4},
        ]}],
    }}


def profile(root, packages):
    env = environment(root)
    cwd = root / 'work'
    cwd.mkdir()
    config = '''model="gpt-6-astra"
model_provider="fixture"
[features]
apps=false
plugins=true
hooks=true
plugin_sharing=false
shell_snapshot=false
[model_providers.fixture]
name="Unused local metadata fixture"
base_url="http://127.0.0.1:9/v1"
wire_api="responses"
requires_openai_auth=false
supports_websockets=false
'''
    for name in packages:
        config += '\n[plugins.' + json.dumps(name + '@owned') + ']\nenabled=true\n'
    write(Path(env['CODEX_HOME']) / 'config.toml', config)
    caches = {}
    for name, source in packages.items():
        cache = Path(env['CODEX_HOME']) / 'plugins/cache/owned' / name / '1.0.0'
        cache.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(source, cache)
        caches[name] = cache
    return env, cwd, caches


def run(root, layouts=None):
    layouts = layouts or ['legacy', 'portable', 'inline', 'fallback']
    verify_versions(root, ['server'])
    source = root / 'source'
    marker = root / 'must-not-execute.txt'
    packages = {}
    for layout in layouts:
        name = 'fixture-' + layout
        package = source / name
        packages[name] = package
        hook_path = 'hooks/hooks.json' if layout in ['legacy','portable'] else 'custom/hooks.json'
        legacy = {'name': name, 'version': '1.0.0'}
        portable = {'$schema': 'https://agent-plugins.org/schemas/1.0.0/plugin.schema.json',
                    'name': name, 'version': '1.0.0'}
        if layout == 'inline':
            portable['extensions'] = {'com.openai': {'hooks': './' + hook_path}}
            write(package / '.codex-plugin/plugin.json', {**legacy, 'hooks': './ignored/hooks.json'})
            write(package / 'ignored/hooks.json', {'hooks': {'SessionEnd': [{'hooks': [
                {'type': 'command', 'command': 'exit 87'},
            ]}]}})
        if layout == 'fallback':
            write(package / '.codex-plugin/plugin.json', {**legacy, 'hooks': './' + hook_path})
        if layout == 'legacy':
            write(package / '.codex-plugin/plugin.json', legacy)
        else:
            write(package / 'plugin.json', portable)
        write(package / hook_path, definitions())
        write(package / 'entry.py', f'from pathlib import Path\nPath({str(marker)!r}).write_text("EXECUTED")\n')
        write(package / 'skills/method/SKILL.md', '---\nname: method\ndescription: Owned unused method\n---\nNot invoked.\n')
        mcp = {'mcpServers': {'marker_' + layout: {'type': 'stdio', 'command': 'python3', 'args': ['${PLUGIN_ROOT}/entry.py']}}}
        if layout == 'legacy':
            write(package / '.mcp.json', mcp)
        else:
            write(package / 'mcp.json', {'$schema': 'https://agent-plugins.org/schemas/1.0.0/mcp.schema.json', **mcp})
    original_sources = files(source)
    report = {'root': str(root), 'checks': {}, 'samples': {}, 'processes': [],
              'layouts': layouts,
              'scope': 'copied package metadata only; original policy and runtime state are not inspection results; '
                       'one synthetic reference thread, zero turns/hooks/tools'}
    checks, samples = report['checks'], report['samples']
    clients, client = [], None
    finished = False

    def check(name, condition):
        checks[name] = bool(condition)
        if not condition:
            raise AssertionError(name)

    def close():
        nonlocal client
        if client:
            client.close()
            report['processes'].append({'label': client.label, 'exit_code': client.proc.returncode})
            check(client.label + '_exited_zero', client.proc.returncode == 0)
            client = None

    def listing(label, cwd):
        response = client.call('hooks/list', {'cwds': [str(cwd)]})
        samples[label] = response
        entry = response['data'][0]
        check(label + '_directory_matches', len(response['data']) == 1 and entry['cwd'] == str(cwd))
        check(label + '_no_warnings_or_errors', not entry['warnings'] and not entry['errors'])
        check(label + '_expected_unique_keys', len(entry['hooks']) == len(packages)*3
              and len({h['key'] for h in entry['hooks']}) == len(packages)*3)
        return entry['hooks']

    try:
        reference = root / 'reference'
        reference.mkdir()
        env, cwd, caches = profile(reference, packages)
        client = Stdio(reference, env, 'reference')
        clients.append(client)
        original_hooks = listing('reference-before-trust', cwd)
        state = {h['key']: {'enabled': True, 'trusted_hash': h['currentHash']} for h in original_hooks}
        client.call('config/value/write', {'keyPath': 'hooks.state', 'value': state, 'mergeStrategy': 'replace'})
        trusted = listing('reference-trusted', cwd)
        check('reference_all_trusted', all(h['trustStatus'] == 'trusted' for h in trusted))
        started = client.call('thread/start', {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture',
            'historyMode': 'legacy', 'approvalPolicy': 'on-request', 'sandbox': 'read-only',
            'config': {'plugins.' + name + '@owned.enabled': False for name in packages}})
        sid = started['thread']['id']
        client.call('thread/inject_items', {'threadId': sid, 'items': [{'type': 'message', 'role': 'user',
            'content': [{'type': 'input_text', 'text': 'SYNTHETIC HISTORY; no input or method use'}]}]})
        samples['reference-thread'] = client.call('thread/read', {'threadId': sid, 'includeTurns': True})
        close()
        before = files(reference)
        report['reference_files_before'] = before
        baseline = {h['key']: h for h in trusted}
        for iteration in [1, 2]:
            inspection = root / ('inspection-' + str(iteration))
            inspection.mkdir()
            env, cwd, copies = profile(inspection, packages)
            check(f'inspection_{iteration}_copies_match_package_bytes', all(files(copies[name]) == files(packages[name]) for name in packages))
            config_path = Path(env['CODEX_HOME']) / 'config.toml'
            config_bytes = config_path.read_bytes()
            client = Stdio(inspection, env, 'inspection-' + str(iteration))
            clients.append(client)
            hooks = listing('inspection-' + str(iteration), cwd)
            for hook in hooks:
                label = str(iteration) + '/' + hook['key']
                original = baseline[hook['key']]
                fields = ['key','pluginId','currentHash','eventName','handlerType','matcher','timeoutSec',
                          'statusMessage','additionalContextLimit','displayOrder','async']
                check(label + '_definition_metadata_matches', all(hook.get(k) == original.get(k) for k in fields))
                check(label + '_trust_not_inherited', hook['trustStatus'] == 'untrusted' and hook['enabled'] and not hook['isManaged'])
                name = hook['pluginId'].split('@')[0]
                relative = Path(hook['sourcePath']).relative_to(copies[name])
                original_relative = Path(original['sourcePath']).relative_to(caches[name])
                check(label + '_source_maps_only_with_package_relative_path', relative == original_relative and relative.is_relative_to('.')
                      and not str(relative).startswith('..'))
            check(f'inspection_{iteration}_config_not_changed_by_reads', config_path.read_bytes() == config_bytes)
            close()
            check(f'inspection_{iteration}_original_profile_and_history_unchanged', files(reference) == before)
            check(f'inspection_{iteration}_no_marker_command_executed', not marker.exists())
        finished = True
    finally:
        close()
        checks['source_packages_unchanged'] = files(source) == original_sources
        checks['no_hook_or_mcp_marker'] = not marker.exists()
        checks['no_turn_or_tool_requested'] = not any(c['method'] in ['turn/start','turn/steer','command/exec','thread/shellCommand','mcpServer/tool/call'] for p in clients for c in p.calls)
        checks['no_hook_notifications'] = not any(e.get('method','').startswith('hook/') for p in clients for e in p.events)
        checks['inspectors_only_initialize_and_list'] = all([c['method'] for c in p.calls] == ['initialize','hooks/list'] for p in clients if p.label.startswith('inspection-'))
        report['reference_files_after'] = files(root / 'reference')
        report['passed'] = finished and len(report['processes']) == 3 and all(checks.values())
        write(root / 'result.json', report)
    print(json.dumps({'checks':len(checks), 'passed':report['passed']},indent=2))
    return report['passed']


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--legacy-only', action='store_true', help='explicit partial contract; does not validate portable hooks')
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-hook-metadata-'))
    print('Evidence directory:', root, flush=True)
    raise SystemExit(0 if run(root, ['legacy'] if args.legacy_only else None) else 1)
