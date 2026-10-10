#!/usr/bin/env python3
"""Observe native prompt ownership, drafts and queue withdrawal in owned CLIs.

Uses startup opt-in and explicit in-session installation of fixture plugins.
The local file channel and optional withdrawal policy are experiments, not a
production control lease. All model responses come from a local script.
"""
import argparse
import base64
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import sys
import tempfile
import threading
import time
import traceback

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO / 'tests'))
from execution_config_probe import CLAUDE, verify_versions, write
from claude_native_input_control_probe import InputProvider
from claude_skill_method_probe import strings
from workbench_boundary_probe import ClaudeProfile, Terminal
from runtime.common import wait_for
from wsl_live_control_probe import identity, alive


def primary_phases(payload):
    # This fixture keeps the native standard tool set. Background status and
    # title calls quote its markers too, but have no tool definitions.
    names = {tool.get('name') for tool in payload.get('tools', [])}
    if not {'Bash', 'Read'}.issubset(names):
        return []
    messages = payload.get('messages', [])
    if not messages or messages[-1].get('role') != 'user':
        return []
    last = strings(messages[-1].get('content'))
    if '<session>' in last and 'Write the title' in last:
        return []
    return re.findall(r'NETCAP_NATIVE_INPUT=([a-z_]+)', last)


def primary_phase(payload):
    phases = primary_phases(payload)
    return phases[-1] if phases else None


class PromptProvider(InputProvider):
    def __init__(self, root, route):
        self.held = threading.Event()
        self.release = threading.Event()
        super().__init__(root, route)

    def failure_response(self, path, payload):
        if primary_phase(payload) == 'api_held' and not self.held.is_set():
            self.held.set()
            write(self.root / 'held.json', {'at': time.time()})
            assert self.release.wait(timeout=80), 'owned provider hold exceeded lease'
            write(self.root / 'released.json', {'at': time.time()})
        return None

    def plan(self, payload):
        fallback = super().plan(payload)
        phases = primary_phases(payload)
        if phases and phases[-1] not in ['run', 'deny']:
            return {'text': 'NATIVE_INPUT_' + phases[-1] + '_DONE'}
        return fallback


def read_json(path):
    try:
        return json.loads(path.read_text())
    except (FileNotFoundError, json.JSONDecodeError):
        return None


