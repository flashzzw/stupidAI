#!/usr/bin/env python3
"""Fixed native per-turn plugin policy across Skill, MCP and a trusted hook.

The local provider is deliberately unavailable. Hook success is reported as an
observation, not assumed; every input is interrupted after a connection error.
No production configuration, shared daemon, installer or listener is used.
"""
import hashlib
import json
from pathlib import Path
import sys
import tempfile
import time

from codex_plugin_cache_probe import BODY
from codex_plugin_context_probe import current_skill_context
from codex_skill_selection_probe import Stdio
from execution_config_probe import environment, verify_versions, write


FIXTURE_ID = 'fixture@owned'
RETAINED_ID = 'retained@owned'


def run(root):
    env = environment(root)
    env['NETCAP_FIXTURE_KEY'] = 'fixture-no-model'
    cwd = root / 'work'
    cwd.mkdir()
    profile = Path(env['CODEX_HOME'])
    trace = root / 'mcp.jsonl'
    caches = {}
    for name in ('fixture', 'retained'):
        cache = profile / f'plugins/cache/owned/{name}/1.0.0'
        caches[name] = cache
        write(cache / '.codex-plugin/plugin.json', {'name': name, 'version': '1.0.0'})
        write(cache / 'skills/method/SKILL.md', f'---\nname: method\ndescription: POLICY_{name.upper()}_DESCRIPTION\n---\nPOLICY_{name.upper()}_BODY\n')
        transport = {'command': sys.executable, 'args': ['-u', '-c', BODY.replace('MARKER', repr(name)), str(trace)]}
        write(cache / '.mcp.json', {'mcpServers': {name + '_server': transport}})
    write(caches['fixture'] / 'hooks/hooks.json', {'hooks': {'SessionStart': [{'hooks': [
        {'type': 'command', 'command': '/usr/bin/true', 'timeout': 1},
    ]}]}})
    write(cwd / '.agents/skills/project/SKILL.md', '---\nname: project\ndescription: POLICY_PROJECT_DESCRIPTION\n---\nPOLICY_PROJECT_BODY\n')
    config = profile / 'config.toml'
    write(config, '''model="gpt-6-astra"
model_provider="fixture"
[model_providers.fixture]
name="Intentionally unavailable local policy probe"
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
codex_hooks=true
plugin_sharing=false
shell_snapshot=false
[plugins."fixture@owned"]
enabled=true
[plugins."retained@owned"]
enabled=true
''')
    verify_versions(root, ['server'])
    original = config.read_bytes()
    hashes = {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for cache in caches.values() for p in cache.rglob('*') if p.is_file()}
    report = {'root': str(root), 'checks': {}, 'cases': {}, 'sessions': {},
              'scope': 'native disabledPluginIds on turn/start and settings/update; mixed Skill/MCP/hook, unavailable local provider; no successful model or tool execution; hook outcomes reported separately'}
    checks, cases, sessions = report['checks'], report['cases'], report['sessions']
    clients, client, active = [], None, None
    finished = False
    base = {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture',
            'approvalPolicy': 'on-request', 'sandbox': 'read-only', 'historyMode': 'legacy'}

    def check(name, ok):
        checks[name] = bool(ok)
        if not ok:
            raise AssertionError(name)

    def inventory():
        return client.call('mcpServerStatus/list', {'threadId': sessions['b'], 'limit': 100})

    def saved_policy(label, sid, expected):
        # Read-only recovery evidence: settings acknowledgements are separately
        # persisted even before another turn. Never infer [] from missing data.
        thread = client.call('thread/read', {'threadId': sid})['thread']
        path = Path(thread['path'])
        check(label + '_policy_owned_path', thread['id'] == sid and path.is_relative_to(profile))
        deadline = time.monotonic() + 2
        samples = []
        while time.monotonic() < deadline:
            samples = []
            for line in path.read_text().splitlines():
                record = json.loads(line)
                payload = record.get('payload', {})
                if record['type'] == 'turn_context':
                    samples.append({'source': 'turn_context', 'turn_id': payload['turn_id'], 'disabled_ids': payload.get('disabled_plugin_ids')})
                elif record['type'] == 'event_msg' and payload.get('type') == 'thread_settings_applied':
                    if payload['thread_id'] != sid:
                        raise AssertionError('settings from another thread')
                    samples.append({'source': 'thread_settings_applied', 'disabled_ids': payload['thread_settings'].get('disabled_plugin_ids')})
            if samples and samples[-1]['disabled_ids'] is not None and sorted(samples[-1]['disabled_ids']) == sorted(expected):
                break
            time.sleep(.02)
        write(root / (label + '-policy.json'), {'native_id': sid, 'records': samples})
        check(label + '_read_only_saved_policy', bool(samples) and samples[-1]['disabled_ids'] is not None and sorted(samples[-1]['disabled_ids']) == sorted(expected))
        return samples[-1]

    def sample(label, sid, enabled, ids=None, first_hook=None):
        nonlocal active
        request = {'threadId': sid, 'input': [
            {'type': 'text', 'text': 'OWNED POLICY PROBE; do not execute tools; provider intentionally unavailable'},
            {'type': 'skill', 'name': 'fixture:method', 'path': str(caches['fixture'] / 'skills/method/SKILL.md')},
        ]}
        if ids is not None:
            request['disabledPluginIds'] = ids
        turn = client.call('turn/start', request)['turn']
        active = {'threadId': sid, 'turnId': turn['id']}
        deadline, attempted = time.monotonic() + 8, False
        while time.monotonic() < deadline:
            client.call('thread/read', {'threadId': sid, 'includeTurns': True})
            events = [e for e in client.events if e.get('params', {}).get('threadId') == sid and e['params'].get('turnId') == turn['id']]
            attempted = any(e.get('method') == 'error' and e['params'].get('error', {}).get('codexErrorInfo', {}).get('responseStreamDisconnected') is not None for e in events)
            if attempted:
                break
            time.sleep(.02)
        client.call('turn/interrupt', active)
        deadline, status = time.monotonic() + 5, None
        while time.monotonic() < deadline:
            thread = client.call('thread/read', {'threadId': sid, 'includeTurns': True})['thread']
            current = next((t for t in thread['turns'] if t['id'] == turn['id']), {})
            status = current.get('status')
            if status in ('interrupted', 'failed', 'completed'):
                break
            time.sleep(.02)
        check(label + '_provider_failure_observed', attempted)
        check(label + '_turn_interrupted', status == 'interrupted')
        active = None
        path = Path(thread['path'])
        check(label + '_owned_rollout', path.is_relative_to(profile))
        raw = path.read_text()
        records = [json.loads(line) for line in raw.splitlines()]
        check(label + '_native_header', records[0]['type'] == 'session_meta' and records[0]['payload']['id'] == sid and records[0]['payload']['cwd'] == str(cwd))
        context = current_skill_context(records)
        selected = [r for r in records if r.get('type') == 'response_item'
                    and r['payload'].get('internal_chat_message_metadata_passthrough', {}).get('turn_id') == turn['id']
                    and 'skills.selected_skill_instructions' in r['payload'].get('internal_chat_message_metadata_passthrough', {}).get('content_item_kinds', [])]
        loaded = json.dumps(selected)
        rows = client.call('mcpServerStatus/list', {'threadId': sid, 'limit': 100})
        check(label + '_mcp_complete', rows['nextCursor'] is None)
        check(label + '_fixture_description', ('POLICY_FIXTURE_DESCRIPTION' in context) == enabled)
        check(label + '_fixture_body', ('POLICY_FIXTURE_BODY' in loaded) == enabled)
        check(label + '_fixture_mcp', any(r['pluginId'] == FIXTURE_ID and r['runtimeStatus'] == 'connected' for r in rows['data']) == enabled)
        check(label + '_retained_plugin_stays_disabled', 'POLICY_RETAINED_DESCRIPTION' not in context and 'POLICY_RETAINED_BODY' not in loaded and not any(r['pluginId'] == RETAINED_ID for r in rows['data']))
        check(label + '_project_skill_preserved', 'POLICY_PROJECT_DESCRIPTION' in context)
        hook_events = [e for e in client.events if e.get('method', '').startswith('hook/') and e.get('params', {}).get('threadId') == sid and e['params'].get('turnId') == turn['id']]
        if first_hook is True:
            check(label + '_hook_reaches_terminal', sum(e['method'] == 'hook/started' for e in hook_events) == 1 and sum(e['method'] == 'hook/completed' for e in hook_events) == 1)
        elif first_hook is False:
            check(label + '_hook_excluded_before_provider', not hook_events)
        expected = [RETAINED_ID] if enabled else [RETAINED_ID, FIXTURE_ID]
        saved_policy(label, sid, expected)
        echo = client.call('thread/resume', {'threadId': sid, 'excludeTurns': True})
        check(label + '_saved_policy_matches', sorted(echo['disabledPluginIds']) == sorted(expected) and echo['thread']['id'] == sid)
        write(root / (label + '-rollout.jsonl'), raw)
        write(root / (label + '-context.json'), {'current_skill_context': context, 'body_records': selected, 'mcp': rows})
        cases[label] = {'native_id': sid, 'turn_id': turn['id'], 'turn_status': status, 'enabled': enabled,
                        'policy_argument': ids, 'saved_disabled_ids': echo['disabledPluginIds'], 'hook_events': hook_events,
                        'model_delivery_verified': False, 'method_usage_verified': False}
        return rows

    try:
        client = Stdio(root, env, 'initial')
        clients.append(client)
        discovered = client.call('hooks/list', {'cwds': [str(cwd)]})
        hooks = [h for h in discovered['data'][0]['hooks'] if h['pluginId'] == FIXTURE_ID]
        check('one_fixture_hook_discovered', len(hooks) == 1)
        hook = hooks[0]
        report['hook'] = hook
        trust = {'hooks.state': {hook['key']: {'enabled': True, 'trusted_hash': hook['currentHash']}}}
        for label in ('a', 'b'):
            started = client.call('thread/start', {**base, 'config': trust})
            sessions[label] = started['thread']['id']
            check(label + '_initial_policy_empty', started['disabledPluginIds'] == [])
        sample('enabled', sessions['a'], True, ids=[RETAINED_ID], first_hook=True)
        sample('disabled-first-turn', sessions['b'], False, ids=[RETAINED_ID, FIXTURE_ID], first_hook=False)
        sample('reenabled-same-id', sessions['b'], True, ids=[RETAINED_ID])
        client.call('thread/settings/update', {'threadId': sessions['b'], 'disabledPluginIds': [RETAINED_ID, FIXTURE_ID]})
        persisted = saved_policy('queued-before-turn', sessions['b'], [RETAINED_ID, FIXTURE_ID])
        check('settings_policy_persisted_before_resume_or_turn', persisted['source'] == 'thread_settings_applied')
        echo = client.call('thread/resume', {'threadId': sessions['b'], 'excludeTurns': True})
        before = inventory()
        write(root / 'queued-before-turn.json', {'echo': echo, 'mcp': before})
        check('settings_ack_saves_disabled_ids', sorted(echo['disabledPluginIds']) == sorted([RETAINED_ID, FIXTURE_ID]))
        check('settings_ack_does_not_replace_current_mcp_inventory', any(r['pluginId'] == FIXTURE_ID for r in before['data']))
        sample('queued-policy-next-turn', sessions['b'], False)
        # The saved policy also applies when config is explicitly supplied on
        # resume. Clear only the selected ID before resuming to reenable it.
        # Neither settings/update nor this control sequence sends model input.
        call_offset = len(client.calls)
        for label, enabled in [('resume-with-saved-disabled', False), ('resume-after-policy-clear', True)]:
            if enabled:
                client.call('thread/settings/update', {'threadId': sessions['b'], 'disabledPluginIds': [RETAINED_ID]})
                saved_policy('cleared-before-resume', sessions['b'], [RETAINED_ID])
            detached = client.call('thread/unsubscribe', {'threadId': sessions['b']})
            check(label + '_unsubscribed', detached['status'] == 'unsubscribed')
            resumed = client.call('thread/resume', {**base, 'threadId': sessions['b'], 'config': trust})
            rows = inventory()
            expected = [RETAINED_ID] if enabled else [RETAINED_ID, FIXTURE_ID]
            write(root / (label + '.json'), {'resumed': resumed, 'mcp': rows})
            check(label + '_same_id_and_saved_policy', resumed['thread']['id'] == sessions['b'] and sorted(resumed['disabledPluginIds']) == sorted(expected))
            check(label + '_mcp_complete', rows['nextCursor'] is None)
            check(label + '_fixture_mcp', any(r['pluginId'] == FIXTURE_ID and r['runtimeStatus'] == 'connected' for r in rows['data']) == enabled)
            check(label + '_retained_disabled', not any(r['pluginId'] == RETAINED_ID for r in rows['data']))
        check('resume_policy_change_no_input', not any(call['method'] in ('turn/start', 'turn/steer') for call in client.calls[call_offset:]))
        client.call('thread/settings/update', {'threadId': sessions['b'], 'disabledPluginIds': [RETAINED_ID, FIXTURE_ID]})
        sibling = client.call('mcpServerStatus/list', {'threadId': sessions['a'], 'limit': 100})
        check('sibling_thread_unchanged', any(r['pluginId'] == FIXTURE_ID for r in sibling['data']) and not any(r['pluginId'] == RETAINED_ID for r in sibling['data']))
        client.close()
        client = Stdio(root, env, 'restored')
        clients.append(client)
        for label, enabled in [('a', True), ('b', False)]:
            resumed = client.call('thread/resume', {**base, 'threadId': sessions[label], 'config': trust})
            check(label + '_cold_resume_keeps_id', resumed['thread']['id'] == sessions[label])
            check(label + '_cold_resume_keeps_policy', sorted(resumed['disabledPluginIds']) == sorted([RETAINED_ID] if enabled else [RETAINED_ID, FIXTURE_ID]))
            sample('restored-' + label, sessions[label], enabled)
        check('profile_unchanged', config.read_bytes() == original)
        check('cache_files_unchanged', all(Path(p).is_file() and hashlib.sha256(Path(p).read_bytes()).hexdigest() == stamp for p, stamp in hashes.items()))
        finished = True
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
        checks['no_install_or_global_config_writes'] = all(call['method'] not in ('plugin/install', 'plugin/uninstall', 'config/value/write', 'config/batchWrite', 'config/mcpServer/reload') for c in clients for call in c.calls)
        checks['mcp_initialize_list_only'] = trace.is_file() and all(json.loads(line)['method'] in ('initialize', 'notifications/initialized', 'tools/list', 'resources/list', 'resources/templates/list') for line in trace.read_text().splitlines())
        checks['no_direct_tool_calls'] = all(call['method'] not in ('mcpServer/tool/call', 'command/exec', 'thread/shellCommand') for c in clients for call in c.calls)
        report['expectations_met'] = finished and all(checks.values())
        report['processes'] = [{'label': c.label, 'pid': c.proc.pid, 'exit_code': c.proc.returncode} for c in clients]
        report['hook_success_observed'] = any(e['method'] == 'hook/completed' and e['params']['run']['status'] == 'completed' for case in cases.values() for e in case['hook_events'])
        write(root / 'result.json', report)
    return report


if __name__ == '__main__':
    evidence = Path(tempfile.mkdtemp(prefix='netcap-w0208-plugin-policy-'))
    print('Evidence directory:', evidence, flush=True)
    report = run(evidence)
    print(json.dumps(report, ensure_ascii=False, indent=2))
    raise SystemExit(0 if report['expectations_met'] else 1)
