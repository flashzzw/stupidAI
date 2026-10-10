#!/usr/bin/env python3
"""Real pinned SDK/bridge metadata reads over isolated synthetic transcripts.

No CLI query, model, socket listener or native control is exercised.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import subprocess
import tempfile
import uuid

from execution_config_probe import environment, write
from console_fixture import stop

REPO = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-sdk-discovery-'))
    print('Evidence directory:', root, flush=True)
    project = root / 'project'
    project.mkdir()
    checks, results = {}, {}
    ids = [str(uuid.uuid4()) for _ in range(53)]
    sdk = json.loads((REPO / 'bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/package.json').read_text())['version']
    assert sdk == '0.3.280', 'SDK version changed'
    for account in ['A', 'B']:
        folder = root / account
        env = environment(folder)
        native = Path(env['CLAUDE_CONFIG_DIR']) / 'projects' / re.sub(r'[^a-zA-Z0-9]', '-', str(project))
        native.mkdir(parents=True)
        for n, session_id in enumerate(ids):
            message = {'type': 'user', 'uuid': str(uuid.uuid4()), 'parentUuid': None, 'sessionId': session_id,
                       'cwd': str(project), 'isSidechain': False, 'timestamp': '2026-10-01T00:00:00Z',
                       'message': {'role': 'user', 'content': f'{account} isolated history {n}'}}
            path = native / (session_id + '.jsonl')
            path.write_text(json.dumps(message) + '\n')
            os.utime(path, (1700000000+n, 1700000000+n))
        before = {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in native.glob('*.jsonl')}
        proc = subprocess.Popen(['node', str(REPO / 'bridges/claude/dist/main.js'), str(folder / 'bridge')],
                                env=env, cwd=project, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, text=True, start_new_session=True)
        selector = selectors.DefaultSelector()
        selector.register(proc.stdout, selectors.EVENT_READ)
        def call(method, params):
            request_id = str(uuid.uuid4())
            proc.stdin.write(json.dumps({'v': 1, 'id': request_id, 'method': method, 'params': params}) + '\n')
            proc.stdin.flush()
            assert selector.select(timeout=15), 'bridge response timed out'
            response = json.loads(proc.stdout.readline())
            assert response['id'] == request_id
            return response
        try:
            hello = call('hello', {})
            first = call('discover', {'cwd': str(project), 'limit': 50, 'cursor': ''})
            assert 'result' in first, first
            first = first['result']
            second = call('discover', {'cwd': str(project), 'limit': 50, 'cursor': first['next_cursor']})['result']
            preview = call('inspect_native', {'cwd': str(project), 'native_id': ids[-1]})['result']
            bad = call('discover', {'cwd': str(project), 'limit': 51})
            traversal = call('inspect_native', {'cwd': str(project), 'native_id': '../../foreign'})
            checks[account+'_pinned_sdk'] = hello['result']['sdk'] == sdk
            checks[account+'_pages'] = len(first['sessions']) == 50 and len(second['sessions']) == 3 and not second.get('next_cursor')
            checks[account+'_scoped_titles'] = all(row['title'].startswith(account+' isolated') and row['cwd'] == str(project) for row in first['sessions'] + second['sessions'])
            checks[account+'_unknown_activity'] = all(row['source'] == 'native_history' and row['native_status'] == 'unknown' for row in first['sessions'])
            checks[account+'_same_id_isolation'] = preview['native_id'] == ids[-1] and preview['title'].startswith(account+' isolated')
            checks[account+'_invalid_inputs'] = bool(bad.get('error') and traversal.get('error'))
            checks[account+'_no_query_journal'] = not list((folder/'bridge').rglob('*.json')) and not list((folder/'bridge').rglob('*.jsonl'))
            checks[account+'_transcripts_unchanged'] = all(hashlib.sha256(Path(path).read_bytes()).hexdigest() == digest for path, digest in before.items())
            results[account] = {'first_count': len(first['sessions']), 'second_count': len(second['sessions']), 'preview': preview,
                                'invalid_page': bad.get('error'), 'invalid_id': traversal.get('error')}
        finally:
            selector.close()
            stop(proc)
    out = {'scope': 'real SDK and production bridge, synthetic native transcript files; no CLI execution or control',
           'root': str(root), 'sdk_version': sdk, 'checks': checks, 'expectations_met': all(checks.values()), 'accounts': results}
    write(args.output, out)
    print(json.dumps(out, ensure_ascii=False, indent=2))
    return 0 if out['expectations_met'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
