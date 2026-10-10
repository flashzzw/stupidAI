#!/usr/bin/env python3
"""Exercise production policy admission inside an owned Linux mount namespace.

The real fixed SDK/CLI read /etc/claude-code, bound to this probe's private
directory. The host policy tree is never changed. No model input is sent.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile

from execution_config_probe import CLAUDE, environment, write
from runtime.common import stop


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bridge', type=Path, help='Use a baseline bridge for the Runtime failure comparison.')
    parser.add_argument('--binary', type=Path, help='Reuse the compiled runtime test binary.')
    parser.add_argument('--surface', choices=['runtime', 'application', 'create', 'candidate'], default='runtime',
                        help='Check Runtime admission, Application/Handler preview, or ordinary creation.')
    args = parser.parse_args()
    if args.bridge and args.surface != 'runtime':
        parser.error('--bridge is only supported by the runtime surface')
    repo = Path(__file__).resolve().parents[1]
    root = Path(tempfile.mkdtemp(prefix='netcap-policy-admission-'))
    print('Evidence directory:', root, flush=True)
    binary = args.binary.resolve() if args.binary else root / 'runtime.test'
    if not args.binary:
        build = subprocess.run(['go', 'test', '-c', '-tags', 'nativeclaude', '-o', str(binary), './internal/runtime'],
                               cwd=repo, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=120)
        write(root / 'build.stdout', build.stdout)
        write(root / 'build.stderr', build.stderr)
        if build.returncode:
            raise SystemExit('native test build failed; see build.stderr')
    env = environment(root / 'environment')
    policy = root / 'policy'
    write(policy / 'managed-settings.json', {})
    # WSL registry subprocesses cannot use host interop inside this namespace.
    # Return an explicit controlled absence; this is not a Windows policy test.
    windows = root / 'controlled-windows'
    registry = windows / 'Windows/System32/reg.exe'
    write(registry, '#!/usr/bin/python3\nimport json,sys\nfrom pathlib import Path\n'
          'with Path(' + repr(str(root / 'registry-calls.jsonl')) + ').open("a") as f:f.write(json.dumps(sys.argv[1:])+"\\n")\n'
          'assert len(sys.argv)==5 and sys.argv[1]=="query" and sys.argv[3:]==["/v","Settings"]\n'
          'sys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\n'
          'raise SystemExit(1)\n')
    registry.chmod(0o700)
    env.update(NETCAP_CLAUDE_POLICY_TEST_ROOT=str(root), NETCAP_CLAUDE_NATIVE_TEST=CLAUDE,
               NETCAP_POLICY_PARENT_NAMESPACE=os.readlink('/proc/self/ns/mnt'),
               NETCAP_SETTINGS_PREVIEW_ROOT=str(root), TMPDIR=str(root))
    if args.bridge:
        env['NETCAP_CLAUDE_POLICY_BRIDGE'] = str(args.bridge.resolve())
    test_name = {'runtime': 'TestClaudeNativePolicyHelperAdmission', 'application': 'TestClaudeNativePluginSettingsPreview', 'create': 'TestClaudeNativePolicyCreateBinding', 'candidate': 'TestClaudeNativeInstalledPluginSettingsCandidate'}[args.surface]
    argv = ['bwrap', '--unshare-user', '--unshare-pid', '--unshare-net', '--die-with-parent', '--ro-bind', '/', '/',
            '--tmpfs', '/etc', '--ro-bind', str(policy), '/etc/claude-code', '--bind', str(root), str(root),
            '--tmpfs', '/mnt', '--ro-bind', str(windows), '/mnt/c', '--tmpfs', '/tmp', '--tmpfs', '/run',
            '--proc', '/proc', '--dev', '/dev', '--chdir', str(repo / 'internal/runtime'),
            str(binary), '-test.run', '^' + test_name + '$', '-test.v', '-test.timeout', '90s']
    write(root / 'invocation.json', {'argv': argv, 'parent_namespace': env['NETCAP_POLICY_PARENT_NAMESPACE'],
                                   'bridge': str(args.bridge) if args.bridge else 'current', 'network': 'isolated namespace',
                                   'registry': 'controlled absent HKLM/HKCU, not actual Windows policy', 'surface': args.surface})
    process = subprocess.Popen(argv, cwd=repo, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
    try:
        stdout, stderr = process.communicate(timeout=105)
        write(root / 'native.stdout', stdout)
        write(root / 'native.stderr', stderr)
    finally:
        stop(process)
    pattern = {'runtime': 'netcap-policy-admission-*/result.json', 'application': 'netcap-settings-preview-native-*/result.json', 'create': 'netcap-policy-create-native-*/result.json', 'candidate': 'netcap-settings-preview-native-*/result.json'}[args.surface]
    directories = sorted(root.glob(pattern))
    report = json.loads(directories[0].read_text()) if len(directories) == 1 else {}
    result = {'exit_code': process.returncode, 'native_result': str(directories[0]) if len(directories) == 1 else None,
              'passed': process.returncode == 0 and report.get('passed') is True,
              'checks': report.get('checks', {}), 'host_policy_modified': False}
    write(root / 'result.json', result)
    print(json.dumps({'passed': result['passed'], 'exit_code': result['exit_code'],
                      'checks': len(result['checks']), 'failed': [k for k, v in result['checks'].items() if not v]}, indent=2))
    raise SystemExit(0 if result['passed'] else 1)


if __name__ == '__main__':
    main()
