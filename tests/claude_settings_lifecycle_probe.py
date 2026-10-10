#!/usr/bin/env python3
"""Read native settings during external edits and observe owned close hooks.

No user/model input. All settings, repositories, environments and hook scripts
belong to the fixture. Reads are raw fixed-version controls, not a public SDK
getSettings API. This probes the prerequisites of explicit settings adoption.
"""
import argparse
import hashlib
import json
from pathlib import Path
import shlex
import subprocess
import tempfile
import time

from claude_plugin_contract_probe import Native
from execution_config_probe import environment, verify_versions, write
from runtime.common import stop


def settings(case, version, config_hook=False):
    command = 'python3 ' + shlex.quote(str(case / 'hook.py'))
    return {'env': {'NETCAP_LIFECYCLE_VERSION': version},
            'permissions': {'deny': ['Read(./' + version + '-only/**)']},
            'hooks': {event: [{'hooks': [{'type': 'command',
                       'command': command + ' ' + version + ' ' + event,
                       'timeout': 3}]}] for event in ['SessionStart', 'SessionEnd'] + (['ConfigChange'] if config_hook else [])}}


def prepare(case, source, config_hook=False):
    env = environment(case)
    cwd = case / 'repo/nested'
    cwd.mkdir(parents=True)
    subprocess.run(['git', 'init', '--quiet', str(case / 'repo')],
                   env={**env, 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': '/dev/null'},
                   check=True, timeout=5, capture_output=True)
    path = {'project': cwd / '.claude/settings.json',
            'local': cwd / '.claude/settings.local.json',
            'primary': case / 'repo/.claude/settings.local.json',
            'custom': case / 'custom/original.json'}[source]
    write(case / 'hook.py', '''import json,os,sys
from pathlib import Path
raw=sys.stdin.read()
body=json.loads(raw) if raw else {}
with Path(__file__).with_name('hooks.jsonl').open('a') as f:
 f.write(json.dumps({'version':sys.argv[1],'event':sys.argv[2],
  'env':os.environ.get('NETCAP_LIFECYCLE_VERSION'), 'cwd':str(Path.cwd()),
  'session_id':body.get('session_id'),'reason':body.get('reason'),
  'stdin_bytes':len(raw.encode())})+'\\n')
''')
    write(path, settings(case, 'v1', config_hook))
    return env, cwd, path


def sample(native, reads='all'):
    methods = {'settings': 'get_settings', 'permissions': 'list_permission_rules', 'hooks': 'get_hooks_listing'}
    return {name: native.call(method) for name, method in methods.items() if reads in ['all', name]}


def marker(view):
    return view.get('settings', {}).get('effective', {}).get('env', {}).get('NETCAP_LIFECYCLE_VERSION')


def hooks(case):
    path = case / 'hooks.jsonl'
    return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []


def run_case(case, source, reads, config_hook):
    env, cwd, path = prepare(case, source, config_hook)
    native = Native(case, env, cwd, settings=str(path) if source == 'custom' else {})
    report = {'native_id': native.id, 'path': str(path), 'cwd': str(cwd), 'samples': [], 'reads_after_change': reads}
    try:
        native.call('initialize')
        report['before'] = sample(native)
        report['startup'] = hooks(case)
        write(path, settings(case, 'v2', config_hook))
        start = time.monotonic()
        # Observe both immediate read and a bounded watcher window. A negative
        # observation is not a claim that the file can never be hot-reloaded.
        for delay in [0, .2, 1, 2]:
            if delay:
                time.sleep(delay)
            report['samples'].append({'elapsed': time.monotonic() - start, 'view': sample(native, reads)})
        report['changed'] = report['samples'][-1]['view']
        report['before_close_hooks'] = hooks(case)
    finally:
        native.close()
        report['closed_hooks'] = hooks(case)
        report['exit_code'] = native.proc.returncode
        report['wire_error'] = native.read_error
        report['file_sha256'] = hashlib.sha256(path.read_bytes()).hexdigest()
        write(case / 'result.json', report)
    return report


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--case', action='append', choices=['project', 'local', 'primary', 'custom'])
    parser.add_argument('--session', action='store_true', help='probe existing production Session restart instead of raw control/SIGTERM')
    parser.add_argument('--reads', choices=['all', 'none', 'settings', 'permissions', 'hooks'], default='all', help='raw read controls after editing; initialization sample is unchanged')
    parser.add_argument('--wait-ms', type=int, default=3500, help='Session wait after external edit before hook read/apply')
    parser.add_argument('--config-hook', action='store_true', help='also observe fixture ConfigChange commands before explicit restart')
    parser.add_argument('--sdk-hook', choices=['allow', 'block'], help='probe a public SDK ConfigChange decision callback instead of the production Session')
    parser.add_argument('--guard-case', choices=['normal', 'disabled', 'managed-only-flag', 'callback-error', 'diagnostic-error', 'delete', 'replace', 'symlink', 'add'], default='normal', help='SDK guard boundary; managed-only is an injected flag, not an enterprise source')
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-settings-lifecycle-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    reports, checks, observations = {}, {}, {}
    for source in args.case or ['project', 'local', 'primary', 'custom']:
        case = root / source
        case.mkdir()
        if args.session or args.sdk_hook:
            env, cwd, path = prepare(case, source, args.config_hook)
            initial = settings(case, 'v1', args.config_hook)
            target = settings(case, 'v2', args.config_hook)
            if args.guard_case == 'disabled':
                initial['disableAllHooks'] = True
                target['disableAllHooks'] = False
                write(path, initial)
            if args.guard_case == 'symlink':
                actual = case / 'elsewhere/actual.json'
                write(actual, initial)
                path.unlink()
                path.symlink_to(actual)
            if args.guard_case == 'add':
                write(Path(env['CLAUDE_CONFIG_DIR']) / 'settings.json', initial)
                path.unlink()
                path.parent.rmdir()
            request = case / 'request.json'
            write(request, {'root': str(case), 'cwd': str(cwd), 'path': str(path),
                            'source': source, 'target': target, 'wait_ms': args.wait_ms, 'sdk_hook': args.sdk_hook, 'guard_case': args.guard_case})
            script = 'claude_settings_lifecycle_guard.mjs' if args.sdk_hook else 'claude_settings_lifecycle_session.mjs'
            proc = subprocess.Popen(['node', str(Path(__file__).with_name(script).resolve()), str(request)],
                                    cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    text=True, start_new_session=True)
            try:
                stdout, stderr = proc.communicate(timeout=45)
                write(case / 'session.stdout.log', stdout)
                write(case / 'session.stderr.log', stderr)
            finally:
                stop(proc)
            report = json.loads((case / 'result.json').read_text())
            report['runner_exit_code'] = proc.returncode
            reports[source] = report
            checks.update({source + '_' + k: v for k, v in report['checks'].items()})
            observations[source] = {'checks': report['checks'], 'markers': report['reports'].get('final_markers'),
                                    'runner_exit_code': proc.returncode, 'error': report['error']}
            print(source, json.dumps(observations[source]), flush=True)
            continue
        report = run_case(case, source, args.reads, args.config_hook)
        reports[source] = report
        wire = json.loads((case / 'wire.json').read_text())
        checks[source + '_initial_source_loaded'] = marker(report['before']) == 'v1'
        checks[source + '_startup_v1'] = any(h['version'] == 'v1' and h['event'] == 'SessionStart' for h in report['startup']) and any(e.get('subtype') == 'hook_response' and e.get('exit_code') == 0 and e.get('outcome') == 'success' and e.get('session_id') == report['native_id'] for e in wire['incoming'])
        checks[source + '_no_extra_startup_on_edit'] = [h for h in report['startup'] if h['event'] == 'SessionStart'] == [h for h in report['before_close_hooks'] if h['event'] == 'SessionStart']
        if args.config_hook:
            checks[source + '_no_config_hook_before_explicit_action'] = not any(h['event'] == 'ConfigChange' for h in report['before_close_hooks'])
        checks[source + '_only_control_requests'] = all(r['type'] == 'control_request' for r in wire['outgoing'])
        checks[source + '_no_model_result'] = all(r.get('type') != 'result' for r in wire['incoming'])
        checks[source + '_closed'] = report['exit_code'] is not None and not report['wire_error']
        # Test the prerequisite required by a simple close-and-reopen adoption
        # route. Failure is retained, not changed into a passing expectation.
        ends = [h for h in report['closed_hooks'] if h['event'] == 'SessionEnd']
        checks[source + '_close_uses_original_hook'] = len(ends) == 1 and ends[0]['version'] == 'v1'
        observations[source] = {'marker_before': marker(report['before']),
                                'markers_after': [marker(s['view']) for s in report['samples']],
                                'end_hooks': ends, 'exit_code': report['exit_code']}
        print(source, json.dumps(observations[source]), flush=True)
    result = {'checks': checks, 'observations': observations, 'reports': reports,
              'passed': all(checks.values()),
              'scope': 'public SDK ConfigChange callback' if args.sdk_hook else 'production Session lifecycle' if args.session else 'fixed raw CLI read-only controls during owned external settings edits; SIGTERM close hooks; no input or production adoption',
              'limitations': ['bounded watcher observation', 'raw SIGTERM and SDK close are separate cases',
                              'metadata is not tool permission execution', 'no managed or remote sources']}
    write(root / 'result.json', result)
    print(json.dumps({'checks': checks, 'passed': result['passed']}, indent=2))
    raise SystemExit(0 if result['passed'] else 1)


if __name__ == '__main__':
    main()
