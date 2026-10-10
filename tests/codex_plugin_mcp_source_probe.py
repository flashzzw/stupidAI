#!/usr/bin/env python3
"""Owned Codex plugin MCP source/cache contracts; initialize/list only.

This exercises plugin installation/configuration in an isolated profile. It
does not use a model provider, call a tool, or establish a method Gate.
"""
import hashlib
import json
from pathlib import Path
import sys
import tempfile

from codex_skill_selection_probe import Stdio
from execution_config_probe import SERVER, environment, verify_versions, write


SERVER_BODY = '''import sys,json,os
from pathlib import Path
for line in sys.stdin:
    req=json.loads(line)
    with Path(sys.argv[1]).open('a') as f:
        f.write(json.dumps({'pid':os.getpid(),'method':req.get('method'),'version':sys.argv[2]})+'\\n')
    if 'id' not in req:continue
    method=req.get('method')
    if method=='initialize':
        meta={'file':str(Path(__file__).resolve()),'cwd':os.getcwd(),'plugin_env':{k:v for k,v in os.environ.items() if k in ('PLUGIN_ROOT','CLAUDE_PLUGIN_ROOT','CODEX_PLUGIN_ROOT')}}
        result={'protocolVersion':req['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'owned-plugin','version':sys.argv[2],'description':json.dumps(meta)}}
    elif method=='tools/list':result={'tools':[{'name':'identity','description':'Owned identity','inputSchema':{'type':'object','properties':{}}}]}
    elif method=='resources/list':result={'resources':[]}
    elif method=='resources/templates/list':result={'resourceTemplates':[]}
    else:raise RuntimeError('unexpected method '+str(method))
    print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}),flush=True)
'''


def file_manifest(root):
    return {str(p.relative_to(root)): {'sha256': hashlib.sha256(p.read_bytes()).hexdigest(),
                                     'mode': p.stat().st_mode & 0o777}
            for p in sorted(root.rglob('*')) if p.is_file()}


