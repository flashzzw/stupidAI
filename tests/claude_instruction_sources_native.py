#!/usr/bin/env python3
"""Inspect owned instruction sources; --loads also uses a local scripted Read."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import uuid

from claude_bridge_smoke import Bridge
from execution_config_probe import CLAUDE, environment, verify_versions, write
from runtime.common import Provider, stop

REPO = Path(__file__).resolve().parents[1]


def inside(root, direct, binary, loads=False):
    verify_versions(root, ['claude'])
    env = environment(root / 'execution')
    profile = Path(env['CLAUDE_CONFIG_DIR'])
    parent = root / 'project'
    cwd = parent / 'nested/work'
    cwd.mkdir(parents=True)
    subprocess.run(['git', 'init', '--quiet', str(cwd)], check=True, env=env)
    files = [profile / 'CLAUDE.md', parent / 'CLAUDE.md', cwd / 'CLAUDE.md',
             cwd / 'reference.md', parent / '.claude/rules/owned.md']
    if loads:
        files += [cwd / 'CLAUDE.local.md', root / 'policy/CLAUDE.md']
    for i, path in enumerate(files):
        write(path, f'PRIVATE_INSTRUCTION_BODY_{i}\nUse only the owned fixture.\n')
    write(cwd / 'CLAUDE.md', 'PRIVATE_INSTRUCTION_BODY_2\n@./reference.md\n')
    if direct:
        request = root / 'direct-request.json'
        write(request, {'cwd': str(cwd), 'output': str(root / 'direct-result.json')})
        result = subprocess.run(['node', str(REPO / 'tests/claude_skill_sources_probe.mjs'), str(request)],
                                cwd=REPO, env=env, text=True, capture_output=True, timeout=30)
        write(root / 'direct.stderr', result.stderr)
        if result.returncode:
            raise RuntimeError('native metadata probe failed; see direct.stderr')
        data = json.loads((root / 'direct-result.json').read_text())
        print(json.dumps({'memory_files': data.get('memory_files'), 'model_results': data.get('model_results')}))
        return
    checks, reports = {}, {}
    client = None
    if loads:
        from claude_instruction_lifecycle_probe import InstructionProvider
        provider = InstructionProvider(root, 'A')
        provider.mode, provider.read_file = 'nested-read', cwd / 'feature/data.txt'
        write(cwd / 'feature/CLAUDE.md', 'PRIVATE_INSTRUCTION_NESTED\nUse only the owned fixture.\n')
        write(provider.read_file, 'OWNED_NESTED_READ_RESULT\n')
    else:
        provider = Provider(root, 'A')
    if binary:
        try:
            request = root / 'application-request.json'
            write(request, {'Root': str(root), 'Cwd': str(cwd), 'Profile': str(profile), 'Executable': CLAUDE,
                            'Endpoint': provider.url, 'Files': [str(p) if p != root / 'policy/CLAUDE.md' else '/etc/claude-code/CLAUDE.md' for p in files],
                            'ReadFile': str(provider.read_file) if loads else ''})
            name = 'TestClaudeNativeInstructionLoads' if loads else 'TestClaudeNativeInstructionSources'
            result = subprocess.run([str(binary), '-test.run=^' + name + '$', '-test.v'],
                cwd=REPO / 'internal/runtime', env={**env, 'NETCAP_INSTRUCTION_REQUEST': str(request), 'NETCAP_INSTRUCTION_KEY': 'fixture-A'},
                text=True, capture_output=True, timeout=90 if loads else 60)
            write(root / 'application.stdout', result.stdout)
            write(root / 'application.stderr', result.stderr)
            write(root / 'provider-result.json', {'requests': len(provider.requests), 'errors': provider.errors})
            print(result.stdout, end='')
            print(result.stderr, end='', file=sys.stderr)
            assert result.returncode == 0 and len(provider.requests) == (4 if loads else 0) and not provider.errors, 'native Application instruction validation failed'
            assert all(r['auth_matches'] and 'count_tokens' not in r['path'] for r in provider.requests), 'unexpected local provider request'
            return
        finally:
            provider.close()

    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    try:
        client = Bridge(REPO / 'bridges/claude/dist/main.js', root, env)
        native = str(uuid.uuid4())
        config = {'native_id': native, 'source': native, 'cwd': str(cwd), 'executable': CLAUDE,
                  'model': 'claude-sonnet-4-6', 'endpoint': provider.url,
                  'permission': 'read_only', 'api_key': 'fixture-A'}
        client.call('create', config)
        before = client.call('snapshot', {'native_id': native})
        catalog = client.call('skill_catalog', {'native_id': native})
        reports['catalog'] = catalog
        sample = catalog['inventory'].get('instructions')
        check('native_instruction_sample_available', sample is not None and sample['available'])
        observed = [item['path'] for item in sample['items']]
        check('user_parent_project_rule_and_import_reported', all(str(path) in observed for path in files))
        check('no_instruction_body_exported', 'PRIVATE_INSTRUCTION_BODY_' not in json.dumps(catalog))
        after = client.call('snapshot', {'native_id': native})
        check('same_query_binding_and_history', before['config'] == after['config'] and before['connection'] == after['connection']
              and before['entries'] == after['entries'] and before['history_digest'] == after['history_digest'])
        check('no_model_input', not any(row['kind'] == 'input' for row in after['entries']))
        write(files[1], 'PRIVATE_INSTRUCTION_BODY_REPLACEMENT\n')
        reports['after_disk_change'] = client.call('skill_catalog', {'native_id': native})
        check('disk_change_does_not_export_new_body', 'PRIVATE_INSTRUCTION_BODY_' not in json.dumps(reports['after_disk_change']))
        client.close()
        client = Bridge(REPO / 'bridges/claude/dist/main.js', root, env)
        before_closed = client.call('snapshot', {'native_id': native})
        rejected = False
        try:
            client.call('skill_catalog', {'native_id': native})
        except AssertionError:
            rejected = True
        after_closed = client.call('snapshot', {'native_id': native})
        check('closed_query_read_does_not_reopen', rejected and not after_closed['live'] and before_closed['entries'] == after_closed['entries'])
        check('no_model_or_token_count_requests', not provider.requests)
    finally:
        if client:
            client.close()
        provider.close()
        write(root / 'result.json', {'checks': checks, 'reports': reports})
    print(json.dumps({'passed': all(checks.values()), 'checks': checks}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside', type=Path)
    parser.add_argument('--direct', action='store_true')
    parser.add_argument('--binary', type=Path)
    parser.add_argument('--loads', action='store_true', help='Validate persistent load notifications with the native Application fixture')
    args = parser.parse_args()
    if args.loads and (args.direct or not args.binary):
        parser.error('--loads requires --binary and cannot be combined with --direct')
    if args.inside:
        return inside(args.inside, args.direct, args.binary, args.loads)
    root = Path(tempfile.mkdtemp(prefix='netcap-instruction-sources-', dir='/var/tmp'))
    (root / 'policy').mkdir()
    registry = root / 'windows/Windows/System32/reg.exe'
    write(registry, '#!/usr/bin/python3\nimport sys\nsys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\nraise SystemExit(1)\n')
    registry.chmod(0o700)
    print('Evidence directory:', root, flush=True)
    command = ['bwrap', '--unshare-user', '--unshare-pid', '--unshare-net', '--die-with-parent', '--ro-bind', '/', '/',
               '--tmpfs', '/etc', '--ro-bind', str(root / 'policy'), '/etc/claude-code', '--bind', str(root), str(root),
               '--tmpfs', '/mnt', '--ro-bind', str(root / 'windows'), '/mnt/c', '--tmpfs', '/tmp', '--tmpfs', '/run', '--proc', '/proc', '--dev', '/dev',
               sys.executable, str(Path(__file__).resolve()), '--inside', str(root)]
    if args.direct:
        command.append('--direct')
    if args.binary:
        command += ['--binary', str(args.binary.resolve())]
    if args.loads:
        command.append('--loads')
    proc = subprocess.Popen(command, cwd=REPO, env=environment(root / 'environment'), text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        out, err = proc.communicate(timeout=105 if args.loads else 75)
        write(root / 'runner.stdout', out)
        write(root / 'runner.stderr', err)
        print(out, end='')
        print(err, end='', file=sys.stderr)
    finally:
        stop(proc)
    raise SystemExit(proc.returncode)


if __name__ == '__main__':
    main()
