#!/usr/bin/env python3
"""Measure hook identity, native definition hashes, trust and cached reloads.

Only owned profiles, immutable controls and explicitly changed test files are
used. No turn, hook command, tool or model request is sent. Metadata
checks never stand in for hook execution or per-thread effective policy.
"""
import hashlib
import json
from pathlib import Path
import tempfile

from codex_skill_selection_probe import Stdio
from execution_config_probe import environment, verify_versions, write


def hook_definition(command='true', timeout=1):
    return {'hooks': {'SessionStart': [{'hooks': [
        {'type': 'command', 'command': command, 'timeout': timeout},
    ]}]}}


def run(root):
    env = environment(root)
    env['NETCAP_FIXTURE_KEY'] = 'unused-owned-fixture'
    cwd = root / 'work'
    cwd.mkdir()
    profile = Path(env['CODEX_HOME'])
    config = profile / 'config.toml'
    write(config, '''model="gpt-6-astra"
model_provider="fixture"
[features]
apps=false
plugins=true
hooks=true
plugin_sharing=false
shell_snapshot=false
[model_providers.fixture]
name="Unused local fixture"
base_url="http://127.0.0.1:9/v1"
env_key="NETCAP_FIXTURE_KEY"
wire_api="responses"
requires_openai_auth=false
supports_websockets=false
[plugins."fixture@owned"]
enabled=true
''')
    caches, paths = {}, {}
    for market in ['owned', 'copy-a', 'copy-b', 'moved', 'command', 'timeout', 'script', 'script-copy', 'literal']:
        cache = profile / 'plugins/cache' / market / 'fixture/1.0.0'
        caches[market] = cache
        relative = 'custom.json' if market == 'moved' else 'hooks/hooks.json'
        paths[market] = cache / relative
        write(cache / '.codex-plugin/plugin.json', {'name': 'fixture', 'version': '1.0.0',
            **({'hooks': './custom.json'} if market == 'moved' else {})})
        command = 'false' if market == 'command' else 'true'
        if market in ['script', 'script-copy']:
            command = 'python3 ${PLUGIN_ROOT}/entry.py'
            write(cache / 'entry.py', 'print("OWNED_SCRIPT_V1")\n')
        if market == 'literal':
            command = 'python3 ' + str(caches['script'] / 'entry.py')
        write(paths[market], hook_definition(command, 2 if market == 'timeout' else 1))
    verify_versions(root, ['server'])
    original_hashes = {str(p): hashlib.sha256(p.read_bytes()).hexdigest()
                       for cache in caches.values() for p in cache.rglob('*') if p.is_file()}
    report = {'root': str(root), 'checks': {}, 'samples': {}, 'processes': [],
        'fixture_hashes_before': original_hashes,
        'scope': 'owned native hook metadata; no turn, hook, model or tool execution; '
                 'profile writes below belong only to the isolated fixture'}
    checks, samples = report['checks'], report['samples']
    client, clients = None, []

    def check(name, ok):
        checks[name] = bool(ok)
        if not ok:
            raise AssertionError(name)

    def catalog(label):
        before = config.read_bytes()
        result = client.call('hooks/list', {'cwds': [str(cwd)]})
        samples[label] = result
        check(label + '_correct_directory_no_errors', len(result['data']) == 1 and
              result['data'][0]['cwd'] == str(cwd) and not result['data'][0]['errors'] and
              not result['data'][0]['warnings'])
        check(label + '_read_does_not_write_profile', config.read_bytes() == before)
        hooks = result['data'][0]['hooks']
        check(label + '_unique_keys_and_sources', len({h['key'] for h in hooks}) == len(hooks)
              and len({h['pluginId'] for h in hooks}) == len(hooks))
        return {h['pluginId'].split('@', 1)[1]: h for h in hooks}

    def profile_write(key, value):
        client.call('config/value/write', {'keyPath': key, 'value': value, 'mergeStrategy': 'replace'})

    def open_client(label):
        nonlocal client
        client = Stdio(root, env, label)
        clients.append(client)

    def close_client():
        nonlocal client
        if client is not None:
            client.close()
            report['processes'].append({'label': client.label, 'exit_code': client.proc.returncode})
            check(client.label + '_exited_zero', client.proc.returncode == 0)
            client = None

    try:
        open_client('initial')
        baseline = catalog('profile-only')
        check('unregistered_caches_not_in_directory_catalog', set(baseline) == {'owned'})
        started = client.call('thread/start', {'cwd': str(cwd), 'model': 'gpt-6-astra',
            'modelProvider': 'fixture', 'approvalPolicy': 'on-request', 'sandbox': 'read-only',
            'config': {'plugins.fixture@copy-a.enabled': True}})
        samples['thread'] = started
        after_thread = catalog('after-thread-override')
        check('thread_override_does_not_change_directory_catalog', after_thread == baseline)
        stopped = client.call('thread/unsubscribe', {'threadId': started['thread']['id']})
        check('owned_thread_unsubscribed_without_input', stopped['status'] == 'unsubscribed')

        profile_write('plugins', {'fixture@' + market: {'enabled': True} for market in caches})
        all_hooks = catalog('all-profile-enabled')
        check('all_nine_definitions_discovered', set(all_hooks) == set(caches))
        check('native_cache_paths_match', all(all_hooks[m]['sourcePath'] == str(paths[m]) for m in caches))
        duplicates = [all_hooks[m] for m in ['owned', 'copy-a', 'copy-b', 'moved']]
        check('constant_command_hash_independent_of_source_identity', len({h['currentHash'] for h in duplicates}) == 1)
        check('identical_handlers_have_distinct_keys', len({h['key'] for h in duplicates}) == 4)
        check('different_command_changes_hash', all_hooks['command']['currentHash'] != baseline['owned']['currentHash'])
        check('different_timeout_changes_hash', all_hooks['timeout']['currentHash'] != baseline['owned']['currentHash'])
        check('script_copies_have_identical_raw_definition', paths['script'].read_bytes() == paths['script-copy'].read_bytes())
        check('script_commands_expand_own_cache_path', all(all_hooks[m]['command'] ==
              'python3 ' + str(caches[m] / 'entry.py') for m in ['script', 'script-copy']))
        check('expanded_plugin_root_does_not_change_raw_definition_hash', all_hooks['script']['currentHash'] ==
              all_hooks['script-copy']['currentHash'])
        check('literal_and_variable_commands_display_identically', all_hooks['literal']['command'] == all_hooks['script']['command'])
        check('different_raw_commands_have_distinct_hashes', all_hooks['literal']['currentHash'] != all_hooks['script']['currentHash'])
        check('all_new_keys_untrusted', all(h['trustStatus'] == 'untrusted' for h in all_hooks.values()))

        trusted = {all_hooks[m]['key']: {'enabled': True, 'trusted_hash': all_hooks[m]['currentHash']}
                   for m in ['copy-a', 'script']}
        profile_write('hooks.state', trusted)
        after_trust = catalog('two-exact-keys-trusted')
        check('trust_is_key_specific', all(h['trustStatus'] == ('trusted' if m in ['copy-a', 'script'] else 'untrusted')
              for m, h in after_trust.items()))

        # The handler is cached by this server. Keep a separate pre-existing
        # definition as a control for the hash expected after a cold reload.
        write(paths['copy-a'], hook_definition('false'))
        write(caches['script'] / 'entry.py', 'print("OWNED_SCRIPT_V2")\n')
        moved_original = paths['moved'].read_bytes()
        paths['moved'].write_text(json.dumps(hook_definition(), separators=(',', ':'), sort_keys=True))
        warm = catalog('warm-after-disk-change')
        check('warm_catalog_keeps_loaded_command', warm['copy-a']['command'] == 'true')
        check('warm_catalog_keeps_loaded_hash_and_trust', warm['copy-a']['currentHash'] == all_hooks['copy-a']['currentHash']
              and warm['copy-a']['trustStatus'] == 'trusted')
        check('disk_command_really_changed', json.loads(paths['copy-a'].read_text()) == hook_definition('false'))
        check('formatting_bytes_really_changed', paths['moved'].read_bytes() != moved_original)
        close_client()

        open_client('cold')
        cold = catalog('cold-after-disk-change')
        check('cold_reload_sees_new_command', cold['copy-a']['command'] == 'false')
        check('cold_reload_hash_matches_independent_control', cold['copy-a']['currentHash'] == all_hooks['command']['currentHash'])
        check('changed_definition_requires_review', cold['copy-a']['trustStatus'] == 'modified')
        check('changed_definition_keeps_same_native_key', cold['copy-a']['key'] == all_hooks['copy-a']['key'])
        check('script_bytes_changed', (caches['script'] / 'entry.py').read_text() == 'print("OWNED_SCRIPT_V2")\n')
        check('script_body_not_part_of_native_definition_hash', cold['script']['currentHash'] == all_hooks['script']['currentHash']
              and cold['script']['trustStatus'] == 'trusted')
        check('json_formatting_not_part_of_native_definition_hash', cold['moved']['currentHash'] == all_hooks['moved']['currentHash'])
        check('other_source_trust_unchanged', cold['copy-b']['trustStatus'] == 'untrusted'
              and cold['owned']['trustStatus'] == 'untrusted')

        trusted[all_hooks['copy-a']['key']]['enabled'] = False
        profile_write('hooks.state', trusted)
        disabled = catalog('hook-disabled')
        check('disabled_hook_still_discovered', 'copy-a' in disabled and not disabled['copy-a']['enabled'])
        check('disable_does_not_trust_changed_definition', disabled['copy-a']['trustStatus'] == 'modified')
        check('unrelated_trust_preserved', disabled['script']['trustStatus'] == 'trusted' and disabled['script']['enabled'])
        profile_write('plugins.fixture@copy-b.enabled', False)
        plugin_disabled = catalog('plugin-disabled')
        check('disabled_plugin_absent_from_directory_catalog', 'copy-b' not in plugin_disabled)
        check('other_plugin_sources_preserved', set(plugin_disabled) == set(caches)-{'copy-b'})
        close_client()
    finally:
        close_client()
        permitted_changes = {str(paths['copy-a']), str(paths['moved']), str(caches['script'] / 'entry.py')}
        report['fixture_hashes_after'] = {p: hashlib.sha256(Path(p).read_bytes()).hexdigest()
                                         for p in original_hashes}
        checks['only_three_planned_fixture_files_changed'] = {p for p, h in original_hashes.items()
            if report['fixture_hashes_after'][p] != h} == permitted_changes
        forbidden = {'turn/start', 'turn/steer', 'thread/shellCommand', 'command/exec', 'mcpServer/tool/call'}
        checks['no_turn_command_or_tool_requested'] = not any(c['method'] in forbidden for p in clients for c in p.calls)
        checks['no_hook_execution_events'] = not any(e.get('method', '').startswith('hook/') for p in clients for e in p.events)
        report['passed'] = len(report['processes']) == 2 and all(checks.values())
        write(root / 'result.json', report)
    print(json.dumps({'checks': len(checks), 'passed': report['passed']}, indent=2))
    return report['passed']


if __name__ == '__main__':
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-hook-trust-'))
    print('Evidence directory:', root, flush=True)
    raise SystemExit(0 if run(root) else 1)
