#!/usr/bin/env python3
"""Owned Codex plugin source/install contracts over stdio, without model turns.

Discovery and installation metadata are not evidence of per-thread loading,
tool execution, hook delivery or successful method use.
"""
import hashlib
import json
from pathlib import Path
import tempfile

from codex_skill_selection_probe import Stdio
from execution_config_probe import SERVER, environment, verify_versions, write, skill


def run(root):
    env = environment(root)
    cwd = root / 'work'
    cwd.mkdir()
    verify_versions(root, ['server'])
    profile = Path(env['CODEX_HOME'])
    write(profile / 'config.toml', '''model="gpt-6-astra"
model_provider="fixture"
approval_policy="on-request"
sandbox_mode="read-only"
[model_providers.fixture]
name="Offline plugin contracts"
base_url="http://127.0.0.1:9/v1"
env_key="NETCAP_FIXTURE_KEY"
wire_api="responses"
requires_openai_auth=false
supports_websockets=false
[features]
apps=false
plugins=true
plugin_sharing=false
shell_snapshot=false
''')
    env['NETCAP_FIXTURE_KEY'] = 'fixture-no-model'
    markets, sources = {}, {}
    for label, base in [('home', Path(env['HOME'])), ('project', cwd)]:
        source = base / 'plugins/fixture'
        sources[label] = source
        write(source / '.codex-plugin/plugin.json', {'name': 'fixture', 'version': '1.0.0', 'description': label + ' owned plugin'})
        skill(source / 'skills/method', 'fixture-' + label, 'PLUGIN_' + label.upper() + '_DESCRIPTION')
        write(source / '.mcp.json', {'mcpServers': {'identity': {'command': 'python3', 'args': [str(source / 'server.py')]}}})
        write(source / 'server.py', f'from pathlib import Path\nPath({str(root / "mcp-started")!r}).write_text("started")\nraise SystemExit("metadata-only fixture must not start MCP")\n')
        write(source / 'hook.py', f'from pathlib import Path\nPath({str(root / "hook-started")!r}).write_text("started")\n')
        write(source / 'hooks/hooks.json', {'hooks': {'PreToolUse': [{'matcher': 'Read', 'hooks': [{'type': 'command', 'command': 'python3 "' + str(source / 'hook.py') + '"'}]}]}})
        market = base / '.agents/plugins/marketplace.json'
        markets[label] = market
        write(market, {'name': 'owned-' + label, 'plugins': [{'name': 'fixture', 'source': {'source': 'local', 'path': './plugins/fixture'}, 'policy': {'installation': 'AVAILABLE', 'authentication': 'ON_INSTALL'}, 'category': 'Productivity'}]})
    checks, observations = {}, {}
    complete = False
    client = None
    def check(name, value):
        checks[name] = bool(value)
        assert value, name
    def call(label, method, params):
        value = client.call(method, params)
        observations[label] = value
        return value
    try:
        client = Stdio(root, env, 'native')
        listing = call('before', 'plugin/list', {'cwds': [str(cwd)], 'marketplaceKinds': ['local'], 'forceRefetch': False})
        check('home_and_project_marketplaces_discovered', all(any(m['name'] == 'owned-' + label for m in listing['marketplaces']) for label in markets))
        for label, market in markets.items():
            detail = call('read-' + label, 'plugin/read', {'pluginName': 'fixture', 'marketplacePath': str(market)})['plugin']
            check(label + '_source_is_explicit', detail['marketplaceName'] == 'owned-' + label and detail['summary']['source']['type'] == 'local')
        check('uninstalled_detail_is_not_enabled_state', not observations['read-project']['plugin']['summary']['installed'] and observations['read-project']['plugin']['skills'][0]['enabled'])
        call('install-project', 'plugin/install', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})
        detail = call('installed-project', 'plugin/read', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})['plugin']
        check('installed_flag_visible', detail['summary']['installed'])
        call('installed-roster', 'plugin/installed', {'cwds': [str(cwd)]})
        call('installed-hooks', 'hooks/list', {'cwds': [str(cwd)]})
        installed_hooks = [h for row in observations['installed-hooks']['data'] for h in row['hooks']]
        check('hook_catalog_reports_cache_and_full_source_id', len(installed_hooks) == 1 and installed_hooks[0]['pluginId'] == 'fixture@owned-project' and Path(installed_hooks[0]['sourcePath']).is_relative_to(profile / 'plugins/cache'))
        check('enabled_hook_is_not_a_trust_grant', installed_hooks[0]['enabled'] and installed_hooks[0]['trustStatus'] == 'untrusted')
        skills = call('skill-discovery', 'skills/list', {'cwds': [str(cwd)], 'forceReload': True})
        bundled = [s for row in skills['data'] for s in row['skills'] if s.get('pluginId') == 'fixture@owned-project']
        check('skill_discovery_uses_installed_cache', len(bundled) == 1 and Path(bundled[0]['path']).is_relative_to(profile / 'plugins/cache'))
        check('detail_source_differs_from_installed_cache', bundled[0]['path'] != detail['skills'][0]['path'])
        # Both marketplaces intentionally provide the same plugin and Skill names.
        skill(sources['home'] / 'skills/method', 'fixture-project', 'PLUGIN_HOME_COLLISION')
        call('install-home', 'plugin/install', {'pluginName': 'fixture', 'marketplacePath': str(markets['home'])})
        both = call('both-installed', 'plugin/installed', {'cwds': [str(cwd)]})
        check('same_name_has_two_installed_source_ids', {p['id'] for m in both['marketplaces'] for p in m['plugins']} == {'fixture@owned-home', 'fixture@owned-project'})
        call('same-name-skills', 'skills/list', {'cwds': [str(cwd)], 'forceReload': True})
        colliding = [s for row in observations['same-name-skills']['data'] for s in row['skills'] if s.get('pluginId')]
        check('same_skill_name_requires_full_plugin_id', len(colliding) == 2 and len({s['name'] for s in colliding}) == 1 and len({s['pluginId'] for s in colliding}) == 2)
        call('disable-project', 'config/value/write', {'keyPath': 'plugins.fixture@owned-project.enabled', 'value': False, 'mergeStrategy': 'replace'})
        disabled = call('disabled-project', 'plugin/read', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})['plugin']
        check('disabled_state_reported', disabled['summary']['installed'] and not disabled['summary']['enabled'])
        check('disabled_detail_still_previews_components', bool(disabled['hooks']) and disabled['mcpServers'] == ['identity'] and disabled['skills'][0]['enabled'])
        call('disabled-skills', 'skills/list', {'cwds': [str(cwd)], 'forceReload': True})
        call('disabled-hooks', 'hooks/list', {'cwds': [str(cwd)]})
        check('global_disable_excludes_only_selected_plugin_hooks', {h['pluginId'] for row in observations['disabled-hooks']['data'] for h in row['hooks']} == {'fixture@owned-home'})
        check('global_disable_excludes_only_selected_plugin_skills', {s['pluginId'] for row in observations['disabled-skills']['data'] for s in row['skills'] if s.get('pluginId')} == {'fixture@owned-home'})
        client.close()
        client = Stdio(root, env, 'restarted')
        restarted = call('restarted-disabled', 'plugin/read', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})['plugin']
        check('restart_keeps_disabled_registration', restarted['summary']['installed'] and not restarted['summary']['enabled'])
        before_cache = Path(bundled[0]['path']).read_text()
        write(sources['project'] / '.codex-plugin/plugin.json', {'name': 'fixture', 'version': '2.0.0', 'description': 'updated source'})
        skill(sources['project'] / 'skills/method', 'fixture-project-v2', 'PLUGIN_PROJECT_V2')
        call('source-updated-detail', 'plugin/read', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})
        call('source-updated-skills', 'skills/list', {'cwds': [str(cwd)], 'forceReload': True})
        check('old_cached_skill_bytes_preserved', Path(bundled[0]['path']).read_text() == before_cache)
        call('reenable-without-reinstall', 'config/value/write', {'keyPath': 'plugins.fixture@owned-project.enabled', 'value': True, 'mergeStrategy': 'replace'})
        call('reenabled-source-updated-skills', 'skills/list', {'cwds': [str(cwd)], 'forceReload': True})
        reenabled = [s for row in observations['reenabled-source-updated-skills']['data'] for s in row['skills'] if s.get('pluginId') == 'fixture@owned-project']
        check('reenable_keeps_installed_version_one', len(reenabled) == 1 and reenabled[0]['path'] == bundled[0]['path'])
        check('local_version_in_detail_can_differ_from_installed_cache', observations['source-updated-detail']['plugin']['summary']['localVersion'] == '2.0.0' and '/1.0.0/' in reenabled[0]['path'])
        call('reinstall-project', 'plugin/install', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})
        call('reinstalled-detail', 'plugin/read', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})
        call('reinstalled-skills', 'skills/list', {'cwds': [str(cwd)], 'forceReload': True})
        call('reinstalled-hooks', 'hooks/list', {'cwds': [str(cwd)]})
        materialized = [s for row in observations['reinstalled-skills']['data'] for s in row['skills'] if s.get('pluginId') == 'fixture@owned-project']
        check('explicit_reinstall_materializes_version_two', len(materialized) == 1 and '/2.0.0/' in materialized[0]['path'])
        cached_v2 = Path(materialized[0]['path'])
        v2_bytes = cached_v2.read_text()
        skill(sources['project'] / 'skills/method', 'fixture-project-same-version', 'PLUGIN_PROJECT_SAME_VERSION_DRIFT')
        call('same-version-source-detail', 'plugin/read', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})
        call('same-version-cache-skills', 'skills/list', {'cwds': [str(cwd)], 'forceReload': True})
        observations['same-version-cache-bytes-unchanged'] = cached_v2.read_text() == v2_bytes
        check('source_edit_alone_does_not_replace_cache_bytes', observations['same-version-cache-bytes-unchanged'])
        call('same-version-reinstall', 'plugin/install', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})
        call('same-version-reinstalled-skills', 'skills/list', {'cwds': [str(cwd)], 'forceReload': True})
        observations['same-version-reinstall-cache'] = {'path': str(cached_v2), 'before_sha256': hashlib.sha256(v2_bytes.encode()).hexdigest(), 'after_sha256': hashlib.sha256(cached_v2.read_bytes()).hexdigest()}
        check('same_version_reinstall_changes_existing_cache_bytes', observations['same-version-reinstall-cache']['before_sha256'] != observations['same-version-reinstall-cache']['after_sha256'])
        env_b = environment(root / 'profile-b')
        with_b = Stdio(root, env_b, 'profile-b')
        try:
            isolated = with_b.call('plugin/list', {'cwds': [str(cwd)], 'marketplaceKinds': ['local']})
            observations['other-profile'] = isolated
            check('installed_state_does_not_cross_profiles', all(not p['installed'] for m in isolated['marketplaces'] for p in m['plugins']))
        finally:
            with_b.close()
        call('uninstall-project', 'plugin/uninstall', {'pluginId': 'fixture@owned-project'})
        uninstalled = call('uninstalled-detail', 'plugin/read', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})['plugin']
        check('uninstall_leaves_source_discoverable', not uninstalled['summary']['installed'] and sources['project'].is_dir())
        call('uninstalled-skills', 'skills/list', {'cwds': [str(cwd)], 'forceReload': True})
        call('uninstalled-hooks', 'hooks/list', {'cwds': [str(cwd)]})
        original_market = json.loads(markets['project'].read_text())
        original_market['plugins'][0]['policy']['installation'] = 'NOT_AVAILABLE'
        write(markets['project'], original_market)
        call('policy-denied-detail', 'plugin/read', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})
        rejected = False
        try:
            call('policy-denied-install', 'plugin/install', {'pluginName': 'fixture', 'marketplacePath': str(markets['project'])})
        except RuntimeError:
            rejected = True
            observations['policy-denied-install'] = client.calls[-1].get('error')
        check('not_available_policy_rejects_install', rejected)
        write(markets['project'], '{invalid')
        broken = call('broken-market', 'plugin/list', {'cwds': [str(cwd)], 'marketplaceKinds': ['local']})
        check('malformed_market_reported_with_other_source_preserved', bool(broken['marketplaceLoadErrors']) and any(m['name'] == 'owned-home' for m in broken['marketplaces']))
        check('no_mcp_or_hook_process_started', not (root / 'mcp-started').exists() and not (root / 'hook-started').exists())
        all_calls = list(client.calls)
        for path in root.glob('*-protocol.json'):
            all_calls.extend(json.loads(path.read_text())['calls'])
        check('no_model_or_thread_requests', not any(c['method'].startswith(('thread/', 'turn/')) for c in all_calls))
        complete = True
    finally:
        if client:
            client.close()
        write(root / 'result.json', {'root': str(root), 'scope': 'owned Codex stdio local plugin sources and installation metadata only; no thread or model input; no production change', 'checks': checks, 'observations': observations, 'expectations_met': complete and all(checks.values()), 'binary_sha256': hashlib.sha256(Path(SERVER).read_bytes()).hexdigest(), 'profile': str(profile), 'markets': {k: str(v) for k,v in markets.items()}})
    print(json.dumps({'checks': checks, 'expectations_met': complete}, indent=2))


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-codex-plugins-'))
    print('Evidence directory:', root, flush=True)
    run(root)


if __name__ == '__main__':
    main()
