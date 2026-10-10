#!/usr/bin/env python3
"""Compare file-backed portable hooks with a legacy control in owned profiles.

Only metadata reads and local fixture plugin installation are requested. There
are no thread/turn, hook, tool or model requests and no user profile changes.
"""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile

from codex_skill_selection_probe import Stdio
from codex_scoped_hook_selection_probe import BASE_CONFIG
from execution_config_probe import SERVER, environment, verify_versions, write


def hashes(root):
    return {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in sorted(root.rglob('*')) if p.is_file()}


def definitions(catalog):
    return [h for item in catalog['data'] for h in item['hooks']]


def catalog_matches(catalog, expected):
    actual = definitions(catalog)
    return (len(catalog['data']) == 1
            and not catalog['data'][0]['warnings'] and not catalog['data'][0]['errors']
            and len(actual) == len(expected)
            and {(h['pluginId'], h['sourcePath'], h['eventName'], h['command']) for h in actual} == set(expected)
            and all(h['source'] == 'plugin' and h['enabled'] and not h['async']
                    and h['trustStatus'] == 'untrusted' for h in actual))


def run(root):
    verify_versions(root, ['server'])
    report = {'root': str(root), 'groups': {}, 'processes': [], 'checks': {},
              'binary_sha256': hashlib.sha256(Path(SERVER).read_bytes()).hexdigest(),
              'scope': 'owned fixed-version metadata and fixture installation only; no thread, hook, model or tool execution'}
    checks = report['checks']

    def close(client, group, phase):
        client.close()
        report['processes'].append({'group': group.name, 'phase': phase,
                                   'pid': client.proc.pid, 'exit_code': client.proc.returncode})
        checks[group.name + '_' + phase + '_metadata_only'] = (
            all(call['method'] in {'initialize', 'config/read', 'hooks/list', 'plugin/read', 'plugin/install'} for call in client.calls)
            and not any(row.get('method', '').startswith(('hook/', 'thread/', 'turn/', 'item/')) for row in client.events))
        checks[group.name + '_' + phase + '_exited_zero'] = client.proc.returncode == 0
    for sharing in [False, True]:
        group = root / ('sharing-on' if sharing else 'sharing-off')
        group.mkdir()
        (group / 'work').mkdir()
        env = environment(group)
        profile = Path(env['CODEX_HOME'])
        config = BASE_CONFIG.replace('plugin_sharing=false', 'plugin_sharing=' + str(sharing).lower())
        entries, packages = [], {}
        for layout in ['legacy', 'root', 'inline', 'fallback']:
            name = 'hook-' + layout
            package = group / 'work/plugins' / name
            identity = {'$schema': 'https://agent-plugins.org/schemas/1.0.0/plugin.schema.json', 'name': name, 'version': '1.0.0'}
            overlay = {'name': name, 'version': '1.0.0'}
            path = 'hooks/hooks.json'
            if layout in ['inline', 'fallback']:
                path = 'hooks/selected.json'
                overlay['hooks'] = './' + path
                if layout == 'inline':
                    identity['extensions'] = {'com.openai': {'hooks': './' + path}}
                    overlay['hooks'] = './ignored.json'
            if layout != 'legacy': write(package / 'plugin.json', identity)
            if layout != 'root': write(package / '.codex-plugin/plugin.json', overlay)
            write(package / path, {'hooks': {'UserPromptSubmit': [{'hooks': [{'type': 'command', 'command': 'true', 'timeout': 1}]}]}})
            write(package / 'ignored.json', {'hooks': {'SessionStart': [{'hooks': [{'type': 'command', 'command': 'false'}]}]}})
            write(package / 'skills/method/SKILL.md', '---\nname: method\ndescription: Owned portable hook contract\n---\nNo method invocation requested.\n')
            cache = profile / 'plugins/cache/direct-hooks' / name / '1.0.0'
            cache.parent.mkdir(parents=True, exist_ok=True)
            shutil.copytree(package, cache)
            packages[layout] = {'name': name, 'source': str(package), 'cache': str(cache), 'path': path}
            entries.append({'name': name, 'source': {'source': 'local', 'path': './plugins/' + name}, 'policy': {'installation': 'AVAILABLE', 'authentication': 'ON_INSTALL'}, 'category': 'Productivity'})
            config += '\n[plugins.' + json.dumps(name + '@direct-hooks') + ']\nenabled=true\n'
        marketplace = group / 'work/.agents/plugins/marketplace.json'
        write(marketplace, {'name': 'installed-hooks', 'plugins': entries})
        write(profile / 'config.toml', config)
        result = {'packages': packages, 'source_hashes': hashes(group / 'work/plugins')}
        report['groups'][group.name] = result
        client = None
        try:
            client = Stdio(group, env, 'metadata')
            result['config'] = client.call('config/read', {'cwd': str(group / 'work'), 'includeLayers': False})
            result['direct'] = client.call('hooks/list', {'cwds': [str(group / 'work')]})
            result['previews'], result['installs'] = {}, {}
            for layout, package in packages.items():
                params = {'marketplacePath': str(marketplace), 'pluginName': package['name']}
                result['previews'][layout] = client.call('plugin/read', params)
                result['installs'][layout] = client.call('plugin/install', params)
            result['installed'] = client.call('hooks/list', {'cwds': [str(group / 'work')]})
        finally:
            if client:
                close(client, group, 'initial')
            db = profile / 'logs_2.sqlite'
            if db.exists():
                with sqlite3.connect('file:' + str(db) + '?mode=ro', uri=True) as connection:
                    result['diagnostics'] = [row[0] for row in connection.execute("select feedback_log_body from logs where feedback_log_body like '%hook%' or feedback_log_body like '%plugin%' order by id")]
            write(root / 'result.json', report)
        def hook(layout, market, path=None):
            package = packages[layout]
            path = path or package['path']
            return (package['name'] + '@' + market,
                    str(profile / 'plugins/cache' / market / package['name'] / '1.0.0' / path),
                    'sessionStart' if path == 'ignored.json' else 'userPromptSubmit',
                    'false' if path == 'ignored.json' else 'true')

        base = [hook('legacy', market) for market in ['direct-hooks', 'installed-hooks']]
        checks[group.name + '_direct_legacy_only'] = catalog_matches(result['direct'], base[:1])
        checks[group.name + '_installed_legacy_only'] = catalog_matches(result['installed'], base)
        checks[group.name + '_sharing_effective'] = result['config']['config']['features']['plugin_sharing'] is sharing
        checks[group.name + '_features_enabled'] = all(result['config']['config']['features'][key] is True for key in ['plugins', 'hooks'])
        for layout, preview in result['previews'].items():
            plugin = preview['plugin']
            checks[group.name + '_' + layout + '_recognized'] = (
                plugin['summary']['name'] == packages[layout]['name']
                and plugin['summary']['localVersion'] == '1.0.0' and len(plugin['skills']) == 1)
            checks[group.name + '_' + layout + '_preview'] = (len(plugin['hooks']) == (1 if layout == 'legacy' else 0))
            checks[group.name + '_' + layout + '_install'] = result['installs'][layout] == {'authPolicy': 'ON_INSTALL', 'appsNeedingAuth': []}

        # Change only the portable entry point in the owned direct-cache copies.
        # The same hook bytes, plugin IDs and config must become visible when
        # parsed via the legacy manifest, then disappear again on restoration.
        root_overlay = Path(packages['root']['cache']) / '.codex-plugin/plugin.json'
        write(root_overlay, {'name': 'hook-root', 'version': '1.0.0'})
        roots = {Path(p['cache']) / 'plugin.json': (Path(p['cache']) / 'plugin.json').read_bytes()
                 for layout, p in packages.items() if layout != 'legacy'}
        cache_before = hashes(profile / 'plugins/cache/direct-hooks')
        try:
            for phase in ['overlay_added', 'root_removed', 'root_restored']:
                if phase == 'root_removed':
                    for path in roots: path.unlink()
                elif phase == 'root_restored':
                    for path, body in roots.items(): path.write_bytes(body)
                client = Stdio(group, env, phase)
                try:
                    result[phase] = client.call('hooks/list', {'cwds': [str(group / 'work')]})
                finally:
                    close(client, group, phase)
                expected = base
                if phase == 'root_removed':
                    expected = base + [hook('root', 'direct-hooks'), hook('inline', 'direct-hooks', 'ignored.json'), hook('fallback', 'direct-hooks')]
                checks[group.name + '_' + phase + '_exact_catalog'] = catalog_matches(result[phase], expected)
        finally:
            for path, body in roots.items(): path.write_bytes(body)
        checks[group.name + '_cache_restored'] = cache_before == hashes(profile / 'plugins/cache/direct-hooks')
        root_overlay.unlink()
        checks[group.name + '_sources_unchanged'] = result['source_hashes'] == hashes(group / 'work/plugins')
        result['no_execution_requested'] = all(value for key, value in checks.items() if key.startswith(group.name) and key.endswith('_metadata_only'))
        write(root / 'result.json', report)
    report['portable_hooks_verified'] = all(
        all(any(h['pluginId'] == package['name'] + '@direct-hooks' for h in definitions(group['direct']))
            for layout, package in group['packages'].items() if layout != 'legacy')
        for group in report['groups'].values())
    report['expectations_met'] = bool(checks) and all(checks.values())
    report['interpretation'] = 'Fixed-version limitation regression; passing does not establish portable hook support.'
    write(root / 'result.json', report)
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--require-portable-hooks', action='store_true',
                        help='Exit nonzero unless portable hooks are discovered; useful as the original red signal.')
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-portable-hook-contract-'))
    print('Evidence directory:', root, flush=True)
    result = run(root)
    print(json.dumps({key: result[key] for key in ['checks', 'expectations_met', 'portable_hooks_verified', 'interpretation']}, indent=2))
    raise SystemExit(0 if (result['portable_hooks_verified'] if args.require_portable_hooks else result['expectations_met']) else 1)
