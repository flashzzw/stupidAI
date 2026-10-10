#!/usr/bin/env python3
"""Production startup-source reader with controlled failures, no Claude query."""
import argparse
import json
from pathlib import Path
import subprocess
import tempfile

from execution_config_probe import environment, write
from runtime.common import stop

MODES = [
    'binding-route', 'binding-state', 'binding-api-helper', 'binding-invalid-env', 'binding-benign',
    'registry-binding-route', 'registry-binding-hkcu', 'registry-binding-invalid-env',
    'valid', 'malformed', 'read-error', 'registry-denied', 'oversized', 'helper', 'source-disappears',
    'helpers-linux', 'helpers-wsl', 'helpers-script', 'helpers-payload', 'helpers-default',
    'helpers-other-platform', 'helpers-null', 'helpers-empty', 'helpers-with-singular',
    'registry-valid', 'registry-expand', 'registry-empty-object', 'registry-hkcu-valid',
    'registry-malformed', 'registry-empty-success', 'registry-empty-value', 'registry-missing-value',
    'registry-wrong-type', 'registry-array', 'registry-null', 'registry-hkcu-malformed',
    'managed-stable', 'managed-stable-link', 'managed-replace', 'managed-chmod',
    'managed-touch', 'managed-retarget', 'managed-disappears', 'managed-replace-on-close', 'managed-before-open', 'managed-realpath-error',
]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode', action='append', choices=MODES, help='Run only the selected case(s).')
    parser.add_argument('--worker', type=Path, help='Use an isolated metadata worker for before/after diagnosis.')
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-startup-preflight-'))
    print('Evidence directory:', root, flush=True)
    cases = {}
    for mode in args.mode or MODES:
        case = root / mode
        env = environment(case)
        # Select the WSL registry branch independently of the test host.
        env['WSL_DISTRO_NAME'] = 'netcap-owned-preflight'
        cwd = case / 'work'
        cwd.mkdir()
        user = Path(env['CLAUDE_CONFIG_DIR']) / 'settings.json'
        project, local, custom = cwd / '.claude/settings.json', cwd / '.claude/settings.local.json', case / 'custom.json'
        write(user, {'env': {'OWNED_STARTUP_SOURCE': 'PRIVATE_PREFLIGHT_SENTINEL'}})
        write(project, '{broken' if mode == 'malformed' else {'enabledPlugins': {'owned@local': False}, **({'env': {'OVERSIZED': 'x' * (1 << 20)}} if mode == 'oversized' else {})})
        write(custom, {'env': {'CUSTOM_SOURCE': 'PRIVATE_PREFLIGHT_SENTINEL'}})
        helper = case / 'helper'
        marker = case / 'helper-executed'
        write(helper, '#!/usr/bin/python3\nfrom pathlib import Path\nPath(' + repr(str(marker)) + ').write_text("unexpected")\n')
        helper.chmod(0o700)
        if mode.startswith('binding-'):
            policy = {'env': {'ANTHROPIC_BASE_URL': 'PRIVATE_PREFLIGHT_SENTINEL'}}
            if mode == 'binding-state':
                policy = {'env': {'CLAUDE_CONFIG_DIR': 'PRIVATE_PREFLIGHT_SENTINEL'}}
            elif mode == 'binding-api-helper':
                policy = {'apiKeyHelper': str(helper)}
            elif mode == 'binding-invalid-env':
                policy = {'env': {'OWNED_INVALID': 42}}
            elif mode == 'binding-benign':
                policy = {'env': {'OWNED_BENIGN': 'PRIVATE_PREFLIGHT_SENTINEL'}}
            write(case / 'controlled-policy/managed-settings.json', policy)
        if mode == 'helper':
            write(case / 'controlled-policy/managed-settings.json', {'policyHelper': {'path': str(helper)}})
        if mode.startswith('helpers-'):
            helpers = {'linux': {'path': str(helper), 'timeoutMs': 2000}}
            if mode == 'helpers-wsl':
                helpers = {'wsl': {'path': str(helper), 'timeoutMs': 2000}}
            elif mode == 'helpers-script':
                helpers = {'linux': {'script': 'echo PRIVATE_PREFLIGHT_SENTINEL', 'interpreter': 'sh', 'timeoutMs': 2000}}
            elif mode == 'helpers-payload':
                helpers = {'linux': {'defaultSettings': {'permissions': {'deny': ['Read(/owned-policy/**)']}}}}
            elif mode == 'helpers-default':
                helpers = {'default': {'permissions': {'deny': ['Read(/owned-policy/**)']}}}
            elif mode == 'helpers-other-platform':
                helpers = {'windows': {'path': str(helper), 'timeoutMs': 2000}}
            elif mode == 'helpers-null':
                helpers = None
            elif mode == 'helpers-empty':
                helpers = {}
            policy = {'policyHelpers': helpers}
            if mode == 'helpers-with-singular':
                policy['policyHelper'] = {'path': str(helper)}
            write(case / 'controlled-policy/managed-settings.json', policy)
        if mode.startswith('managed-'):
            policy = case / 'controlled-policy/managed-settings.json'
            value = {'enabledPlugins': {'owned@managed': False}, 'env': {'OWNED_MANAGED_SOURCE': 'PRIVATE_PREFLIGHT_SENTINEL'}}
            if mode in ['managed-stable-link', 'managed-retarget']:
                for target in ['first', 'second']:
                    write(case / ('controlled-policy/targets/' + target + '.json'), value)
                policy.symlink_to(case / 'controlled-policy/targets/first.json')
            else:
                write(policy, value)
        request = {'root': str(case), 'mode': mode, 'cwd': str(cwd), 'user': str(user), 'project': str(project), 'local': str(local), 'custom': str(custom), 'helper_marker': str(marker)}
        if args.worker:
            request['worker'] = str(args.worker.resolve())
        write(case / 'request.json', request)
        process = subprocess.Popen(['node', str(Path(__file__).with_suffix('.mjs').resolve()), str(case / 'request.json')], cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
        try:
            stdout, stderr = process.communicate(timeout=25)
            write(case / 'stdout.log', stdout)
            write(case / 'stderr.log', stderr)
        finally:
            stop(process)
        result = json.loads((case / 'result.json').read_text()) if (case / 'result.json').exists() else {'passed': False, 'checks': {}}
        cases[mode] = {'exit_code': process.returncode, **result}
    passed = all(c['exit_code'] == 0 and c['passed'] for c in cases.values())
    write(root / 'result.json', {'cases': cases, 'passed': passed})
    print(json.dumps({'passed': passed, 'cases': {k: {'exit_code': v['exit_code'], 'failed': [n for n, ok in v['checks'].items() if not ok]} for k, v in cases.items()}}, indent=2))
    raise SystemExit(0 if passed else 1)


if __name__ == '__main__':
    main()
