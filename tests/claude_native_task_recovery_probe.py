#!/usr/bin/env python3
"""Observe native background Bash output across an owned worker restart.

Uses a real CLI and local provider. A recovered Read of the original output
file is reported separately from automatic notification and foreground-tool
recovery. No duplicate Bash action is approved or executed.
"""
import argparse
import json
import os
from pathlib import Path
import re
import shlex
import tempfile
import time
import traceback

from execution_config_probe import CLAUDE, Provider, verify_versions, wait_for, write
from native_claude_lifecycle_probe import alive, crash_owned
from workbench_boundary_probe import ClaudeProfile

START = 'NETCAP_NATIVE_BACKGROUND_TASK'
READ = 'NETCAP_READ_ORIGINAL_BACKGROUND_OUTPUT'
OUTPUT = 'NETCAP_ORIGINAL_BACKGROUND_OUTPUT'
PENDING = 'NETCAP_BACKGROUND_TASK_PENDING'
RECOVERED = 'NETCAP_BACKGROUND_OUTPUT_OBSERVED'


def text_content(value):
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        return '\n'.join(part.get('text', '') for part in value if isinstance(part, dict))
    return ''


def parts(payload, kind):
    return [part for message in payload.get('messages', []) if isinstance(message.get('content'), list)
            for part in message['content'] if part.get('type') == kind]


def last_prompt(payload, marker):
    messages = payload.get('messages', [])
    if not messages or messages[-1].get('role') != 'user':
        return False
    content = messages[-1].get('content')
    blocks = content if isinstance(content, list) else [{'text': content}]
    return any(isinstance(block.get('text'), str) and block['text'].strip() == marker for block in blocks)


class TaskProvider(Provider):
    def __init__(self, root, route):
        super().__init__(root, route)
        self.cwd = root / 'work'
        self.bash_id = 'original_background_bash'
        self.read_id = 'read_original_background_output'
        self.started = self.read_issued = False
        self.output_path = None
        self.replays = 0

    def tool(self, payload, seq, name, call_id, args):
        message = {'id': f'msg_{seq}', 'type': 'message', 'role': 'assistant', 'model': payload['model'],
                   'content': [], 'stop_reason': None, 'stop_sequence': None,
                   'usage': {'input_tokens': 32, 'output_tokens': 0}}
        return self.sse([('message_start', {'message': message}),
            ('content_block_start', {'index': 0, 'content_block': {'type': 'tool_use', 'id': call_id, 'name': name, 'input': {}}}),
            ('content_block_delta', {'index': 0, 'delta': {'type': 'input_json_delta', 'partial_json': json.dumps(args)}}),
            ('content_block_stop', {'index': 0}),
            ('message_delta', {'delta': {'stop_reason': 'tool_use', 'stop_sequence': None}, 'usage': {'output_tokens': 32}}),
            ('message_stop', {})])

    def anthropic(self, payload, answer, seq):
        for result in parts(payload, 'tool_result'):
            if result.get('tool_use_id') == self.bash_id:
                match = re.search(r'(/[^\s<>]+\.output)', text_content(result.get('content')))
                if match:
                    self.output_path = match[1]
            if result.get('tool_use_id') == self.read_id and OUTPUT in text_content(result.get('content')):
                return super().anthropic(payload, RECOVERED, seq)
        if last_prompt(payload, START):
            if self.started:
                self.replays += 1
                return super().anthropic(payload, 'NETCAP_DUPLICATE_REQUEST_NOT_EXECUTED', seq)
            self.started = True
            return self.tool(payload, seq, 'Bash', self.bash_id, {
                'command': shlex.join(['/usr/bin/python3', str(self.cwd / 'owned_tool.py')]),
                'description': 'Run the owned background-output recovery fixture', 'run_in_background': True})
        messages = payload.get('messages', [])
        last = text_content(messages[-1].get('content')) if messages else ''
        notification = '<task-notification>' in last and self.output_path and self.output_path in last
        if self.output_path and not self.read_issued and (last_prompt(payload, READ) or notification):
            self.read_issued = True
            return self.tool(payload, seq, 'Read', self.read_id, {'file_path': self.output_path})
        if any(result.get('tool_use_id') == self.bash_id for result in parts(payload, 'tool_result')):
            return super().anthropic(payload, PENDING, seq)
        return super().anthropic(payload, answer, seq)


