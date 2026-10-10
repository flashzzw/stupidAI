#!/usr/bin/env python3
"""Read-only audit of captured Windows bytes, native results and SQLite state."""
import argparse
import base64
from collections import defaultdict
import json
import os
from pathlib import Path
import sqlite3

from wsl_live_control_probe import alive


def load(path):
    return json.loads(path.read_text())


def rows(path):
    return [json.loads(line) for line in path.read_text().splitlines() if line]


def decode(root):
    transcript = load(root / 'host-transcript.json')
    sent, received = defaultdict(bytearray), defaultdict(bytearray)
    for entry in transcript['records']:
        request, response = entry['request'], entry['response']
        assert response['ok'], 'Windows control request failed'
        if request['op'] == 'write':
            sent[request['name']].extend(base64.b64decode(request['data'], validate=True))
        if request['op'] == 'read':
            received[request['name']].extend(base64.b64decode(response['result']['data'], validate=True))
    # Windows reads are arbitrary byte chunks, including split UTF-8 and JSON.
    def lines(data):
        assert not data or data.endswith(b'\n'), 'incomplete Windows byte stream'
        return [json.loads(line) for line in data.decode('utf-8').splitlines() if line]
    inputs, outputs = {k: lines(v) for k, v in sent.items()}, {k: lines(v) for k, v in received.items()}
    http = []
    for name in sorted(k for k in inputs if k.startswith('http-')):
        # The first relay output identifies its owned receiving process.
        responses = [r for r in outputs[name] if 'status' in r]
        assert len(inputs[name]) == len(responses), 'HTTP request/reply count differs'
        for request, response in zip(inputs[name], responses):
            http.append({'relay': name, 'path': request['path'], 'request': request.get('body'),
                         'status': response['status'], 'response': response['body']})
    return transcript, inputs, outputs, http


