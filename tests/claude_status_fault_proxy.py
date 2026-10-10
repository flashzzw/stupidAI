#!/usr/bin/env python3
"""Owned fixture proxy: fail only MCP status RPCs while the marker exists."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import threading

CLI = '/home/zzw/.local/bin/claude'
if '--version' in sys.argv:
    os.execv(CLI, [CLI, *sys.argv[1:]])

marker = Path(os.environ['NETCAP_STATUS_FAULT'])
child = subprocess.Popen([CLI, *sys.argv[1:]], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=sys.stderr, text=True, bufsize=1)
lock = threading.Lock()


def output(line):
    with lock:
        sys.stdout.write(line)
        sys.stdout.flush()


def forward():
    for line in child.stdout: output(line)


def terminate(*_):
    raise SystemExit(0)


signal.signal(signal.SIGTERM, terminate)
reader = threading.Thread(target=forward, daemon=True)
reader.start()
try:
    for line in sys.stdin:
        message = json.loads(line)
        if marker.exists() and message.get('type') == 'control_request' and message.get('request', {}).get('subtype') == 'mcp_status':
            with marker.with_suffix('.observed').open('a') as log: log.write('mcp_status rejected\n')
            output(json.dumps({'type': 'control_response', 'response': {'subtype': 'error', 'request_id': message['request_id'], 'error': 'fixture MCP status unavailable'}}) + '\n')
        else:
            child.stdin.write(line)
            child.stdin.flush()
finally:
    child.stdin.close()
    if child.poll() is None: child.terminate()
    try: child.wait(timeout=5)
    except subprocess.TimeoutExpired:
        child.kill()
        child.wait(timeout=5)
    reader.join(timeout=2)
