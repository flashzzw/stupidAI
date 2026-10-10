#!/usr/bin/env python3
"""Check local IPC/Windows interop prerequisites without models or user state.

All sockets and directories belong to this process. Windows is queried only for
its PowerShell version. Passing this probe does not validate W-series behavior.
"""
import argparse
import errno
import json
from pathlib import Path
import platform
import socket
import subprocess
import tempfile

POWERSHELL = Path('/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe')


def check_socket(operation, run):
    try:
        run()
        return {'operation': operation, 'status': 'passed'}
    except OSError as error:
        return {'operation': operation, 'status': 'blocked' if error.errno in (errno.EPERM, errno.EACCES) else 'failed',
                'errno': error.errno, 'error_code': errno.errorcode.get(error.errno, 'UNKNOWN')}


def anonymous_send():
    left, right = socket.socketpair()
    try:
        left.settimeout(2)
        right.settimeout(2)
        if left.send(b'x') != 1 or right.recv(1) != b'x':
            raise OSError('anonymous socket did not transfer its byte')
    finally:
        left.close()
        right.close()


def listen_unix(root):
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as listener:
        listener.bind(str(root / 'owned.sock'))
        listener.listen(1)


def listen_tcp():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(('127.0.0.1', 0))
        listener.listen(1)


def windows_version():
    result = {'operation': 'Windows PowerShell read-only version', 'executable_present': POWERSHELL.is_file()}
    if not result['executable_present']:
        return {**result, 'status': 'missing'}
    try:
        completed = subprocess.run([str(POWERSHELL), '-NoLogo', '-NoProfile', '-NonInteractive',
                                    '-Command', '$PSVersionTable.PSVersion.ToString()'],
                                   capture_output=True, text=True, errors='replace', timeout=10)
        return {**result, 'status': 'passed' if completed.returncode == 0 and completed.stdout.strip() else 'failed',
                'exit_code': completed.returncode, 'stdout': completed.stdout[:512], 'stderr': completed.stderr[:512]}
    except (OSError, subprocess.TimeoutExpired) as error:
        return {**result, 'status': 'failed', 'error_type': type(error).__name__}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='netcap-environment-') as folder:
        checks = {
            'anonymous_unix_send': check_socket('Unix socketpair send', anonymous_send),
            'unix_listener': check_socket('Unix socket bind/listen', lambda: listen_unix(Path(folder))),
            'local_http_listener': check_socket('loopback TCP bind/listen', listen_tcp),
            'windows_interop': windows_version(),
        }
    result = {'kernel': platform.release(), 'checks': checks,
              'prerequisites_met': all(item['status'] == 'passed' for item in checks.values()),
              'scope': 'owned IPC and read-only Windows launch prerequisites only; no CLI session, provider, model or existing daemon modified'}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False, indent=2), flush=True)
    return 0 if result['prerequisites_met'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
