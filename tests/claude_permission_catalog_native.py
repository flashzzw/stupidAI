#!/usr/bin/env python3
"""Original-query permission reports and live policy decisions with owned CLIs."""
import argparse
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile

from claude_skill_method_probe import SkillProvider, strings
from execution_config_probe import CLAUDE, environment, verify_versions, write
from runtime.common import stop

REPO = Path(__file__).resolve().parents[1]


class PermissionProvider(SkillProvider):
    def plan(self, payload):
        messages = payload.get('messages', [])
        phases = [(i, re.search(r'NETCAP_PERMISSION_CASE=([a-z-]+) ROUTE=([AB])', strings(m.get('content'))))
                  for i, m in enumerate(messages) if m.get('role') == 'user']
        phases = [(i, match.group(1), match.group(2)) for i, match in phases if match]
        if not phases:
            return {'text': 'No permission diagnostic task.'}
        start, phase, route = phases[-1]
        if route != self.route:
            raise AssertionError('permission input reached another provider')
        issued = any(m.get('role') == 'assistant' and any(b.get('type') == 'tool_use' for b in m.get('content', []) if isinstance(b, dict)) for m in messages[start:])
        return {'text': 'NETCAP_PERMISSION_DONE_' + phase} if issued else {'tool': 'Read', 'input': {'file_path': str(self.target)}}


def audit_requests(root, providers):
    checks, phases = {}, {}
    for route, provider in providers.items():
        checks[route + '_local_auth_and_clean_provider'] = all(row['auth_matches'] for row in provider.requests) and not provider.errors
        checks[route + '_no_token_count_request'] = all('count_tokens' not in row['path'] for row in provider.requests)
        for row in provider.requests:
            messages = row['body'].get('messages', [])
            marked = [(i, re.search(r'NETCAP_PERMISSION_CASE=([a-z-]+) ROUTE=([AB])', strings(m.get('content'))))
                      for i, m in enumerate(messages) if m.get('role') == 'user']
            marked = [(i, m.group(1), m.group(2)) for i, m in marked if m]
            if not marked:
                continue
            start, phase, actual_route = marked[-1]
            assert actual_route == route
            record = phases.setdefault(route + '/' + phase, {'requests': 0, 'calls': [], 'results': []})
            record['requests'] += 1
            for message in messages[start:]:
                for block in message.get('content', []) if isinstance(message.get('content'), list) else []:
                    if not isinstance(block, dict):
                        continue
                    if block.get('type') == 'tool_use' and not any(old['id'] == block['id'] for old in record['calls']):
                        record['calls'].append(block)
                    if block.get('type') == 'tool_result' and not any(old['tool_use_id'] == block['tool_use_id'] for old in record['results']):
                        record['results'].append(block)
    expected = {'A/initial': True, 'A/managed': False, 'A/ask-allow': False, 'A/ask-deny': True, 'A/resumed': True}
    checks['only_five_explicit_native_inputs'] = set(phases) == set(expected)
    for key, denied in expected.items():
        phase = phases.get(key, {})
        calls, results = phase.get('calls', []), phase.get('results', [])
        checks[key + '_one_exact_read'] = len(calls) == 1 and calls[0]['name'] == 'Read' and calls[0]['input']['file_path'] == str(providers['A'].target)
        checks[key + '_actual_native_decision'] = len(results) == 1 and bool(results[0].get('is_error')) == denied
        checks[key + '_result_correlated_to_call'] = len(calls) == len(results) == 1 and results[0]['tool_use_id'] == calls[0]['id']
        checks[key + '_two_local_model_requests'] = phase.get('requests') == 2
        checks[key + '_read_content_matches_decision'] = len(results) == 1 and ('OWNED_PERMISSION_TARGET_A' in strings(results[0].get('content'))) != denied
        if key == 'A/ask-allow':
            checks[key + '_approved_read_returns_new_file_bytes'] = len(results) == 1 and 'OWNED_PERMISSION_TARGET_A_APPROVED' in strings(results[0].get('content'))
    checks['other_profile_had_no_model_request'] = len(providers['B'].requests) == 0
    write(root / 'request-audit.json', {'checks': checks, 'phases': phases})
    return checks


