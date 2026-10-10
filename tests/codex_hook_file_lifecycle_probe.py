#!/usr/bin/env python3
"""Measure inherited hook file refresh at native thread boundaries.

Uses owned user/project files, explicit thread-only trust, a timeout marker and
an unavailable loopback provider. Every submitted input is explicitly cancelled.
No production source adoption or successful hook/model execution is claimed.
"""
import hashlib
import json
from pathlib import Path
import shlex
import sys
import tempfile
import time

from codex_scoped_hook_selection_probe import BASE_CONFIG
from codex_skill_selection_probe import Stdio
from execution_config_probe import environment, verify_versions, write


def run(root):
    verify_versions(root, ['server'])
    env = environment(root)
    cwd = root / 'work'
    cwd.mkdir()
    profile = Path(env['CODEX_HOME'])
    marker = root / 'hook-inputs.jsonl'
    runner = root / 'marker.py'
    write(runner, 'import json,os,sys\nfrom pathlib import Path\n'
          'data=json.load(sys.stdin)\n'
          f'with Path({str(marker)!r}).open("a") as f:\n'
          ' f.write(json.dumps({"source":sys.argv[1],"version":sys.argv[2],"pid":os.getpid(),"input":data})+"\\n")\n'
          'os.execl("/usr/bin/sleep","sleep","2")\n')
    files = {'user': profile / 'hooks.json', 'project': cwd / '.codex/hooks.json'}
    versions = {}
    for version in ['v1', 'v2']:
        versions[version] = {}
        for source, path in files.items():
            command = 'python3 ' + shlex.quote(str(runner)) + ' ' + source + ' ' + version
            body = {'hooks': {'UserPromptSubmit': [{'hooks': [{'type': 'command', 'command': command, 'timeout': 1}]}]}}
            versions[version][source] = json.dumps(body, indent=2) + '\n'
    def install(version):
        for source, path in files.items():
            write(path, versions[version][source])
    install('v1')
    config = profile / 'config.toml'
    write(config, BASE_CONFIG + '\n[projects.' + json.dumps(str(cwd)) + ']\ntrust_level="trusted"\n')
    config_hash = hashlib.sha256(config.read_bytes()).hexdigest()
    runner_hash = hashlib.sha256(runner.read_bytes()).hexdigest()
    checks, samples, sessions, processes = {}, {}, {}, []
    report = {'root': str(root), 'checks': checks, 'samples': samples, 'sessions': sessions,
              'processes': processes, 'source_paths': {k: str(v) for k,v in files.items()},
              'source_sha256': {version: {source: hashlib.sha256(body.encode()).hexdigest() for source,body in rows.items()} for version,rows in versions.items()},
              'scope': 'owned fixed native stdio, explicit per-thread hook trust, timeout markers and cancelled unavailable-loopback-provider turns; file refresh contracts only'}
    client, active = None, None
    def check(name, value):
        checks[name] = bool(value)
    def close():
        nonlocal client
        if client is not None:
            client.close()
            processes.append({'label': client.label, 'pid': client.proc.pid, 'exit_code': client.proc.returncode})
            check(client.label + '_exited_zero', client.proc.returncode == 0)
            client = None
    def catalog(label):
        value = client.call('hooks/list', {'cwds': [str(cwd)]})
        samples[label] = value
        check(label + '_complete', len(value['data']) == 1 and value['data'][0]['cwd'] == str(cwd) and not value['data'][0]['warnings'] and not value['data'][0]['errors'])
        return value['data'][0]['hooks']
    def trust(hooks):
        return {'hooks.state': {h['key']: {'enabled': True, 'trusted_hash': h['currentHash']} for h in hooks}}
    base = {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture',
            'approvalPolicy': 'on-request', 'sandbox': 'read-only', 'historyMode': 'legacy'}
    def create(label, overrides):
        value = client.call('thread/start', {**base, 'config': overrides})
        sessions[label] = value['thread']['id']
        samples[label + '-start'] = value
        return sessions[label]
    def resume(label, sid, overrides=None):
        params = {**base, 'threadId': sid}
        if overrides is not None:
            params['config'] = overrides
        value = client.call('thread/resume', params)
        samples[label] = value
        check(label + '_same_id', value['thread']['id'] == sid)
    def detach(label, sid):
        samples[label] = client.call('thread/unsubscribe', {'threadId': sid})
        check(label + '_acknowledged', samples[label]['status'] in ['unsubscribed', 'notLoaded'])
    def turn(label, sid):
        nonlocal active
        prompt = 'OWNED FILE HOOK ' + label + '; provider intentionally unavailable'
        started = client.call('turn/start', {'threadId': sid, 'input': [{'type': 'text', 'text': prompt}]})['turn']
        active = {'threadId': sid, 'turnId': started['id']}
        provider_attempt = False
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            client.call('thread/read', {'threadId': sid, 'includeTurns': True})
            events = [e for e in client.events if e.get('params', {}).get('threadId') == sid and e.get('params', {}).get('turnId') == started['id']]
            provider_attempt = any(e.get('method') == 'error' and e['params'].get('error', {}).get('codexErrorInfo', {}).get('responseStreamDisconnected') is not None for e in events)
            if provider_attempt:
                break
            time.sleep(.05)
        client.call('turn/interrupt', active)
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            thread = client.call('thread/read', {'threadId': sid, 'includeTurns': True})['thread']
            final = next(t for t in thread['turns'] if t['id'] == started['id'])
            if final['status'] in ['interrupted','failed','completed']:
                break
            time.sleep(.05)
        assert final['status'] == 'interrupted', 'fixture input not explicitly cancelled'
        active = None
        events = [e for e in client.events if e.get('params', {}).get('threadId') == sid and e.get('params', {}).get('turnId') == started['id']]
        completed = [e['params']['run'] for e in events if e.get('method') == 'hook/completed']
        payloads = [json.loads(line) for line in marker.read_text().splitlines()] if marker.exists() else []
        payloads = [p for p in payloads if p['input'].get('session_id') == sid and p['input'].get('prompt') == prompt]
        samples[label] = {'thread_id': sid, 'turn_id': started['id'], 'events': events,
                          'completions': completed, 'payloads': payloads, 'thread': thread,
                          'provider_connection_error_observed': provider_attempt,
                          'actual_versions': sorted(p['source'] + ':' + p['version'] for p in payloads)}
        check(label + '_provider_attempt', provider_attempt)
        check(label + '_cancelled_no_model_result', final['status'] == 'interrupted' and not any(i['type'] in ['agentMessage','commandExecution','mcpToolCall'] for i in final['items']))
        check(label + '_exact_source_paths', sorted(h['sourcePath'] for h in completed) == sorted(str(files[p['source']]) for p in payloads))
        check(label + '_intentional_timeout_only', len(completed) == len(payloads) and all(h['status'] == 'failed' and h['durationMs'] >= 900 and {'kind':'error','text':'hook timed out after 1s'} in h['entries'] for h in completed))
        write(root / 'result.json', report)
        return samples[label]['actual_versions']
    try:
        client = Stdio(root, env, 'initial')
        v1 = catalog('catalog-v1')
        assert len(v1) == 2 and {h['source'] for h in v1} == {'user','project'}
        check('original_untrusted', all(h['trustStatus'] == 'untrusted' for h in v1))
        a = create('A', trust(v1))
        b = create('B', trust(v1))
        check('A-original_exact_v1', turn('A-v1', a) == ['project:v1','user:v1'])
        install('v2')
        v2 = catalog('catalog-v2')
        check('both_file_definitions_changed', len(v2) == 2 and {h['key'] for h in v2} == {h['key'] for h in v1} and all(h['currentHash'] != next(p['currentHash'] for p in v1 if p['key'] == h['key']) for h in v2))
        turn('A-file-change-no-resume', a)
        resume('A-hot-v2', a, trust(v2))
        turn('A-after-hot-resume', a)
        detach('A-detach-old-trust', a)
        resume('A-detached-stale-trust', a, trust(v1))
        turn('A-detached-old-trust', a)
        detach('A-detach', a)
        resume('A-detached-v2', a, trust(v2))
        turn('A-after-detached-resume', a)
        turn('B-still-v1-binding', b)
        before = client.call('thread/read', {'threadId': a, 'includeTurns': True})['thread']['turns']
        close()
        client = Stdio(root, env, 'cold')
        resume('A-cold-no-overrides', a)
        check('cold_history_preserved', client.call('thread/read', {'threadId': a, 'includeTurns': True})['thread']['turns'] == before)
        turn('A-cold-omitted-trust', a)
        detach('A-cold-detach', a)
        resume('A-cold-v2', a, trust(v2))
        turn('A-cold-v2-explicit', a)
        files['user'].unlink()
        catalog('catalog-user-deleted')
        turn('A-user-file-deleted', a)
        install('v1')
        catalog('catalog-original-restored')
        turn('A-files-restored-no-resume', a)
        detach('A-restore-detach', a)
        resume('A-original-binding', a, trust(v1))
        turn('A-original-explicit', a)
    finally:
        if client is not None and active is not None:
            try: client.call('turn/interrupt', active)
            except Exception: pass
        close()
        check('profile_config_unchanged', hashlib.sha256(config.read_bytes()).hexdigest() == config_hash)
        check('marker_script_unchanged', hashlib.sha256(runner.read_bytes()).hexdigest() == runner_hash)
        report['measurement_checks_passed'] = all(checks.values())
        expected = {
            'A-v1': ['project:v1','user:v1'],
            'A-file-change-no-resume': ['project:v1','user:v1'],
            'A-after-hot-resume': ['project:v1','user:v1'],
            'A-detached-old-trust': [],
            'A-after-detached-resume': ['project:v2','user:v2'],
            'B-still-v1-binding': ['project:v1','user:v1'],
            'A-cold-omitted-trust': [],
            'A-cold-v2-explicit': ['project:v2','user:v2'],
            'A-user-file-deleted': ['project:v2','user:v2'],
            'A-files-restored-no-resume': ['project:v2','user:v2'],
            'A-original-explicit': ['project:v1','user:v1'],
        }
        for label, versions in expected.items():
            check(label + '_verified_refresh_contract', samples.get(label, {}).get('actual_versions') == versions)
        report['expectations_met'] = all(checks.values())
        report['exploratory'] = False
        write(root / 'result.json', report)
    return report


if __name__ == '__main__':
    root = Path(tempfile.mkdtemp(prefix='netcap-w0209-hook-file-lifecycle-'))
    print('Evidence directory:', root, flush=True)
    report = run(root)
    print(json.dumps({'actual_versions': {k:v['actual_versions'] for k,v in report['samples'].items() if 'actual_versions' in v},
                      'expectations_met':report['expectations_met'], 'failed':[k for k,v in report['checks'].items() if not v]}, indent=2))
    sys.exit(0 if report['expectations_met'] else 1)