def run(root, background=False, witness='none', revoke_queued=False, inflight='model'):
    result = {'root': str(root), 'background': background, 'witness_mode': witness, 'revoke_queued': revoke_queued, 'inflight': inflight, 'scope': 'owned isolated native session; startup function-hooks opt-in; in-session plugin install; local scripted provider',
              'production_handoff_verified': False, 'checks': {}, 'samples': {}, 'writes': []}
    checks, samples = result['checks'], result['samples']
    p = None
    worker = None
    tool = None
    channel = root / 'channel'
    next_command = 0

    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    def events(kind=None, witness=False):
        directory = channel / 'witness/events' if witness else channel / 'events'
        rows = [read_json(path) for path in sorted(directory.glob('*.json'))]
        return [r for r in rows if r and (kind is None or r['kind'] == kind)]

    def send(terminal, text, label):
        record = {'at': time.time(), 'label': label, 'pid': terminal.proc.pid,
                  'data_base64': base64.b64encode(text.encode()).decode()}
        result['writes'].append(record)
        terminal.send(text)
        record['written'] = True

    def command(op, **kwargs):
        nonlocal next_command
        next_command += 1
        data = {'id': next_command, 'op': op, **kwargs}
        path = channel / 'commands' / (str(next_command) + '.json')
        temporary = path.with_suffix('.tmp')
        write(temporary, data)
        temporary.replace(path)
        return next_command

    def response(cid, terminal):
        answer = terminal.until(lambda: read_json(channel / 'responses' / (str(cid) + '.json')), 'module response ' + str(cid), timeout=12)
        assert 'error' not in answer, answer
        return answer['result']

    def inspect(label, terminal):
        samples[label] = response(command('inspect'), terminal)
        return samples[label]

    try:
        check('owned_system_policy_mount', os.path.samefile(root / 'policy', '/etc/claude-code'))
        verify_versions(root, ['claude'])
        p = ClaudeProfile(root / 'A', 'A', provider_factory=PromptProvider)
        p.env['CLAUDE_CODE_ENABLE_FUNCTION_HOOKS'] = '1'
        if inflight == 'tool':
            write(p.cwd / 'owned_tool.py', '''import json,os,sys,time
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
        result['initial_enabled_plugins'] = read_json(Path(p.env['CLAUDE_CONFIG_DIR']) / 'settings.json').get('enabledPlugins', {})
        plugin = root / 'market/owned-mod'
        write(plugin / '.claude-plugin/plugin.json', {'name': 'netcap-input-observer-fixture', 'version': '0.0.2', 'description': 'Owned native prompt API probe'})
        module = Path(__file__).with_name('claude_native_prompt_module.ts').read_text().replace('__OWNED_CHANNEL_ROOT__', str(channel))
        write(plugin / 'hooks/register.ts', module)
        classic = root / 'classic.py'
        write(classic, "import json,sys\nfrom pathlib import Path\ne=json.load(sys.stdin)\nwith Path(__file__).with_name('classic.jsonl').open('a') as f:f.write(json.dumps({k:e.get(k) for k in ['session_id','prompt','hook_event_name','prompt_source']})+'\\n')\n")
        hooks = {'modules': ['./register.ts'], 'hooks': {'UserPromptSubmit': [{'hooks': [{'type': 'command', 'command': shlex.join(['/usr/bin/python3', str(classic)])}]}]}}
        market = {'name': 'owned-functions', 'owner': {'name': 'Fixture'}, 'plugins': [{'name': 'netcap-input-observer-fixture', 'source': './owned-mod'}]}
        if witness != 'none':
            witness_module = Path(__file__).with_name('claude_native_prompt_witness.ts').read_text().replace('__OWNED_CHANNEL_ROOT__', str(channel)).replace('__REVOKE_QUEUED__', 'yes' if revoke_queued else 'no')
            other = root / 'market/owned-witness'
            write(other / '.claude-plugin/plugin.json', {'name': 'netcap-input-witness-fixture', 'version': '0.0.1', 'description': 'Owned native prompt provenance and withdrawal fixture'})
            write(other / 'hooks/witness.ts', witness_module)
            write(other / 'hooks/hooks.json', {'modules': ['./witness.ts']})
            market['plugins'].append({'name': 'netcap-input-witness-fixture', 'source': './owned-witness'})
        write(plugin / 'hooks/hooks.json', hooks)
        write(root / 'market/.claude-plugin/marketplace.json', market)
        for item in market['plugins']:
            package = root / 'market' / item['source']
            validation = subprocess.run([CLAUDE, 'plugin', 'validate', str(package)], env=p.env, cwd=p.cwd, capture_output=True, text=True, timeout=20)
            write(root / ('validate-' + item['name'] + '.stdout'), validation.stdout)
            write(root / ('validate-' + item['name'] + '.stderr'), validation.stderr)
            check('valid_manifest_' + item['name'], validation.returncode == 0)
        argv = [CLAUDE, *(['--bg'] if background else []), 'NETCAP_NATIVE_INPUT=seed_a', '--permission-mode', 'default']
        result['initial_argv'] = argv
        handled = set()

        def requested(phase):
            return [r for r in p.provider.requests if 'count_tokens' not in r['path'] and phase in primary_phases(r['body'])]

        def started():
            text = re.sub(r'\s+', '', terminal.text())
            for key, phrase in [('theme', 'Choosethetextstyle'), ('notes', 'PressEntertocontinue'), ('trust', 'Yes,Itrustthisfolder')]:
                if key not in handled and phrase in text:
                    handled.add(key)
                    send(terminal, '\x1b[B\r' if key == 'trust' else '\r', 'onboarding-' + key)
                    return False
            return bool(requested('seed_a'))

        if background:
            p.cli(argv[1:], 'launch')
            wait_for(lambda: requested('seed_a'), 'owned background seed')
            original = wait_for(lambda: next((r for r in p.agents('original') if r.get('status') == 'idle'), None), 'background seed completes')
            terminal = p.attach(original['id'], 'attached')
            result['attach'] = identity(terminal.proc.pid)
            check('native_background_identity', original['kind'] != 'interactive' and original['pid'] != terminal.proc.pid)
        else:
            terminal = Terminal(argv, p.env, p.cwd, p.root / 'foreground.ansi')
            p.terminals.append(terminal)
            terminal.until(started, 'owned foreground seed', timeout=30)
            terminal.until(lambda: 'NATIVE_INPUT_seed_a_DONE' in terminal.text(), 'original seed complete')
            original = p.agents('original')
            check('single_original_foreground', len(original) == 1 and original[0]['pid'] == terminal.proc.pid)
            original = original[0]
        terminal.until(lambda: 'NATIVE_INPUT_seed_a_DONE' in terminal.text(), 'original native view')
        worker = identity(original['pid'])
        result.update(original=original, worker=worker)
        config = Path(p.env['CLAUDE_CONFIG_DIR'])
        inline = [('/plugin marketplace add ' + str(root / 'market'), 'marketplace', config / 'plugins/known_marketplaces.json', 'owned-functions'),
                  ('/plugin install netcap-input-observer-fixture@owned-functions', 'plugin', config / 'plugins/installed_plugins.json', 'netcap-input-observer-fixture@owned-functions')]
        if witness == 'plugin':
            inline.append(('/plugin install netcap-input-witness-fixture@owned-functions', 'witness', config / 'plugins/installed_plugins.json', 'netcap-input-witness-fixture@owned-functions'))
        for text, label, path, key in inline:
            send(terminal, text, label + '-text');terminal.drain(.2);send(terminal, '\r', label + '-enter')
            confirmed = False

            def installed():
                nonlocal confirmed
                text = re.sub(r'\s+', '', terminal.text()[-3500:])
                if label in ['plugin', 'witness'] and not confirmed and key.split('@')[0] in text and '>Installforyou(userscope)' in text:
                    confirmed = True
                    result['explicit_owned_install_confirmation'] = True
                    write(root / 'install-confirmation.ansi', terminal.output.decode(errors='replace'))
                    send(terminal, '\r', 'confirm-owned-user-scope')
                data = read_json(path)
                return data and key in data.get('plugins', data)

            terminal.until(installed, 'in-session ' + label, timeout=25)
            terminal.drain(.3)
        send(terminal, '/reload-plugins', 'reload-text');terminal.drain(.2);send(terminal, '\r', 'reload-enter')
        ready = terminal.until(lambda: read_json(channel / 'ready.json'), 'native module becomes ready', timeout=15)
        check('module_original_id', ready['id'] == original['sessionId'])
        if witness != 'none':
            witness_ready = terminal.until(lambda: read_json(channel / 'witness/ready.json'), 'native witness ready', timeout=15)
            check('witness_original_id', witness_ready['id'] == original['sessionId'])
        check('no_initial_plugin', not result['initial_enabled_plugins'] and '--plugin-dir' not in argv)
        empty = inspect('empty', terminal)
        check('inspect_original_id_cwd', empty['id'] == empty['lastId'] == original['sessionId'] and empty['cwd'] == str(p.cwd))
        check('inspect_has_seed_history', any('NETCAP_NATIVE_INPUT=seed_a' in m['text'] for m in empty['messages']))
        draft = 'NETCAP_NATIVE_INPUT=human_draft 中文🧪'
        send(terminal, draft, 'type-original-draft')
        before = inspect('draft_before_api', terminal)
        check('reads_actual_draft', before['draft']['text'] == draft)
        check('reads_utf16_cursor', before['draft']['cursor'] == len(draft.encode('utf-16-le')) // 2)
        check('draft_not_submitted', not requested('human_draft'))
        samples['api_idle_receipt'] = response(command('submit', text='NETCAP_NATIVE_INPUT=api_idle'), terminal)
        terminal.until(lambda: requested('api_idle'), 'native plugin input reaches local provider')
        terminal.until(lambda: 'NATIVE_INPUT_api_idle_DONE' in terminal.text(), 'native plugin input completes')
        after = inspect('draft_after_api', terminal)
        check('api_preserves_draft_and_cursor', after['draft'] == before['draft'])
        check('api_did_not_submit_draft', not requested('human_draft'))
        plugin_prompts = [e for e in events('prompt.submit') if 'NETCAP_NATIVE_INPUT=api_idle' in e['data']['text']]
        samples['api_idle_prompt_events'] = plugin_prompts
        # The first real run returned native plugin provenance but did not call
        # this module's own prompt hook. Keep both observations independently.
        check('native_plugin_origin_receipt', samples['api_idle_receipt'].get('origin') == {'kind': 'plugin', 'name': 'netcap-input-observer-fixture'})
        check('one_idle_api_request', len(requested('api_idle')) == 1)
        send(terminal, '\r', 'submit-preserved-human-draft')
        terminal.until(lambda: requested('human_draft'), 'original draft submitted by terminal')
        terminal.until(lambda: 'NATIVE_INPUT_human_draft_DONE' in terminal.text(), 'draft completes')
        samples['after_human_draft'] = inspect('after_human_draft', terminal)
        human = [e for e in events('prompt.submit') if e['data']['text'] == draft]
        samples['human_draft_prompt_events'] = human
        check('native_human_origin', len(human) == 1 and human[0]['data']['origin']['kind'] == 'composer')
        check('human_draft_submitted_once', len(requested('human_draft')) == 1)
        cid = command('submit', text='NETCAP_NATIVE_INPUT=' + ('api_held' if inflight == 'model' else 'run'))
        if inflight == 'model':
            terminal.until(lambda: p.provider.held.is_set(), 'owned main model request held')
        else:
            terminal.until(lambda: any(r.get('waitingFor') == 'permission prompt' for r in p.agents('approval-pending')), 'original native tool approval')
            terminal.until(lambda: 'owned_tool.py' in terminal.text() and re.search(r'❯\s*1\.\s*Yes\b', terminal.text()), 'visible single-use owned tool approval')
            inspect('approval_pending', terminal)
            check('pending_approval_has_no_tool_effect', not (p.cwd / 'starts.jsonl').exists())
            write(root / 'approval-before.ansi', terminal.output.decode(errors='replace'))
            send(terminal, '\r', 'explicit-single-tool-approval')
            terminal.until(lambda: (p.cwd / 'starts.jsonl').exists(), 'original approved tool starts')
            starts = [json.loads(line) for line in (p.cwd / 'starts.jsonl').read_text().splitlines()]
            check('single_original_tool_start', len(starts) == 1 and starts[0]['phase'] == 'run')
            tool = identity(starts[0]['pid']);samples['tool'] = tool
        samples['held_receipt'] = response(cid, terminal)
        held_snapshot = inspect('held', terminal)
        check('held_has_original_turn', bool(held_snapshot['activeTurn']))
        queued_id = command('submit', text='NETCAP_NATIVE_INPUT=api_queued')
        terminal.until(lambda: any(e['data'].get('id') == queued_id for e in events('command.begin')), 'queued command dispatched once')
        terminal.drain(1)
        samples['queued_response_before_release'] = read_json(channel / 'responses' / (str(queued_id) + '.json'))
        check('queued_not_in_model_while_held', not requested('api_queued'))
        send(terminal, 'NETCAP_NATIVE_INPUT=human_takeover', 'type-human-takeover');terminal.drain(.2);send(terminal, '\r', 'submit-human-takeover')
        human_takeover = terminal.until(lambda: [e for e in events('prompt.submit') if e['data']['text'] == 'NETCAP_NATIVE_INPUT=human_takeover'], 'human Enter event while original model request held')
        samples['human_takeover_prompt_events'] = human_takeover
        check('human_event_refers_to_running_turn', human_takeover[0]['data'].get('turnId') == held_snapshot['activeTurn'])
        samples['before_release'] = inspect('before_release', terminal)
        check('no_queued_model_before_release', not requested('api_queued') and not requested('human_takeover'))
        if inflight == 'model':
            write(root / 'release-record.json', {'at': time.time(), 'kind': 'model'})
            p.provider.release.set()
        else:
            check('manual_input_preserves_original_running_tool', alive(tool) and len((p.cwd / 'starts.jsonl').read_text().splitlines()) == 1)
            write(root / 'release-record.json', {'at': time.time(), 'kind': 'tool', 'pid': tool['pid']})
            write(p.cwd / 'release', 'finish original owned tool')
        samples['queued_receipt'] = response(queued_id, terminal)
        terminal.until(lambda: requested('human_takeover'), 'human takeover eventually reaches local provider', timeout=25)
        terminal.until(lambda: 'NATIVE_INPUT_human_takeover_DONE' in terminal.text(), 'human takeover completes')
        terminal.drain(.7)
        samples['final'] = inspect('final', terminal)
        if inflight == 'tool':
            expected = read_json(p.cwd / 'tool-output.json')
            returned = p.provider.results.get('run', {})
            values = []
            for line in strings(returned.get('content', '')).splitlines():
                try: values.append(json.loads(line))
                except ValueError: pass
            samples['tool_output'] = expected;samples['tool_result_in_model'] = returned
            check('full_original_tool_output_returns', expected is not None and expected['pid'] == tool['pid'] and values.count(expected) == 1 and not returned.get('is_error'))
            check('original_tool_exits_without_replay', not alive(tool) and len((p.cwd / 'starts.jsonl').read_text().splitlines()) == 1)
        result['primary_request_phases'] = [primary_phase(r['body']) for r in p.provider.requests if primary_phase(r['body'])]
        result['queued_request_count_after_human'] = len(requested('api_queued'))
        result['queue_submit_events'] = [e for e in events('prompt.submit') if 'api_queued' in e['data']['text']]
        result['witness_idle_prompt_events'] = [e for e in events('prompt.submit', witness=True) if 'api_idle' in e['data']['text']]
        result['witness_queue_prompt_events'] = [e for e in events('prompt.submit', witness=True) if 'api_queued' in e['data']['text']]
        if revoke_queued:
            check('queued_native_call_returns_drop', samples['queued_receipt'] == {'drop': 'OWNED_PLUGIN_INPUT_WITHDRAWN_AFTER_MANUAL'})
            check('withdrawn_prompt_not_in_model', not requested('api_queued'))
            check('withdrawn_prompt_has_no_turn', not any('api_queued' in e['data']['text'] for e in events('turn.start')))
            held_turn = held_snapshot['activeTurn']
            check('original_inflight_turn_completed', any(e['data']['turnId'] == held_turn and e['data']['reason'] == 'answer' and not e['data']['isAborted'] for e in events('turn.complete')))
            samples['after_manual_receipt'] = response(command('submit', text='NETCAP_NATIVE_INPUT=api_after_manual'), terminal)
            check('later_native_plugin_call_also_dropped', samples['after_manual_receipt'] == {'drop': 'OWNED_PLUGIN_INPUT_WITHDRAWN_AFTER_MANUAL'} and not requested('api_after_manual'))
        for phase in ['hook_drop', 'hook_throw']:
            send(terminal, 'NETCAP_NATIVE_INPUT=' + phase, phase + '-text');terminal.drain(.2);send(terminal, '\r', phase + '-enter')
            if phase == 'hook_drop':
                terminal.until(lambda: 'OWNED_HOOK_REFUSED' in terminal.text(), 'native explicit hook refusal')
                check('explicit_hook_drop_not_in_model', not requested(phase))
            else:
                terminal.until(lambda: requested(phase), 'faulted native hook passes input')
                terminal.until(lambda: 'NATIVE_INPUT_hook_throw_DONE' in terminal.text(), 'faulted hook turn completes')
                check('faulted_hook_did_not_block_input', len(requested(phase)) == 1)
        samples['after_hook_failure'] = inspect('after_hook_failure', terminal)
        before_types = len([r for r in p.provider.requests if primary_phase(r['body'])])
        send(terminal, '/plugin-types', 'types-text');terminal.drain(.2);send(terminal, '\r', 'types-enter')
        terminal.until(lambda: (p.cwd / '.claude/types/claude-code.d.ts').is_file(), 'actual native type declarations')
        result['generated_types'] = [str(path.relative_to(root)) for path in (p.cwd / '.claude/types').glob('*.d.ts')]
        check('type_generation_adds_no_primary_input', len([r for r in p.provider.requests if primary_phase(r['body'])]) == before_types)
        result['primary_request_phases'] = [primary_phase(r['body']) for r in p.provider.requests if primary_phase(r['body'])]
        final = p.agents('final')
        check('same_original_process_session', len(final) == 1 and final[0]['sessionId'] == original['sessionId'] and final[0]['pid'] == worker['pid'] and alive(worker))
        check('local_provider_auth', all(r['auth_matches'] for r in p.provider.requests))
        check('provider_no_errors', not p.provider.errors)
        result['observation_complete'] = True
    except BaseException as error:
        result['error'] = repr(error)
        write(root / 'error.txt', traceback.format_exc())
    finally:
        if p:
            p.provider.release.set()
            if inflight == 'tool' and not (p.cwd / 'release').exists(): write(p.cwd / 'release', 'owned fixture cleanup')
            result['provider_request_count'] = len(p.provider.requests)
            result['provider_errors'] = p.provider.errors
            result['events'] = events()
            result['witness_events'] = events(witness=True)
            p.close()
            result['owned_profile_stopped'] = p.cleaned
            result['worker_stopped'] = not alive(worker) if worker else None
            result['tool_stopped'] = not alive(tool) if tool else None
        write(root / 'result.json', result)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside', type=Path)
    parser.add_argument('--background', action='store_true')
    parser.add_argument('--witness', choices=['none', 'plugin'], default='none')
    parser.add_argument('--revoke-queued', action='store_true')
    parser.add_argument('--inflight', choices=['model', 'tool'], default='model')
    args = parser.parse_args()
    if args.revoke_queued and args.witness != 'plugin':
        parser.error('--revoke-queued requires the separate witness plugin')
    if args.inside:
        def expired(*_):
            raise TimeoutError('owned native prompt probe lease expired')
        signal.signal(signal.SIGALRM, expired);signal.alarm(180)
        result = run(args.inside, args.background, args.witness, args.revoke_queued, args.inflight)
        print(json.dumps({k: v for k, v in result.items() if k not in ['samples', 'writes', 'events', 'witness_events']}, ensure_ascii=False), flush=True)
        raise SystemExit(0 if result.get('observation_complete') else 1)
    root = Path(tempfile.mkdtemp(prefix='nc-native-prompt-', dir='/var/tmp'))
    print('Evidence directory:', root, flush=True)
    write(root / 'fixture-source/native_prompt_probe.py', Path(__file__).read_text())
    write(root / 'fixture-source/prompt_module.ts', Path(__file__).with_name('claude_native_prompt_module.ts').read_text())
    write(root / 'fixture-source/witness_module.ts', Path(__file__).with_name('claude_native_prompt_witness.ts').read_text())
    write(root / 'policy/managed-settings.json', {})
    registry = root / 'windows/Windows/System32/reg.exe'
    write(registry, '#!/usr/bin/python3\nimport sys\nsys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\nraise SystemExit(1)\n');registry.chmod(0o700)
    command = ['bwrap', '--unshare-user', '--die-with-parent', '--ro-bind', '/', '/', '--tmpfs', '/etc', '--ro-bind', '/etc/passwd', '/etc/passwd', '--ro-bind', '/etc/group', '/etc/group',
               '--ro-bind', str(root / 'policy'), '/etc/claude-code', '--bind', str(root), str(root), '--tmpfs', '/tmp', '--tmpfs', '/run', '--tmpfs', '/mnt', '--ro-bind', str(root / 'windows'), '/mnt/c',
               '--proc', '/proc', '--dev', '/dev', '/usr/bin/python3', str(Path(__file__).resolve()), '--inside', str(root)]
    if args.background: command.append('--background')
    command.extend(['--witness', args.witness])
    command.extend(['--inflight', args.inflight])
    if args.revoke_queued: command.append('--revoke-queued')
    write(root / 'launch.json', {'command': command})
    exit_code = subprocess.run(command).returncode
    if exit_code == 0:
        native_types = root / 'A/work/.claude/types/claude-code.d.ts'
        typecheck = [str(REPO / 'bridges/claude/node_modules/.bin/tsc'), '--strict', '--noEmit', '--skipLibCheck',
                     '--target', 'ES2022', '--module', 'ESNext', '--moduleResolution', 'bundler', '--lib', 'ES2022',
                     *[str(path) for path in sorted((root / 'market').glob('*/hooks/*.ts'))], str(native_types)]
        checked = subprocess.run(typecheck, cwd=REPO, capture_output=True, text=True, timeout=30)
        write(root / 'module-typecheck.stdout', checked.stdout)
        write(root / 'module-typecheck.stderr', checked.stderr)
        result = read_json(root / 'result.json')
        result['module_typecheck'] = {'command': typecheck, 'exit_code': checked.returncode,
                                    'native_type_header': native_types.read_text().splitlines()[0]}
        write(root / 'result.json', result)
        print(json.dumps({'module_typecheck': result['module_typecheck']}, ensure_ascii=False), flush=True)
        exit_code = checked.returncode
    raise SystemExit(exit_code)


if __name__ == '__main__':
    main()
