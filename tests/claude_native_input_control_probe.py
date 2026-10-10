#!/usr/bin/env python3
"""Native attach input/approval sharing and production readonly boundaries.

All sessions, tools, PTYs and providers belong to a fresh filesystem fixture.
The result separates observed protocol facts from the unproven exclusive
input contract; successful observation never grants production control.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import sys
import tempfile
import time
import traceback

from claude_skill_method_probe import SkillProvider, strings
from claude_worker_retry_probe import ObservationConsole
from execution_config_probe import verify_versions, write
from workbench_boundary_probe import ClaudeProfile
from runtime.common import wait_for
from wsl_live_control_probe import identity, alive

REPO = Path(__file__).resolve().parents[1]


def last_marker(payload, marker):
    messages = payload.get('messages', [])
    if not messages or messages[-1].get('role') != 'user':
        return False
    content = messages[-1].get('content')
    parts = content if isinstance(content, list) else [{'text': content}]
    return any(isinstance(p, dict) and isinstance(p.get('text'), str) and p['text'].strip() == marker for p in parts)


class InputProvider(SkillProvider):
    def __init__(self, root, route):
        self.cwd, self.results = root / 'work', {}
        super().__init__(root, route)

    def plan(self, payload):
        messages = payload.get('messages', [])
        calls = {}
        for message in messages:
            for block in message.get('content', []) if isinstance(message.get('content'), list) else []:
                if block.get('type') == 'tool_use' and block.get('name') == 'Bash':
                    match = re.fullmatch(r'OWNED_NATIVE_INPUT_(deny|run)', block.get('input', {}).get('description', ''))
                    if match:
                        calls[block['id']] = match[1]
                if block.get('type') == 'tool_result' and block.get('tool_use_id') in calls:
                    self.results[calls[block['tool_use_id']]] = block
        phases = [(n, re.search(r'NETCAP_NATIVE_INPUT=([a-z_]+)', strings(m.get('content'))))
                  for n, m in enumerate(messages) if m.get('role') == 'user']
        phases = [(n, match.group(1)) for n, match in phases if match]
        if not phases:
            return {'text': 'No owned input-control task.'}
        _, phase = phases[-1]
        if phase not in ['deny', 'run']:
            return {'text': 'NATIVE_INPUT_' + phase + '_DONE'}
        if phase in self.results:
            return {'text': 'NATIVE_INPUT_' + phase + '_RETURNED'}
        return {'tool': 'Bash', 'input': {'command': shlex.join(['/usr/bin/python3', str(self.cwd / 'owned_tool.py'), phase]),
                                        'description': 'OWNED_NATIVE_INPUT_' + phase, 'timeout': 120000}}


def run(root):
    checks, samples, frames, writes, processes = {}, {}, {}, [], {}
    result = {'root': str(root), 'checks': checks, 'samples': samples, 'frames': frames, 'writes': writes,
              'process_samples': processes,
              'scope': 'real native supervisor and PTYs; owned policy mount and local providers; production readonly Handler',
              'input_exclusivity': 'unverified', 'goal_acceptance_met': False}
    profiles, app, tool = {}, None, None

    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    def send(terminal, data, label):
        record = {'label': label, 'pid': terminal.proc.pid, 'data_base64': base64.b64encode(data.encode()).decode(), 'at': time.time()}
        writes.append(record)
        terminal.send(data)
        record['written'] = True

    def prompt(terminal, phase):
        send(terminal, 'NETCAP_NATIVE_INPUT=' + phase, phase + '-text')
        terminal.drain(.3)
        send(terminal, '\r', phase + '-enter')

    def requests(profile, phase):
        return [r for r in profile.provider.requests if last_marker(r['body'], 'NETCAP_NATIVE_INPUT=' + phase)]

    def roster(profile, tag):
        rows = json.loads(profile.cli(['agents', '--json', '--all'], tag))
        assert len(rows) == 1, 'owned profile has another native job'
        return rows[0]

    def capture_processes(stage):
        observed = {}
        for name in ['worker', 'sibling_worker', 'first_attach', 'second_attach', 'console', 'tool']:
            if name not in samples:
                continue
            expected = samples[name]
            try:
                current = identity(expected['pid'])
            except (FileNotFoundError, ProcessLookupError):
                current = None
            observed[name] = {'expected': expected, 'observed': current}
        processes[stage] = {'at': time.time(), 'processes': observed}

    try:
        check('owned_system_policy_mount', os.path.samefile(root / 'policy', '/etc/claude-code'))
        artifacts = [Path(__file__).resolve(), REPO / '.dev/bin/netcap']
        if (REPO / 'bin/netcap').is_file():
            artifacts.append(REPO / 'bin/netcap')
        result['artifacts'] = {str(path.relative_to(REPO)): hashlib.sha256(path.read_bytes()).hexdigest()
                               for path in artifacts}
        verify_versions(root, ['claude'])
        for label in ['A', 'B']:
            profile = ClaudeProfile(root / label, label, provider_factory=InputProvider)
            profiles[label] = profile
            write(profile.root / 'key.py', "import json,os,time\nfrom pathlib import Path\nwith Path(__file__).with_name('helper-calls.jsonl').open('a') as f:f.write(json.dumps({'pid':os.getpid(),'ppid':os.getppid(),'at':time.time()})+'\\n')\nprint('fixture-" + label + "')\n")
            write(profile.cwd / 'owned_tool.py', '''import json,os,sys,time
from pathlib import Path
root=Path(__file__).parent;phase=sys.argv[1]
with (root/'starts.jsonl').open('a') as f:f.write(json.dumps({'pid':os.getpid(),'phase':phase,'start_ticks':Path('/proc/self/stat').read_text().rsplit(')',1)[1].split()[19]})+'\\n')
deadline=time.monotonic()+110
while phase=='run' and not (root/'release').exists():
 if time.monotonic()>deadline:raise TimeoutError('owned tool was not released')
 time.sleep(.05)
value={'marker':'OWNED_NATIVE_TOOL_OUTPUT','phase':phase,'pid':os.getpid(),'cwd':os.getcwd(),'uid':os.getuid()}
(root/'tool-output.json').write_text(json.dumps(value))
print(json.dumps(value),flush=True)
''')
            profile.cli(['--bg', 'NETCAP_NATIVE_INPUT=seed_' + label.lower(), '--permission-mode', 'default'], 'launch')
            wait_for(lambda: requests(profile, 'seed_' + label.lower()), 'original seed model input')
            wait_for(lambda: roster(profile, 'seed-idle').get('status') == 'idle', 'known completed seed')
        a, b = profiles['A'], profiles['B']
        initial, sibling = roster(a, 'original'), roster(b, 'original')
        worker, sibling_worker = identity(initial['pid']), identity(sibling['pid'])
        samples.update(original=initial, sibling=sibling, worker=worker, sibling_worker=sibling_worker)
        native = initial['sessionId']

        def native_tool_result(phase):
            paths = list((Path(a.env['CLAUDE_CONFIG_DIR']) / 'projects').glob('*/' + native + '.jsonl'))
            if len(paths) != 1:
                return None
            # The native writer can be appending its final line while sampled.
            rows = [json.loads(line) for line in paths[0].read_text().splitlines(keepends=True)
                    if line.endswith('\n')]
            calls, replies = {}, []
            for row in rows:
                blocks = row.get('message', {}).get('content', [])
                for block in blocks if isinstance(blocks, list) else []:
                    if (block.get('type') == 'tool_use' and block.get('name') == 'Bash' and
                            block.get('input', {}).get('description') == 'OWNED_NATIVE_INPUT_' + phase):
                        calls[block['id']] = row['uuid']
                    if block.get('type') == 'tool_result' and block.get('tool_use_id') in calls:
                        replies.append({'record_uuid': row['uuid'], 'call_uuid': calls[block['tool_use_id']],
                                        'native_id': row.get('sessionId'), 'result': block})
            assert len(calls) <= 1 and len(replies) <= 1, 'native tool was replayed'
            return replies[0] if replies else None

        one = a.attach(initial['id'], 'first-attach')
        one.until(lambda: 'NETCAP_NATIVE_INPUT=seed_a' in one.text(), 'first original view')
        # A native selection failure is evidence, not an attach timeout. The
        # experiment may use the roster's short selector only after recording
        # the exact full-ID outcome and rechecking the original identity.
        full_id_view = a.attach(native, 'full-id-attach')
        full_id_view.until(lambda: full_id_view.proc.poll() is not None or
                           'NETCAP_NATIVE_INPUT=seed_a' in full_id_view.text(), 'full-ID selection outcome')
        full_id_supported = full_id_view.proc.poll() is None
        samples['full_id_attach'] = {'selector': native, 'supported': full_id_supported,
                                     'exit_code': full_id_view.proc.poll(), 'output': full_id_view.text()}
        if full_id_supported:
            two = full_id_view
        else:
            check('full_id_rejection_is_explicit', "No job matching '" + native + "'" in full_id_view.text())
            selected = roster(a, 'short-selector-check')
            check('short_selector_matches_original_full_id', selected['sessionId'] == native and
                  selected['id'] == initial['id'] and selected['pid'] == worker['pid'] and alive(worker))
            two = a.attach(selected['id'], 'second-attach')
            two.until(lambda: 'NETCAP_NATIVE_INPUT=seed_a' in two.text(), 'second original view')
        samples['first_attach'], samples['second_attach'] = identity(one.proc.pid), identity(two.proc.pid)
        check('both_attach_clients_remain_alive', one.proc.poll() is None and two.proc.poll() is None)
        attached = roster(a, 'after-second-attach')
        check('second_attach_keeps_original_full_id_and_worker', attached['sessionId'] == native and
              attached['pid'] == worker['pid'] and alive(worker))
        prompt(one, 'first')
        wait_for(lambda: requests(a, 'first'), 'input from first attached client')
        prompt(two, 'second')
        wait_for(lambda: requests(a, 'second'), 'input from second attached client')
        check('both_clients_can_submit_to_original_worker', len(requests(a, 'first')) == len(requests(a, 'second')) == 1 and one.proc.poll() is None and two.proc.poll() is None)
        result['input_exclusivity'] = 'violated'
        send(one, 'NETCAP_NATIVE_INPUT=shared_', 'draft-first-part')
        one.drain(.3)
        check('draft_not_submitted_by_first_client', not requests(a, 'shared_'))
        send(two, 'draft', 'draft-second-part')
        two.drain(.3)
        send(two, '\r', 'draft-submit-by-second')
        wait_for(lambda: requests(a, 'shared_draft'), 'shared draft submitted by other client')
        check('unsubmitted_draft_is_shared_between_clients', len(requests(a, 'shared_draft')) == 1)

        helper_before = (a.root / 'helper-calls.jsonl').read_bytes()
        (a.root / 'helper-before-read.jsonl').write_bytes(helper_before)
        app = ObservationConsole(a)
        samples['console'] = identity(app.process.pid)
        preview = app.api('/api/native-sessions/preview', {'project_id': 'native-project', 'instance_id': 'native-claude', 'native_id': native})
        binding = app.api('/api/native-sessions/bind', {'command_id': 'bind-owned-native', 'project_id': 'native-project', 'instance_id': 'native-claude', 'native_id': native, 'preview_token': preview['binding_token']})
        sid = binding['session_id']
        result['session_id'], result['native_id'] = sid, native

        def observe(stage):
            before_requests = len(a.provider.requests)
            value = app.api('/api/sessions/' + sid + '/native/observe', {})
            history = app.api('/api/sessions/' + sid + '/native/history', {'cursor': ''})
            detail = app.api('/api/sessions/' + sid)
            frames[stage] = {'/api/workspace': app.api('/api/workspace'), '/api/sessions/' + sid: detail,
                             '/api/sessions/' + sid + '/native/observe': value,
                             '/api/sessions/' + sid + '/native/history': history}
            samples[stage] = value
            capture_processes(stage)
            check(stage + '_history_keeps_original_readonly_identity', history['read_only'] and history['session']['native_id'] == native)
            check(stage + '_production_retains_unknown_occupancy', value['status'] == 'sampled' and value['read_only'] and value['view']['status'] == 'unknown' and not value['view']['evidence']['complete'] and not value['view'].get('execution_clear', False))
            for kind in ['send', 'resume', 'takeover', 'interrupt', 'respond']:
                app.api('/api/commands', {'command_id': stage + '-reject-' + kind, 'kind': kind, 'session_id': sid, 'text': 'UNAUTHORIZED_PRODUCTION_INPUT'}, status=409)
            after = app.api('/api/sessions/' + sid)
            check(stage + '_readonly_record_and_operations_preserved', after['session'] == detail['session'] and not after['operations'])
            check(stage + '_production_does_not_send_model_input', len(a.provider.requests) == before_requests)

        observe('two-client-idle')
        check('native_metadata_does_not_run_auth_helper', (a.root / 'helper-calls.jsonl').read_bytes() == helper_before)
        app.api('/api/commands', {'command_id': 'reject-create', 'kind': 'create', 'project_id': 'native-project', 'route_id': 'unused', 'permission': 'read_only'}, status=409)
        prompt(one, 'deny')
        wait_for(lambda: roster(a, 'deny-pending').get('waitingFor') == 'permission prompt', 'first native tool permission')
        one.until(lambda: 'OWNED_NATIVE_INPUT_deny' in one.text() or 'owned_tool.py' in one.text(), 'owned refusal dialog')
        two.drain(.3)
        check('pending_tool_has_no_effect', not (a.cwd / 'starts.jsonl').exists())
        observe('approval-pending')
        text = one.text()
        options = re.findall(r'(\d+)\.\s*No\b', text)
        assert options, 'native refusal option missing'
        option = int(options[-1])
        assert 1 <= option <= 5, 'unexpected refusal option'
        check('approval_initially_selects_single_use_yes', bool(re.search(r'❯\s*1\.\s*Yes\b', text)))
        write(a.root / 'refusal-before.ansi', one.output.decode(errors='replace'))
        send(one, '\x1b[B' * (option - 1), 'select-refusal-in-first-client')
        two.drain(.3)
        samples['refusal_option'] = option
        write(a.root / 'refusal-selected-first.ansi', one.output.decode(errors='replace'))
        write(a.root / 'refusal-selected-second.ansi', two.output.decode(errors='replace'))
        check('selection_alone_does_not_approve', not (a.cwd / 'starts.jsonl').exists())
        send(two, '\r', 'confirm-refusal-in-second-client')
        refusal = wait_for(lambda: native_tool_result('deny'), 'original native refusal record')
        samples['refusal'] = refusal
        two.until(lambda: 'Interrupted · What should Claude do instead?' in two.text(), 'native refusal finishes turn')
        check('clients_share_the_original_approval_selection', refusal['result'].get('is_error') and
              refusal['native_id'] == native and not (a.cwd / 'starts.jsonl').exists())
        check('refusal_does_not_automatically_continue_model', 'deny' not in a.provider.results)

        prompt(two, 'run')
        wait_for(lambda: roster(a, 'run-pending').get('waitingFor') == 'permission prompt', 'second single-use approval')
        # Claude can append a native token reminder to the original result.
        # Keep the original bytes and the added context separately visible.
        in_model = a.provider.results.get('deny', {})
        samples['refusal_in_model'] = in_model
        check('original_refusal_enters_next_explicit_model_input', all(
            in_model.get(key) == refusal['result'].get(key) for key in ['tool_use_id', 'is_error']) and
            isinstance(in_model.get('content'), str) and in_model['content'].startswith(refusal['result']['content']))
        samples['refusal_native_suffix'] = in_model['content'][len(refusal['result']['content']):]
        two.drain(.3)
        send(two, '\r', 'explicit-single-tool-approval')
        wait_for(lambda: (a.cwd / 'starts.jsonl').exists(), 'original owned tool starts')
        starts = [json.loads(line) for line in (a.cwd / 'starts.jsonl').read_text().splitlines()]
        check('one_explicitly_approved_tool', len(starts) == 1 and starts[0]['phase'] == 'run')
        tool = identity(starts[0]['pid']);samples['tool'] = tool
        observe('tool-running')
        send(one, '\x1a', 'detach-first-client')
        one.until(lambda: one.proc.poll() is not None, 'first client detached')
        capture_processes('first-client-detached')
        check('detaching_one_client_preserves_other_and_tool', two.proc.poll() is None and alive(worker) and alive(tool))
        # The second client stays attached while the original tool completes;
        # no new input is used to recover or replace that execution.
        write(a.cwd / 'release', 'finish original tool')
        wait_for(lambda: 'run' in a.provider.results, 'original complete tool result')
        expected = json.loads((a.cwd / 'tool-output.json').read_text())
        values = []
        for line in strings(a.provider.results['run']['content']).splitlines():
            try:
                values.append(json.loads(line))
            except ValueError:
                pass
        check('complete_output_returns_to_original_context', expected in values and expected['pid'] == tool['pid'] and not a.provider.results['run'].get('is_error'))
        check('original_tool_exits_without_replay', len((a.cwd / 'starts.jsonl').read_text().splitlines()) == 1 and not alive(tool))
        observe('tool-finished')
        app.close();result['console_exit'] = app.process.returncode;app = None
        capture_processes('console-closed')
        check('closing_readonly_console_keeps_native_worker', alive(worker) and two.proc.poll() is None)
        final = roster(a, 'final')
        check('all_inputs_keep_original_full_id', final['sessionId'] == native and all(json.loads(r['body']['metadata']['user_id'])['session_id'] == native for r in a.provider.requests if any(last_marker(r['body'], 'NETCAP_NATIVE_INPUT=' + phase) for phase in ['seed_a', 'first', 'second', 'shared_draft', 'deny', 'run'])))
        check('sibling_profile_unchanged', alive(sibling_worker) and roster(b, 'sibling-final')['sessionId'] == sibling['sessionId'] and len(requests(b, 'seed_b')) == 1)
        check('local_provider_auth_and_errors', all(not p.provider.errors and all(r['auth_matches'] for r in p.provider.requests) for p in profiles.values()))
        check('all_production_reads_and_rejections_keep_auth_helper_untouched',
              (a.root / 'helper-calls.jsonl').read_bytes() == helper_before)
        result['observation_completed'] = True
    except BaseException as error:
        result['error'] = repr(error)
        write(root / 'error.txt', traceback.format_exc())
    finally:
        if app:
            app.close();result['console_exit'] = app.process.returncode
        for p in profiles.values():
            write(p.cwd / 'release', 'owned cleanup')
            p.close()
        capture_processes('cleanup')
        result['owned_profiles_stopped'] = bool(profiles) and all(p.cleaned for p in profiles.values())
        checks['owned_process_cleanup_complete'] = result['owned_profiles_stopped'] and all(
            not alive(row['expected']) for row in processes['cleanup']['processes'].values())
        result['provider_requests'] = {label: len(p.provider.requests) for label, p in profiles.items()}
        result['observed_checks_passed'] = bool(result.get('observation_completed')) and all(checks.values())
        write(root / 'result.json', result)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside', type=Path)
    args = parser.parse_args()
    if args.inside:
        def timeout(*_):
            raise TimeoutError('owned native input probe lease expired')
        signal.signal(signal.SIGALRM, timeout);signal.alarm(180)
        result = run(args.inside)
        print(json.dumps({k: result.get(k) for k in ['root', 'observed_checks_passed', 'input_exclusivity', 'goal_acceptance_met', 'checks', 'error']}, ensure_ascii=False), flush=True)
        return 0 if result['observed_checks_passed'] else 1
    root = Path(tempfile.mkdtemp(prefix='nc-native-input-', dir='/var/tmp'))
    print('Evidence directory:', root, flush=True)
    write(root / 'policy/managed-settings.json', {})
    registry = root / 'windows/Windows/System32/reg.exe'
    write(registry, '#!/usr/bin/python3\nimport sys\nsys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\nraise SystemExit(1)\n')
    registry.chmod(0o700)
    command = ['bwrap', '--unshare-user', '--die-with-parent', '--ro-bind', '/', '/', '--tmpfs', '/etc',
               '--ro-bind', '/etc/passwd', '/etc/passwd', '--ro-bind', '/etc/group', '/etc/group',
               '--ro-bind', str(root / 'policy'), '/etc/claude-code', '--bind', str(root), str(root),
               '--tmpfs', '/tmp', '--tmpfs', '/run', '--tmpfs', '/mnt', '--ro-bind', str(root / 'windows'), '/mnt/c',
               '--proc', '/proc', '--dev', '/dev', '/usr/bin/python3', str(Path(__file__).resolve()), '--inside', str(root)]
    write(root / 'launch.json', {'command': command, 'parent_mnt_namespace': os.readlink('/proc/self/ns/mnt')})
    return subprocess.run(command).returncode


if __name__ == '__main__':
    raise SystemExit(main())
