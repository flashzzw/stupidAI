#!/usr/bin/env python3
"""Probe native hook availability and late installation without granting control.

Result fields distinguish successful observation from actual hook activation.
Profiles, marketplaces and model providers belong to the temporary fixture.
"""
import argparse
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import sys
import tempfile
import traceback

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO / 'tests'))
from execution_config_probe import CLAUDE, verify_versions, write
from claude_native_input_control_probe import InputProvider, last_marker
from workbench_boundary_probe import ClaudeProfile, Terminal
from runtime.common import wait_for
from wsl_live_control_probe import identity, alive


def run(root, foreground=False, enable_function_hooks=False, late_plugin=False, enable_after_start=False, in_session_install=False):
    result = {'root': str(root), 'foreground': foreground, 'enable_function_hooks': enable_function_hooks,
              'late_plugin': late_plugin, 'enable_after_start': enable_after_start,
              'in_session_install': in_session_install,
              'managed_policy_file_present': (root / 'policy/managed-settings.json').is_file(),
              'scope': 'owned native module feature probe; local providers; no input control granted'}
    p = None
    try:
        assert os.path.samefile(root / 'policy', '/etc/claude-code')
        verify_versions(root, ['claude'])
        p = ClaudeProfile(root / 'A', 'A', provider_factory=InputProvider)
        if enable_function_hooks:
            p.env['CLAUDE_CODE_ENABLE_FUNCTION_HOOKS'] = '1'
        plugin = root / 'market/owned-mod' if late_plugin else root / 'owned-mod'
        write(plugin / '.claude-plugin/plugin.json', {'name': 'netcap-input-observer-fixture', 'version': '0.0.1', 'description': 'Owned native input module probe'})
        classic = root / 'classic.py'
        write(classic, '''import json,os,sys
from pathlib import Path
event=json.load(sys.stdin)
row={key:event.get(key) for key in ['session_id','prompt','hook_event_name','permission_mode']}
row.update(pid=os.getpid(),ppid=os.getppid())
with Path(__file__).with_name('classic.jsonl').open('a') as f:f.write(json.dumps(row)+'\\n')
''')
        write(plugin / 'hooks/hooks.json', {'modules': ['./register.ts'], 'hooks': {
            'UserPromptSubmit': [{'hooks': [{'type': 'command', 'command': shlex.join(['/usr/bin/python3', str(classic)])}]}]}})
        module = '''import type { Register } from 'claude-code';
export const register: Register = (on) => {
  on('session.start', async ($, e, next) => {
    await $.fs.write(__START__, JSON.stringify({id:await $.session.id(),event:e}));
    return next(e);
  });
  on('prompt.submit', ($, e, next) => {
    if (e.text === 'NETCAP_NATIVE_INPUT=module_probe') {
      return next({...e,text:'NETCAP_NATIVE_INPUT=module_rewritten'});
    }
    if (e.text === 'NETCAP_NATIVE_INPUT=module_block') return {drop:'OWNED_MODULE_REFUSED'};
    return next(e);
  });
};
'''.replace('__START__', json.dumps(str(root / 'module-start.json')))
        write(plugin / 'hooks/register.ts', module)
        for label, args in [('plugin-help', ['plugin', '--help']), ('plugin-test-help', ['plugin', 'test', '--help']),
                            ('plugin-validate', ['plugin', 'validate', str(plugin)])]:
            out = subprocess.run([CLAUDE, *args], env=p.env, cwd=p.cwd, capture_output=True, text=True, timeout=25)
            write(root / (label + '.stdout'), out.stdout);write(root / (label + '.stderr'), out.stderr)
            result[label] = {'exit_code': out.returncode}
        def requested(phase):
            return [request for request in p.provider.requests if last_marker(request['body'], 'NETCAP_NATIVE_INPUT=' + phase)]
        extra = ([] if late_plugin else ['--plugin-dir', str(plugin)]) + ['--permission-mode', 'default']
        result['initial_plugin_arguments'] = not late_plugin
        result['initial_plugin_settings'] = json.loads((Path(p.env['CLAUDE_CONFIG_DIR']) / 'settings.json').read_text()).get('enabledPlugins', {})
        if foreground:
            argv = [CLAUDE, 'NETCAP_NATIVE_INPUT=seed_a', *extra]
            result['initial_argv'] = argv
            terminal = Terminal(argv, p.env, p.cwd, p.root / 'foreground.ansi')
            p.terminals.append(terminal)
            handled = set()
            def started():
                text = re.sub(r'\s+', '', terminal.text())
                for key, phrase in [('theme', 'Choosethetextstyle'), ('notes', 'PressEntertocontinue'), ('trust', 'Yes,Itrustthisfolder')]:
                    if key not in handled and phrase in text:
                        handled.add(key)
                        terminal.send('\x1b[B\r' if key == 'trust' else '\r')
                        return False
                return bool(requested('seed_a'))
            terminal.until(started, 'owned foreground onboarding and seed request', timeout=30)
        else:
            p.cli(['--bg', 'NETCAP_NATIVE_INPUT=seed_a', *extra], 'launch')
            wait_for(lambda: requested('seed_a'), 'native seed request')
        def roster():
            rows = p.agents('roster');assert len(rows) == 1;return rows[0]
        wait_for(lambda: roster().get('status') == 'idle', 'native seed completes')
        original = roster();worker = identity(original['pid']);result['original'], result['worker'] = original, worker
        if not foreground:
            terminal = p.attach(original['id'], 'attached')
        terminal.until(lambda: 'NATIVE_INPUT_seed_a_DONE' in terminal.text(), 'original native view')
        if late_plugin:
            assert foreground, 'late installation probe targets an independent foreground'
            terminal.send('NETCAP_NATIVE_INPUT=module_before');terminal.drain(.2);terminal.send('\r')
            terminal.until(lambda: bool(requested('module_before')), 'original input before plugin installation')
            terminal.until(lambda: 'NATIVE_INPUT_module_before_DONE' in terminal.text(), 'original pre-install turn completes')
            if enable_after_start:
                settings_file = Path(p.env['CLAUDE_CONFIG_DIR']) / 'settings.json'
                settings = json.loads(settings_file.read_text())
                settings.setdefault('env', {})['CLAUDE_CODE_ENABLE_FUNCTION_HOOKS'] = '1'
                write(settings_file, settings)
            write(root / 'market/.claude-plugin/marketplace.json', {'name': 'owned-functions', 'owner': {'name': 'Fixture'},
                  'plugins': [{'name': 'netcap-input-observer-fixture', 'source': './owned-mod'}]})
            if in_session_install:
                config_dir = Path(p.env['CLAUDE_CONFIG_DIR'])
                def registered(path, key):
                    try:
                        value = json.loads(path.read_text())
                    except (FileNotFoundError, json.JSONDecodeError):
                        return False
                    return key in value.get('plugins', value)
                inline = [('/plugin marketplace add ' + str(root / 'market'), 'inline-marketplace',
                           lambda: registered(config_dir / 'plugins/known_marketplaces.json', 'owned-functions')),
                          ('/plugin install netcap-input-observer-fixture@owned-functions', 'inline-install',
                           lambda: registered(config_dir / 'plugins/installed_plugins.json', 'netcap-input-observer-fixture@owned-functions'))]
                for command, label, complete in inline:
                    terminal.send(command);terminal.drain(.2);terminal.send('\r')
                    confirmed = False
                    def completed():
                        nonlocal confirmed
                        text = re.sub(r'\s+', '', terminal.text()[-3500:])
                        if (label == 'inline-install' and not confirmed and
                                'netcap-input-observer-fixture' in text and '>Installforyou(userscope)' in text):
                            write(root / 'inline-install-confirmation.ansi', terminal.output.decode(errors='replace'))
                            confirmed = True
                            result['explicit_user_scope_install_confirmed'] = True
                            terminal.send('\r')
                        return complete()
                    terminal.until(completed, label, timeout=25)
                    terminal.drain(.3)
            else:
                for label, argv in [('marketplace-add', ['plugin', 'marketplace', 'add', str(root / 'market')]),
                                    ('plugin-install', ['plugin', 'install', 'netcap-input-observer-fixture@owned-functions', '--scope', 'user', '--json'])]:
                    p.cli(argv, label)
            before_reload = len(terminal.output)
            terminal.send('/reload-plugins');terminal.drain(.2);terminal.send('\r');terminal.drain(2)
            result['reload_output'] = terminal.output[before_reload:].decode(errors='replace')
            result['worker_after_install'] = identity(worker['pid'])
        for phase in ['module_probe', 'module_block']:
            terminal.send('NETCAP_NATIVE_INPUT=' + phase);terminal.drain(.2);terminal.send('\r')
            terminal.until(lambda: bool(requested(phase)) or (phase == 'module_probe' and bool(requested('module_rewritten'))) or
                           (phase == 'module_block' and 'OWNED_MODULE_REFUSED' in terminal.text()), 'module outcome ' + phase)
            wait_for(lambda: roster().get('status') == 'idle', 'settled module attempt')
        before = len(p.provider.requests)
        terminal.send('/plugin-types');terminal.drain(.2);terminal.send('\r');terminal.drain(2)
        result['types_command_model_request_count'] = len(p.provider.requests) - before
        result['generated_types'] = [str(path.relative_to(root)) for path in p.cwd.glob('.claude/types/*.d.ts')]
        result['module_start_record'] = json.loads((root / 'module-start.json').read_text()) if (root / 'module-start.json').is_file() else None
        result['module_marker_rewritten'] = len(requested('module_rewritten')) == 1 and not requested('module_probe')
        result['module_drop_enforced'] = not requested('module_block') and 'OWNED_MODULE_REFUSED' in terminal.text()
        result['classic_hook_calls'] = [json.loads(line) for line in (root / 'classic.jsonl').read_text().splitlines()] if (root / 'classic.jsonl').is_file() else []
        result['original_worker_preserved'] = alive(worker) and roster()['pid'] == worker['pid'] and roster()['sessionId'] == original['sessionId']
        result['provider_requests'] = len(p.provider.requests)
        result['provider_errors'] = p.provider.errors
        result['provider_auth_matches'] = all(request['auth_matches'] for request in p.provider.requests)
        result['observation_complete'] = True
    except BaseException as error:
        result['error'] = repr(error);write(root / 'error.txt', traceback.format_exc())
    finally:
        if p:
            p.close();result['owned_profile_stopped'] = p.cleaned
            result['worker_stopped'] = not alive(result['worker']) if 'worker' in result else None
        write(root / 'result.json', result)
    return result


