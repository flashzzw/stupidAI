#!/usr/bin/env python3
"""Compare native MCP stdin with Node spawn; exit nonzero if MCP cannot connect.

No model prompt or listening service. Uses the existing pinned native probe and
isolated environment; failure observations never replace connection assertions.
"""
import argparse
import json
from pathlib import Path
import stat
import subprocess
import sys
import tempfile

from console_fixture import stop
from execution_config_probe import environment, verify_versions, write

REPO = Path(__file__).resolve().parents[1]
SERVER = '''import json,sys,os,stat
from pathlib import Path
root=Path(sys.argv[1])
def note(kind,**extra):
 with (root/'trace.jsonl').open('a') as f:f.write(json.dumps({'kind':kind,**extra})+'\\n')
s=os.fstat(0)
note('stdin',blocking=os.get_blocking(0),target=os.readlink('/proc/self/fd/0'),mode=stat.S_IFMT(s.st_mode))
for line in sys.stdin:
 r=json.loads(line)
 if 'id' not in r:continue
 note('request',method=r['method'])
 if r['method']=='initialize':v={'protocolVersion':r['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'diagnostic','version':'1'}}
 elif r['method']=='tools/list':v={'tools':[{'name':'identity','description':'fixture','inputSchema':{'type':'object','properties':{}}}]}
 else:v={}
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':v}),flush=True)
note('eof')
'''
NODE_CONTROL = '''import {spawn} from 'node:child_process';
const child=spawn(process.argv[2],[process.argv[3],process.argv[4]],{stdio:['pipe','pipe','pipe']});
child.stdout.pipe(process.stdout);child.stderr.pipe(process.stderr);
child.on('error',()=>{process.exitCode=1;});
child.on('close',code=>{process.exitCode=code??1;});
child.stdin.end(JSON.stringify({jsonrpc:'2.0',id:1,method:'initialize',params:{protocolVersion:'2024-11-05',capabilities:{},clientInfo:{name:'control',version:'1'}}})+'\\n');
'''


def run(argv, cwd, env, folder):
    proc = subprocess.Popen(argv, cwd=cwd, env=env, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, text=True, start_new_session=True)
    try:
        stdout, stderr = proc.communicate(timeout=30)
        write(folder / 'stderr.log', stderr)
        return {'exit_code': proc.returncode, 'stdout': stdout, 'stderr_empty': not stderr}
    finally:
        stop(proc)


def trace(folder):
    path = folder / 'trace.jsonl'
    return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []


def blocking_socket(events):
    return bool(events and events[0].get('blocking') is True
                and events[0].get('mode') == stat.S_IFSOCK)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path)
    parser.add_argument('--observe-send', action='store_true', help='Linux/glibc only: compile a passive libc observer; requires cc. Records metadata, never changes native send results.')
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-stdio-comparison-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    write(root / 'server.py', SERVER)
    write(root / 'node-control.mjs', NODE_CONTROL)
    native = root / 'native'
    env = environment(native)
    if args.observe_send:
        observer = root / 'send-observer.so'
        subprocess.run(['cc', '-shared', '-fPIC', '-Wall', '-Wextra', '-Werror', '-o',
                        str(observer), str(REPO / 'tests/claude_send_observer.c'), '-ldl'], check=True)
        env.update(LD_PRELOAD=str(observer), NETCAP_SEND_OBSERVER_LOG=str(native / 'send-calls.jsonl'))
    cwd = native / 'work'
    cwd.mkdir()
    subprocess.run(['git', 'init', '--quiet', str(cwd)], env=env, check=True)
    write(Path(env['CLAUDE_CONFIG_DIR']) / '.claude.json', {
        'projects': {str(cwd): {'hasTrustDialogAccepted': True}}})
    write(native / 'settings.json', {})
    write(native / 'definition.json', {'mcpServers': {'identity': {
        'command': sys.executable, 'args': [str(root / 'server.py'), str(native)]}}})
    write(native / 'input.json', {
        'cwd': str(cwd), 'settings': str(native / 'settings.json'),
        **({'debug_file': str(native / 'native-debug.log')} if args.observe_send else {}),
        'selections': [{'asset_id': 'test', 'server': 'identity', 'mode': 'enabled',
                        'source_path': str(native / 'definition.json')}],
        'output': str(native / 'result.json')})
    result = {'root': str(root), 'versions': json.loads((root / 'versions.json').read_text())}
    result['native'] = run(['node', str(REPO / 'tests/claude_mcp_sources_probe.mjs'),
                            str(native / 'input.json')], cwd, env, native)
    result['native']['trace'] = trace(native)
    result['native']['snapshot'] = json.loads((native / 'result.json').read_text())
    if args.observe_send:
        calls = native / 'send-calls.jsonl'
        result['send_observer'] = {
            'scope': 'passive libc send/sendto metadata in isolated native probe; return values and errno unchanged',
            'calls': [json.loads(line) for line in calls.read_text().splitlines()] if calls.exists() else [],
        }
    control = root / 'node'
    result['node_control'] = run(['node', str(root / 'node-control.mjs'), sys.executable,
                                  str(root / 'server.py'), str(control)], root,
                                 environment(control), control)
    result['node_control']['trace'] = trace(control)
    snapshot = result['native']['snapshot']
    response = json.loads(result['node_control']['stdout'])
    checks = {
        'node_initialize': result['node_control']['exit_code'] == 0
            and response.get('id') == 1
            and response.get('result', {}).get('protocolVersion') == '2024-11-05',
        'both_blocking_sockets': all(blocking_socket(result[key]['trace'])
                                     for key in ['native', 'node_control']),
        'no_model_results': snapshot['model_results'] == 0,
        'native_connected': result['native']['exit_code'] == 0
            and snapshot['inventory']['available'] and snapshot['selection_matches']
            and not snapshot['verification_error']
            and snapshot['inventory']['servers'] == [
                {'name': 'identity', 'status': 'connected', 'source': 'dynamic', 'tools': ['identity']}],
    }
    result['checks'] = checks
    result['expectations_met'] = all(checks.values())
    result['native_eof_before_initialize'] = any(e['kind'] == 'eof' for e in result['native']['trace']) and not any(
        e.get('method') == 'initialize' for e in result['native']['trace'])
    write(root / 'comparison.json', result)
    if args.output:
        write(args.output, result)
    print(json.dumps(result, ensure_ascii=False, indent=2), flush=True)
    return 0 if result['expectations_met'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
