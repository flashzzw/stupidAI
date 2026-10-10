#!/usr/bin/env python3
"""Read-only Windows -> WSL launch/argument probe, using an isolated CLI profile.

Run in WSL. --local-check validates only the Linux receiver, not Windows.
No model calls, listener, installation, distribution termination or user edits.
"""
import argparse
import base64
import json
import os
from pathlib import Path
import pwd
import subprocess
import sys
import tempfile
import uuid

from execution_config_probe import environment

SCRIPT = Path(__file__).resolve()
POWERSHELL = Path('/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe')
CLI = '/home/zzw/.local/share/claude/versions/2.1.280'
VALUES = ['', 'two words', '中文参数', 'double"quote', "single'quote",
          'trailing\\', '$(not-a-command)', '& literal | text']


def decode_wsl_output(encoded):
    raw = base64.b64decode(encoded, validate=True)
    if raw.startswith((b'\xff\xfe', b'\xfe\xff')):
        encoding = 'utf-16'
    elif b'W\x00s\x00l\x00/' in raw:
        # WSL error-code text identifies BOM-less native UTF-16LE output.
        encoding = 'utf-16le'
    else:
        encoding = 'utf-8-sig'
    value = raw.decode(encoding)
    if '\x00' in value or '\ufffd' in value:
        raise ValueError('WSL output contains undecoded or replacement characters')
    return value, encoding


def expected_launch_error(name, record):
    code = {'missing_distro': 'WSL_E_DISTRO_NOT_FOUND',
            'missing_user': 'WSL_E_USER_NOT_FOUND'}[name]
    return (record['exit_code'] not in (None, 0) and
            code in record['stdout'] + record['stderr'])


def sample(root, binary, values):
    result = {'cwd': os.getcwd(), 'uid': os.getuid(),
              'user': pwd.getpwuid(os.getuid()).pw_name,
              'distro': os.environ.get('WSL_DISTRO_NAME'),
              'pid': os.getpid(), 'argv': values}
    try:
        completed = subprocess.run([binary, '--version'],
                                   env=environment(Path(root)),
                                   capture_output=True, text=True, timeout=10)
        result['cli'] = {'exit_code': completed.returncode,
                         'stdout': completed.stdout.strip(),
                         'stderr': completed.stderr[:1000]}
    except FileNotFoundError:
        result['cli'] = {'error': 'cli_missing'}
    except subprocess.TimeoutExpired:
        result['cli'] = {'error': 'cli_timeout'}
    print(json.dumps(result, ensure_ascii=True), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--local-check', action='store_true')
    options = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-wsl-host-'))
    print('Evidence directory:', root, flush=True)
    distro = os.environ.get('WSL_DISTRO_NAME')
    user = pwd.getpwuid(os.getuid()).pw_name
    report = {'scope': 'Linux receiver only' if options.local_check else
              'Windows launch, identity, argv and CLI version only; not live session control',
              'host_verified': False, 'expectations_met': False}
    try:
        if not distro:
            raise RuntimeError('WSL_DISTRO_NAME is required; target is never guessed')
        normal = root / 'work'
        special = root / "含空格 project 'quote"
        normal.mkdir()
        special.mkdir()
        cases = []
        for name, cwd, target_distro, target_user, binary in [
            ('normal', normal, distro, user, CLI),
            ('special', special, distro, user, CLI),
            ('repeat', normal, distro, user, CLI),
            ('missing_cli', normal, distro, user, str(root / 'missing-cli')),
            ('missing_distro', normal, 'netcap-missing-' + uuid.uuid4().hex, user, CLI),
            ('missing_user', normal, distro, 'netcap-missing-' + uuid.uuid4().hex, CLI),
        ]:
            linux = ['/usr/bin/python3', str(SCRIPT), '--sample',
                     str(root / ('profile-' + name)), binary, *VALUES]
            arguments = ['--distribution', target_distro, '--user', target_user,
                         '--cd', str(cwd), '--exec', *linux]
            cases.append({'name': name, 'cwd': str(cwd), 'linux': linux,
                          'windows_arguments': subprocess.list2cmdline(arguments)})
        (root / 'requests.json').write_text(json.dumps(cases, ensure_ascii=False, indent=2))
        if options.local_check:
            records = []
            for case in cases[:4]:
                done = subprocess.run(case['linux'], cwd=case['cwd'], capture_output=True,
                                      text=True, timeout=15)
                records.append({'name': case['name'], 'completed': True,
                                'exit_code': done.returncode, 'stdout': done.stdout,
                                'stderr': done.stderr, 'output_complete': True})
        else:
            host_cases = [{'name': c['name'], 'windows_arguments': c['windows_arguments']}
                          for c in cases]
            payload = base64.b64encode(json.dumps({'cases': host_cases}).encode()).decode()
            script = SCRIPT.with_suffix('.ps1').read_text()
            command = '& {\n' + script + '\n} ' + "'" + payload + "'"
            encoded = base64.b64encode(command.encode('utf-16le')).decode()
            done = subprocess.run([str(POWERSHELL), '-NoLogo', '-NoProfile',
                                   '-NonInteractive', '-EncodedCommand', encoded],
                                  capture_output=True, timeout=180)
            report['host_exit_code'] = done.returncode
            (root / 'host-stdout.txt').write_bytes(done.stdout)
            (root / 'host-stderr.txt').write_bytes(done.stderr)
            if done.returncode != 0:
                raise RuntimeError('Windows launcher failed; see host-stderr.txt')
            host = json.loads(done.stdout.decode('utf-8-sig'))
            report['host'] = host
            report['host_verified'] = host.get('platform') == 'Win32NT'
            records = host['cases']
            for record in records:
                for stream in ('stdout', 'stderr'):
                    record[stream], record[stream + '_encoding'] = decode_wsl_output(
                        record[stream + '_base64'])
        report['records'] = records
        checks = {}
        expected_names = [c['name'] for c in (cases[:4] if options.local_check else cases)]
        checks['all_requested_cases_returned_once'] = [r['name'] for r in records] == expected_names
        for record in records:
            name = record['name']
            checks[name + '_completed'] = record['completed'] and record['output_complete']
            if name in ('missing_distro', 'missing_user'):
                checks[name + '_rejected'] = expected_launch_error(name, record)
                continue
            value = json.loads(record['stdout'])
            case = next(c for c in cases if c['name'] == name)
            checks[name + '_identity'] = (value['uid'] == os.getuid() and
                value['user'] == user and value['distro'] == distro and value['cwd'] == case['cwd'])
            checks[name + '_argv'] = value['argv'] == VALUES
            checks[name + '_cli'] = (value['cli'].get('error') == 'cli_missing' if name == 'missing_cli'
                else value['cli'].get('exit_code') == 0 and value['cli'].get('stdout') == '2.1.280 (Claude Code)')
            checks[name + '_receiver_exit'] = record['exit_code'] == 0
        report['checks'] = checks
        report['expectations_met'] = all(checks.values()) and (
            options.local_check or report['host_verified'])
    except subprocess.TimeoutExpired:
        report['error_type'] = 'TimeoutExpired'
        report['error'] = 'Probe process timed out; host verification remains incomplete'
    except (OSError, ValueError, KeyError, RuntimeError) as error:
        report['error_type'] = type(error).__name__
        report['error'] = str(error)
    (root / 'result.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)
    return 0 if report['expectations_met'] else 1


if __name__ == '__main__':
    if sys.argv[1:2] == ['--sample']:
        sample(sys.argv[2], sys.argv[3], sys.argv[4:])
    else:
        raise SystemExit(main())
