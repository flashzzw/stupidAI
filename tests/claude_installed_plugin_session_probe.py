#!/usr/bin/env python3
"""Owned installed plugins: startup exclusion, original ID and source identity."""
import json
from pathlib import Path
import subprocess
import tempfile

from claude_plugin_contract_probe import Native, plugin, digest_tree
from execution_config_probe import CLAUDE, environment, verify_versions, write
from runtime.common import stop


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-installed-session-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    env = environment(root)
    cwd = root / 'work'
    cwd.mkdir()
    market = root / 'marketplace'
    names = ['w0208-selected', 'w0208-sibling']
    for name in names:
        source = market / name
        plugin(source, name)
        write(source / 'hooks/hooks.json', {'hooks': {'SessionStart': [{
            'matcher': 'startup|resume', 'hooks': [{'type': 'command',
                'command': 'python3 "' + str(root / 'startup.py') + '" ' + name,
                'timeout': 5}]}]}})
    write(root / 'startup.py', 'import os,json\nfrom pathlib import Path\np=Path(os.environ["NETCAP_INSTALLED_STARTUP_LOG"])\nwith p.open("a") as f:f.write(json.dumps({"plugin":__import__("sys").argv[1],"custom":os.environ.get("NETCAP_CUSTOM_SETTINGS_SENTINEL", "")})+"\\n")\n')
    write(market / '.claude-plugin/marketplace.json', {'name': 'w0208-local',
        'owner': {'name': 'Owned fixture'},
        'plugins': [{'name': n, 'source': './' + n} for n in names]})
    installed = []
    commands = [['plugin', 'marketplace', 'add', str(market)]] + [
        ['plugin', 'install', n + '@w0208-local', '--scope', 'user', '--json'] for n in names]
    for args in commands:
        proc = subprocess.Popen([CLAUDE, *args], cwd=cwd, env=env, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, text=True, start_new_session=True)
        try:
            stdout, stderr = proc.communicate(timeout=35)
            installed.append({'args': args, 'exit_code': proc.returncode,
                              'stdout': stdout, 'stderr': stderr})
        finally:
            stop(proc)
            write(root / 'installation.json', installed)
        if proc.returncode:
            raise RuntimeError('owned local installation failed')
    custom = root / 'custom/settings.json'
    write(custom, {'permissions': {'ask': ['Read'], 'deny': ['Bash(rm:*)']},
                   'env': {'NETCAP_CUSTOM_SETTINGS_SENTINEL': 'custom-source-preserved'},
                   'enabledPlugins': {'w0208-sibling@w0208-local': True}})
    resources_before = digest_tree(market)
    profile_before = digest_tree(Path(env['CLAUDE_CONFIG_DIR']) / 'plugins')
    request = {'root': str(root), 'cwd': str(cwd), 'custom': str(custom),
               'profile': env['CLAUDE_CONFIG_DIR'], 'market': str(market), 'names': names}
    write(root / 'request.json', request)
    proc = subprocess.Popen(['node', str(Path(__file__).with_suffix('.mjs')), str(root / 'request.json')],
                            cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            text=True, start_new_session=True)
    try:
        stdout, stderr = proc.communicate(timeout=150)
        write(root / 'stdout.log', stdout)
        write(root / 'stderr.log', stderr)
    finally:
        stop(proc)
    result = json.loads((root / 'result.json').read_text()) if (root / 'result.json').exists() else {'checks': {}}
    preinit = root / 'preinitialize'
    preinit.mkdir()
    native = Native(preinit, {**env, 'NETCAP_INSTALLED_STARTUP_LOG': str(preinit / 'startup.jsonl')}, cwd)
    preinit_error = ''
    try:
        native.call('apply_flag_settings', settings={'enabledPlugins': {'w0208-selected@w0208-local': False}})
    except RuntimeError as error:
        preinit_error = str(error)
    finally:
        native.close()
    preinit_stderr = (preinit / 'stderr.log').read_text()
    result['preinitialize'] = {'error': preinit_error, 'stderr': preinit_stderr,
                               'exit_code': native.proc.returncode,
                               'wire': json.loads((preinit / 'wire.json').read_text())}
    result['sdk_runner_exit_code'] = proc.returncode
    result['checks'].update(sdk_runner_exit_zero=proc.returncode == 0,
                            preinitialize_control_rejected=native.proc.returncode == 1 and
                            'requires the initialize control request as the first stdin line' in preinit_stderr,
                            source_files_unchanged=digest_tree(market) == resources_before,
                            installed_registry_and_cache_unchanged=digest_tree(Path(env['CLAUDE_CONFIG_DIR']) / 'plugins') == profile_before)
    registry = json.loads((Path(env['CLAUDE_CONFIG_DIR']) / 'plugins/installed_plugins.json').read_text())
    result['installed_registry'] = registry
    result['known_marketplaces'] = json.loads((Path(env['CLAUDE_CONFIG_DIR']) / 'plugins/known_marketplaces.json').read_text())
    result['checks']['registry_cache_is_not_loaded_local_source'] = all(
        p['path'] != registry['plugins'][p['source']][0]['installPath']
        for p in result.get('reports', {}).get('enabled', {}).get('loaded', {}).get('plugins', []))
    result['passed'] = all(result['checks'].values())
    write(root / 'result.json', result)
    print(json.dumps({'checks': result['checks'], 'passed': result['passed']}, indent=2))
    raise SystemExit(0 if result['passed'] else 1)


if __name__ == '__main__':
    main()
