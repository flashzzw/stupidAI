#!/usr/bin/env python3
"""Reconcile permission HTTP reports against native wire, journals and SQLite."""
import argparse
from collections import Counter, defaultdict
import hashlib
import json
from pathlib import Path
import re
import sqlite3
import stat
from urllib.parse import quote


def compact(value, sort_keys=False):
    return json.dumps(value, ensure_ascii=False, separators=(',', ':'), sort_keys=sort_keys)


def go_json(value):
    return compact(value).replace('&', '\\u0026').replace('<', '\\u003c').replace('>', '\\u003e').replace('\u2028', '\\u2028').replace('\u2029', '\\u2029')


def native_projection(state):
    groups = defaultdict(set)
    for item in state['rules']:
        groups[(item['source'], item['behavior'], item['rule'])].add(item.get('notInEffect', False))
    conflicts = {key for key, values in groups.items() if len(values) > 1}
    items = [{'behavior': row['behavior'], 'source': row['source'], 'rule': row['rule'], 'editability': row['editability'],
              'not_in_effect': row.get('notInEffect', False), 'conflicting': (row['source'], row['behavior'], row['rule']) in conflicts} for row in state['rules']]
    directories = [{'path': row['path'], 'source': row['source']} for row in state['workspaceDirectories']]
    errors = [{'file': row['file'], 'field': row.get('path', '')} for row in state.get('errors', [])]
    canonical = {'version': 1, 'cwd': state['originalCwd'], 'managed_only': state['managedOnly'],
                 'items': sorted(go_json(row) for row in items), 'directories': sorted(go_json(row) for row in directories), 'errors': sorted(go_json(row) for row in errors)}
    return {'items': items, 'directories': directories, 'errors': errors, 'managed_only': state['managedOnly'],
            'conflict_count': len(conflicts), 'sample_hash': hashlib.sha256(go_json(canonical).encode()).hexdigest()}


