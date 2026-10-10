#!/usr/bin/env python3
"""Recheck saved instruction observations without starting a CLI or model request."""
import argparse
from collections import Counter
import json
from pathlib import Path
import sqlite3


def read(path):
    return json.loads(path.read_text())


def audit(args, checks):
    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    fixture = read(args.native / 'loads-browser-fixture.json')
    request = read(args.native / 'application-request.json')
    sid = fixture['sessions']['a']
    session = lambda frame, id=sid: fixture['frames'][frame]['/api/sessions/' + id]['session']
    initial, restored = (session(name) for name in ['initial', 'restored'])
    native = initial['scope']['native_id']
    journal = [json.loads(line) for line in (args.native / 'application/runtimes/claude' / (native + '.jsonl')).read_text().splitlines()]
    loads = {entry['load']['id']: entry['load'] for entry in journal if entry['kind'] == 'instruction_load'}
    check('journal_keeps_fifteen_unique_notifications', len(loads) == 15)
    check('journal_keeps_two_queries_and_three_inputs', len({item['query_id'] for item in loads.values()}) == 2 and sum(entry['kind'] == 'input' for entry in journal) == 3)
    for frame in fixture['frames']:
        value = session(frame)['view']['instruction_loads']
        check(frame + '_only_claims_received_metadata', value['available'] and not value['complete'] and not value['recording_failed'] and 'PRIVATE_INSTRUCTION' not in json.dumps(value))
        check(frame + '_metadata_matches_saved_notifications', all(all(item.get(key) == val for key, val in loads[item['id']].items() if key != 'version') for item in value['items']))
    startup = initial['view']['instruction_loads']['items']
    check('startup_paths_match_all_seven_owned_sources', len(startup) == 7 and {item['path'] for item in startup} == set(request['Files']))
    check('startup_preserves_native_source_categories', {item['source'] for item in startup} == {'Project', 'User', 'Local', 'Managed'})
    check('import_keeps_parent_path', any(item['path'].endswith('/reference.md') and item.get('parent_path') == request['Cwd'] + '/CLAUDE.md' for item in startup))
    observed = fixture['nested']
    saved = loads[observed['id']]
    check('nested_reason_and_trigger_are_native', saved['reason'] == 'nested_traversal' and saved['trigger_path'] == request['ReadFile'])
    links = [entry['link'] for entry in journal if entry['kind'] == 'instruction_prompt']
    check('one_native_read_link_was_recorded', len(links) == 1 and links[0]['tool'] == 'Read')
    link = links[0]
    check('load_and_tool_prompt_bind_same_query_and_configuration', all(saved[key] == link[key] for key in ['native_id', 'cwd', 'query_id', 'configuration_id', 'prompt_id']))
    events = [entry for entry in journal if entry['kind'] == 'event' and entry['event'].get('type') == 'assistant'
              and any(block.get('type') == 'tool_use' and block.get('id') == link['tool_use_id'] for block in entry['event']['message']['content'])]
    check('one_assistant_tool_message_identifies_input', len(events) == 1)
    event = events[0]
    operation = event['event']['user_message_uuid']
    check('assistant_identity_is_main_query_and_not_replay', event['event']['session_id'] == native and not event['event'].get('parent_tool_use_id') and not event['event'].get('isReplay') and event['query_id'] == link['query_id'])
    check('native_prompt_differs_from_input_uuid', operation != link['prompt_id'] and operation == observed['operation'])
    check('input_journal_has_explicit_matching_query', any(entry['kind'] == 'input' and entry['operation'] == operation and entry['query_id'] == link['query_id'] for entry in journal))
    check('input_link_does_not_copy_tool_arguments', set(link) == {'version', 'native_id', 'cwd', 'query_id', 'configuration_id', 'at', 'prompt_id', 'tool_use_id', 'tool'})
    connection = sqlite3.connect((args.native / 'application/console.sqlite').resolve().as_uri() + '?mode=ro', uri=True)
    try:
        operations = {id: json.loads(body) for id, body in connection.execute('SELECT id,body FROM operations')}
        sessions = {id: json.loads(body) for id, body in connection.execute('SELECT id,body FROM sessions')}
    finally:
        connection.close()
    check('sqlite_contains_only_explicit_creates_and_inputs', Counter(op['intent']['kind'] for op in operations.values()) == {'create': 2, 'send': 3})
    check('sqlite_operations_have_terminal_success', all(op['status'] == 'succeeded' for op in operations.values()))
    original = operations[operation]
    check('sqlite_original_input_binds_session_and_native_id', original['session_id'] == sid and original['intent']['kind'] == 'send' and original['intent']['scope']['native_id'] == native)
    check('sqlite_original_snapshot_matches_observed_snapshot', original['execution_snapshot_id'] == observed['execution_snapshot_id'] == fixture['original_snapshot'])
    check('sqlite_retains_all_observations_after_shutdown', {item['id'] for item in sessions[sid]['view']['instruction_loads']['items']} == set(loads))
    offline = session('offline')['view']['instruction_loads']
    check('offline_history_is_not_current', len(offline['items']) == 8 and not offline.get('current_query_id') and all(not item['current'] for item in offline['items']))
    history = restored['view']['instruction_loads']
    check('original_id_resume_has_separate_query', restored['scope']['native_id'] == native and history['current_query_id'] != initial['view']['instruction_loads']['current_query_id'])
    check('resumed_history_preserves_original_operation', any(item['id'] == observed['id'] and item['operation'] == operation and item['execution_snapshot_id'] == original['execution_snapshot_id'] and not item['current'] for item in history['items']))
    check('current_and_prior_notification_counts_are_seven_and_eight', Counter(item['current'] for item in history['items']) == {True: 7, False: 8})
    check('second_session_never_acquires_nested_notice', all(len(session(frame, fixture['sessions']['b'])['view']['instruction_loads']['items']) == 7 for frame in fixture['frames']))
    requests = [read(path) for path in sorted((args.native / 'provider-A').glob('request-*.json'))]
    check('four_local_model_requests_without_token_count_requests', len(requests) == 4 and all(row['auth_matches'] and row['path'] == '/v1/messages?beta=true' for row in requests))
    tools = {block['id']: block for row in requests for message in row['body'].get('messages', []) for block in message.get('content', []) if isinstance(block, dict) and block.get('type') == 'tool_use'}
    check('provider_saw_one_owned_read_with_same_tool_id', len(tools) == 1 and tools[link['tool_use_id']]['name'] == 'Read' and tools[link['tool_use_id']]['input']['file_path'] == request['ReadFile'])
    check('provider_saw_nested_read_result', any('OWNED_NESTED_READ_RESULT' in json.dumps(row['body']) for row in requests))

    if args.prompt:
        report = read(args.prompt / 'sdk-result.json')
        prompt_link = report['tool_links'][0]
        sent = next(row for row in report['sent'] if row['stage'] == 'hot')['input_uuid']
        notice = next(row for row in report['loads'] if row['load_reason'] == 'nested_traversal')
        check('independent_probe_keeps_distinct_prompt_and_input', prompt_link['prompt_id'] == notice['prompt_id'] and sent != notice['prompt_id'])
        check('independent_probe_pretool_payload_and_callback_ids_match', prompt_link['tool_use_id'] == prompt_link['callback_tool_use_id'])
        check('instruction_callback_id_is_not_the_native_tool_id', notice['callback_tool_use_id'] != prompt_link['tool_use_id'])
        check('independent_probe_native_assistant_identifies_sdk_input', any(event.get('type') == 'assistant' and event.get('user_message_uuid') == sent and any(block.get('id') == prompt_link['tool_use_id'] for block in event['message']['content']) for event in report['events']))

    if args.approvals:
        total, denied, approvals = 0, 0, 0
        for policy in ['managed-only', 'all-sources', 'managed-hooks-only', 'all-hooks-disabled']:
            managed = policy != 'all-sources'
            report = read(args.approvals / policy / 'result.json')
            check(policy + '_native_permission_policy_is_observed', all(row['permissions']['state']['managedOnly'] is managed for row in report['metadata'].values()))
            for stage, value in report['reports'].items():
                cases = value['cases']
                check(policy + '_' + stage + '_all_eleven_tools_have_native_result', len(cases) == 11 and all(row['result']['session_id'] == report['id'] and row['result']['user_message_uuid'] == row['operation'] for row in cases.values()))
                for name, row in cases.items():
                    expected_deny = name in {'read-managed-deny', 'read-policy-anchor', 'write-managed-deny'} or not managed and name in {'read-user-deny', 'read-project-deny', 'read-custom-deny', 'edit-custom-deny'}
                    expected_ask = name in {'read-managed-ask', 'edit-user-allow', 'write-managed-ask'} or managed and name == 'edit-custom-deny'
                    check(policy + '_' + stage + '_' + name + '_native_result_and_approval', bool(row['tool_result'].get('is_error')) == expected_deny and len(row['approvals']) == int(expected_ask))
                    total += 1
                    denied += expected_deny
                    approvals += len(row['approvals'])
        check('approval_matrix_keeps_eighty_eight_tools_thirty_reviews_and_thirty_two_denials', (total, approvals, denied) == (88, 30, 32))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--native', type=Path, required=True)
    parser.add_argument('--prompt', type=Path)
    parser.add_argument('--approvals', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    checks, completed = {}, False
    try:
        audit(args, checks)
        completed = True
    finally:
        args.output.write_text(json.dumps({'passed': completed and bool(checks) and all(checks.values()), 'checks': checks}, indent=2) + '\n')
    print(json.dumps({'passed': all(checks.values()), 'checks': len(checks)}))


if __name__ == '__main__':
    main()
