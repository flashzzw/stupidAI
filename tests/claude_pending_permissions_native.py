#!/usr/bin/env python3
"""Pending original approvals across owned native policy changes; no real provider."""
import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile

from claude_permission_catalog_native import PermissionProvider
from claude_skill_method_probe import strings
from execution_config_probe import CLAUDE, environment, verify_versions, write
from runtime.common import stop

REPO = Path(__file__).resolve().parents[1]
MODES = ['unchanged', 'deny', 'restore', 'invalid', 'user-invalid', 'local-drift', 'interrupt']


def inside(root, binary, mode):
    assert os.path.samefile(root / 'policy', '/etc/claude-code')
    assert os.readlink('/proc/self/ns/mnt') != os.environ['NETCAP_PENDING_PARENT_NAMESPACE']
    verify_versions(root, ['claude'])
    env = environment(root / 'application-environment')
    cwd = root / 'work'
    cwd.mkdir()
    subprocess.run(['git', 'init', '--quiet', str(cwd)], env=env, check=True)
    write(cwd / '.claude/settings.local.json', {'env': {'NETCAP_PENDING_VERSION': 'original'}})
    initial = {'allowManagedPermissionRulesOnly': True, 'permissions': {'ask': ['Read(./target-A.txt)', 'Read(./target-B.txt)']}}
    write(root / 'policy/managed-settings.json', initial)
    providers, profiles = {}, {}
    proc = None
    try:
        for label in ['A', 'B']:
            profile = Path(environment(root / ('execution-' + label))['CLAUDE_CONFIG_DIR'])
            target = cwd / ('target-' + label + '.txt')
            write(target, 'OWNED_PENDING_POLICY_CONTENT_' + label + '\n')
            provider = PermissionProvider(root, label)
            provider.target = target
            providers[label] = provider
            wrapper = root / ('node-' + label)
            write(wrapper, '#!/bin/sh\nexport NETCAP_SETTINGS_WIRE_MODE=session\nexport NETCAP_SETTINGS_WIRE=' + shlex.quote(str(root / ('wire-' + label + '.jsonl'))) + '\nexec ' + shlex.quote(shutil.which('node')) + ' --import ' + shlex.quote(str(REPO / 'tests/claude_settings_wire_capture.mjs')) + ' "$@"\n')
            wrapper.chmod(0o700)
            profiles[label] = {'Profile': str(profile), 'Endpoint': provider.url, 'Node': str(wrapper)}
        request = root / 'application-request.json'
        write(request, {'Root': str(root), 'Cwd': str(cwd), 'Executable': CLAUDE, 'Mode': mode, 'Profiles': profiles})
        proc = subprocess.Popen([str(binary), '-test.run=^TestClaudeNativePendingPermissionChanges$', '-test.v'],
            cwd=REPO / 'internal/runtime', env={**env, 'NETCAP_PENDING_REQUEST': str(request), 'NETCAP_PENDING_A': 'fixture-A', 'NETCAP_PENDING_B': 'fixture-B'},
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        stdout, stderr = proc.communicate(timeout=100)
        write(root / 'application.stdout', stdout)
        write(root / 'application.stderr', stderr)
        print(stdout, end='')
        print(stderr, end='', file=sys.stderr)
        checks = {'application_passed': proc.returncode == 0,
                  'other_profile_has_no_model_request': not providers['B'].requests,
                  'owned_auth_and_clean_providers': all(not p.errors and all(r['auth_matches'] for r in p.requests) for p in providers.values())}
        if (root / 'application-result.json').exists():
            checks.update(json.loads((root / 'application-result.json').read_text())['checks'])
        calls, outputs = {}, {}
        for row in providers['A'].requests:
            for message in row['body'].get('messages', []):
                blocks = message.get('content')
                for block in blocks if isinstance(blocks, list) else []:
                    if block.get('type') == 'tool_use':
                        calls[block['id']] = block
                    elif block.get('type') == 'tool_result':
                        outputs[block['tool_use_id']] = block
        wire_calls, wire_outputs = {}, {}
        for line in (root / 'wire-A.jsonl').read_text().splitlines():
            row = json.loads(line)
            message = row.get('message', {})
            content = message.get('message', {}).get('content')
            if row.get('direction') != 'response' or message.get('type') not in ['assistant', 'user'] or not isinstance(content, list):
                continue
            for block in content:
                if block.get('type') == 'tool_use':
                    wire_calls[block['id']] = block
                elif block.get('type') == 'tool_result':
                    wire_outputs[block['tool_use_id']] = block
        checks['one_original_read_call'] = len(wire_calls) == 1 and all(v['name'] == 'Read' and v['input']['file_path'] == str(providers['A'].target) for v in wire_calls.values())
        checks['native_file_bytes_only_after_unchanged_approval'] = ('OWNED_PENDING_POLICY_CONTENT_A' in strings(list(wire_outputs.values()))) == (mode == 'unchanged')
        if mode != 'interrupt':
            checks['one_correlated_tool_result'] = len(outputs) == 1 and set(outputs) == set(calls)
            checks['actual_tool_decision'] = len(outputs) == 1 and all(bool(v.get('is_error')) == (mode != 'unchanged') for v in outputs.values())
            checks['two_local_model_requests'] = len(providers['A'].requests) == 2
        checks['file_bytes_only_after_unchanged_approval'] = ('OWNED_PENDING_POLICY_CONTENT_A' in strings(list(outputs.values()))) == (mode == 'unchanged')
        result = {'mode': mode, 'checks': checks, 'passed': bool(checks) and all(checks.values()),
                  'calls': calls, 'outputs': outputs, 'wire_calls': wire_calls, 'wire_outputs': wire_outputs,
                  'requests': {label: len(p.requests) for label, p in providers.items()}}
        write(root / 'result.json', result)
        print(json.dumps({'root': str(root), 'mode': mode, 'checks': len(checks), 'failed': [k for k, v in checks.items() if not v]}))
        if not result['passed']:
            raise SystemExit(1)
    finally:
        stop(proc)
        for provider in providers.values():
            provider.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--case', choices=MODES, default='deny')
    parser.add_argument('--inside', type=Path)
    args = parser.parse_args()
    if args.inside:
        return inside(args.inside, args.binary, args.case)
    root = Path(tempfile.mkdtemp(prefix='netcap-pending-permissions-', dir='/var/tmp'))
    write(root / 'policy/managed-settings.json', {})
    registry = root / 'windows/Windows/System32/reg.exe'
    write(registry, '#!/usr/bin/python3\nimport sys\nsys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\nraise SystemExit(1)\n')
    registry.chmod(0o700)
    env = environment(root / 'environment')
    env['NETCAP_PENDING_PARENT_NAMESPACE'] = os.readlink('/proc/self/ns/mnt')
    command = ['bwrap', '--unshare-user', '--unshare-pid', '--unshare-net', '--die-with-parent', '--ro-bind', '/', '/',
        '--tmpfs', '/etc', '--ro-bind', str(root / 'policy'), '/etc/claude-code', '--bind', str(root), str(root),
        '--tmpfs', '/mnt', '--ro-bind', str(root / 'windows'), '/mnt/c', '--tmpfs', '/tmp', '--tmpfs', '/run',
        '--proc', '/proc', '--dev', '/dev', sys.executable, str(Path(__file__).resolve()), '--inside', str(root), '--case', args.case, '--binary', str(args.binary.resolve())]
    write(root / 'invocation.json', {'command': command, 'namespace': env['NETCAP_PENDING_PARENT_NAMESPACE'], 'registry': 'owned absence, not Windows verification'})
    print('Evidence directory:', root, flush=True)
    proc = subprocess.Popen(command, cwd=REPO, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        stdout, stderr = proc.communicate(timeout=120)
        write(root / 'runner.stdout', stdout)
        write(root / 'runner.stderr', stderr)
        print(stdout, end='')
        print(stderr, end='', file=sys.stderr)
    finally:
        stop(proc)
    raise SystemExit(proc.returncode)


if __name__ == '__main__':
    main()
