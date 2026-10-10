#!/usr/bin/env python3
"""Read native prompt evidence without starting a CLI or trusting probe verdicts."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import shlex

from claude_native_input_control_audit import blocks, load, output_values, plain, rows
from wsl_live_control_probe import alive


def phases(message, include_tool_results=False):
    text = []
    for block in blocks(message):
        if block.get('type') == 'text':
            text.append(block.get('text', ''))
        elif include_tool_results and block.get('type') == 'tool_result':
            content = block.get('content', '')
            text.append(content if isinstance(content, str) else json.dumps(content))
    return re.findall(r'NETCAP_NATIVE_INPUT=([a-z_]+)', '\n'.join(text))


def audit(root, checks):
    root = root.resolve()
    observed = load(root / 'result.json')
    original = observed['original']
    native = original['sessionId']
    revoke = observed.get('revoke_queued', False)
    inflight = observed.get('inflight', 'model')
    held_phase = 'run' if inflight == 'tool' else 'api_held'
    sender = {'kind': 'plugin', 'name': 'netcap-input-observer-fixture'}

    def check(name, condition):
        checks[name] = bool(condition)
        assert condition, name

    requests = [load(path) for path in sorted((root / 'A/provider-A').glob('request-*.json'),
                key=lambda path: int(path.stem.split('-')[-1]))]
    # Use the actual native main-loop system prompt, independently of the
    # probe's standard-tool classifier. Background labels quote input too.
    main = [request['body'] for request in requests if any(
        block.get('text', '').lstrip().startswith('You are an interactive agent that helps users with software engineering tasks.')
        for block in blocks({'content': request['body'].get('system', [])}))]
    check('owned_provider_authenticates_all_requests', bool(requests) and all(r['auth_matches'] for r in requests))
    check('all_main_requests_keep_original_full_id', bool(main) and all(
        json.loads(packet['metadata']['user_id'])['session_id'] == native for packet in main))
    actual = [phase for packet in main for phase in phases(packet['messages'][-1], True)]
    expected = ['seed_a', 'api_idle', 'human_draft', held_phase]
    if inflight == 'tool':
        expected.append('human_takeover')
    if not revoke:
        expected.append('api_queued')
    if inflight == 'model':
        expected.append('human_takeover')
    expected.append('hook_throw')
    check('primary_requests_deliver_expected_inputs_once', actual == expected and len(main) == len(expected))
    check('explicitly_dropped_input_never_reaches_model', all('hook_drop' not in phases(packet['messages'][-1], True) for packet in main))

    paths = list((root / 'A/claude/projects').glob('*/' + native + '.jsonl'))
    check('one_original_native_history', len(paths) == 1)
    history = rows(paths[0])
    check('native_transcript_identity_is_preserved', all(row.get('sessionId') == native for row in history
          if row.get('type') in ['user', 'assistant', 'system']))
    user_rows = [row for row in history if row.get('type') == 'user' and phases(row.get('message', {}))]
    native_expected = [phase for phase in expected if not (inflight == 'tool' and phase == 'human_takeover')]
    check('native_standalone_messages_match_actual_delivery_form',
          [phase for row in user_rows for phase in phases(row['message'])] == native_expected)
    check('native_message_uuids_are_unique', len({row['uuid'] for row in user_rows}) == len(user_rows))
    for row in user_rows:
        phase = phases(row['message'])[0]
        check('native_origin_' + phase, row.get('origin') == (sender if phase in ['api_idle', 'api_held', 'api_queued', 'run'] else {'kind': 'human'}))
    queue_rows = [row for row in history if row.get('type') == 'queue-operation']
    for phase in ['api_idle', held_phase, 'api_queued', 'human_takeover']:
        check('native_enqueue_once_' + phase, len([row for row in queue_rows if row.get('operation') == 'enqueue'
              and row.get('content') == 'NETCAP_NATIVE_INPUT=' + phase]) == 1)

    commands = [load(path) for path in sorted((root / 'channel/commands').glob('*.json'), key=lambda path: int(path.stem))]
    check('fixture_command_ids_are_unique_and_sequential', [c['id'] for c in commands] == list(range(1, len(commands) + 1)))
    receipts = {c['id']: load(root / 'channel/responses' / (str(c['id']) + '.json')) for c in commands}
    check('every_fixture_command_has_one_matching_receipt', all(receipts[c['id']]['id'] == c['id'] and 'error' not in receipts[c['id']] for c in commands))
    submissions = {c['text'].split('=', 1)[1]: c for c in commands if c['op'] == 'submit'}
    check('fixture_submissions_have_no_retry', len(submissions) == len([c for c in commands if c['op'] == 'submit']))
    for phase, command in submissions.items():
        expected_receipt = {'drop': 'OWNED_PLUGIN_INPUT_WITHDRAWN_AFTER_MANUAL'} if revoke and phase in ['api_queued', 'api_after_manual'] else {'text': command['text'], 'origin': sender}
        check('native_receipt_' + phase, receipts[command['id']]['result'] == expected_receipt)

    driver = [load(path) for path in sorted((root / 'channel/events').glob('*.json'))]
    witness = [load(path) for path in sorted((root / 'channel/witness/events').glob('*.json'))]

    def events(kind, phase=None, source=driver):
        selected = [event for event in source if event['kind'] == kind]
        if phase is not None:
            selected = [event for event in selected if 'NETCAP_NATIVE_INPUT=' + phase in event['data'].get('text', '')]
        return selected

    for label, source in [('driver', driver), ('witness', witness)]:
        if not source:
            continue
        check(label + '_event_coordinates_unique', len({(e.get('generation'), e['seq']) for e in source}) == len(source))
        starts = [event for event in source if event['kind'] == 'session.start']
        check(label + '_loads_in_original_session', bool(starts) and all(e['data']['id'] == native for e in starts))
    check('submitting_module_does_not_observe_its_own_prompt', not events('prompt.submit', 'api_idle') and not events('prompt.submit', 'api_queued'))
    if observed['witness_mode'] == 'plugin':
        for phase in submissions:
            found = events('prompt.submit', phase, witness)
            check('other_plugin_observes_native_sender_' + phase, len(found) == 1 and found[0]['data']['origin'] == sender)

    typed = {entry['label']: base64.b64decode(entry['data_base64'], validate=True).decode() for entry in observed['writes']}
    draft = typed['type-original-draft']
    check('original_typed_unicode_draft_is_recorded', draft == 'NETCAP_NATIVE_INPUT=human_draft 中文🧪')
    check('only_enter_submits_preserved_draft', typed['submit-preserved-human-draft'] == '\r')
    snapshots = [(c, receipts[c['id']]['result']) for c in commands if c['op'] == 'inspect']
    before = next(snapshot for command, snapshot in snapshots if snapshot['draft']['text'] == draft and command['id'] < submissions['api_idle']['id'])
    after = next(snapshot for command, snapshot in snapshots if snapshot['draft']['text'] == draft and command['id'] > submissions['api_idle']['id'])
    check('draft_and_utf16_cursor_preserved_across_native_api', before['draft'] == after['draft'] == {'text': draft, 'cursor': len(draft.encode('utf-16-le')) // 2})
    check('all_reads_keep_original_id_and_directory', all(snapshot['id'] == snapshot['lastId'] == native and snapshot['cwd'] == str(root / 'A/work') for _, snapshot in snapshots))
    check('inspect_history_retains_original_seed', all(any('NETCAP_NATIVE_INPUT=seed_a' in message['text'] for message in snapshot['messages']) for _, snapshot in snapshots))
    check('inspect_messages_do_not_supply_native_uuid', all('uuid' not in message and 'handle' not in message for _, snapshot in snapshots for message in snapshot['messages']))
    human = events('prompt.submit', 'human_draft')
    check('terminal_draft_has_native_composer_origin', len(human) == 1 and human[0]['data']['origin'] == {'kind': 'composer'})

    held = events('turn.start', held_phase)
    check('one_original_running_turn', len(held) == 1)
    held_id = held[0]['data']['turnId']
    completed = [e for e in events('turn.complete') if e['data']['turnId'] == held_id]
    check('original_running_turn_finishes_normally', len(completed) == 1 and completed[0]['data']['reason'] == 'answer' and not completed[0]['data']['isAborted'])
    manual = events('prompt.submit', 'human_takeover')
    check('actual_manual_enter_is_linked_to_original_running_turn', len(manual) == 1 and manual[0]['data']['origin'] == {'kind': 'composer'} and manual[0]['data']['turnId'] == held_id)
    queued = submissions['api_queued']
    begin = [e for e in events('command.begin') if e['data']['id'] == queued['id']]
    returned = [e for e in events('command.return') if e['data']['id'] == queued['id']]
    check('queue_call_remains_pending_through_manual_enter', len(begin) == len(returned) == 1 and
          begin[0]['at'] < manual[0]['at'] < completed[0]['at'] <= returned[0]['at'])
    if witness:
        dispatch = events('prompt.submit', 'api_queued', witness)
        check('other_plugin_sees_queued_call_only_after_original_turn', len(dispatch) == 1 and dispatch[0]['at'] >= completed[0]['at'])
    queued_turn = events('turn.start', 'api_queued')
    if revoke:
        withdrawn = [e for e in witness if e['kind'] == 'prompt.withdrawn']
        check('two_actual_native_calls_withdrawn_after_same_manual_input', len(withdrawn) == 2 and all(e['data']['manualTurn'] == held_id for e in withdrawn))
        check('withdrawn_input_enters_no_turn_or_user_message', not queued_turn and 'api_queued' not in native_expected and 'api_after_manual' not in actual)
    else:
        check('passive_observation_leaves_queued_call_running', len(queued_turn) == 1)
        if inflight == 'model':
            next_human = events('turn.start', 'human_takeover')
            check('queued_plugin_runs_before_later_human_turn', len(next_human) == 1 and queued_turn[0]['at'] < next_human[0]['at'])
    check('explicit_drop_and_fault_are_both_observed', len(events('prompt.submit', 'hook_drop')) == len(events('prompt.submit', 'hook_throw')) == 1)
    check('throwing_hook_allows_a_real_native_turn', len(events('turn.start', 'hook_throw')) == 1 and not events('turn.start', 'hook_drop'))

    native_blocks = [block for row in history for block in blocks(row.get('message', {}))]
    calls = [block for block in native_blocks if block.get('type') == 'tool_use']
    if inflight == 'tool':
        check('one_original_bash_call', len(calls) == 1 and calls[0]['name'] == 'Bash')
        call = calls[0]
        check('bash_only_runs_owned_fixture', shlex.split(call['input']['command']) == ['/usr/bin/python3', str(root / 'A/work/owned_tool.py'), 'run'])
        starts = rows(root / 'A/work/starts.jsonl')
        output = load(root / 'A/work/tool-output.json')
        check('one_real_tool_execution_with_original_identity', len(starts) == 1 and starts[0]['phase'] == 'run' and all(
            starts[0][key] == observed['samples']['tool'][key] for key in ['pid', 'start_ticks']))
        native_results = [block for block in native_blocks if block.get('type') == 'tool_result' and block.get('tool_use_id') == call['id']]
        returned_results = [block for packet in main for block in blocks(packet['messages'][-1])
                            if block.get('type') == 'tool_result' and block.get('tool_use_id') == call['id']]
        check('one_complete_result_in_original_native_history', len(native_results) == 1 and not native_results[0].get('is_error') and output_values(native_results[0]).count(output) == 1)
        check('one_complete_result_enters_original_model_context', len(returned_results) == 1 and not returned_results[0].get('is_error') and output_values(returned_results[0]).count(output) == 1)
        check('manual_text_joins_original_tool_result', 'NETCAP_NATIVE_INPUT=human_takeover' in returned_results[0]['content'] and not events('turn.start', 'human_takeover'))
        check('native_queue_records_manual_input_consumption', len([row for row in queue_rows if row.get('operation') == 'remove' and row.get('content') == 'NETCAP_NATIVE_INPUT=human_takeover']) == 1)
        check('approval_was_visible_and_single_use', bool(re.search(r'❯\s*1\.\s*Yes\b', plain(root / 'approval-before.ansi'))) and typed['explicit-single-tool-approval'] == '\r')
        check('original_tool_has_stopped', not alive(observed['samples']['tool']))
    else:
        check('model_hold_case_invokes_no_tools', not calls)
    final = load(root / 'A/final.stdout')
    check('final_native_roster_preserves_original_executor', len(final) == 1 and all(final[0][key] == original[key] for key in ['pid', 'sessionId', 'cwd', 'startedAt']))
    check('original_executor_stopped_after_fixture_cleanup', not alive(observed['worker']))
    if observed['background']:
        check('native_attach_was_distinct_and_is_stopped', observed['attach']['pid'] != original['pid'] and not alive(observed['attach']))
    else:
        check('original_foreground_receives_terminal_input_directly', all(entry['pid'] == original['pid'] for entry in observed['writes']))

    type_path = root / 'A/work/.claude/types/claude-code.d.ts'
    check('type_contract_was_generated_by_tested_native_version', type_path.read_text().startswith('// Written by Claude Code 2.1.280.'))
    check('actual_modules_pass_generated_type_contract', observed['module_typecheck']['exit_code'] == 0 and not (root / 'module-typecheck.stdout').read_text() and not (root / 'module-typecheck.stderr').read_text())
    return {'root': str(root), 'native_id': native, 'checks': checks, 'main_requests': len(main),
            'auxiliary_requests': len(requests) - len(main), 'main_marker_sequence': actual,
            'native_standalone_input_uuids': [row['uuid'] for row in user_rows],
            'native_type_sha256': hashlib.sha256(type_path.read_bytes()).hexdigest(),
            'scope': 'read-only independent inspection of raw requests, native journal, channel events, receipts and process identities; no production handoff claim'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('root', type=Path)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    checks = {}
    try:
        result = audit(args.root, checks)
        exit_code = 0
    except Exception as error:
        result = {'root': str(args.root), 'checks': checks, 'error': repr(error)}
        exit_code = 1
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False, indent=2))
    raise SystemExit(exit_code)


if __name__ == '__main__':
    main()
