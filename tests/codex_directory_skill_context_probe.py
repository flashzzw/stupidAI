#!/usr/bin/env python3
"""Fixed native directory Skill selection, prepared context and resume behavior.

Only owned stdio and an unavailable loopback provider are used. Every native
turn is interrupted. Prepared descriptions/body loads do not prove delivery,
method use or a Gate. No production configuration or listener is changed.
"""
import hashlib
import json
from pathlib import Path
import tempfile
import time

from codex_plugin_context_probe import current_skill_context
from codex_skill_selection_probe import Stdio, selection_overrides
from execution_config_probe import environment, verify_versions, write


def run(root):
    env = environment(root)
    env['NETCAP_FIXTURE_KEY'] = 'owned-unavailable'
    cwd = root / 'work'
    cwd.mkdir()
    profile = Path(env['CODEX_HOME'])
    paths = {
        'shared': Path(env['HOME']) / '.agents/skills/shared/SKILL.md',
        'retained': Path(env['HOME']) / '.agents/skills/retained/SKILL.md',
        'project': cwd / '.agents/skills/project/SKILL.md',
    }
    for label, path in paths.items():
        write(path, f'---\nname: directory-{label}\ndescription: DIRECTORY_{label.upper()}_DESCRIPTION\n---\nDIRECTORY_{label.upper()}_BODY\n')
    write(cwd / 'AGENTS.md', 'DIRECTORY_PROJECT_RULE: only inspect this owned fixture.\n')
    config = profile / 'config.toml'
    write(config, '''model="gpt-6-astra"
model_provider="fixture"
approval_policy="on-request"
sandbox_mode="read-only"
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
plugins=false
shell_snapshot=false
[[skills.config]]
path=''' + json.dumps(str(paths['retained'])) + '\nenabled=false\n')
    verify_versions(root, ['server'])
    original = config.read_bytes()
    hashes = {str(path): hashlib.sha256(path.read_bytes()).hexdigest() for path in paths.values()}
    report = {'root': str(root), 'checks': {}, 'cases': {}, 'sessions': {}, 'observations': {},
              'scope': 'directory Skill per-thread config; prepared descriptions and explicit body injection; interrupted unavailable local provider; no production selection, successful model/tool/hook or method Gate'}
    checks, cases, sessions = report['checks'], report['cases'], report['sessions']
    clients, client, active = [], None, None
    finished = False
    base = {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture',
            'approvalPolicy': 'on-request', 'sandbox': 'read-only', 'historyMode': 'legacy'}

    def check(name, ok):
        checks[name] = bool(ok)

    def resume(label, sid, config_override, detach=True):
        if detach:
            out = client.call('thread/unsubscribe', {'threadId': sid})
            check(label + '_detach_acknowledged', out['status'] in ('unsubscribed', 'notSubscribed', 'notLoaded'))
        params = {**base, 'threadId': sid}
        if config_override is not None:
            params['config'] = config_override
        out = client.call('thread/resume', params)
        check(label + '_same_id', out['thread']['id'] == sid)
        write(root / (label + '-resume.json'), out)

    def sample(label, sid, expected_shared, explicit=False, expected_project=True, explicit_source='shared'):
        nonlocal active
        inputs = [{'type': 'text', 'text': 'OWNED DIRECTORY SKILL PROBE; provider intentionally unavailable; do not execute tools'}]
        if explicit:
            inputs.append({'type': 'skill', 'name': 'directory-' + explicit_source, 'path': str(paths[explicit_source])})
        turn = client.call('turn/start', {'threadId': sid, 'input': inputs})['turn']
        active = {'threadId': sid, 'turnId': turn['id']}
        attempted, deadline = False, time.monotonic() + 6
        while time.monotonic() < deadline:
            client.call('thread/read', {'threadId': sid, 'includeTurns': True})
            attempted = any(event.get('method') == 'error' and event.get('params', {}).get('threadId') == sid
                            and event['params'].get('turnId') == turn['id']
                            and event['params'].get('error', {}).get('codexErrorInfo', {}).get('responseStreamDisconnected') is not None
                            for event in client.events)
            if attempted: break
            time.sleep(.03)
        client.call('turn/interrupt', active)
        status, deadline = None, time.monotonic() + 5
        while time.monotonic() < deadline:
            thread = client.call('thread/read', {'threadId': sid, 'includeTurns': True})['thread']
            status = next((item['status'] for item in thread['turns'] if item['id'] == turn['id']), None)
            if status in ('interrupted', 'failed', 'completed'): break
            time.sleep(.03)
        check(label + '_provider_failure_observed', attempted)
        check(label + '_turn_interrupted', status == 'interrupted')
        active = None
        path = Path(thread['path'])
        if not path.is_relative_to(profile): raise AssertionError('unowned rollout')
        raw = path.read_bytes()
        records = [json.loads(line) for line in raw.splitlines()]
        header = records[0]
        check(label + '_native_identity', header['type'] == 'session_meta' and header['payload']['id'] == sid
              and header['payload']['cwd'] == str(cwd) and header['payload']['cli_version'] == '0.158.0')
        context = current_skill_context(records)
        selected = [record for record in records if record.get('type') == 'response_item'
                    and record['payload'].get('internal_chat_message_metadata_passthrough', {}).get('turn_id') == turn['id']
                    and 'skills.selected_skill_instructions' in record['payload'].get('internal_chat_message_metadata_passthrough', {}).get('content_item_kinds', [])]
        body = json.dumps(selected)
        descriptions = {key: f'DIRECTORY_{key.upper()}_DESCRIPTION' in context for key in paths}
        loads = {key: f'DIRECTORY_{key.upper()}_BODY' in body for key in paths}
        check(label + '_unrelated_disabled_preserved', not descriptions['retained'])
        check(label + '_project_source_matches_selection', descriptions['project'] == expected_project)
        check(label + '_descriptions_do_not_contain_bodies', '_BODY' not in context)
        if expected_shared is not None:
            check(label + '_shared_description', descriptions['shared'] == expected_shared)
        if not explicit:
            check(label + '_no_unrequested_body', not any(loads.values()))
        cases[label] = {'native_id': sid, 'turn_id': turn['id'], 'status': status, 'explicit_input': explicit,
                        'explicit_source': explicit_source if explicit else None,
                        'descriptions': descriptions, 'loads': loads, 'history_turns': len(thread['turns']),
                        'rollout_sha256': hashlib.sha256(raw).hexdigest(), 'context_sha256': hashlib.sha256(context.encode()).hexdigest(),
                        'model_delivery_verified': False, 'method_usage_verified': False}
        write(root / (label + '-rollout.jsonl'), raw.decode())
        write(root / (label + '-context.json'), {'context': context, 'selected_body_records': selected})

    try:
        client = Stdio(root, env, 'initial')
        clients.append(client)
        native_config = client.call('config/read', {'cwd': str(cwd), 'includeLayers': False})['config']
        disabled = selection_overrides(native_config, paths['shared'], False)
        enabled = selection_overrides(native_config, paths['shared'], True)
        project_disabled = selection_overrides(native_config, paths['project'], False)
        check('overrides_preserve_other_disabled_source', all(any(item['path'] == str(paths['retained']) and not item['enabled'] for item in override['skills.config']) for override in [disabled, enabled, project_disabled]))
        for label, override in [('a', {}), ('b', disabled), ('c', {}), ('d', project_disabled)]:
            started = client.call('thread/start', {**base, 'config': override})
            sessions[label] = started['thread']['id']
            check(label + '_bound_cwd', started['cwd'] == str(cwd))
        sample('a-enabled', sessions['a'], True, explicit=True)
        sample('b-disabled', sessions['b'], False, explicit=True)
        sample('c-default-again', sessions['c'], True)
        sample('d-project-disabled', sessions['d'], True, explicit=True, expected_project=False, explicit_source='project')
        resume('a-hot-disable', sessions['a'], disabled, detach=False)
        sample('a-hot-disable', sessions['a'], None, explicit=True)
        resume('a-detached-disable', sessions['a'], disabled)
        sample('a-detached-disable', sessions['a'], False, explicit=True)
        resume('b-detached-enable', sessions['b'], enabled)
        sample('b-detached-enable', sessions['b'], True, explicit=True)
        sample('c-after-changes', sessions['c'], True)
        listing = client.call('skills/list', {'cwds': [str(cwd)], 'forceReload': True})
        write(root / 'directory-skills.json', listing)
        check('directory_listing_keeps_global_enable', any(item['path'] == str(paths['shared']) and item['enabled'] for item in listing['data'][0]['skills']))
        client.close()
        client = Stdio(root, env, 'restored')
        clients.append(client)
        resume('a-cold-without-override', sessions['a'], None, detach=False)
        sample('a-cold-without-override', sessions['a'], None, explicit=True)
        resume('a-cold-explicit-disable', sessions['a'], disabled)
        sample('a-cold-explicit-disable', sessions['a'], False, explicit=True)
        resume('b-cold-enable', sessions['b'], enabled, detach=False)
        sample('b-cold-enable', sessions['b'], True, explicit=True)
        resume('d-cold-project-disabled', sessions['d'], project_disabled, detach=False)
        sample('d-cold-project-disabled', sessions['d'], True, explicit=True, expected_project=False, explicit_source='project')
        report['observations'] = {
            'hot_resume_applies_disable': not cases['a-hot-disable']['descriptions']['shared'],
            'cold_resume_without_override_keeps_disable': not cases['a-cold-without-override']['descriptions']['shared'],
            'disabled_explicit_loads_body': cases['b-disabled']['loads']['shared'],
            'detached_disabled_explicit_loads_body': cases['a-detached-disable']['loads']['shared'],
        }

        check('hot_resume_does_not_apply_new_skill_config', not report['observations']['hot_resume_applies_disable'])
        check('cold_resume_requires_explicit_skill_config', not report['observations']['cold_resume_without_override_keeps_disable'])
        check('disabled_explicit_body_is_not_loaded', not report['observations']['disabled_explicit_loads_body'])
        check('detached_disabled_explicit_body_is_not_loaded', not report['observations']['detached_disabled_explicit_loads_body'])
        check('project_disabled_body_not_loaded', not cases['d-project-disabled']['loads']['project'] and not cases['d-cold-project-disabled']['loads']['project'])
        check('enabled_explicit_loads_body', cases['a-enabled']['loads']['shared'] and cases['b-detached-enable']['loads']['shared'] and cases['b-cold-enable']['loads']['shared'])
        check('a_cold_history_preserved', cases['a-cold-explicit-disable']['history_turns'] == 5)
        check('b_cold_history_preserved', cases['b-cold-enable']['history_turns'] == 3)
        check('d_cold_history_preserved', cases['d-cold-project-disabled']['history_turns'] == 2)
        check('profile_configuration_unchanged', config.read_bytes() == original)
        check('source_files_unchanged', all(Path(path).is_file() and hashlib.sha256(Path(path).read_bytes()).hexdigest() == digest for path, digest in hashes.items()))
        finished = True
    finally:
        if active and client:
            try: client.call('turn/interrupt', active)
            except Exception: pass
        for owned in clients:
            if not owned.log.closed: owned.close()
        report['processes'] = [{'label': owned.label, 'pid': owned.proc.pid, 'exit_code': owned.proc.returncode} for owned in clients]
        report['expectations_met'] = finished and all(checks.values())
        write(root / 'result.json', report)
    return report


if __name__ == '__main__':
    root = Path(tempfile.mkdtemp(prefix='netcap-w0207-directory-context-'))
    print('Evidence directory:', root, flush=True)
    result = run(root)
    print(json.dumps({'checks': result['checks'], 'observations': result['observations'], 'expectations_met': result['expectations_met']}, indent=2))
    raise SystemExit(0 if result['expectations_met'] else 1)