def run(root, case):
    profile = ClaudeProfile(root / 'profile', 'A', provider_factory=TaskProvider)
    checks, samples = {}, {}
    result = {'case': case, 'scope': 'native background Bash, worker restart, original output file and native Read',
              'checks': checks, 'samples': samples, 'full_foreground_recovery_acceptance': False}
    tool_pid = None

    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    def roster(tag):
        rows = json.loads(profile.cli(['agents', '--json', '--all'], tag))
        assert len(rows) == 1
        return rows[0]

    def transcript():
        return '\n'.join(path.read_text() for path in Path(profile.env['CLAUDE_CONFIG_DIR']).glob('projects/*/*.jsonl'))

    def process_identity(pid):
        try:
            proc = Path(f'/proc/{pid}')
            before = (proc / 'stat').read_text().split(') ', 1)[1].split()[19]
            env = dict(item.split(b'=', 1) for item in (proc / 'environ').read_bytes().split(b'\0') if b'=' in item)
            executable = str((proc / 'exe').readlink())
            after = (proc / 'stat').read_text().split(') ', 1)[1].split()[19]
            if before != after or not alive(pid):
                return None
            return {'pid': pid, 'start_ticks': before, 'executable': executable,
                    'profile_matches': env.get(b'CLAUDE_CONFIG_DIR', b'').decode() == profile.env['CLAUDE_CONFIG_DIR']}
        except (FileNotFoundError, ProcessLookupError):
            return None

    try:
        write(profile.cwd / 'owned_tool.py', '''import json,os,time
from pathlib import Path
root=Path(__file__).parent
with (root/'starts.txt').open('a') as f:f.write(str(os.getpid())+'\\n')
deadline=time.monotonic()+150
while not (root/'release').exists():
 if time.monotonic()>deadline:raise TimeoutError('owned fixture was not released')
 time.sleep(.05)
result={'marker':'NETCAP_ORIGINAL_BACKGROUND_OUTPUT','pid':os.getpid()}
(root/'completed.json').write_text(json.dumps(result))
print(json.dumps(result),flush=True)
''')
        profile.cli(['--bg', START, '--permission-mode', 'default'], 'launch')
        original = wait_for(lambda: (job if (job := roster('first-permission')).get('waitingFor') == 'permission prompt' else None), 'original Bash permission')
        samples['original'] = original
        samples['original_process'] = process_identity(original['pid'])
        check('original_worker_identity_verified', bool(samples['original_process']) and samples['original_process']['profile_matches']
              and samples['original_process']['executable'] == str(Path(CLAUDE).resolve()))
        check('approval_precedes_original_bash', not (profile.cwd / 'starts.txt').exists())
        terminal = profile.attach(original['id'], 'first-approval')
        terminal.until(lambda: 'Yes' in terminal.text() and 'owned_tool.py' in terminal.text(), 'original Bash permission visible')
        terminal.send('\r')
        wait_for((profile.cwd / 'starts.txt').exists, 'original background tool begins')
        tool_pid = int((profile.cwd / 'starts.txt').read_text().strip())
        wait_for(lambda: profile.provider.output_path and PENDING in transcript(), 'native background acknowledgement and completed launch turn')
        output = Path(profile.provider.output_path)
        samples['original_tool'] = {'pid': tool_pid, 'stdout': str(Path(f'/proc/{tool_pid}/fd/1').readlink()),
                                    'start_ticks': Path(f'/proc/{tool_pid}/stat').read_text().split(') ', 1)[1].split()[19]}
        samples['native_output_path'] = str(output)
        check('native_acknowledgement_names_actual_output', str(output) == samples['original_tool']['stdout'])
        check('output_path_bound_to_full_session', original['sessionId'] in str(output))
        check('original_output_empty_before_release', output.read_bytes() == b'')
        samples['requests_before_restart'] = len(profile.provider.requests)
        crash_owned(terminal.proc.pid, profile)
        terminal.proc.wait(timeout=5)
        if case == 'worker-crash':
            crash_owned(original['pid'], profile)
        else:
            samples['respawn_response'] = profile.cli(['respawn', original['id']], 'respawn')
        wait_for(lambda: not alive(original['pid']), 'original worker exits')
        replacement = wait_for(lambda: (job if (job := roster('replacement')).get('pid') and job['pid'] != original['pid'] else None), 'replacement worker appears', timeout=40)
        samples['replacement'] = replacement
        samples['replacement_process'] = wait_for(lambda: process_identity(replacement['pid']), 'replacement is a live process')
        check('replacement_worker_identity_verified', samples['replacement_process']['profile_matches']
              and samples['replacement_process']['executable'] == str(Path(CLAUDE).resolve()))
        check('same_full_id_new_worker', replacement['sessionId'] == original['sessionId'])
        check('original_tool_survives_restart', alive(tool_pid) and Path(f'/proc/{tool_pid}/stat').read_text().split(') ', 1)[1].split()[19] == samples['original_tool']['start_ticks'])
        restored = profile.attach(original['id'], 'restored')
        restored.until(lambda: PENDING in restored.text(), 'original completed launch turn restored')
        write(profile.cwd / 'release', 'complete original task only')
        wait_for((profile.cwd / 'completed.json').exists, 'original tool completes')
        wait_for(lambda: not alive(tool_pid), 'original tool exits')
        expected = (profile.cwd / 'completed.json').read_text()
        samples['expected_output'] = expected
        check('native_output_starts_with_original_result', output.read_text().splitlines()[0] == expected)
        # Give the native completion event time to arrive before explicit input.
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline and RECOVERED not in restored.text():
            restored.drain(.2)
        before_explicit = len(profile.provider.requests)
        samples['requests_before_explicit_input'] = before_explicit
        samples['automatic_output_observed'] = any(expected in text_content(part.get('content'))
            for request in profile.provider.requests for part in parts(request['body'], 'tool_result')
            if part.get('tool_use_id') == profile.provider.read_id)
        if not samples['automatic_output_observed']:
            restored.send(READ + '\r')
            restored.until(lambda: RECOVERED in restored.text(), 'original output read in original session', timeout=30)
        observed = [request for request in profile.provider.requests if any(
            part.get('tool_use_id') == profile.provider.read_id and expected in text_content(part.get('content'))
            for part in parts(request['body'], 'tool_result'))]
        check('exact_original_output_reaches_model', bool(observed))
        check('read_context_preserves_original_bash', all(any(part.get('id') == profile.provider.bash_id
            for part in parts(request['body'], 'tool_use')) for request in observed))
        check('read_context_preserves_native_output_ack', all(any(part.get('tool_use_id') == profile.provider.bash_id
            and str(output) in text_content(part.get('content')) for part in parts(request['body'], 'tool_result')) for request in observed))
        check('output_keeps_original_full_id', all(json.loads(request['body']['metadata']['user_id'])['session_id'] == original['sessionId'] for request in observed))
        samples['output_after_recovery'] = output.read_text()
        native_lines = samples['output_after_recovery'].splitlines()
        read_results = [text_content(part.get('content')) for request in observed for part in parts(request['body'], 'tool_result')
                        if part.get('tool_use_id') == profile.provider.read_id]
        samples['read_results'] = read_results
        def rendered_lines(value):
            lines = value.split('\n\n<system-reminder>', 1)[0].splitlines()
            return [match[2] for line in lines if (match := re.fullmatch(r'(\d+)\t?(.*)', line))]
        check('native_read_preserves_output_file_lines', all(rendered_lines(value)[:len(native_lines)] == native_lines for value in read_results))
        notifications = []
        for request in profile.provider.requests:
            messages = request['body'].get('messages', [])
            last = text_content(messages[-1].get('content')) if messages else ''
            if '<task-notification>' in last and str(output) in last:
                notifications.append(last)
        samples['completion_notifications'] = notifications
        codes = {int(value) for notification in notifications for value in re.findall(r'exit code (-?\d+)', notification)}
        result['native_exit_code'] = next(iter(codes)) if len(codes) == 1 else None
        result['native_exit_status_verified'] = False
        check('native_exit_status_remains_unknown', (case == 'worker-crash' and not notifications and not codes)
              or (case == 'respawn' and codes == {-1} and '[process exited while detached; exit code unknown]' in samples['output_after_recovery']))
        check('one_actual_bash_execution', (profile.cwd / 'starts.txt').read_text().splitlines() == [str(tool_pid)])
        mains = [request for request in profile.provider.requests if last_prompt(request['body'], START)]
        samples['automatic_prompt_replays'] = len(mains) - 1
        check('completed_launch_prompt_not_replayed', len(mains) == 1 and profile.provider.replays == 0)
        check('local_requests_authenticated', not profile.provider.errors and all(request['auth_matches'] for request in profile.provider.requests))
        result['completed'] = True
        result['native_background_output_recovered'] = True
    except Exception as error:
        result['error'] = repr(error)
        write(root / 'error.txt', traceback.format_exc())
    finally:
        write(profile.cwd / 'release', 'cleanup')
        if tool_pid:
            wait_for(lambda: not alive(tool_pid), 'owned task exits during cleanup', timeout=5)
        if profile.provider.output_path and Path(profile.provider.output_path).is_file():
            write(root / 'native-task-output.txt', Path(profile.provider.output_path).read_text())
        profile.close()
        checks['owned_profile_stopped'] = profile.cleaned
        result['provider_requests'] = len(profile.provider.requests)
        result['expectations_met'] = result.get('completed', False) and all(checks.values())
        write(root / 'result.json', result)
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--case', choices=['worker-crash', 'respawn'], default='worker-crash')
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-claude-task-recovery-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    result = run(root, args.case)
    print(json.dumps({key: result[key] for key in ['case', 'expectations_met', 'native_background_output_recovered',
        'full_foreground_recovery_acceptance', 'native_exit_code', 'native_exit_status_verified', 'checks', 'error'] if key in result}, ensure_ascii=False), flush=True)
    raise SystemExit(0 if result['expectations_met'] else 1)
