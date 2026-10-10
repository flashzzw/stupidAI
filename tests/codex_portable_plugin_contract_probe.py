#!/usr/bin/env python3
"""Compare portable plugin interpretation in owned caches and local installs.

Only isolated stdio, fixture MCP initialization/listing and local plugin files
are used. No model turn, hook or tool call is requested. Sharing on/off is a
controlled native feature comparison, never a change to a user profile.
"""
import hashlib
import json
from pathlib import Path
import shutil
import tempfile
import time

from codex_skill_selection_probe import Stdio
from execution_config_probe import environment, verify_versions, write


def run(root):
    verify_versions(root, ['server'])
    results = {'root': str(root), 'cases': {}, 'checks': {}, 'processes': [],
               'scope': 'owned 0.158.0 stdio; portable declarations versus actual preview/MCP metadata; no model, hook or tool execution'}
    for sharing in [False, True]:
        group = root / ('sharing-on' if sharing else 'sharing-off')
        group.mkdir()
        cwd = group / 'work'
        cwd.mkdir()
        env = environment(group)
        env['NETCAP_FIXTURE_KEY'] = 'owned-unavailable'
        profile = Path(env['CODEX_HOME'])
        write(profile / 'config.toml', '''model="gpt-6-astra"
model_provider="fixture"
approval_policy="on-request"
sandbox_mode="read-only"
[features]
plugins=true
apps=false
shell_snapshot=false
plugin_sharing=''' + str(sharing).lower() + '''
[model_providers.fixture]
name="Owned unavailable provider"
base_url="http://127.0.0.1:9/v1"
wire_api="responses"
env_key="NETCAP_FIXTURE_KEY"
requires_openai_auth=false
supports_websockets=false
''')
        script = group / 'mcp.py'
        write(script, '''import json,sys
for line in sys.stdin:
 r=json.loads(line)
 with open(sys.argv[1],'a') as f:f.write(json.dumps({'method':r.get('method')})+'\\n')
 if 'id' not in r:continue
 if r['method']=='initialize':v={'protocolVersion':r['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'owned','version':'1'}}
 elif r['method']=='tools/list':v={'tools':[{'name':'identity','inputSchema':{'type':'object','properties':{}}}]}
 elif r['method'] in ('resources/list','resources/templates/list'):v={('resources' if r['method']=='resources/list' else 'resourceTemplates'):[]}
 else:raise RuntimeError('unexpected execution')
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':v}),flush=True)
''')
        layouts = ['root', 'root-schema', 'root-complete', 'inline-empty', 'inline-schema', 'inline-defined', 'fallback']
        packages = {}
        market_plugins = []
        for layout in layouts:
            name = 'root-' + layout
            source = cwd / 'plugins' / name
            definition = lambda origin: {'mcpServers': {origin + '_' + layout.replace('-', '_'): {'type': 'stdio', 'command': 'python3', 'args': ['-u', str(script), str(group / 'mcp.jsonl')]}}}
            identity = {'name': name, 'version': '1.0.0'}
            overlay = {'name': 'overlay-' + layout, 'version': '2.0.0', 'mcpServers': './overlay-mcp.json', 'skills': './overlay-skills'}
            if layout in ['inline-empty', 'inline-schema']: identity['extensions'] = {'com.openai': {}}
            if layout == 'inline-defined': identity['extensions'] = {'com.openai': {'mcpServers': './inline-mcp.json', 'skills': './inline-skills'}}
            if layout in ['root-schema', 'root-complete', 'inline-schema', 'inline-defined', 'fallback']: identity['$schema'] = 'https://agent-plugins.org/schemas/1.0.0/plugin.schema.json'
            write(source / 'plugin.json', identity)
            if layout not in ['root', 'root-schema', 'root-complete']: write(source / '.codex-plugin/plugin.json', overlay)
            for filename, origin in [('mcp.json', 'canonical'), ('.mcp.json', 'legacy'), ('overlay-mcp.json', 'overlay'), ('inline-mcp.json', 'inline')]:
                config = definition(origin)
                if filename == 'mcp.json' and layout in ['root-complete', 'inline-schema', 'inline-defined', 'fallback']:
                    config['$schema'] = 'https://agent-plugins.org/schemas/1.0.0/mcp.schema.json'
                write(source / filename, config)
            for folder, label in [('skills', 'canonical'), ('overlay-skills', 'overlay'), ('inline-skills', 'inline')]:
                write(source / folder / 'method/SKILL.md', f'---\nname: {label}\ndescription: OWNED_{label.upper()}_{layout}\n---\nOwned fixture method.\n')
            cache = profile / 'plugins/cache/direct-contract' / name / '1.0.0'
            cache.parent.mkdir(parents=True, exist_ok=True)
            shutil.copytree(source, cache)
            packages[layout] = {'name': name, 'source': source, 'cache': cache}
            market_plugins.append({'name': name, 'source': {'source': 'local', 'path': './plugins/' + name}, 'policy': {'installation': 'AVAILABLE', 'authentication': 'ON_INSTALL'}, 'category': 'Productivity'})
        marketplace = cwd / '.agents/plugins/marketplace.json'
        write(marketplace, {'name': 'owned-contract', 'plugins': market_plugins})
        client = None
        try:
            client = Stdio(group, env, 'native')
            features = client.call('experimentalFeature/list', {})
            write(group / 'features.json', features)
            for layout, package in packages.items():
                name = package['name']
                case = {'name': name, 'sharing': sharing}
                results['cases'][group.name + '/' + layout] = case
                case['preview'] = client.call('plugin/read', {'marketplacePath': str(marketplace), 'pluginName': name})
                for mode in ['direct', 'installed']:
                    if mode == 'installed':
                        try:
                            case['install'] = client.call('plugin/install', {'marketplacePath': str(marketplace), 'pluginName': name})
                        except RuntimeError:
                            case['initial_install_error'] = client.calls[-1].get('error')
                            if layout != 'inline-empty': raise
                            overlay_path = package['source'] / '.codex-plugin/plugin.json'
                            overlay = json.loads(overlay_path.read_text())
                            overlay['name'] = name
                            write(overlay_path, overlay)
                            case['matching_overlay_name_preview'] = client.call('plugin/read', {'marketplacePath': str(marketplace), 'pluginName': name})
                            case['install'] = client.call('plugin/install', {'marketplacePath': str(marketplace), 'pluginName': name})
                        case['installed_cache_files'] = {str(p.relative_to(profile)): hashlib.sha256(p.read_bytes()).hexdigest() for p in (profile / 'plugins/cache/owned-contract' / name).rglob('*') if p.is_file()}
                    target = name + ('@direct-contract' if mode == 'direct' else '@owned-contract')
                    overrides = {'plugins.' + p['name'] + '@' + market + '.enabled': p['name'] + '@' + market == target
                                 for p in packages.values() for market in ['direct-contract', 'owned-contract']}
                    out = client.call('thread/start', {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture', 'approvalPolicy': 'on-request', 'sandbox': 'read-only', 'config': overrides})
                    sid = out['thread']['id']
                    deadline, inventory = time.monotonic() + 4, None
                    while time.monotonic() < deadline:
                        inventory = client.call('mcpServerStatus/list', {'threadId': sid, 'limit': 100})
                        if inventory['data'] and all(row.get('runtimeStatus') not in ('starting', 'pending') for row in inventory['data']): break
                        time.sleep(.05)
                    case[mode] = {'native_id': sid, 'inventory': inventory}
                    client.call('thread/unsubscribe', {'threadId': sid})
        finally:
            if client:
                client.close()
                results['processes'].append({'label': group.name, 'pid': client.proc.pid, 'exit_code': client.proc.returncode})
            write(root / 'result.json', results)
        trace = (group / 'mcp.jsonl').read_text() if (group / 'mcp.jsonl').exists() else ''
        results['checks'][group.name + '_no_model_or_tool_call'] = not any(call['method'] in ['turn/start', 'mcpServer/tool/call'] for call in client.calls) and 'tools/call' not in trace
        results['checks'][group.name + '_exited'] = client.proc.returncode == 0
        flag = next((item for item in features['data'] if item['name'] == 'plugin_sharing'), None)
        results['checks'][group.name + '_sharing_flag_observed'] = flag is not None and flag['enabled'] == sharing
        for layout in layouts:
            label = group.name + '/' + layout
            case = results['cases'][label]
            complete = layout in ['root-complete', 'inline-schema', 'inline-defined', 'fallback']
            expected = ('canonical_' if complete else 'legacy_' if layout == 'root' else 'overlay_') + layout.replace('-', '_')
            direct = [item['name'] for item in case['direct']['inventory']['data']]
            installed = [item['name'] for item in case['installed']['inventory']['data']]
            summary = case['preview']['plugin']['summary']
            results['checks'][label + '_actual_direct_inventory'] = direct == ([] if layout in ['root','root-schema'] else [expected])
            results['checks'][label + '_actual_installed_inventory'] = installed == ([] if layout == 'root-schema' else [expected])
            results['checks'][label + '_schema_selects_identity_version'] = summary['localVersion'] == (None if layout == 'root' else '2.0.0' if layout == 'inline-empty' else '1.0.0')
            results['checks'][label + '_preview_matches_metadata'] = case['preview']['plugin']['mcpServers'] == ([] if layout == 'root-schema' else [expected])
            results['checks'][label + '_canonical_mcp_matches_schema'] = any(name.startswith('canonical_') for name in direct + installed) == complete
            if layout == 'inline-empty':
                results['checks'][label + '_overlay_identity_checked_at_install'] = 'does not match marketplace plugin name' in case['initial_install_error']['message']
            case['portable_mcp_selection_verified'] = complete
    results['expectations_met'] = all(results['checks'].values())
    write(root / 'result.json', results)
    return results


if __name__ == '__main__':
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-portable-contract-'))
    print('Evidence directory:', root, flush=True)
    result = run(root)
    print(json.dumps({'checks': result['checks'], 'cases': {key: {mode: [row['name'] for row in value[mode]['inventory']['data']] for mode in ['direct', 'installed']} for key, value in result['cases'].items()}}, indent=2))
    raise SystemExit(0 if result['expectations_met'] else 1)
