#!/usr/bin/env python3
"""Check per-thread plugin hook trust, exclusion, same-ID changes and cold restore.

All profiles, plugins, trust and markers belong to this fixture. Hooks write an
owned marker and deliberately exceed a one-second deadline. Invocation and
failure provenance are measured; successful hook/model execution is not claimed.
"""
import hashlib
import json
from pathlib import Path
import shutil
import tempfile
import time

from codex_skill_selection_probe import Stdio
from execution_config_probe import environment, verify_versions, write


BASE_CONFIG = '''model="gpt-6-astra"
model_provider="fixture"
[features]
apps=false
plugins=true
hooks=true
plugin_sharing=false
shell_snapshot=false
[model_providers.fixture]
name="Unavailable local scoped hook fixture"
base_url="http://127.0.0.1:9/v1"
wire_api="responses"
requires_openai_auth=false
supports_websockets=false
request_max_retries=0
stream_max_retries=0
'''


def hashes(root):
    return {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in sorted(root.rglob('*')) if p.is_file()}


def run(root):
    verify_versions(root, ['server'])
    env = environment(root)
    profile = Path(env['CODEX_HOME'])
    cwd = root / 'work'
    cwd.mkdir()
    marker = root / 'hook-inputs.jsonl'
    packages, ids = {}, {}
    for label, name, version in [('inherited', 'inherited', '1.0.0'),
                                 ('v1', 'fixture', '1.0.0'), ('v2', 'fixture', '2.0.0')]:
        package = root / 'source' / label
        write(package / '.codex-plugin/plugin.json', {'name': name, 'version': version})
        write(package / 'hooks/hooks.json', {'hooks': {'UserPromptSubmit': [{'hooks': [
            {'type': 'command', 'command': 'python3 ${PLUGIN_ROOT}/run.py', 'timeout': 1},
        ]}]}})
        write(package / 'run.py', 'import json,os,sys\n'
              'from pathlib import Path\n'
              'data=json.load(sys.stdin)\n'
              f'with Path({str(marker)!r}).open("a") as f:\n'
              f' f.write(json.dumps({{"label":{label!r},"pid":os.getpid(),"input":data}})+"\\n")\n'
              'os.execl("/usr/bin/sleep","sleep","2")\n')
        packages[label] = (package, name, version)
        stamp = hashlib.sha256(json.dumps(hashes(package), sort_keys=True).encode()).hexdigest()
        ids[label] = name + '@netcap-' + stamp[:32]

    inspection_ids = {label: key.replace('@netcap-', '@inspection-') for label, key in ids.items()}

    def materialize(target, identities):
        paths = {}
        for label, (package, name, version) in packages.items():
            market = identities[label].split('@', 1)[1]
            path = target / 'plugins/cache' / market / name / version
            shutil.copytree(package, path)
            paths[label] = path
        return paths

    checks, samples, sessions, clients = {}, {}, {}, []
    report = {'root': str(root), 'checks': checks, 'samples': samples, 'sessions': sessions,
              'processes': [], 'source_hashes': {k: hashes(v[0]) for k, v in packages.items()},
              'scope': 'owned native stdio; thread-only plugin copies and exact hook trust; '
                       'explicit timeout fixture, unavailable local provider, no production selection or successful execution'}
    client, active = None, None

    def check(name, value):
        checks[name] = bool(value)

    def open_client(base, environ, label):
        out = Stdio(base, environ, label)
        clients.append(out)
        return out

    def close_client():
        nonlocal client
        if client is not None:
            client.close()
            report['processes'].append({'label': client.label, 'exit_code': client.proc.returncode})
            check(client.label + '_exited_zero', client.proc.returncode == 0)
            client = None

    try:
        # Read immutable definitions elsewhere; never enable selected copies in
        # the execution profile just to discover their key/hash.
        inspector = root / 'inspection'
        inspector_env = environment(inspector)
        inspector_profile = Path(inspector_env['CODEX_HOME'])
        (inspector / 'work').mkdir(parents=True)
        materialize(inspector_profile, inspection_ids)
        write(inspector_profile / 'config.toml', BASE_CONFIG + ''.join(
            f'\n[plugins.{json.dumps(key)}]\nenabled=true\n' for key in inspection_ids.values()))
        client = open_client(inspector, inspector_env, 'metadata')
        catalog = client.call('hooks/list', {'cwds': [str(inspector / 'work')]})
        hooks = catalog['data'][0]['hooks']
        samples['isolated-package-metadata'] = catalog
        assert len(hooks) == 3, 'fixture package metadata incomplete'
        originals = {label: next(h for h in hooks if h['pluginId'] == ident) for label, ident in inspection_ids.items()}
        definitions = {label: {**h, 'key': ids[label] + h['key'][len(inspection_ids[label]):]}
                       for label, h in originals.items()}
        check('three_exact_keys_untrusted', len({h['key'] for h in hooks}) == 3 and
              all(h['trustStatus'] == 'untrusted' for h in hooks))
        check('same_handler_hash_different_full_package_versions', definitions['v1']['currentHash'] ==
              definitions['v2']['currentHash'] and ids['v1'] != ids['v2'])
        check('inspection_identity_not_execution_identity', all(originals[k]['key'] != definitions[k]['key']
              and originals[k]['key'].startswith(inspection_ids[k] + ':') for k in ids))
        samples['translated-trust-bindings'] = {label: {'inspection_key': originals[label]['key'],
            'execution_key': definitions[label]['key'], 'native_hash': definitions[label]['currentHash']}
            for label in ids}
        close_client()
        paths = materialize(profile, ids)
        inherited = definitions['inherited']
        inherited_state = {inherited['key']: {'enabled': True, 'trusted_hash': inherited['currentHash']}}
        config_path = profile / 'config.toml'
        write(config_path, BASE_CONFIG + f'\n[plugins.{json.dumps(ids["inherited"])}]\nenabled=true\n'
              f'\n[hooks.state.{json.dumps(inherited["key"])}]\nenabled=true\ntrusted_hash={json.dumps(inherited["currentHash"])}\n')
        original = config_path.read_bytes()
        cache_before = hashes(profile / 'plugins/cache')
        client = open_client(root, env, 'initial')
        samples['native-config'] = client.call('config/read', {'cwd': str(cwd), 'includeLayers': True})
        check('profile_hook_state_readable', samples['native-config']['config'].get('hooks', {}).get('state') == inherited_state)
        baseline = client.call('hooks/list', {'cwds': [str(cwd)]})
        samples['directory-baseline'] = baseline
        check('selected_caches_absent_from_profile_catalog', [h['pluginId'] for h in baseline['data'][0]['hooks']] == [ids['inherited']])
        check('inherited_translated_key_and_hash_match_native', baseline['data'][0]['hooks'][0]['key'] == inherited['key']
              and baseline['data'][0]['hooks'][0]['currentHash'] == inherited['currentHash']
              and baseline['data'][0]['hooks'][0]['trustStatus'] == 'trusted')

        def overrides(version, enabled=True, trusted=True, trust_version=None):
            state = dict(inherited_state)
            if trusted:
                h = definitions[trust_version or version]
                state[h['key']] = {'enabled': True, 'trusted_hash': h['currentHash']}
            return {'plugins.' + ids['v1'] + '.enabled': version == 'v1' and enabled,
                    'plugins.' + ids['v2'] + '.enabled': version == 'v2' and enabled,
                    'hooks.state': state}

        base = {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture',
                'approvalPolicy': 'on-request', 'sandbox': 'read-only', 'historyMode': 'legacy'}

        def create(label, config):
            result = client.call('thread/start', {**base, 'config': config})
            sessions[label] = result['thread']['id']
            samples[label + '-start'] = result
            check(label + '_no_hook_on_creation', not any(e.get('method', '').startswith('hook/') and
                  e.get('params', {}).get('threadId') == sessions[label] for e in client.events))
            return sessions[label]

        def turn(label, sid, expected):
            nonlocal active
            before_input = client.call('thread/read', {'threadId': sid, 'includeTurns': False})
            start = client.call('turn/start', {'threadId': sid, 'input': [{'type': 'text',
                'text': 'OWNED SCOPED HOOK ' + label + '; provider intentionally unavailable'}]})['turn']
            active = {'threadId': sid, 'turnId': start['id']}
            deadline = time.monotonic() + 8
            while time.monotonic() < deadline:
                client.call('thread/read', {'threadId': sid, 'includeTurns': True})
                events = [e for e in client.events if e.get('params', {}).get('threadId') == sid
                          and e.get('params', {}).get('turnId') == start['id']]
                completions = [e['params']['run'] for e in events if e.get('method') == 'hook/completed']
                provider_attempt = any(e.get('method') == 'error' and
                    e['params'].get('error', {}).get('codexErrorInfo', {}).get('responseStreamDisconnected') is not None
                    for e in events)
                if provider_attempt:
                    break
                time.sleep(.05)
            client.call('turn/interrupt', active)
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                history = client.call('thread/read', {'threadId': sid, 'includeTurns': True})['thread']
                final = next(t for t in history['turns'] if t['id'] == start['id'])
                if final['status'] in ('interrupted', 'failed', 'completed'):
                    break
                time.sleep(.05)
            assert final['status'] == 'interrupted', 'owned turn not cancelled'
            active = None
            payloads = [json.loads(line) for line in marker.read_text().splitlines()] if marker.exists() else []
            payloads = [p for p in payloads if p['input'].get('session_id') == sid and
                        p['input'].get('prompt', '') == 'OWNED SCOPED HOOK ' + label + '; provider intentionally unavailable']
            actual = sorted(p['label'] for p in payloads)
            samples[label] = {'thread_id': sid, 'turn_id': start['id'], 'events': events,
                              'completions': completions, 'marker_payloads': payloads,
                              'before_input_read': before_input,
                              'provider_connection_error_observed': provider_attempt,
                              'thread_read': history, 'expected_invocations': sorted(expected)}
            check(label + '_exact_hook_invocations', actual == sorted(expected))
            check(label + '_exact_native_source_paths', sorted(h['sourcePath'] for h in completions) ==
                  sorted(str(paths[v] / 'hooks/hooks.json') for v in expected))
            check(label + '_intentional_timeout_recorded', len(completions) == len(expected) and
                  all(h['status'] == 'failed' and h['source'] == 'plugin' and h['durationMs'] >= 900 and
                      {'kind': 'error', 'text': 'hook timed out after 1s'} in h['entries'] for h in completions))
            check(label + '_provider_attempt_after_hooks', provider_attempt)
            check(label + '_turn_cancelled_without_model_success', final['status'] == 'interrupted' and
                  not any(i.get('type') == 'agentMessage' for i in final.get('items', [])))
            check(label + '_execution_profile_unchanged', config_path.read_bytes() == original)

        a = create('A', overrides('v1'))
        b = create('B', overrides('v1', trusted=False))
        c = create('C', overrides('v1', enabled=False))
        d = create('D', overrides('v2', trust_version='v1'))
        wrong_namespace = overrides('v1', trusted=False)
        h = originals['v1']
        wrong_namespace['hooks.state'][h['key']] = {'enabled': True, 'trusted_hash': h['currentHash']}
        e = create('E', wrong_namespace)
        turn('A-trusted-v1', a, ['inherited', 'v1'])
        turn('B-untrusted-v1', b, ['inherited'])
        turn('C-disabled-v1', c, ['inherited'])
        turn('D-stale-v1-trust-for-v2', d, ['inherited'])
        turn('E-package-inspection-key-not-trust', e, ['inherited'])
        client.call('thread/settings/update', {'threadId': a, 'disabledPluginIds': [ids['v1']]})
        turn('A-live-policy-disabled', a, ['inherited'])
        client.call('thread/settings/update', {'threadId': a, 'disabledPluginIds': []})
        turn('A-live-policy-enabled', a, ['inherited', 'v1'])
        samples['directory-after-turns'] = client.call('hooks/list', {'cwds': [str(cwd)]})
        check('thread_trust_does_not_change_directory_catalog', samples['directory-after-turns'] == baseline)

        def resume(label, sid, config):
            params = {**base, 'threadId': sid}
            if config is not None:
                params['config'] = config
            result = client.call('thread/resume', params)
            samples[label + '-resume'] = result
            check(label + '_same_native_id', result['thread']['id'] == sid)

        resume('A-hot-untrust', a, overrides('v1', trusted=False))
        turn('A-hot-resume-keeps-old-trust', a, ['inherited', 'v1'])
        client.call('thread/unsubscribe', {'threadId': a})
        resume('A-disable', a, overrides('v1', enabled=False))
        turn('A-disabled-same-id', a, ['inherited'])
        client.call('thread/unsubscribe', {'threadId': a})
        resume('A-upgrade', a, overrides('v2'))
        turn('A-upgraded-v2-same-id', a, ['inherited', 'v2'])
        turn('B-still-untrusted', b, ['inherited'])
        close_client()
        client = open_client(root, env, 'cold')
        resume('A-cold-without-override', a, None)
        turn('A-cold-omission-loses-selection', a, ['inherited'])
        client.call('thread/unsubscribe', {'threadId': a})
        resume('A-cold', a, overrides('v2'))
        turn('A-cold-restored-v2', a, ['inherited', 'v2'])
        resume('B-cold', b, overrides('v1', trusted=False))
        turn('B-cold-still-untrusted', b, ['inherited'])
        check('all_plugin_cache_files_unchanged', hashes(profile / 'plugins/cache') == cache_before)
        check('all_source_packages_unchanged', {k: hashes(v[0]) for k, v in packages.items()} == report['source_hashes'])
        # Preserve exact stored settings for assessing input-before-execution
        # verification separately from the invocation evidence above.
        rollouts = sorted(profile.rglob('rollout-*.jsonl'))
        report['rollout_files'] = []
        for path in rollouts:
            target = root / 'rollout-evidence' / path.name
            write(target, path.read_text())
            report['rollout_files'].append(str(target))
    finally:
        if active and client:
            try:
                client.call('turn/interrupt', active)
            except Exception:
                pass
        close_client()
        check('no_execution_profile_writes_requested', not any(c['method'].startswith('config/') and
              c['method'] != 'config/read' for p in clients for c in p.calls))
        check('no_explicit_tool_or_command_requests', not any(c['method'] in
              ('command/exec', 'thread/shellCommand', 'mcpServer/tool/call') for p in clients for c in p.calls))
        report['passed'] = len(report['processes']) == 3 and all(checks.values())
        write(root / 'result.json', report)
    print(json.dumps({'checks': len(checks), 'failed': [k for k, v in checks.items() if not v],
                      'passed': report['passed']}, indent=2))
    return report['passed']


if __name__ == '__main__':
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-scoped-hooks-'))
    print('Evidence directory:', root, flush=True)
    raise SystemExit(0 if run(root) else 1)
