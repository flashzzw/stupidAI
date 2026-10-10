#!/usr/bin/env python3
"""Owned native plugin cache/loaded-process boundary, without model requests.

The MCP server is embedded in the transport definition, so its implementation
has no dependency on an external source script. One explicit synthetic history
item persists the thread for cold resume; this is not model or tool history.
"""
import hashlib
import json
from pathlib import Path
import sys
import tempfile

from codex_skill_selection_probe import Stdio
from execution_config_probe import SERVER, environment, verify_versions, write


BODY = '''import sys,json,os
from pathlib import Path
marker=MARKER
for line in sys.stdin:
    req=json.loads(line)
    with Path(sys.argv[1]).open('a') as f:
        f.write(json.dumps({'pid':os.getpid(),'marker':marker,'method':req.get('method')})+'\\n')
    if 'id' not in req:continue
    method=req.get('method')
    if method=='initialize':
        result={'protocolVersion':req['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'owned-cache','version':marker}}
    elif method=='tools/list':result={'tools':[{'name':'identity','inputSchema':{'type':'object','properties':{}}}]}
    elif method=='resources/list':result={'resources':[]}
    elif method=='resources/templates/list':result={'resourceTemplates':[]}
    else:raise RuntimeError('unexpected method '+str(method))
    print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}),flush=True)
'''