if __name__ == '__main__':
    parser=argparse.ArgumentParser();parser.add_argument('--inside',type=Path)
    parser.add_argument('--foreground',action='store_true');parser.add_argument('--no-managed-policy',action='store_true')
    parser.add_argument('--enable-function-hooks',action='store_true');parser.add_argument('--late-plugin',action='store_true')
    parser.add_argument('--enable-after-start',action='store_true');parser.add_argument('--in-session-install',action='store_true');args=parser.parse_args()
    if args.inside:
        def expired(*_):raise TimeoutError('owned native module probe lease expired')
        signal.signal(signal.SIGALRM,expired);signal.alarm(150)
        result=run(args.inside,args.foreground,args.enable_function_hooks,args.late_plugin,args.enable_after_start,args.in_session_install)
        print(json.dumps(result,ensure_ascii=False),flush=True)
        raise SystemExit(0 if result.get('observation_complete') else 1)
    root=Path(tempfile.mkdtemp(prefix='nc-native-mods-',dir='/var/tmp'))
    print('Evidence directory:',root,flush=True)
    write(root/'fixture-source/claude_native_function_hooks_probe.py',Path(__file__).read_text())
    (root/'policy').mkdir()
    if not args.no_managed_policy:write(root/'policy/managed-settings.json',{})
    registry=root/'windows/Windows/System32/reg.exe'
    write(registry,'#!/usr/bin/python3\nimport sys\nsys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\nraise SystemExit(1)\n');registry.chmod(0o700)
    command=['bwrap','--unshare-user','--die-with-parent','--ro-bind','/','/','--tmpfs','/etc','--ro-bind','/etc/passwd','/etc/passwd','--ro-bind','/etc/group','/etc/group',
             '--ro-bind',str(root/'policy'),'/etc/claude-code','--bind',str(root),str(root),'--tmpfs','/tmp','--tmpfs','/run','--tmpfs','/mnt',
             '--ro-bind',str(root/'windows'),'/mnt/c','--proc','/proc','--dev','/dev','/usr/bin/python3',str(Path(__file__).resolve()),'--inside',str(root)]
    if args.foreground:command.append('--foreground')
    if args.no_managed_policy:command.append('--no-managed-policy')
    if args.enable_function_hooks:command.append('--enable-function-hooks')
    if args.late_plugin:command.append('--late-plugin')
    if args.enable_after_start:command.append('--enable-after-start')
    if args.in_session_install:command.append('--in-session-install')
    write(root/'launch.json',{'command':command})
    raise SystemExit(subprocess.run(command).returncode)
