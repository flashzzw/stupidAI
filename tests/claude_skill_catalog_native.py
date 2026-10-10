#!/usr/bin/env python3
"""Read existing native Skill inventories without starting a replacement query."""
import json
from pathlib import Path
import tempfile
import uuid
from claude_bridge_smoke import Bridge
from execution_config_probe import environment, verify_versions, write


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-w0207-catalog-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    env = environment(root)
    cwd = root / 'work'
    cwd.mkdir()
    write(Path(env['CLAUDE_CONFIG_DIR']) / 'skills/user-key/SKILL.md', '---\nname: user-method\ndescription: user method description\n---\nPRIVATE_USER_BODY\n')
    write(cwd / '.claude/skills/project-key/SKILL.md', '---\nname: project-method\ndescription: project method description\n---\nPRIVATE_PROJECT_BODY\n')
    write(root / 'external/SKILL.md', '---\nname: external-method\ndescription: external method description\n---\nPRIVATE_PLUGIN_BODY\n')
    checks, reports = {}, {}
    client = None
    def check(name, ok):
        checks[name] = bool(ok)
        assert ok, name
    def config():
        native = str(uuid.uuid4())
        return {'native_id': native, 'source': native, 'cwd': str(cwd), 'executable': '/home/zzw/.local/bin/claude', 'model': 'claude-sonnet-4-6', 'endpoint': 'http://127.0.0.1:1', 'permission': 'read_only', 'api_key': 'fixture-no-model'}
    try:
        client = Bridge(Path('bridges/claude/dist/main.js').resolve(), root, env)
        first = config()
        client.call('create', first)
        before = client.call('snapshot', {'native_id': first['native_id']})
        catalog = client.call('skill_catalog', {'native_id': first['native_id']})
        reports['inherited'] = catalog
        check('catalog_original_id', catalog['config']['id'] == first['native_id'])
        check('inherited_without_explicit_selection', not catalog['config'].get('skill_selections') and catalog['inventory']['available'])
        check('user_source', any(s['name'] == 'user-method' and s['source'] == 'userSettings' for s in catalog['inventory']['descriptions']))
        check('project_source', any(s['name'] == 'project-method' and s['source'] == 'projectSettings' for s in catalog['inventory']['descriptions']))
        check('metadata_description', any(c['name'] == 'user-method' and c['description'] == 'user method description (user)' for c in catalog['inventory']['commands']))
        after = client.call('snapshot', {'native_id': first['native_id']})
        check('catalog_keeps_query_and_entries', before['connection'] == after['connection'] and before['entries'] == after['entries'] and before['config'] == after['config'])
        second = config()
        plugin = 'netcap-skill-' + 'a' * 24
        second['skill_selections'] = [{'asset_id': 'external', 'name': plugin + ':external-method', 'native_key': plugin + ':method', 'mode': 'enabled', 'source': 'plugin', 'source_path': str(root / 'external/SKILL.md'), 'discovery_path': str(root / 'bridge-state/skill-resources' / plugin / ('f' * 64) / 'skills/method/SKILL.md'), 'delivery': 'local_plugin', 'plugin_name': plugin, 'resource_root': str(root / 'external'), 'resource_kind': 'directory', 'resource_hash': 'f' * 64}]
        client.call('create', second)
        reports['selected'] = client.call('skill_catalog', {'native_id': second['native_id']})
        check('plugin_origin', any(s.get('plugin_name') == plugin and s['source'] == 'plugin' for s in reports['selected']['inventory']['descriptions']))
        check('plugin_aliases', any(plugin + ':method' in c['aliases'] for c in reports['selected']['inventory']['commands']))
        check('catalog_never_exports_body', 'PRIVATE_' not in json.dumps(reports))
        check('zero_model_inputs', all(not any(e['kind'] == 'input' for e in client.call('snapshot', {'native_id': c['native_id']})['entries']) for c in [first, second]))
        client.close()
        client = Bridge(Path('bridges/claude/dist/main.js').resolve(), root, env)
        closed = client.call('snapshot', {'native_id': first['native_id']})
        rejected = False
        try:
            client.call('skill_catalog', {'native_id': first['native_id']})
        except AssertionError as error:
            rejected = '原生查询未开启' in str(error)
        check('closed_query_unavailable', rejected)
        restored = client.call('snapshot', {'native_id': first['native_id']})
        check('catalog_does_not_open_query', not restored['live'] and closed['entries'] == restored['entries'])
        reports['closed'] = restored
    finally:
        if client:
            client.close()
        write(root / 'result.json', {'checks': checks, 'reports': reports, 'scope': 'native Session/SDK catalog read; no input or new query on catalog request'})
    print(json.dumps({'checks': checks, 'expectations_met': all(checks.values())}, indent=2))


if __name__ == '__main__':
    main()