def run(root):
    env = environment(root)
    env['NETCAP_FIXTURE_KEY'] = 'fixture-no-model'
    cwd = root / 'work'
    cwd.mkdir()
    profile = Path(env['CODEX_HOME'])
    source = cwd / 'plugins/fixture'
    cache = profile / 'plugins/cache/owned/fixture/1.0.0'
    trace = root / 'mcp.jsonl'

    def transport(marker):
        return {'command': sys.executable, 'args': ['-u', '-c', BODY.replace('MARKER', repr(marker)), str(trace)]}

    def definition(marker):
        return {'mcpServers': {'identity': transport(marker)}}

    write(source / '.codex-plugin/plugin.json', {'name': 'fixture', 'version': '1.0.0'})
    write(source / '.mcp.json', definition('original'))
    other = transport('unrelated')
    write(profile / 'config.toml', '''model="gpt-6-astra"
model_provider="fixture"
[model_providers.fixture]
name="Unused owned provider"
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
''' + '\n[mcp_servers.unrelated]\ncommand=' + json.dumps(other['command']) + '\nargs=' + json.dumps(other['args']) + '\n')
    market = cwd / '.agents/plugins/marketplace.json'
    write(market, {'name': 'owned', 'plugins': [{'name': 'fixture', 'source': {'source': 'local', 'path': './plugins/fixture'},
          'policy': {'installation': 'AVAILABLE', 'authentication': 'ON_INSTALL'}, 'category': 'Productivity'}]})
    verify_versions(root, ['server'])
    clients, observations, checks, stamps = [], {}, {}, {}
    client = None
    finished = False

    def check(name, ok):
        checks[name] = bool(ok)
        assert ok, name

    def call(label, method, params):
        value = client.call(method, params)
        observations[label] = value
        return value

    def inventory(label, sid):
        rows = call(label, 'mcpServerStatus/list', {'threadId': sid, 'detail': 'toolsAndAuthOnly'})
        assert rows['nextCursor'] is None
        return {x['name']: x for x in rows['data']}

    base = {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture', 'historyMode': 'legacy',
            'approvalPolicy': 'on-request', 'sandbox': 'read-only'}

    def start(label):
        result = call(label + '-start', 'thread/start', {**base, 'config': {}})
        sid = result['thread']['id']
        return sid, inventory(label + '-inventory', sid)

    def marker(rows):
        row = rows['identity']
        assert row['runtimeStatus'] == 'connected' and row['pluginId'] == 'fixture@owned'
        return row['serverInfo']['version']

    def stamp():
        return {str(p.relative_to(source)): hashlib.sha256(p.read_bytes()).hexdigest()
                for p in sorted(source.rglob('*')) if p.is_file()}

    try:
        client = Stdio(root, env, 'initial')
        clients.append(client)
        call('install', 'plugin/install', {'pluginName': 'fixture', 'marketplacePath': str(market)})
        original = (cache / '.mcp.json').read_bytes()
        profile_before = (profile / 'config.toml').read_bytes()
        stamps['source-before'] = stamp()
        stamps['cache-before'] = hashlib.sha256(original).hexdigest()
        sid, rows = start('original')
        check('original_embedded_definition_loaded', marker(rows) == 'original')
        call('synthetic-history', 'thread/inject_items', {'threadId': sid, 'items': [{'type': 'message', 'role': 'user',
             'content': [{'type': 'input_text', 'text': 'SYNTHETIC CACHE PROBE HISTORY; no model or tool execution'}]}]})
        write(cache / '.mcp.json', definition('cache-changed'))
        stamps['cache-changed'] = hashlib.sha256((cache / '.mcp.json').read_bytes()).hexdigest()
        check('source_and_profile_unchanged_after_cache_edit', stamp() == stamps['source-before'] and (profile / 'config.toml').read_bytes() == profile_before)
        check('cache_content_changed_under_same_path', stamps['cache-before'] != stamps['cache-changed'])
        check('running_thread_keeps_original_definition', marker(inventory('running-after-cache-edit', sid)) == 'original')
        prior_pids = {json.loads(line)['pid'] for line in trace.read_text().splitlines() if line.strip()}
        _, fresh = start('fresh-after-cache-edit')
        check('same_backend_new_thread_keeps_original_definition', marker(fresh) == 'original')
        new_initializations = [json.loads(line) for line in trace.read_text().splitlines() if line.strip()]
        new_initializations = [row for row in new_initializations if row['method'] == 'initialize' and row['pid'] not in prior_pids]
        observations['new-thread-initializations'] = new_initializations
        check('new_mcp_process_also_receives_original_definition', any(row['marker'] == 'original' for row in new_initializations))
        check('native_did_not_restore_cache_bytes', hashlib.sha256((cache / '.mcp.json').read_bytes()).hexdigest() == stamps['cache-changed'])
        roster = call('installed-after-cache-edit', 'plugin/installed', {'cwds': [str(cwd)]})
        summary = next(p for m in roster['marketplaces'] for p in m['plugins'] if p['id'] == 'fixture@owned')
        check('installed_metadata_keeps_source_version', summary['localVersion'] == '1.0.0' and summary['installed'] and summary['enabled'])
        client.close()
        client = Stdio(root, env, 'restored')
        clients.append(client)
        resumed = call('restored', 'thread/resume', {**base, 'threadId': sid, 'config': {}})
        check('cold_resume_retains_native_id', resumed['thread']['id'] == sid)
        check('cold_resume_loads_changed_cache', marker(inventory('restored-inventory', sid)) == 'cache-changed')
        history = Path(resumed['thread']['path'])
        check('synthetic_history_remains', history.is_relative_to(profile) and 'SYNTHETIC CACHE PROBE HISTORY' in history.read_text())
        # Restore the exact definition, while the resumed process still has its
        # previous configuration. A disk snapshot cannot describe both at once.
        (cache / '.mcp.json').write_bytes(original)
        check('running_resumed_thread_keeps_changed_definition', marker(inventory('restored-after-disk-rollback', sid)) == 'cache-changed')
        _, sibling_rows = start('sibling-after-rollback')
        check('same_backend_after_rollback_keeps_changed_definition', marker(sibling_rows) == 'cache-changed')
        client.close()
        client = Stdio(root, env, 'excluded')
        clients.append(client)
        excluded = call('excluded-resume', 'thread/resume', {**base, 'threadId': sid, 'config': {'plugins.fixture@owned.enabled': False}})
        off = inventory('excluded-inventory', sid)
        check('cold_resume_exclusion_retains_id', excluded['thread']['id'] == sid)
        check('cold_resume_excludes_plugin_mcp', 'identity' not in off)
        check('cold_resume_preserves_unrelated_mcp', off['unrelated']['runtimeStatus'] == 'connected' and off['unrelated']['pluginId'] is None)
        _, on = start('other-thread-enabled')
        check('other_thread_still_loads_plugin', marker(on) == 'original')
        check('excluded_thread_remains_excluded', 'identity' not in inventory('excluded-still', sid))
        stamps['source-after'] = stamp()
        check('source_profile_and_cache_restored', stamps['source-after'] == stamps['source-before'] and (profile / 'config.toml').read_bytes() == profile_before and (cache / '.mcp.json').read_bytes() == original)
        check('no_model_turns', all(not c['method'].startswith('turn/') for proc in clients for c in proc.calls))
        logs = [json.loads(line) for line in trace.read_text().splitlines() if line.strip()]
        check('no_tool_calls', all(row['method'] != 'tools/call' for row in logs))
        finished = True
    finally:
        if client:
            client.close()
        checks['owned_processes_exited_zero'] = bool(clients) and all(p.proc.returncode == 0 for p in clients)
        write(root / 'result.json', {'root': str(root), 'checks': checks, 'observations': observations, 'stamps': stamps,
              'expectations_met': finished and all(checks.values()), 'binary_sha256': hashlib.sha256(Path(SERVER).read_bytes()).hexdigest(),
              'scope': 'native stdio cache mutation/active instances/cold resume and exclusion; synthetic persisted history; no model/tool calls; no production selection implementation'})
    print(json.dumps({'checks': checks, 'expectations_met': finished and all(checks.values())}, indent=2))
    return finished and all(checks.values())


if __name__ == '__main__':
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-codex-cache-'))
    print('Evidence directory:', root, flush=True)
    raise SystemExit(0 if run(root) else 1)
