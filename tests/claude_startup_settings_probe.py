#!/usr/bin/env python3
"""Compare native custom-file semantics before composing installed exclusions.

No model input or MCP. Hooks only write fixture markers; raw controls read
settings and permission metadata and do not grant tool authorization.
"""
import json
import copy
from pathlib import Path
import subprocess
import tempfile

from claude_plugin_contract_probe import Native, plugin, install, digest_tree
from execution_config_probe import environment, verify_versions, write
from runtime.common import stop


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-startup-settings-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    env = environment(root)
    cwd = root / 'work'
    custom_dir = root / 'custom/location'
    derived_dir = root / 'state/settings'
    for directory in [cwd, custom_dir, derived_dir]:
        for name in ['extra', 'scratch', 'secret']:
            (directory / name).mkdir(parents=True, exist_ok=True)
        write(directory / 'local-hook.py', 'import os,json\nfrom pathlib import Path\nwith Path(os.environ["NETCAP_SETTINGS_LOG"]).open("a") as f:f.write(json.dumps({"source":' + repr(directory.name) + ',"cwd":str(Path.cwd()),"marker":os.environ.get("NETCAP_CUSTOM_SETTINGS_SENTINEL", "")})+"\\n")\n')
    source = root / 'marketplace/selected'
    plugin(source, 'w0208-selected')
    write(source / 'hooks/hooks.json', {'hooks': {'SessionStart': [{'hooks': [{
        'type': 'command', 'command': 'python3 "' + str(source / 'startup.py') + '"', 'timeout': 5}]}]}})
    write(source / 'startup.py', 'import os,json\nfrom pathlib import Path\nwith Path(os.environ["NETCAP_SETTINGS_LOG"]).open("a") as f:f.write(json.dumps({"source":"installed"})+"\\n")\n')
    install(root, env, cwd)
    write(cwd / '.claude/settings.json', {'permissions': {'deny': ['Read(./project-secret/**)']},
                                         'env': {'NETCAP_PROJECT_SENTINEL': 'project-preserved'}})
    settings = {
        'env': {'NETCAP_CUSTOM_SETTINGS_SENTINEL': 'custom-preserved'},
        'enabledPlugins': {'w0208-preserved@w0208-local': False},
        'permissions': {'allow': ['Read(./allowed/**)'], 'deny': ['Read(./secret/**)'],
                        'ask': ['Bash'], 'additionalDirectories': ['./extra']},
        'sandbox': {'enabled': False, 'filesystem': {'allowWrite': ['./scratch'], 'denyRead': ['./secret']}},
        'hooks': {'SessionStart': [{'hooks': [{'type': 'command', 'command': 'python3 ./local-hook.py', 'timeout': 5}]}]},
    }
    original = custom_dir / 'original.json'
    copied = derived_dir / 'copy.json'
    composed = derived_dir / 'composed.json'
    write(original, settings)
    write(copied, settings)
    target = {**settings, 'enabledPlugins': {**settings['enabledPlugins'], 'w0208-selected@w0208-local': False}}
    write(composed, target)
    derived_dir.chmod(0o700)
    for path in [copied, composed]:
        path.chmod(0o600)
    sources_before = {str(p): digest_tree(p) for p in [custom_dir, derived_dir, source, cwd]}
    cases = [('original', str(original)), ('object', settings), ('copied-file', str(copied)),
             ('composed-object', target), ('composed-file', str(composed))]
    reports = {}
    for label, value in cases:
        case = root / label
        case.mkdir()
        log = case / 'startup.jsonl'
        native = Native(case, {**env, 'NETCAP_SETTINGS_LOG': str(log)}, cwd, settings=value)
        report = {}
        try:
            report['initialized'] = native.call('initialize')
            report['settings'] = native.call('get_settings')
            report['permissions'] = native.call('list_permission_rules')
            report['plugins'] = native.call('reload_plugins')
            report['hooks'] = native.call('get_hooks_listing')
        finally:
            native.close()
            report['startup'] = [json.loads(x) for x in log.read_text().splitlines()] if log.exists() else []
            report['native_id'] = native.id
            report['exit_code'] = native.proc.returncode
            write(case / 'result.json', report)
            reports[label] = report
        print(label, 'sampled', flush=True)
    write(root / 'session-request.json', {'root': str(root), 'cwd': str(cwd),
                                         'original': str(original), 'composed': str(composed)})
    proc = subprocess.Popen(['node', str(Path(__file__).with_name('claude_startup_settings_session.mjs')),
                             str(root / 'session-request.json')], env=env, cwd=cwd,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
    try:
        stdout, stderr = proc.communicate(timeout=45)
        write(root / 'session.stdout.log', stdout)
        write(root / 'session.stderr.log', stderr)
    finally:
        stop(proc)
    session = json.loads((root / 'session-result.json').read_text()) if (root / 'session-result.json').exists() else {}
    checks = {}
    baseline = reports['original']
    expected = copy.deepcopy(baseline['settings'])
    expected['effective']['enabledPlugins']['w0208-selected@w0208-local'] = False
    flag = next(s for s in expected['sources'] if s['source'] == 'flagSettings')
    flag['settings']['enabledPlugins']['w0208-selected@w0208-local'] = False
    for label, report in reports.items():
        wire = json.loads((root / label / 'wire.json').read_text())
        composed_case = label.startswith('composed-')
        checks[label + '_settings_preserved'] = report['settings'] == (expected if composed_case else baseline['settings'])
        checks[label + '_permission_metadata_preserved'] = report['permissions'] == baseline['permissions']
        checks[label + '_custom_hook_and_cwd_preserved'] = [x for x in report['startup'] if x['source'] != 'installed'] == [
            {'source': 'work', 'cwd': str(cwd), 'marker': 'custom-preserved'}]
        checks[label + '_installed_startup_selection'] = sum(x['source'] == 'installed' for x in report['startup']) == (0 if composed_case else 1)
        checks[label + '_installed_inventory_selection'] = sum(p['name'] == 'w0208-selected' for p in report['plugins']['plugins']) == (0 if composed_case else 1)
        checks[label + '_only_metadata_requests'] = bool(wire['outgoing']) and all(x['type'] == 'control_request' for x in wire['outgoing'])
        checks[label + '_no_model_result'] = all(x.get('type') != 'result' for x in wire['incoming'])
        checks[label + '_process_terminal'] = report['exit_code'] is not None
    checks['sources_unchanged'] = all(digest_tree(Path(p)) == stamp for p, stamp in sources_before.items())
    checks['derived_file_private'] = composed.stat().st_mode & 0o777 == 0o600 and derived_dir.stat().st_mode & 0o777 == 0o700
    checks['production_session_candidate'] = proc.returncode == 0 and session.get('passed') is True
    result = {'checks': checks, 'reports': reports, 'session': session, 'passed': all(checks.values()),
              'scope': 'raw native read-only settings/permission metadata with fixture startup hook execution; no model/MCP/tool permission exercise',
              'limitations': ['relative sandbox paths were read back only; sandbox disabled',
                              'additionalDirectories remained absent from workspaceDirectories; no directory access claim',
                              'no concurrent hot-update, managed policy or model-history recovery validation']}
    write(root / 'result.json', result)
    print(json.dumps({'checks': checks, 'passed': result['passed']}, indent=2))
    raise SystemExit(0 if result['passed'] else 1)


if __name__ == '__main__':
    main()