def run(root):
    env = environment(root)
    env['NETCAP_FIXTURE_KEY'] = 'fixture-no-model'
    cwd = root / 'work'
    cwd.mkdir()
    profile = Path(env['CODEX_HOME'])
    source = cwd / 'plugins/fixture'
    script, trace = source / 'server.py', root / 'mcp-trace.jsonl'
    write(script, SERVER_BODY)
    script.chmod(0o755)
    write(source / '.codex-plugin/plugin.json', {'name': 'fixture', 'version': '1.0.0'})
    source.chmod(0o700)
    (source / '.codex-plugin').chmod(0o700)

    def server(version):
        return {'command': sys.executable, 'args': ['-u', str(script), str(trace), version]}

    write(source / '.mcp.json', {'mcpServers': {'identity': server('1')}})
    control = server('configuration')
    write(profile / 'config.toml', '''model="gpt-6-astra"
model_provider="fixture"
[model_providers.fixture]
name="Unused local provider"
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
''' + '\n[mcp_servers.unrelated]\ncommand=' + json.dumps(control['command']) +
          '\nargs=' + json.dumps(control['args']) + '\n')
    market = cwd / '.agents/plugins/marketplace.json'
    write(market, {'name': 'owned', 'plugins': [{'name': 'fixture',
        'source': {'source': 'local', 'path': './plugins/fixture'},
        'policy': {'installation': 'AVAILABLE', 'authentication': 'ON_INSTALL'},
        'category': 'Productivity'}]})
    verify_versions(root, ['server'])
    checks, observations, manifests = {}, {}, {}
    client = None
    completed = False

    def check(name, ok):
        checks[name] = bool(ok)
        assert ok, name

    try:
        client = Stdio(root, env, 'native')

        def call(label, method, params):
            value = client.call(method, params)
            observations[label] = value
            return value

        def install(label):
            return call(label, 'plugin/install', {'pluginName': 'fixture', 'marketplacePath': str(market)})

        def start(label, overrides=None):
            value = call(label + '-start', 'thread/start', {'cwd': str(cwd), 'model': 'gpt-6-astra',
                'modelProvider': 'fixture', 'approvalPolicy': 'on-request', 'sandbox': 'read-only',
                'historyMode': 'legacy', 'config': overrides or {}})
            return inventory(label, value['thread']['id'])

        def inventory(label, native_id):
            rows = call(label + '-inventory', 'mcpServerStatus/list',
                        {'threadId': native_id, 'detail': 'toolsAndAuthOnly'})
            assert rows['nextCursor'] is None
            return {x['name']: x for x in rows['data']}

        install('install-v1')
        profile_before = (profile / 'config.toml').read_bytes()
        cache1 = profile / 'plugins/cache/owned/fixture/1.0.0'
        manifests['source-v1'], manifests['cache-v1'] = file_manifest(source), file_manifest(cache1)
        check('cache_files_and_executable_modes_match_source', manifests['source-v1'] == manifests['cache-v1'])
        check('source_and_cache_directory_modes_differ', (source / '.codex-plugin').stat().st_mode & 0o777 == 0o700 and (cache1 / '.codex-plugin').stat().st_mode & 0o777 == 0o755)
        on = start('enabled')
        check('plugin_connected_with_full_id', on['identity']['runtimeStatus'] == 'connected' and on['identity']['pluginId'] == 'fixture@owned')
        check('unrelated_configuration_preserved', on['unrelated']['runtimeStatus'] == 'connected' and on['unrelated']['pluginId'] is None)
        check('plugin_tools_discovered', set(on['identity']['tools']) == {'identity'})
        meta = json.loads(on['identity']['serverInfo']['description'])
        check('absolute_external_command_keeps_project_cwd', meta['file'] == str(script) and meta['cwd'] == str(cwd))
        check('mcp_does_not_receive_hook_plugin_root_environment', meta['plugin_env'] == {})
        off = start('disabled', {'plugins.fixture@owned.enabled': False})
        check('thread_override_excludes_plugin_mcp', 'identity' not in off)
        check('thread_override_preserves_unrelated_mcp', off['unrelated']['runtimeStatus'] == 'connected')
        first = inventory('enabled-still', observations['enabled-start']['thread']['id'])
        check('sibling_thread_remains_enabled', first['identity']['runtimeStatus'] == 'connected' and first['identity']['pluginId'] == 'fixture@owned')
        check('thread_selection_did_not_write_profile', (profile / 'config.toml').read_bytes() == profile_before)

        write(source / '.codex-plugin/plugin.json', {'name': 'fixture', 'version': '2.0.0'})
        write(source / '.mcp.json', {'mcpServers': {'identity': server('2')}})
        roster = call('source-v2-roster', 'plugin/installed', {'cwds': [str(cwd)]})
        summary = next(p for m in roster['marketplaces'] for p in m['plugins'] if p['id'] == 'fixture@owned')
        stale = start('source-v2-without-install')
        check('installed_local_version_is_source_preview', summary['localVersion'] == '2.0.0' and stale['identity']['serverInfo']['version'] == '1')
        check('source_update_alone_keeps_v1_cache', file_manifest(cache1) == manifests['cache-v1'])
        install('install-v2')
        upgraded = start('installed-v2')
        check('explicit_install_loads_v2_transport_definition', upgraded['identity']['serverInfo']['version'] == '2')
        cache2 = profile / 'plugins/cache/owned/fixture/2.0.0'
        manifests['cache-v2-before'] = file_manifest(cache2)
        write(source / '.mcp.json', {'mcpServers': {'identity': server('3')}})
        check('same_version_source_edit_does_not_change_cache', file_manifest(cache2) == manifests['cache-v2-before'])
        install('reinstall-same-v2')
        manifests['cache-v2-after'] = file_manifest(cache2)
        replaced = start('same-version-reinstalled')
        check('same_cache_path_can_change_transport_bytes', manifests['cache-v2-before']['.mcp.json'] != manifests['cache-v2-after']['.mcp.json'] and replaced['identity']['serverInfo']['version'] == '3')

        call('user-name-collision', 'config/value/write', {'keyPath': 'mcp_servers.identity',
             'value': server('user-collision'), 'mergeStrategy': 'replace'})
        collision = start('user-masks-plugin')
        check('user_definition_masks_same_name_plugin', collision['identity']['pluginId'] is None and collision['identity']['serverInfo']['version'] == 'user-collision')
        disabled_user = start('user-disabled', {'mcp_servers.identity.enabled': False})
        check('disabled_user_definition_does_not_fall_back_to_plugin', disabled_user['identity']['runtimeStatus'] == 'disabled' and disabled_user['identity']['pluginId'] is None)
        check('no_model_requests', not any(c['method'].startswith('turn/') for c in client.calls))
        rows = [json.loads(line) for line in trace.read_text().splitlines() if line.strip()]
        check('no_tool_invocations', not any(r['method'] == 'tools/call' for r in rows))
        completed = True
    finally:
        if client:
            client.close()
            checks['owned_native_process_exited_0'] = client.proc.returncode == 0
        write(root / 'result.json', {'root': str(root), 'checks': checks, 'observations': observations,
            'manifests': manifests, 'expectations_met': completed and all(checks.values()),
            'binary_sha256': hashlib.sha256(Path(SERVER).read_bytes()).hexdigest(),
            'scope': 'owned stdio plugin/user MCP initialize/list; source/cache metadata and thread overrides; no model/tool/method execution'})
    print(json.dumps({'checks': checks, 'expectations_met': completed and all(checks.values())}, indent=2))
    return completed and all(checks.values())


if __name__ == '__main__':
    evidence = Path(tempfile.mkdtemp(prefix='netcap-w0208-codex-plugin-mcp-sources-'))
    print('Evidence directory:', evidence, flush=True)
    raise SystemExit(0 if run(evidence) else 1)
