#!/usr/bin/env python3
"""Read production SDK history from synthetic isolated transcripts, without a query."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import selectors
import subprocess
import tempfile
import uuid

from execution_config_probe import environment, write
from console_fixture import stop

REPO = Path(__file__).resolve().parents[1]


def transcript(project, session_id, account, count):
    rows, parent = [], None
    for n in range(count):
        message_id = str(uuid.uuid4())
        role, content = 'user', f'{account} message {n}'
        if n == 1:
            role, content = 'assistant', [{'type': 'tool_use', 'id': 'tool-1', 'name': 'Read', 'input': {'file_path': 'sample.txt'}}]
        elif n == 2:
            content = [{'type': 'tool_result', 'tool_use_id': 'tool-1', 'content': f'{account} tool output'}]
        elif n == 4:
            role, content = 'assistant', [{'type': 'future_block', 'opaque': f'{account} preserved'}]
        row = {'type': role, 'uuid': message_id, 'parentUuid': parent, 'sessionId': session_id,
               'cwd': str(project), 'isSidechain': False, 'timestamp': '2026-10-01T00:00:00Z',
               'message': {'role': role, 'content': content}}
        if n == 3:
            row.update(type='system', subtype='local_command', content=f'{account} system note')
            row.pop('message')
        rows.append(row)
        parent = message_id
    return rows


def save(path, rows):
    path.write_text(''.join(json.dumps(row) + '\n' for row in rows))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-sdk-history-'))
    project = root / 'project'
    project.mkdir()
    other = root / 'other-project'
    other.mkdir()
    session_id = str(uuid.uuid4())
    checks, results = {}, {}
    sdk = json.loads((REPO / 'bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/package.json').read_text())['version']
    assert sdk == '0.3.280'
    for account in ['A', 'B']:
        folder = root / account
        env = environment(folder)
        native = Path(env['CLAUDE_CONFIG_DIR']) / 'projects' / re.sub(r'[^a-zA-Z0-9]', '-', str(project))
        native.mkdir(parents=True)
        path = native / (session_id + '.jsonl')
        rows = transcript(project, session_id, account, 53)
        rows.extend([
            {'type': 'custom-title', 'customTitle': account+' title', 'sessionId': session_id},
            {'type': 'summary', 'summary': account+' old branch summary', 'leafUuid': rows[4]['uuid']},
            {'type': 'future_record', 'opaque': account+' raw event'},
            {**rows[3], 'content': account+' updated system note'},
        ])
        save(path, rows)
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        proc = subprocess.Popen(['node', str(REPO / 'bridges/claude/dist/main.js'), str(folder / 'bridge')],
                                env=env, cwd=project, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, text=True, start_new_session=True)
        selector = selectors.DefaultSelector()
        selector.register(proc.stdout, selectors.EVENT_READ)

        def call(params):
            request_id = str(uuid.uuid4())
            proc.stdin.write(json.dumps({'v': 1, 'id': request_id, 'method': 'read_native_history', 'params': params}) + '\n')
            proc.stdin.flush()
            assert selector.select(timeout=20), 'history bridge response timed out'
            response = json.loads(proc.stdout.readline())
            assert response['id'] == request_id
            return response

        try:
            params = {'cwd': str(project), 'native_id': session_id}
            first = call(params)
            assert 'result' in first, first
            first = first['result']
            entries = first['entries']
            second = call(params)['result']
            checks[account+'_all_messages'] = len(entries) == 57 and [e['data'].get('uuid') for e in entries] == [r.get('uuid') for r in rows]
            checks[account+'_raw_metadata_and_repeated_uuid'] = entries[53]['data']['customTitle'] == account+' title' and entries[54]['data']['summary'] == account+' old branch summary' and entries[55]['data']['opaque'] == account+' raw event' and entries[56]['data']['uuid'] == entries[3]['data']['uuid'] and entries[56]['id'] != entries[3]['id']
            checks[account+'_tool_call'] = entries[1]['data']['message']['content'][0]['input'] == {'file_path': 'sample.txt'}
            checks[account+'_tool_result'] = entries[2]['data']['message']['content'][0]['content'] == account+' tool output'
            checks[account+'_system_message'] = entries[3]['kind'] == 'system' and account+' system note' in json.dumps(entries[3]['data'])
            checks[account+'_unknown_block'] = entries[4]['data']['message']['content'][0] == {'type': 'future_block', 'opaque': account+' preserved'}
            checks[account+'_same_id_isolation'] = entries[0]['data']['message']['content'] == account+' message 0' and first['session']['native_id'] == session_id
            checks[account+'_stable_reread'] = first == second
            checks[account+'_transcript_unchanged'] = hashlib.sha256(path.read_bytes()).hexdigest() == digest
            foreign = call({'cwd': str(other), 'native_id': session_id})
            missing = call({'cwd': str(project), 'native_id': str(uuid.uuid4())})
            traversal = call({'cwd': str(project), 'native_id': '../../foreign'})
            checks[account+'_foreign_missing_invalid_rejected'] = all(r.get('error') and 'result' not in r for r in [foreign, missing, traversal])
            # Change content without changing the count or IDs: pagination must
            # bind actual payloads rather than mtime, count or message IDs alone.
            rows[2]['message']['content'][0]['content'] = account+' changed output'
            save(path, rows)
            changed = call(params)['result']
            checks[account+'_changed_tool_output_visible'] = changed != first and changed['entries'][2]['data']['message']['content'][0]['content'] == account+' changed output'
            if account == 'A':
                large_id = str(uuid.uuid4())
                large = native / (large_id+'.jsonl')
                save(large, transcript(project, large_id, account, 5001))
                overflow = call({'cwd': str(project), 'native_id': large_id})
                checks['entry_limit_rejected_without_partial_result'] = bool(overflow.get('error')) and 'result' not in overflow
                huge_id = str(uuid.uuid4())
                huge_rows = transcript(project, huge_id, account, 1)
                huge_rows[0]['message']['content'] = 'x'*(1024*1024)
                save(native/(huge_id+'.jsonl'), huge_rows)
                huge = call({'cwd': str(project), 'native_id': huge_id})
                checks['byte_limit_rejected_without_partial_result'] = bool(huge.get('error')) and 'result' not in huge
            checks[account+'_no_query_journal'] = not list((folder/'bridge').rglob('*.json')) and not list((folder/'bridge').rglob('*.jsonl'))
            results[account] = {'count': len(entries), 'first_five': entries[:5], 'foreign_error': foreign.get('error'), 'missing_error': missing.get('error')}
        finally:
            selector.close()
            stop(proc)
    result = {'scope': 'real SDK + production bridge reading synthetic native files; no CLI query, model or control',
              'root': str(root), 'sdk_version': sdk, 'checks': checks, 'expectations_met': all(checks.values()), 'accounts': results}
    write(args.output, result)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0 if result['expectations_met'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