def audit(root):
    root = root.resolve()
    result = load(root / 'result.json')
    transcript, inputs, outputs, http = decode(root)
    checks = {}

    def check(name, condition):
        checks[name] = bool(condition)
        assert condition, name

    def detail(stage):
        return result['frames'][stage]['/api/sessions/' + result['sessions']['A']]

    samples = result['samples']
    first, final = detail('pending'), detail('next-input')
    sid, native = first['session']['id'], first['session']['scope']['native_id']
    pending = samples['original_pending']
    original = next(op for op in final['operations'] if op['id'] == pending['native_turn'])
    response = next(op for op in final['operations'] if op['intent']['kind'] == 'respond')
    snapshot = first['session']['execution_snapshot']['id']
    check('real_windows_host_and_original_binary', transcript['host']['platform'] == 'Win32NT' and samples['console_initial'] in outputs['console'])
    for stage, frame in result['frames'].items():
        for path, body in frame.items():
            check(stage + '_' + ('workspace' if path == '/api/workspace' else 'detail') + '_from_windows_bytes',
                  any(row['path'] == path and row['status'] == 200 and row['response'] == body for row in http))
    commands = [r for r in http if r['path'] == '/api/commands']
    allows = [r for r in commands if r['request']['kind'] == 'respond']
    check('one_explicit_original_approval', len(allows) == 1 and allows[0]['request']['pending_id'] == pending['id'] and allows[0]['request']['option_id'] == 'allow' and allows[0]['response']['operation_id'] == response['id'])
    sends = [r for r in commands if r['request']['kind'] == 'send']
    check('only_two_explicit_inputs_to_original_session', len(sends) == 2 and all(r['request']['session_id'] == sid and r['status'] == 202 for r in sends))
    check('inflight_resume_refused', any(r['request']['command_id'] == 'reject-resume-running' and r['status'] == 409 for r in commands))
    resumes = [r for r in commands if r['request']['command_id'] == 'resume-idle-original']
    check('explicit_idle_resume_reuses_receipt', len(resumes) == 2 and resumes[0]['response'] == resumes[1]['response'])
    for stage in ['running', 'reconnected', 'manual']:
        frame = detail(stage)
        send = next(op for op in frame['operations'] if op['id'] == original['id'])
        respond = next(op for op in frame['operations'] if op['id'] == response['id'])
        check(stage + '_original_receipt_and_snapshot', frame['session']['scope']['native_id'] == native and send['receipt'] == original['receipt'] and send['execution_snapshot_id'] == snapshot)
        check(stage + '_submitted_response_stays_running', respond['status'] == 'running' and send['status'] == 'running')
    finished = detail('finished')
    check('terminal_keeps_manual_control', finished['session']['paused'] and finished['session']['control_mode'] == 'human_takeover')
    check('original_send_and_response_have_real_terminal_status', original['status'] == response['status'] == 'succeeded' and response['receipt']['native_turn'] == original['id'])
    check('next_input_keeps_native_id_and_snapshot', final['session']['scope']['native_id'] == native and final['session']['execution_snapshot']['id'] == snapshot)

    cwd = root / "Claude 工作目录 with 'quote"
    expected = {'tag': 'OWNED_CLAUDE_HOST_RESULT', 'cwd': str(cwd), 'uid': os.getuid(), 'value': '中文完整结果'}
    starts = rows(cwd / 'starts.jsonl')
    check('one_owned_tool_and_complete_local_output', len(starts) == 1 and load(cwd / 'expected.json') == expected and load(cwd / 'finished.json') == expected)
    binding = samples['tool_binding']
    check('native_tool_process_identity_matches_its_output', binding['reported'] == starts[0] and binding['host']['start_ticks'] == starts[0]['start_ticks'] and binding['host']['uid'] == expected['uid'] and binding['host']['cwd'] == str(cwd))
    requests = [load(p) for p in sorted((root / 'provider-A').glob('request-*.json'))]
    check('three_local_requests_with_correct_route', len(requests) == 3 and all(r['auth_matches'] for r in requests))
    check('sibling_provider_received_no_input', not list((root / 'provider-B').glob('request-*.json')))
    blocks = [c for m in requests[1]['body']['messages'] if isinstance(m['content'], list) for c in m['content']]
    calls = [c for c in blocks if c.get('type') == 'tool_use']
    returned = [c for c in blocks if c.get('type') == 'tool_result']
    check('original_native_tool_result_correlation', len(calls) == len(returned) == 1 and calls[0]['id'] == returned[0]['tool_use_id'] and not returned[0].get('is_error'))

    def output_has_value(block):
        content = block['content']
        text = content if isinstance(content, str) else '\n'.join(c.get('text', '') for c in content)
        values = []
        for line in text.splitlines():
            try:
                values.append(json.loads(line))
            except ValueError:
                pass
        return values.count(expected) == 1

    check('complete_original_output_reached_model', output_has_value(returned[0]))
    journal = rows(root / 'console/runtimes/A' / (native + '.jsonl'))
    entered = [e for e in journal if e['kind'] == 'input']
    terminals = [e for e in journal if e.get('event', {}).get('type') == 'result']
    approved = [e for e in journal if e['kind'] == 'permission_check' and e['action'] == 'checked_allow']
    check('journal_has_only_explicit_original_inputs', [e['operation'] for e in entered] == [r['response']['operation_id'] for r in sends])
    check('journal_records_two_successful_original_results', len(terminals) == 2 and [e['operation'] for e in terminals] == [e['operation'] for e in entered] and all(e['event']['session_id'] == native and e['event']['subtype'] == 'success' for e in terminals))
    check('journal_has_one_original_permission_decision', len(approved) == 1 and approved[0]['operation'] == original['id'] and approved[0]['request_id'] == pending['request_id'])
    initialized = [e for e in journal if e['kind'] == 'initialized']
    check('one_native_query_and_connection', len(initialized) == 1 and initialized[0]['connection'] == pending['connection_id'] and initialized[0]['pid'] == samples['original_native_processes']['A']['pid'])
    histories = list((root / 'profile-A/claude/projects').rglob(native + '.jsonl'))
    check('one_native_history_file', len(histories) == 1)
    native_blocks = [c for row in rows(histories[0]) if isinstance(row.get('message', {}).get('content'), list) for c in row['message']['content']]
    native_calls = [b for b in native_blocks if b.get('type') == 'tool_use' and b['id'] == calls[0]['id']]
    native_results = [b for b in native_blocks if b.get('type') == 'tool_result' and b['tool_use_id'] == calls[0]['id']]
    check('native_history_has_single_original_tool_and_result', len(native_calls) == len(native_results) == 1 and output_has_value(native_results[0]))
    check('go_crash_is_actual_windows_control_command', result['mode'] == 'host-disconnect' or any(q['op'] == 'crash' for q in inputs['console']) and any(r.get('exit_code') == -9 for r in outputs['console']))
    if result['mode'] == 'go-crash':
        for label in ['A', 'B']:
            before, after = samples['original_bridges'][label], samples['reconnected_bridges'][label]
            check(label + '_original_bridge_reconnected', all(before[k] == after[k] for k in ['pid', 'start_ticks', 'uid', 'exe', 'cwd', 'args', 'socket_inode']))
    if result['mode'] == 'pending-go-crash':
        recovered = detail('pending-after-go-restart')
        p = next(p for p in recovered['session']['view']['pending'] if p['id'] == pending['id'])
        check('pending_recovery_retains_exact_request_identity', all(p[k] == pending[k] for k in ['id', 'native_id', 'native_turn', 'connection_id', 'request_id', 'status']))

    with sqlite3.connect('file:' + str(root / 'console/console.sqlite') + '?mode=ro', uri=True) as db:
        check('sqlite_integrity', db.execute('PRAGMA integrity_check').fetchall() == [('ok',)])
        sessions = [json.loads(row[0]) for row in db.execute('SELECT body FROM sessions')]
        operations = [json.loads(row[0]) for row in db.execute('SELECT body FROM operations')]
    check('two_distinct_saved_profiles_and_native_ids', len(sessions) == 2 and len({s['scope']['native_id'] for s in sessions}) == 2 and len({s['scope']['state_namespace'] for s in sessions}) == 2)
    check('all_six_original_operations_completed', len(operations) == 6 and all(o['status'] == 'succeeded' for o in operations))
    for op in final['operations']:
        saved = next(o for o in operations if o['id'] == op['id'])
        check('sqlite_' + op['intent']['kind'] + '_' + op['id'], saved['receipt'] == op['receipt'] and saved.get('execution_snapshot_id') == op.get('execution_snapshot_id') and saved['intent'] == op['intent'])
    # http-receiver-identity.json is overwritten on reconnect. Check both
    # recorded receivers, alongside every independently bound bridge and CLI.
    identities = [r['identity'] for r in result['cleanup']]
    identities += [samples[name]['process'] for name in ['http-1', 'http-2']]
    identities += list(samples['original_bridges'].values()) + list(samples['original_native_processes'].values())
    unique = {(p['pid'], p['start_ticks']): p for p in identities}
    check('all_recorded_linux_processes_exited', bool(unique) and all(not alive(p) for p in unique.values()))
    stops = [row['response']['result'] for row in transcript['records'] if row['request']['op'] == 'stop']
    check('three_windows_children_exited', len(stops) == 3 and all(s['exited'] and s['stderr_complete'] for s in stops))
    return {'root': str(root), 'mode': result['mode'], 'checks': checks, 'passed': all(checks.values()),
            'http_count': len(http), 'linux_processes': len(unique), 'http': http}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('root', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    outcome = audit(args.root)
    args.output.write_text(json.dumps(outcome, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({k: outcome[k] for k in ['root', 'mode', 'passed', 'http_count', 'linux_processes']} | {'checks': len(outcome['checks'])}, ensure_ascii=False))