def wire_records(path):
    rows, offset = [], 0
    for line in path.read_bytes().splitlines(keepends=True):
        end = offset + len(line)
        if line.strip():
            rows.append((offset, end, json.loads(line)))
        offset = end
    return rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--native', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    root = args.native.resolve()
    load = lambda relative: json.loads((root / relative).read_text())
    result, request = load('application-result.json'), load('application-request.json')
    wires = {label: wire_records(root / ('wire-' + label + '.jsonl')) for label in ['A', 'B']}
    checks, reads = {}, []

    def check(name, value):
        checks[name] = bool(value)

    check('fixed_cli_version', load('versions.json')['claude']['version'] == '2.1.280 (Claude Code)')
    check('isolated_native_fixture', '--unshare-net' in load('invocation.json')['command'] and '--unshare-pid' in load('invocation.json')['command'])
    check('native_assertions_all_passed', bool(result['checks']) and all(result['checks'].values()))
    db = sqlite3.connect('file:' + quote(str(root / 'application/console.sqlite')) + '?mode=ro', uri=True)
    try:
        sessions = {row['id']: row for body, in db.execute('SELECT body FROM sessions') for row in [json.loads(body)]}
        operations = [json.loads(body) for body, in db.execute('SELECT body FROM operations')]
    finally:
        db.close()
    check('only_expected_persisted_operations', Counter(row['intent']['kind'] for row in operations) == {'create': 2, 'send': 5, 'respond': 2})
    check('all_operations_completed', all(row['status'] == 'succeeded' for row in operations))
    check('only_two_original_sessions', set(sessions) == set(result['sessions'].values()))

    bindings = {}
    for label, sid in result['sessions'].items():
        session = sessions[sid]
        initial = result['frames']['initial']['/api/sessions/' + sid]['session']
        native_id = session['scope']['native_id']
        metadata = load('application/runtimes/' + label + '/' + native_id + '.json')
        config = {key: value for key, value in metadata.items() if key != '_configuration'}
        configuration_id = hashlib.sha256(compact(config, sort_keys=True).encode()).hexdigest()
        bindings[label] = {'native_id': native_id, 'configuration_id': configuration_id, 'sid': sid}
        check(label + '_sqlite_preserves_original_scope', session['scope'] == initial['scope'])
        check(label + '_sqlite_preserves_original_snapshot', session['execution_snapshot'] == initial['execution_snapshot'])
        check(label + '_permission_mode_preserved', session['permission'] == initial['permission'] == 'read_only')
        for name in ['profile_source', 'settings_source']:
            source = session['execution_snapshot'][name]
            path = Path(source['path'])
            content_hash = hashlib.sha256(path.read_bytes()).hexdigest()
            stamp = '"." ' + stat.filemode(path.stat().st_mode) + '\n' + content_hash + '\n'
            check(label + '_' + name + '_actual_source_stamp_restored', hashlib.sha256(stamp.encode()).hexdigest() == source['sha256'])
            check(label + '_' + name + '_actual_source_path', str(path.resolve()) == source['resolved_path'] and source['kind'] == 'file')
        journal = [json.loads(line) for line in (root / ('application/runtimes/' + label + '/' + native_id + '.jsonl')).read_text().splitlines()]
        check(label + '_owned_query_closed', journal[-1]['kind'] == 'closed')
        inputs = [entry for entry in journal if entry['kind'] == 'input']
        check(label + '_no_metadata_inputs', len(inputs) == (5 if label == 'A' else 0))
        if label == 'A':
            check('five_distinct_journal_operations', len({entry['operation'] for entry in inputs}) == 5)
            check('same_query_for_first_four_inputs', len({entry['query_id'] for entry in inputs[:4]}) == 1)
            check('new_query_only_after_explicit_continuation', inputs[4]['query_id'] != inputs[0]['query_id'])
            check('query_report_matches_journal', inputs[0]['query_id'] == result['reports']['initial-A']['query_id'] and inputs[4]['query_id'] == result['reports']['resumed']['query_id'])

    mutators = {'initialize', 'apply_flag_settings', 'set_permission_mode', 'mcp_set_servers', 'reload_plugins', 'reload_skills', 'update_settings', 'interrupt'}
    for index, trace in enumerate(result['http']):
        if not trace['path'].endswith('/permissions/catalog'):
            continue
        label = next(label for label, sid in result['sessions'].items() if '/'+sid+'/' in trace['path'])
        prefix = 'http_' + str(index)
        between = [row for start, end, row in wires[label] if start >= trace['wire_before'].get(label, 0) and end <= trace['wire_after'].get(label, 0)]
        outgoing = [row['message'] for row in between if row['direction'] == 'request']
        check(prefix + '_never_writes_or_sends_input', not any(row.get('type') == 'user' or row.get('request', {}).get('subtype') in mutators for row in outgoing))
        if trace['status'] != 200:
            check(prefix + '_closed_read_does_not_start_query', trace['status'] == 409 and not any(row.get('request', {}).get('subtype') == 'initialize' for row in outgoing))
            continue
        calls = [row for row in outgoing if row.get('type') == 'control_request' and row['request']['subtype'] == 'list_permission_rules']
        replies = [row['message']['response'] for row in between if row['direction'] == 'response' and row['message'].get('type') == 'control_response' and any(call['request_id'] == row['message']['response']['request_id'] for call in calls)]
        check(prefix + '_one_native_read_reply', len(calls) == len(replies) == 1 and replies[0]['subtype'] == 'success')
        if len(replies) != 1:
            continue
        raw = replies[0]['response']['state']
        expected, report = native_projection(raw), trace['response']
        check(prefix + '_native_rows_sources_and_ignored_markers', report['items'] == expected['items'])
        check(prefix + '_native_directories_errors_and_policy', all(report[key] == expected[key] for key in ['directories', 'errors', 'managed_only', 'conflict_count']))
        check(prefix + '_independently_recomputed_digest', report['sample_hash'] == expected['sample_hash'])
        check(prefix + '_original_query_binding', report['native_id'] == bindings[label]['native_id'] and report['cwd'] == request['Cwd'] and report['configuration_id'] == bindings[label]['configuration_id'])
        check(prefix + '_partial_scope_and_redacted_diagnostics', report['complete'] is False and report['source'] == 'claude.list_permission_rules' and 'PRIVATE_INVALID_VALUE' not in compact(report))
        reads.append({'index': index, 'label': label, 'request_id': calls[0]['request_id'], 'sample_hash': report['sample_hash'], 'query_id': report['query_id'], 'conflicts': report['conflict_count']})

    rows = [row for _, _, row in wires['A']]
    permissions = [row['message'] for row in rows if row['direction'] == 'response' and row['message'].get('type') == 'control_request' and row['message']['request']['subtype'] == 'can_use_tool']
    answers = [row['message']['response'] for row in rows if row['direction'] == 'request' and row['message'].get('type') == 'control_response' and any(p['request_id'] == row['message']['response']['request_id'] for p in permissions)]
    check('exactly_two_distinct_single_tool_approvals', len(permissions) == 2 and len({p['request_id'] for p in permissions}) == 2)
    check('approval_then_refusal_not_sticky_allow', [a['response']['behavior'] for a in answers] == ['allow', 'deny'])
    check('approval_inputs_are_owned_reads', all(p['request']['tool_name'] == 'Read' and p['request']['input']['file_path'] == str(root / 'work/target-A.txt') for p in permissions))

    expected = {'initial': True, 'managed': False, 'ask-allow': False, 'ask-deny': True, 'resumed': True}
    phases = defaultdict(list)
    for path in sorted((root / 'provider-A').glob('request-*.json'), key=lambda p: int(p.stem.split('-')[-1])):
        record = json.loads(path.read_text())
        messages = record['body']['messages']
        found = [(index, re.search(r'NETCAP_PERMISSION_CASE=([a-z-]+) ROUTE=A', compact(message.get('content')))) for index, message in enumerate(messages) if message['role'] == 'user']
        found = [(index, match.group(1)) for index, match in found if match]
        if found:
            index, phase = found[-1]
            phases[phase].append(messages[index:])
        check('provider_' + path.stem + '_local_auth', record['auth_matches'])
    check('exactly_five_native_inputs_and_ten_requests', set(phases) == set(expected) and sum(len(value) for value in phases.values()) == 10)
    for phase, denied in expected.items():
        messages = phases[phase][-1]
        content = [block for message in messages for block in message.get('content', []) if isinstance(block, dict)]
        calls = [block for block in content if block.get('type') == 'tool_use']
        results = [block for block in content if block.get('type') == 'tool_result']
        check(phase + '_one_actual_read', len(calls) == len(results) == 1 and calls[0]['name'] == 'Read' and calls[0]['input']['file_path'] == str(root / 'work/target-A.txt') and results[0]['tool_use_id'] == calls[0]['id'])
        check(phase + '_actual_decision_reached_model', len(results) == 1 and bool(results[0].get('is_error')) == denied)
        marker = 'OWNED_PERMISSION_TARGET_A_APPROVED' if phase == 'ask-allow' else 'OWNED_PERMISSION_TARGET_A'
        check(phase + '_actual_bytes_match_decision', len(results) == 1 and (marker in compact(results[0])) != denied)
    check('other_provider_had_no_input', not list((root / 'provider-B').glob('request-*.json')))
    check('new_read_returns_modified_owned_bytes', (root / 'work/target-A.txt').read_text() == 'OWNED_PERMISSION_TARGET_A_APPROVED\n')
    check('unchanged_report_has_same_digest', result['reports']['initial-A']['sample_hash'] == result['reports']['repeated-A']['sample_hash'])
    check('hot_report_changes_without_new_binding', result['reports']['initial-A']['query_id'] == result['reports']['managed']['query_id'] and result['reports']['initial-A']['sample_hash'] != result['reports']['managed']['sample_hash'])
    args.output.write_text(json.dumps({'checks': checks, 'passed': all(checks.values()), 'native_reads': reads, 'root': str(root)}, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'checks': len(checks), 'failed': [key for key, value in checks.items() if not value], 'native_reads': len(reads)}))
    raise SystemExit(0 if all(checks.values()) else 1)


if __name__ == '__main__':
    main()
