#!/usr/bin/env python3
"""Native plugin Skill context and MCP selection with an unavailable provider.

Every turn is explicitly interrupted after the local connection error. Native
rollout context proves preparation/exposure and explicit body injection only;
it does not prove a model received the context, used a method or passed a Gate.
No listener, external provider, plugin install or shared configuration is used.
"""
import hashlib
import json
from pathlib import Path
import sys
import tempfile
import time

from codex_plugin_cache_probe import BODY
from codex_skill_selection_probe import Stdio
from execution_config_probe import environment, verify_versions, write


def current_skill_context(records):
    """Apply the observed world-state patches, rather than search old history."""
    skills = None
    for record in records:
        if record.get('type') != 'world_state':
            continue
        payload = record['payload']
        if payload.get('full'):
            skills = None
        if 'host_skills' in payload.get('state', {}):
            patch = payload['state']['host_skills']
            if patch is None:
                skills = None
            elif isinstance(patch, dict):
                skills = {**(skills or {}), **patch}
            else:
                raise ValueError('unsupported native host_skills patch')
    if not isinstance(skills, dict) or not isinstance(skills.get('body'), str):
        raise ValueError('native current Skill context unavailable')
    return skills['body']


def run(root):
    env = environment(root)
    env['NETCAP_FIXTURE_KEY'] = 'fixture-no-model'
    cwd = root / 'work'
    cwd.mkdir()
    profile = Path(env['CODEX_HOME'])
    trace = root / 'mcp.jsonl'
    caches = {}

    def transport(marker):
        return {'command': sys.executable, 'args': ['-u', '-c', BODY.replace('MARKER', repr(marker)), str(trace)]}

    for version in ('one', 'two'):
        cache = profile / f'plugins/cache/netcap-{version}/fixture/1.0.0'
        caches[version] = cache
        write(cache / '.codex-plugin/plugin.json', {'name': 'fixture', 'version': '1.0.0'})
        write(cache / '.mcp.json', {'mcpServers': {f'plugin_{version}': transport(version)}})
        write(cache / 'skills/method/SKILL.md', f'---\nname: method\ndescription: W0208_PLUGIN_{version.upper()}_DESCRIPTION\n---\nW0208_PLUGIN_{version.upper()}_BODY\n')
    write(cwd / '.agents/skills/inherited/SKILL.md', '---\nname: inherited\ndescription: W0208_INHERITED_DESCRIPTION\n---\nW0208_INHERITED_BODY\n')
    unrelated = transport('unrelated')
    write(profile / 'config.toml', '''model="gpt-6-astra"
model_provider="fixture"
[model_providers.fixture]
name="Unavailable owned provider"
base_url="http://127.0.0.1:9/v1"
env_key="NETCAP_FIXTURE_KEY"
wire_api="responses"
requires_openai_auth=false
supports_websockets=false
request_max_retries=0
stream_max_retries=0
[features]
apps=false
plugins=true
plugin_sharing=false
shell_snapshot=false
''' + '\n[mcp_servers.unrelated]\ncommand=' + json.dumps(unrelated['command']) + '\nargs=' + json.dumps(unrelated['args']) + '\n')
    verify_versions(root, ['server'])
    base = {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture',
            'approvalPolicy': 'on-request', 'sandbox': 'read-only', 'historyMode': 'legacy'}
    config = profile / 'config.toml'
    original = config.read_bytes()
    cache_stamps = {str(p): hashlib.sha256(p.read_bytes()).hexdigest()
                    for cache in caches.values() for p in cache.rglob('*') if p.is_file()}
    clients, checks, cases, sessions = [], {}, {}, {}
    report = {'root': str(root), 'checks': checks, 'cases': cases, 'sessions': sessions,
              'scope': 'native Skill prepared context and explicit body injection, mixed MCP initialize/list; interrupted unavailable local provider, no successful model/tool/hook execution'}
    client, active = None, None
    completed = False

    def check(name, ok):
        checks[name] = bool(ok)
        if not ok:
            raise AssertionError(name)

    def overrides(version, enabled=True):
        return {f'plugins.fixture@netcap-{version}.enabled': enabled} if version else {}

    def start(label, version, enabled=True):
        result = client.call('thread/start', {**base, 'config': overrides(version, enabled)})
        sessions[label] = result['thread']['id']
        return sessions[label]

    def resume(label, sid, version, enabled=True, detach=True):
        if detach:
            reply = client.call('thread/unsubscribe', {'threadId': sid})
            check(label + '_detach_acknowledged', reply['status'] in ('unsubscribed', 'notSubscribed', 'notLoaded'))
        result = client.call('thread/resume', {**base, 'threadId': sid, 'config': overrides(version, enabled)})
        check(label + '_same_id', result['thread']['id'] == sid)

    def sample(label, sid, expected_version, explicit=None):
        nonlocal active
        inputs = [{'type': 'text', 'text': 'OWNED CONTEXT PROBE; do not execute tools; provider intentionally unavailable'}]
        if explicit:
            inputs.append({'type': 'skill', 'name': 'fixture:method', 'path': str(caches[explicit] / 'skills/method/SKILL.md')})
        turn = client.call('turn/start', {'threadId': sid, 'input': inputs})['turn']
        active = {'threadId': sid, 'turnId': turn['id']}
        deadline, attempted = time.monotonic() + 6, False
        while time.monotonic() < deadline:
            client.call('thread/read', {'threadId': sid, 'includeTurns': True})
            attempted = any(e.get('method') == 'error' and e.get('params', {}).get('threadId') == sid
                            and e['params'].get('turnId') == turn['id']
                            and e['params'].get('error', {}).get('codexErrorInfo', {}).get('responseStreamDisconnected') is not None
                            for e in client.events)
            if attempted:
                break
            time.sleep(.05)
        client.call('turn/interrupt', active)
        deadline, terminal = time.monotonic() + 5, None
        while time.monotonic() < deadline:
            thread = client.call('thread/read', {'threadId': sid, 'includeTurns': True})['thread']
            terminal = next((t for t in thread['turns'] if t['id'] == turn['id']), None)
            if terminal and terminal['status'] in ('failed', 'interrupted', 'completed'):
                break
            time.sleep(.05)
        check(label + '_unavailable_provider_observed', attempted)
        check(label + '_turn_interrupted', terminal and terminal['status'] == 'interrupted')
        active = None
        rollout = Path(thread['path'])
        check(label + '_owned_rollout', rollout.is_relative_to(profile))
        raw = rollout.read_bytes()
        records = [json.loads(line) for line in raw.splitlines()]
        header = records[0]
        check(label + '_native_header_matches', header['type'] == 'session_meta' and header['payload']['id'] == sid and header['payload']['cwd'] == str(cwd))
        context = current_skill_context(records)
        own_items = [r for r in records if r.get('type') == 'response_item'
                     and r['payload'].get('internal_chat_message_metadata_passthrough', {}).get('turn_id') == turn['id']]
        selected = [r for r in own_items if 'skills.selected_skill_instructions' in
                    r['payload'].get('internal_chat_message_metadata_passthrough', {}).get('content_item_kinds', [])]
        loaded = json.dumps(selected)
        rows = client.call('mcpServerStatus/list', {'threadId': sid, 'limit': 100})
        check(label + '_mcp_complete', rows['nextCursor'] is None)
        plugins = [r for r in rows['data'] if r['pluginId'] is not None]
        check(label + '_matching_mcp_plugin', len(plugins) == (1 if expected_version else 0) and
              (not plugins or plugins[0]['pluginId'] == f'fixture@netcap-{expected_version}' and
               plugins[0]['name'] == f'plugin_{expected_version}' and plugins[0]['runtimeStatus'] == 'connected' and
               plugins[0]['serverInfo']['version'] == expected_version))
        check(label + '_unrelated_mcp_preserved', any(r['name'] == 'unrelated' and r['pluginId'] is None and r['runtimeStatus'] == 'connected' for r in rows['data']))
        check(label + '_only_expected_description', all((f'W0208_PLUGIN_{v.upper()}_DESCRIPTION' in context) == (v == expected_version) for v in caches))
        check(label + '_inherited_description_preserved', 'W0208_INHERITED_DESCRIPTION' in context)
        check(label + '_description_does_not_include_body', '_BODY' not in context)
        check(label + '_selected_body_only_when_enabled', all((f'W0208_PLUGIN_{v.upper()}_BODY' in loaded) == (v == expected_version == explicit) for v in caches))
        if expected_version:
            check(label + '_context_uses_selected_cache_root', str(caches[expected_version].parent.parent) in context)
        write(root / (label + '-rollout.jsonl'), raw.decode())
        write(root / (label + '-context.json'), {'current_skill_context': context, 'selected_body_records': selected, 'mcp': rows})
        cases[label] = {'native_id': sid, 'turn_id': turn['id'], 'turn_status': terminal['status'],
                        'expected_version': expected_version, 'explicit_input': explicit,
                        'current_context_sha256': hashlib.sha256(context.encode()).hexdigest(),
                        'selected_body_records': len(selected), 'rollout_sha256': hashlib.sha256(raw).hexdigest(),
                        'history_turns': len(thread['turns']), 'model_delivery_verified': False, 'method_usage_verified': False}
        return records

    try:
        client = Stdio(root, env, 'initial')
        clients.append(client)
        a, b = start('a', 'one'), start('b', 'one')
        disabled, inherited = start('disabled', 'one', False), start('default', None)
        sample('a-initial', a, 'one')
        sample('b-initial', b, 'one')
        sample('disabled', disabled, None, explicit='one')
        sample('default', inherited, None)
        resume('hot-resume', a, 'two', detach=False)
        sample('hot-resume', a, 'one')
        resume('upgrade', a, 'two')
        upgraded = sample('upgrade', a, 'two')
        check('upgrade_keeps_old_description_in_history', 'W0208_PLUGIN_ONE_DESCRIPTION' in json.dumps(upgraded))
        sample('b-after-upgrade', b, 'one')
        resume('inherit', a, None)
        released = sample('inherit', a, None)
        check('inherit_keeps_old_descriptions_in_history', all(f'W0208_PLUGIN_{v.upper()}_DESCRIPTION' in json.dumps(released) for v in caches))
        resume('add', a, 'two')
        sample('add-explicit', a, 'two', explicit='two')
        resume('disable-after-body', a, 'two', enabled=False)
        disabled_body = sample('disable-after-body', a, None, explicit='two')
        check('disable_does_not_erase_previously_injected_body', 'W0208_PLUGIN_TWO_BODY' in json.dumps(disabled_body))
        directory = client.call('skills/list', {'cwds': [str(cwd)], 'forceReload': True})
        write(root / 'directory-skills.json', directory)
        check('directory_listing_is_not_thread_selection', not any(s.get('pluginId') for s in directory['data'][0]['skills']))
        client.close()
        client = Stdio(root, env, 'restored')
        clients.append(client)
        resume('cold-resume', b, 'one', detach=False)
        sample('cold-resume', b, 'one', explicit='one')
        check('cold_resume_keeps_prior_turns', cases['cold-resume']['history_turns'] == 3)
        check('profile_configuration_unchanged', config.read_bytes() == original)
        check('fixed_cache_bytes_unchanged', all(Path(p).is_file() and hashlib.sha256(Path(p).read_bytes()).hexdigest() == stamp for p, stamp in cache_stamps.items()))
        completed = True
    finally:
        if client and active:
            try:
                client.call('turn/interrupt', active)
            except Exception:
                pass
        for c in clients:
            if not c.log.closed:
                c.close()
        checks['owned_backends_exited_zero'] = bool(clients) and all(c.proc.returncode == 0 for c in clients)
        checks['no_direct_tools_or_global_writes'] = all(call['method'] not in
            ('mcpServer/tool/call', 'plugin/install', 'plugin/uninstall', 'config/value/write', 'config/batchWrite', 'config/mcpServer/reload')
            for c in clients for call in c.calls)
        checks['no_hook_or_tool_execution_events'] = all(not e.get('method', '').startswith('hook/') and
            e.get('params', {}).get('item', {}).get('type') not in ('commandExecution', 'mcpToolCall')
            for c in clients for e in c.events)
        checks['mcp_initialize_list_only'] = trace.is_file() and all(json.loads(line)['method'] in
            ('initialize', 'notifications/initialized', 'tools/list', 'resources/list', 'resources/templates/list')
            for line in trace.read_text().splitlines())
        report['expectations_met'] = completed and all(checks.values())
        report['processes'] = [{'label': c.label, 'pid': c.proc.pid, 'exit_code': c.proc.returncode} for c in clients]
        write(root / 'result.json', report)
    return report


if __name__ == '__main__':
    evidence = Path(tempfile.mkdtemp(prefix='netcap-w0208-plugin-context-'))
    print('Evidence directory:', evidence, flush=True)
    result = run(evidence)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    raise SystemExit(0 if result['expectations_met'] else 1)
