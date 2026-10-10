#!/usr/bin/env python3
"""Read native hook sources through the production bridge without invoking them."""
import json
from pathlib import Path
import tempfile
import uuid

from claude_bridge_smoke import Bridge
from claude_plugin_contract_probe import plugin, install
from execution_config_probe import environment, verify_versions, write


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-hook-catalog-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    env = environment(root)
    cwd = root / 'work'
    cwd.mkdir()
    plugin(root / 'marketplace/selected', 'w0208-selected')
    install(root, env, cwd)
    profile = Path(env['CLAUDE_CONFIG_DIR']) / 'settings.json'
    settings = json.loads(profile.read_text())
    settings['hooks'] = {'PreToolUse': [{'matcher': 'Read', 'hooks': [{'type': 'command', 'command': 'echo PRIVATE_HOOK_SECRET; exit 7'}]}]}
    write(profile, settings)
    write(cwd / '.claude/settings.json', {'hooks': {'PostToolUse': [{'matcher': 'Read', 'hooks': [{'type': 'command', 'command': 'echo PRIVATE_PROJECT_HOOK'}]}]}})
    config = {'native_id': str(uuid.uuid4()), 'cwd': str(cwd), 'executable': '/home/zzw/.local/bin/claude',
              'model': 'claude-sonnet-4-6', 'endpoint': 'http://127.0.0.1:1', 'permission': 'read_only', 'api_key': 'fixture-no-model'}
    config['source'] = config['native_id']
    checks, reports = {}, {}
    client = None
    def check(name, ok):
        checks[name] = bool(ok)
        assert ok, name
    try:
        client = Bridge(Path('bridges/claude/dist/main.js').resolve(), root, env)
        client.call('create', config)
        before = client.call('snapshot', {'native_id': config['native_id']})
        reports['catalog'] = client.call('hook_catalog', {'native_id': config['native_id']})
        catalog = reports['catalog']
        check('original_identity', catalog['config']['id'] == config['native_id'] and catalog['connection'] == before['connection'])
        inventory = catalog['inventory']
        check('native_hooks_listed', inventory['available'] and len(inventory['items']) == 3)
        check('user_hook_source', any(h['source'] == 'userSettings' and h['event'] == 'PreToolUse' for h in inventory['items']))
        check('project_hook_source', any(h['source'] == 'projectSettings' and h['event'] == 'PostToolUse' for h in inventory['items']))
        check('plugin_full_source_key', any(h['source'] == 'pluginHook' and h['plugin_name'] == 'w0208-selected@w0208-local' for h in inventory['items']))
        check('no_command_or_editable_secrets', 'PRIVATE_' not in json.dumps(catalog) and 'commandText' not in json.dumps(catalog) and 'editable' not in json.dumps(catalog))
        check('registered_not_disabled', all(not h['disabled'] for h in inventory['items']) and not inventory['policy']['all_disabled'])
        again = client.call('hook_catalog', {'native_id': config['native_id']})
        check('repeated_read_same_rows', again['inventory']['items'] == inventory['items'])
        after = client.call('snapshot', {'native_id': config['native_id']})
        check('query_history_and_configuration_unchanged', all(before[k] == after[k] for k in ['connection','entries','config','history']))
        check('zero_model_and_hook_execution', not any(e['kind'] == 'input' or e.get('event',{}).get('subtype') in ['hook_started','hook_response'] for e in after['entries']))
        disabled = dict(config, native_id=str(uuid.uuid4()))
        disabled['source'] = disabled['native_id']
        write(root / 'disable-hooks.json', {'disableAllHooks': True})
        disabled['settings_file'] = str(root / 'disable-hooks.json')
        client.call('create', disabled)
        reports['disabled'] = client.call('hook_catalog', {'native_id': disabled['native_id']})
        disabled_inventory = reports['disabled']['inventory']
        check('native_disabled_rows_and_policy', len(disabled_inventory['items']) == 3 and all(h['disabled'] for h in disabled_inventory['items']) and disabled_inventory['policy']['all_disabled'])
        client.close()
        client = Bridge(Path('bridges/claude/dist/main.js').resolve(), root, env)
        closed = client.call('snapshot', {'native_id': config['native_id']})
        rejected = False
        try:
            client.call('hook_catalog', {'native_id': config['native_id']})
        except AssertionError as error:
            rejected = '原生查询未开启' in str(error)
        check('closed_query_rejected', rejected)
        restored = client.call('snapshot', {'native_id': config['native_id']})
        check('read_does_not_restore_query', not restored['live'] and closed['entries'] == restored['entries'])
    finally:
        if client:
            client.close()
        write(root / 'result.json', {'checks': checks, 'reports': reports, 'scope': 'production bridge/Session/native hooks menu, no hook or model execution', 'expectations_met': len(checks) == 13 and all(checks.values())})
    print(json.dumps({'checks': checks, 'expectations_met': all(checks.values())}, indent=2))


if __name__ == '__main__':
    main()