def inside(root, binary):
    assert os.path.samefile(root / 'policy', '/etc/claude-code')
    assert os.readlink('/proc/self/ns/mnt') != os.environ['NETCAP_PERMISSION_PARENT_NAMESPACE']
    verify_versions(root, ['claude'])
    env = environment(root / 'application-environment')
    cwd = root / 'work'
    cwd.mkdir()
    subprocess.run(['git', 'init', '--quiet', str(cwd)], env=env, check=True)
    write(cwd / '.claude/settings.json', {'permissions': {'deny': ['Read(./project-private/**)']}})
    write(cwd / '.claude/settings.local.json', {'permissions': {'ask': ['Read(./local-review/**)']}})
    providers, profiles = {}, {}
    proc = None
    try:
        for route in ['A', 'B']:
            profile = Path(environment(root / ('execution-' + route))['CLAUDE_CONFIG_DIR'])
            extra = root / ('extra-' + route)
            extra.mkdir()
            user = {'permissions': {'deny': ['Read(./target-' + route + '.txt)'], 'additionalDirectories': [str(extra)]}}
            write(profile / 'settings.json', user)
            custom = root / ('custom-' + route + '.json')
            write(custom, {'permissions': {'allow': ['Read(./target-' + route + '.txt)']}})
            target = cwd / ('target-' + route + '.txt')
            write(target, 'OWNED_PERMISSION_TARGET_' + route + '\n')
            provider = PermissionProvider(root, route)
            provider.target = target
            providers[route] = provider
            wrapper = root / ('node-' + route)
            write(wrapper, '#!/bin/sh\nexport NETCAP_SETTINGS_WIRE_MODE=session\nexport NETCAP_SETTINGS_WIRE=' + shlex.quote(str(root / ('wire-' + route + '.jsonl'))) + '\nexec ' + shlex.quote(shutil.which('node')) + ' --import ' + shlex.quote(str(REPO / 'tests/claude_settings_wire_capture.mjs')) + ' "$@"\n')
            wrapper.chmod(0o700)
            profiles[route] = {'Profile': str(profile), 'SettingsFile': str(custom), 'Endpoint': provider.url, 'Node': str(wrapper), 'UserSettings': user}
        initial_policy = {'permissions': {'allow': ['Read(./target-A.txt)', 'Read(./target-B.txt)'], 'deny': ['Read(./managed-private/**)']}}
        write(root / 'policy/managed-settings.json', initial_policy)
        request = root / 'application-request.json'
        write(request, {'Root': str(root), 'Cwd': str(cwd), 'Executable': CLAUDE, 'Profiles': profiles, 'InitialPolicy': initial_policy})
        proc = subprocess.Popen([str(binary), '-test.run=^TestClaudeNativePermissionCatalog$', '-test.v'],
            cwd=REPO / 'internal/runtime', env={**env, 'NETCAP_PERMISSION_REQUEST': str(request), 'NETCAP_PERMISSION_A': 'fixture-A', 'NETCAP_PERMISSION_B': 'fixture-B'},
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        stdout, stderr = proc.communicate(timeout=160)
        write(root / 'application.stdout', stdout)
        write(root / 'application.stderr', stderr)
        print(stdout, end='')
        print(stderr, end='', file=sys.stderr)
        checks = audit_requests(root, providers)
        checks['application_passed'] = proc.returncode == 0
        if (root / 'application-result.json').exists():
            checks.update(json.loads((root / 'application-result.json').read_text())['checks'])
        write(root / 'result.json', {'checks': checks, 'passed': all(checks.values()), 'requests': {route: len(p.requests) for route, p in providers.items()},
            'scope': 'original Application/Handler/SDK/CLI queries; isolated local scripted providers and owned managed policy, not a real enterprise policy service'})
        print(json.dumps({'checks': len(checks), 'failed': [name for name, passed in checks.items() if not passed], 'root': str(root)}))
        if not all(checks.values()):
            raise RuntimeError('native permission catalog acceptance failed; see application.stdout and result.json')
    finally:
        stop(proc)
        for provider in providers.values():
            provider.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--inside', type=Path)
    args = parser.parse_args()
    if args.inside:
        return inside(args.inside, args.binary)
    root = Path(tempfile.mkdtemp(prefix='netcap-permission-catalog-', dir='/var/tmp'))
    write(root / 'policy/managed-settings.json', {})
    registry = root / 'windows/Windows/System32/reg.exe'
    write(registry, '#!/usr/bin/python3\nimport sys\nsys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\nraise SystemExit(1)\n')
    registry.chmod(0o700)
    env = environment(root / 'environment')
    env['NETCAP_PERMISSION_PARENT_NAMESPACE'] = os.readlink('/proc/self/ns/mnt')
    command = ['bwrap', '--unshare-user', '--unshare-pid', '--unshare-net', '--die-with-parent', '--ro-bind', '/', '/',
        '--tmpfs', '/etc', '--ro-bind', str(root / 'policy'), '/etc/claude-code', '--bind', str(root), str(root),
        '--tmpfs', '/mnt', '--ro-bind', str(root / 'windows'), '/mnt/c', '--tmpfs', '/tmp', '--tmpfs', '/run',
        '--proc', '/proc', '--dev', '/dev', sys.executable, str(Path(__file__).resolve()), '--inside', str(root), '--binary', str(args.binary.resolve())]
    write(root / 'invocation.json', {'command': command, 'namespace': env['NETCAP_PERMISSION_PARENT_NAMESPACE'], 'registry': 'owned absence, not Windows verification'})
    print('Evidence directory:', root, flush=True)
    proc = subprocess.Popen(command, cwd=REPO, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        stdout, stderr = proc.communicate(timeout=180)
        write(root / 'runner.stdout', stdout)
        write(root / 'runner.stderr', stderr)
        print(stdout, end='')
        print(stderr, end='', file=sys.stderr)
    finally:
        stop(proc)
    raise SystemExit(proc.returncode)


if __name__ == '__main__':
    main()
