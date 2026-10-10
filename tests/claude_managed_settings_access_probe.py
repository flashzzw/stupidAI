#!/usr/bin/env python3
"""Execute managed Read/Edit rules with original/derived settings in isolation."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile

from claude_derived_settings_access_probe import AccessProvider
from claude_plugin_contract_probe import Native, plugin, install, digest_tree
from execution_config_probe import CLAUDE, environment, verify_versions, write
from runtime.common import stop


def exercise(root, managed_only, policy_file, hook_policy=None):
    root.mkdir()
    env = environment(root)
    cwd, custom_dir = root / 'work', root / 'custom'
    cwd.mkdir(); custom_dir.mkdir(mode=0o700)
    subprocess.run(['git', 'init', '--quiet', str(cwd)], env=env, check=True)
    source = root / 'marketplace/selected'
    plugin(source, 'w0208-selected')
    write(source / 'hooks/hooks.json', {'hooks': {'SessionStart': [{'hooks': [{'type': 'command',
          'command': 'python3 "${CLAUDE_PLUGIN_ROOT}/startup.py"', 'timeout': 5}]}]}})
    marker_script = 'import os,json\nfrom pathlib import Path\nwith Path(os.environ["NETCAP_SETTINGS_ACCESS_LOG"]).open("a") as f:f.write(json.dumps({"source":%r,"root":os.environ.get("CLAUDE_PLUGIN_ROOT")})+"\\n")\n'
    write(source / 'startup.py', marker_script % 'installed')
    install(root, env, cwd)
    profile = Path(env['CLAUDE_CONFIG_DIR'])
    registry = json.loads((profile / 'plugins/installed_plugins.json').read_text())
    cache = Path(registry['plugins']['w0208-selected@w0208-local'][0]['installPath'])
    assert cache != source and cache.is_dir() and not cache.is_symlink()
    write(cache / 'startup.py', marker_script % 'cache-canary')
    policy = {'allowManagedPermissionRulesOnly': managed_only, 'permissions': {
        'deny': ['Read(./managed-deny/**)', 'Edit(./managed-deny/**)', 'Read(//etc/claude-code/anchored/**)'],
        'allow': ['Read(./user-deny/**)', 'Read(./project-deny/**)', 'Read(./custom-deny/**)', 'Edit(./custom-deny/**)', 'Read(./user-allow/**)'],
        'ask': ['Read(./managed-ask/**)', 'Edit(./managed-ask/**)']}}
    if hook_policy:
        policy.update(hook_policy)
    write(policy_file, policy)
    write(root / 'managed-policy.json', policy)
    user = json.loads((profile / 'settings.json').read_text())
    user['permissions'] = {'deny': ['Read(./user-deny/**)'], 'allow': ['Edit(./user-allow/**)', 'Read(./managed-deny/**)', 'Edit(./managed-deny/**)']}
    write(profile / 'settings.json', user)
    write(cwd / '.claude/settings.json', {'permissions': {'deny': ['Read(./project-deny/**)']}})
    custom = {'env': {'NETCAP_MANAGED_ACCESS': 'managed-access-preserved'}, 'permissions': {
        'deny': ['Read(./custom-deny/**)', 'Edit(./custom-deny/**)']}, 'sandbox': {'enabled': False},
        'hooks': {'SessionStart': [{'hooks': [{'type': 'command', 'command': 'python3 "' + str(cwd / 'custom-hook.py') + '"', 'timeout': 5}]}]}}
    write(cwd / 'custom-hook.py', 'import os,json\nfrom pathlib import Path\nwith Path(os.environ["NETCAP_SETTINGS_ACCESS_LOG"]).open("a") as f:f.write(json.dumps({"source":"custom","value":os.environ.get("NETCAP_MANAGED_ACCESS")})+"\\n")\n')
    original, composed = custom_dir / 'original.json', custom_dir / '.netcap-settings-fixture.json'
    write(original, custom); original.chmod(0o600)
    write(composed, {**custom, 'enabledPlugins': {'w0208-selected@w0208-local': False}}); composed.chmod(0o600)
    for name in ['managed-deny', 'user-deny', 'project-deny', 'custom-deny', 'user-allow', 'managed-ask']:
        write(cwd / name / 'read.txt', 'OWNED_' + name.upper().replace('-', '_') + '_BEFORE\n')
    before_custom = (cwd / 'custom-deny/read.txt').read_text()
    before_allowed = (cwd / 'user-allow/read.txt').read_text()
    cases = []
    for name in ['managed-deny', 'user-deny', 'project-deny', 'custom-deny', 'user-allow', 'managed-ask']:
        denied = name == 'managed-deny' or not managed_only and name in ['user-deny', 'project-deny', 'custom-deny']
        cases.append({'name': 'read-' + name, 'tool': 'Read', 'input': {'file_path': str(cwd / name / 'read.txt')},
                      'expected': 'denied' if denied else 'success', 'approvals': int(name == 'managed-ask'),
                      **({} if denied else {'marker': 'OWNED_' + name.upper().replace('-', '_') + '_BEFORE'})})
    cases += [
        {'name': 'read-policy-anchor', 'tool': 'Read', 'input': {'file_path': '/etc/claude-code/anchored/private.txt'}, 'expected': 'denied', 'approvals': 0},
        {'name': 'edit-custom-deny', 'tool': 'Edit', 'input': {'file_path': str(cwd / 'custom-deny/read.txt'), 'old_string': before_custom.strip(), 'new_string': 'OWNED_CUSTOM_AFTER', 'replace_all': False},
         'expected': 'success' if managed_only else 'denied', 'approvals': int(managed_only), 'after': 'OWNED_CUSTOM_AFTER\n' if managed_only else before_custom},
        {'name': 'edit-user-allow', 'tool': 'Edit', 'input': {'file_path': str(cwd / 'user-allow/read.txt'), 'old_string': before_allowed.strip(), 'new_string': 'OWNED_USER_ALLOW_AFTER', 'replace_all': False},
         'expected': 'success', 'approvals': 1, 'after': 'OWNED_USER_ALLOW_AFTER\n'},
        {'name': 'write-managed-deny', 'tool': 'Write', 'input': {'file_path': str(cwd / 'managed-deny/new.txt'), 'content': 'MUST_NOT_WRITE\n'}, 'expected': 'denied', 'approvals': 0, 'absent': True},
        {'name': 'write-managed-ask', 'tool': 'Write', 'input': {'file_path': str(cwd / 'managed-ask/new.txt'), 'content': 'OWNED_APPROVED_WRITE\n'}, 'expected': 'success', 'approvals': 1, 'after': 'OWNED_APPROVED_WRITE\n'},
    ]
    originals = {str(path): path.read_bytes() for path in [original, composed, profile / 'settings.json', profile / 'plugins/installed_plugins.json', profile / 'plugins/known_marketplaces.json', cwd / '.claude/settings.json', policy_file]}
    sources, cache_files = digest_tree(source), digest_tree(cache)
    metadata = {}
    for label, settings in [('original', original), ('composed', composed)]:
        directory = root / ('metadata-' + label); directory.mkdir()
        native = Native(directory, {**env, 'NETCAP_SETTINGS_ACCESS_LOG': str(root / 'unexpected-metadata-hook.jsonl')}, cwd, str(settings), extra=['--bare', '--no-session-persistence'])
        try:
            native.call('initialize')
            metadata[label] = {'settings': native.call('get_settings'), 'permissions': native.call('list_permission_rules'), 'hooks': native.call('get_hooks_listing'), 'mcp': native.call('mcp_status')}
            write(directory / 'metadata.json', metadata[label])
        finally:
            native.close()
    provider = AccessProvider(root, cases)
    request = {'root': str(root), 'cwd': str(cwd), 'original': str(original), 'composed': str(composed),
               'endpoint': provider.url, 'executable': CLAUDE, 'provider_log': str(provider.root), 'cases': cases,
               'source_key': 'w0208-selected@w0208-local', 'plugin_source': str(source), 'cache_source': str(cache),
               'managed_only': managed_only, 'ordinary_hooks': not hook_policy, 'reset_files': [{'path': str(cwd / 'custom-deny/read.txt'), 'content': before_custom}, {'path': str(cwd / 'user-allow/read.txt'), 'content': before_allowed}]}
    write(root / 'request.json', request)
    env.update(NETCAP_SETTINGS_WIRE_MODE='session', NETCAP_SETTINGS_WIRE=str(root / 'session-wire.jsonl'))
    proc = None
    try:
        proc = subprocess.Popen(['node', '--import', str(Path(__file__).with_name('claude_settings_wire_capture.mjs')), str(Path(__file__).with_suffix('.mjs')), str(root / 'request.json')], cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
        stdout, stderr = proc.communicate(timeout=240)
        write(root / 'runner.stdout', stdout); write(root / 'runner.stderr', stderr)
    finally:
        stop(proc); provider.close()
    result = json.loads((root / 'session-result.json').read_text())
    checks = result['checks']
    checks['runner_exit_zero'] = proc.returncode == 0
    checks['metadata_managed_only_is_actual'] = all(value['permissions']['state']['managedOnly'] is managed_only for value in metadata.values())
    checks['metadata_original_derived_permission_state_equal'] = metadata['original']['permissions']['state'] == metadata['composed']['permissions']['state']
    checks['metadata_bare_no_hooks_or_mcp'] = not (root / 'unexpected-metadata-hook.jsonl').exists() and all('bareMode' in value['hooks'] and not value['mcp']['mcpServers'] for value in metadata.values())
    checks['original_settings_registration_sources_unchanged'] = all(Path(name).read_bytes() == body for name, body in originals.items()) and digest_tree(source) == sources and digest_tree(cache) == cache_files
    checks['provider_auth_and_no_errors'] = bool(provider.requests) and all(row['auth_matches'] for row in provider.requests) and not provider.errors
    text = json.dumps([block for row in provider.requests for message in row['body'].get('messages', [])
                       for block in (message.get('content', []) if isinstance(message.get('content'), list) else []) if block.get('type') == 'tool_result'])
    denied_markers = ['OWNED_MANAGED_DENY_BEFORE', 'OWNED_POLICY_ANCHOR_PRIVATE'] + ([] if managed_only else ['OWNED_USER_DENY_BEFORE', 'OWNED_PROJECT_DENY_BEFORE', 'OWNED_CUSTOM_DENY_BEFORE'])
    checks['denied_contents_never_reach_provider'] = all(marker not in text for marker in denied_markers)
    checks['file_deny_untouched'] = not (cwd / 'managed-deny/new.txt').exists() and (cwd / 'managed-deny/read.txt').read_text() == 'OWNED_MANAGED_DENY_BEFORE\n'
    checks['private_file_mode_and_directory'] = composed.stat().st_mode & 0o777 == 0o600 and composed.parent == original.parent
    result.update({'request': request, 'metadata': metadata, 'provider_requests': len(provider.requests), 'source_files': sources, 'cache_files': cache_files,
                   'expectations_met': all(checks.values()), 'scope': 'Real Session/SDK/CLI + local scripted provider; managed settings from an owned mount, controlled absent Windows registry. Not Application installed-source apply admission.'})
    write(root / 'result.json', result)
    composed.unlink()
    return result


def inside(root, surface, bridge):
    expected = os.environ['NETCAP_POLICY_PARENT_NAMESPACE']
    assert os.readlink('/proc/self/ns/mnt') != expected and os.path.samefile(root / 'policy', '/etc/claude-code')
    verify_versions(root, ['claude'])
    if surface != 'access':
        allowed_tool = 'mcp__identity__whoami' if surface == 'mcp' else 'mcp__plugin_plugin-suite_identity__whoami'
        write(root / 'policy/managed-settings.json', {'allowManagedPermissionRulesOnly': True, 'permissions': {'allow': [allowed_tool]}})
        metadata = root / 'policy-observation'; metadata.mkdir()
        observer_env = environment(metadata)
        observer = Native(metadata, observer_env, metadata, extra=['--bare', '--no-session-persistence'])
        try:
            observer.call('initialize')
            rules = observer.call('list_permission_rules')
            write(metadata / 'permissions.json', rules)
            assert rules['state']['managedOnly'] is True
            assert any(row['source'] == 'policySettings' and row['behavior'] == 'allow' and row['rule'] == allowed_tool for row in rules['state']['rules'])
        finally:
            observer.close()
        os.environ['TMPDIR'] = str(root)
        tempfile.tempdir = None
        if surface == 'mcp':
            from claude_mcp_selection_smoke import MCPFixture as Fixture, run
        else:
            from claude_plugin_execution_smoke import PluginFixture as Fixture, run
        with Fixture(str(Path(__file__).resolve().parents[1] / '.dev/bin/netcap')) as fixture:
            print('Regression directory:', fixture.root, flush=True)
            if bridge:
                for profile in fixture.profiles.values():
                    profile['instance']['bridge'] = str(bridge / 'main.js')
            verify_versions(fixture.root, ['claude'])
            fixture.register()
            run(fixture)
        candidates = list(root.glob('*/result.json'))
        assert len(candidates) == 1, candidates
        report = json.loads(candidates[0].read_text())
        write(root / 'result.json', {'surface': surface, 'native_result': str(candidates[0]), 'passed': report.get('expectations_met') is True, 'checks': len(report['checks'])})
        raise SystemExit(0 if report.get('expectations_met') is True else 1)
    results = {}
    for name, value, hooks in [('managed-only', True, None), ('all-sources', False, None), ('managed-hooks-only', True, {'allowManagedHooksOnly': True}), ('all-hooks-disabled', True, {'disableAllHooks': True})]:
        result = exercise(root / name, value, root / 'policy/managed-settings.json', hooks)
        results[name] = {'checks': len(result['checks']), 'passed': result['expectations_met'], 'failed': [key for key, ok in result['checks'].items() if not ok]}
    write(root / 'policy/managed-settings.json', {})
    write(root / 'result.json', {'cases': results, 'passed': all(row['passed'] for row in results.values())})
    print(json.dumps(results, indent=2), flush=True)
    raise SystemExit(0 if all(row['passed'] for row in results.values()) else 1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside', type=Path)
    parser.add_argument('--surface', choices=['access', 'mcp', 'plugin'], default='access')
    parser.add_argument('--bridge', type=Path, help='Baseline bridge for the managed MCP/plugin comparison only.')
    args = parser.parse_args()
    if args.inside:
        inside(args.inside, args.surface, args.bridge)
    if args.bridge and args.surface == 'access':
        parser.error('--bridge is only supported for MCP/plugin regression')
    repo = Path(__file__).resolve().parents[1]
    root = Path(tempfile.mkdtemp(prefix='netcap-managed-settings-access-'))
    print('Evidence directory:', root, flush=True)
    write(root / 'policy/managed-settings.json', {})
    write(root / 'policy/anchored/private.txt', 'OWNED_POLICY_ANCHOR_PRIVATE\n')
    registry = root / 'windows/Windows/System32/reg.exe'
    write(registry, '#!/usr/bin/python3\nimport sys\nsys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\nraise SystemExit(1)\n')
    registry.chmod(0o700)
    env = environment(root / 'environment')
    env['NETCAP_POLICY_PARENT_NAMESPACE'] = os.readlink('/proc/self/ns/mnt')
    argv = ['bwrap', '--unshare-user', '--unshare-pid', '--unshare-net', '--die-with-parent', '--ro-bind', '/', '/',
            '--tmpfs', '/etc', '--ro-bind', str(root / 'policy'), '/etc/claude-code', '--bind', str(root), str(root),
            '--tmpfs', '/mnt', '--ro-bind', str(root / 'windows'), '/mnt/c', '--tmpfs', '/tmp', '--tmpfs', '/run', '--proc', '/proc', '--dev', '/dev',
            'python3', str(Path(__file__).resolve()), '--inside', str(root), '--surface', args.surface]
    if args.bridge:
        argv += ['--bridge', str(args.bridge.resolve())]
    write(root / 'invocation.json', {'argv': argv, 'parent_namespace': env['NETCAP_POLICY_PARENT_NAMESPACE'], 'network': 'isolated', 'registry': 'controlled absence; not real Windows policy'})
    proc = subprocess.Popen(argv, cwd=repo, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
    try:
        stdout, stderr = proc.communicate(timeout=520)
        write(root / 'runner.stdout', stdout); write(root / 'runner.stderr', stderr)
    finally:
        stop(proc)
    report = json.loads((root / 'result.json').read_text()) if (root / 'result.json').exists() else {}
    print(json.dumps({'exit_code': proc.returncode, 'result': report}, indent=2))
    raise SystemExit(proc.returncode)


if __name__ == '__main__':
    main()
