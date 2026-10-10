#!/usr/bin/env python3
"""Audit native attach evidence without sending input or trusting probe verdicts."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import shlex
import sqlite3

from wsl_live_control_probe import alive


def load(path):
    return json.loads(path.read_text())


def rows(path):
    return [json.loads(line) for line in path.read_text().splitlines() if line]


def blocks(message):
    content = message.get('content', [])
    return content if isinstance(content, list) else [{'type': 'text', 'text': content}]


def markers(messages):
    return [block['text'] for message in messages if message.get('role') == 'user'
            for block in blocks(message) if re.fullmatch(r'NETCAP_NATIVE_INPUT=[a-z_]+', block.get('text', ''))]


def plain(path):
    return re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', path.read_text())


def output_values(block):
    content = block.get('content', '')
    text = content if isinstance(content, str) else '\n'.join(item.get('text', '') for item in content)
    values = []
    for line in text.splitlines():
        try:
            values.append(json.loads(line))
        except ValueError:
            pass
    return values


def audit(root):
    root = root.resolve()
    result = load(root / 'result.json')
    checks = {}

    def check(name, condition):
        checks[name] = bool(condition)
        assert condition, name

    samples = result['samples']
    native, sid = result['native_id'], result['session_id']
    expected_markers = ['NETCAP_NATIVE_INPUT=' + value
                        for value in ['seed_a', 'first', 'second', 'shared_draft', 'deny', 'run']]
    requests, main, histories = {}, {}, {}
    for label in ['A', 'B']:
        requests[label] = [load(path) for path in sorted((root / label / ('provider-' + label)).glob('request-*.json'),
                          key=lambda path: int(path.stem.split('-')[-1]))]
        main[label] = [request['body'] for request in requests[label] if markers(request['body'].get('messages', []))]
        original = samples['original' if label == 'A' else 'sibling']
        paths = list((root / label / 'claude/projects').glob('*/' + original['sessionId'] + '.jsonl'))
        check(label + '_one_original_native_history', len(paths) == 1)
        histories[label] = rows(paths[0])
        check(label + '_requests_authenticated_by_owned_provider', bool(requests[label]) and all(r['auth_matches'] for r in requests[label]))
        check(label + '_all_main_requests_keep_full_identity', bool(main[label]) and all(
            json.loads(request['metadata']['user_id'])['session_id'] == original['sessionId'] for request in main[label]))
        check(label + '_history_keeps_full_identity', all(row['sessionId'] == original['sessionId']
              for row in histories[label] if row.get('type') in ['user', 'assistant', 'system']))
    check('six_explicit_inputs_and_one_tool_followup', len(main['A']) == 7 and
          [marker for request in main['A'] for marker in markers(request['messages'][-1:])] == expected_markers)
    check('only_sibling_seed_input', len(main['B']) == 1 and markers(main['B'][0]['messages']) == ['NETCAP_NATIVE_INPUT=seed_b'])
    check('native_history_contains_each_original_input_once', [row['message']['content'] for row in histories['A']
          if row.get('type') == 'user' and isinstance(row.get('message', {}).get('content'), str)] == expected_markers)
    check('no_production_rejection_text_reached_any_model', all('UNAUTHORIZED_PRODUCTION_INPUT' not in json.dumps(request)
          for group in requests.values() for request in group))

    native_blocks = [block for row in histories['A'] for block in blocks(row.get('message', {}))]
    calls = [block for block in native_blocks if block.get('type') == 'tool_use']
    check('exactly_two_original_owned_bash_requests', len(calls) == 2 and all(block['name'] == 'Bash' for block in calls))
    calls = {call['input']['description'].removeprefix('OWNED_NATIVE_INPUT_'): call for call in calls}
    check('one_refused_and_one_explicitly_approved_request', set(calls) == {'deny', 'run'})
    returns = {}
    for phase, call in calls.items():
        check(phase + '_command_only_targets_owned_tool', shlex.split(call['input']['command']) ==
              ['/usr/bin/python3', str(root / 'A/work/owned_tool.py'), phase])
        returned = [block for block in native_blocks if block.get('type') == 'tool_result' and block['tool_use_id'] == call['id']]
        check(phase + '_one_original_native_result', len(returned) == 1)
        returns[phase] = returned[0]
    check('native_refusal_is_error_for_original_request', returns['deny']['is_error'] and 'rejected' in returns['deny']['content'])
    next_input = next(request for request in main['A'] if markers(request['messages'][-1:]) == ['NETCAP_NATIVE_INPUT=run'])
    carried = [block for message in next_input['messages'] for block in blocks(message)
               if block.get('type') == 'tool_result' and block['tool_use_id'] == calls['deny']['id']]
    check('next_explicit_input_retains_original_refusal', len(carried) == 1 and carried[0]['is_error'] and
          carried[0]['content'].startswith(returns['deny']['content']))
    suffix = carried[0]['content'][len(returns['deny']['content']):]
    check('refusal_suffix_is_separately_identified_native_context', not suffix or bool(re.fullmatch(
          r'\n\n<system-reminder>\n<total_tokens>\d+ tokens left</total_tokens>\n</system-reminder>', suffix)))
    check('refusal_did_not_trigger_an_automatic_model_followup', not any(
        not markers(request['messages'][-1:]) and markers(request['messages'])[-1] == 'NETCAP_NATIVE_INPUT=deny'
        for request in main['A']))

    starts = rows(root / 'A/work/starts.jsonl')
    output = load(root / 'A/work/tool-output.json')
    check('one_actual_tool_and_no_refused_tool_effect', len(starts) == 1 and starts[0]['phase'] == 'run')
    check('tool_start_matches_sampled_process_identity', all(starts[0][key] == samples['tool'][key] for key in ['pid', 'start_ticks']))
    check('complete_output_belongs_to_original_tool', output == {'marker': 'OWNED_NATIVE_TOOL_OUTPUT', 'phase': 'run',
          'pid': starts[0]['pid'], 'cwd': str(root / 'A/work'), 'uid': samples['worker']['uid']})
    check('original_native_history_has_complete_output', not returns['run'].get('is_error') and output_values(returns['run']).count(output) == 1)
    followup = [request for request in main['A'] if not markers(request['messages'][-1:])]
    received = [block for request in followup for block in blocks(request['messages'][-1])
                if block.get('type') == 'tool_result' and block['tool_use_id'] == calls['run']['id']]
    check('one_followup_returns_complete_output_to_same_model_context', len(followup) == len(received) == 1 and
          not received[0].get('is_error') and output_values(received[0]).count(output) == 1)

    writes = result['writes']
    decoded = {row['label']: base64.b64decode(row['data_base64'], validate=True) for row in writes}
    expected_labels = ['first-text', 'first-enter', 'second-text', 'second-enter', 'draft-first-part',
                       'draft-second-part', 'draft-submit-by-second', 'deny-text', 'deny-enter',
                       'select-refusal-in-first-client', 'confirm-refusal-in-second-client', 'run-text',
                       'run-enter', 'explicit-single-tool-approval', 'detach-first-client']
    check('recorded_test_input_bytes_match_expected_sequence', [row['label'] for row in writes] == expected_labels and
          all(row['written'] for row in writes) and all(a['at'] < b['at'] for a, b in zip(writes, writes[1:])))
    first_labels = {'first-text', 'first-enter', 'draft-first-part', 'deny-text', 'deny-enter',
                    'select-refusal-in-first-client', 'detach-first-client'}
    check('input_bytes_belong_to_two_distinct_clients', samples['first_attach']['pid'] != samples['second_attach']['pid'] and all(
        row['pid'] == samples['first_attach' if row['label'] in first_labels else 'second_attach']['pid'] for row in writes))
    check('shared_unsubmitted_draft_bytes_form_one_original_input', decoded['draft-first-part'] == b'NETCAP_NATIVE_INPUT=shared_' and
          decoded['draft-second-part'] == b'draft' and decoded['draft-submit-by-second'] == b'\r')
    no = re.findall(r'(\d+)\.\s*No\b', plain(root / 'A/refusal-before.ansi'))
    check('first_client_selects_only_the_visible_no_option', bool(no) and
          decoded['select-refusal-in-first-client'] == b'\x1b[B' * (int(no[-1]) - 1))
    for label in ['first', 'second']:
        check(label + '_terminal_displays_shared_no_selection', bool(re.search(r'❯\s*No\b',
              plain(root / ('A/refusal-selected-' + label + '.ansi'))[-600:])))
    check('second_client_confirms_refusal_and_later_one_approval', decoded['confirm-refusal-in-second-client'] ==
          decoded['explicit-single-tool-approval'] == b'\r' and decoded['detach-first-client'] == b'\x1a')
    check('full_uuid_attach_is_explicitly_rejected', samples['full_id_attach']['exit_code'] == 1 and
          "No job matching '" + native + "'" in (root / 'A/full-id-attach.ansi').read_text())
    for tag in ['original', 'short-selector-check', 'after-second-attach', 'final']:
        listed = load(root / ('A/' + tag + '.stdout'))
        check(tag + '_roster_binds_short_selector_to_original_full_id_and_worker', len(listed) == 1 and
              all(listed[0][key] == samples['original'][key] for key in ['id', 'sessionId', 'pid', 'cwd', 'startedAt']))

    identity_keys = ['pid', 'start_ticks', 'cwd', 'exe', 'uid']
    for stage, frame in result['process_samples'].items():
        for name, record in frame['processes'].items():
            expected = samples[name]
            gone = stage == 'cleanup' or (name == 'first_attach' and stage in ['first-client-detached', 'tool-finished', 'console-closed']) or (
                name == 'tool' and stage in ['tool-finished', 'console-closed']) or (name == 'console' and stage == 'console-closed')
            current = record['observed']
            check(stage + '_' + name + '_identity_and_lifetime', record['expected'] == expected and
                  (current is None if gone else current is not None and current['state'] != 'Z' and
                   all(current[key] == expected[key] for key in identity_keys)))
    check('all_sampled_owned_processes_are_still_stopped', all(not alive(samples[name])
          for name in ['worker', 'sibling_worker', 'first_attach', 'second_attach', 'console', 'tool']))

    http = load(root / 'A/application/http.json')
    detail_path = '/api/sessions/' + sid
    initial = result['frames']['two-client-idle'][detail_path]['session']
    for stage, frame in result['frames'].items():
        for path, body in frame.items():
            check(stage + '_' + path.rsplit('/', 1)[-1] + '_matches_recorded_handler_response', any(
                row['path'] == path and row['response'] == {'status': 200, 'body': body} for row in http))
        detail, sample = frame[detail_path], frame[detail_path + '/native/observe']
        history = frame[detail_path + '/native/history']
        check(stage + '_readonly_history_keeps_original_identity', history['read_only'] and
              history['session']['native_id'] == native and history['state_namespace'] == str(root / 'A/claude') and
              history['coverage'] == 'claude_main_transcript_entries' and bool(history['entries']))
        check(stage + '_readonly_binding_preserved', detail['session'] == initial and not detail['operations'] and
              initial['scope']['native_id'] == native and initial['control_mode'] == 'native_readonly' and initial['paused'])
        check(stage + '_sample_never_proves_input_or_complete_occupancy', sample['read_only'] and sample['status'] == 'sampled' and
              sample['view']['status'] == 'unknown' and not sample['view']['evidence']['complete'] and not sample['view']['execution_clear'])
        commands = [row for row in http if row['path'] == '/api/commands' and row['request']['command_id'].startswith(stage + '-reject-')]
        check(stage + '_all_five_execution_commands_rejected', len(commands) == 5 and
              {row['request']['kind'] for row in commands} == {'send', 'resume', 'takeover', 'interrupt', 'respond'} and
              all(row['response']['status'] == 409 and '只读来源' in row['response']['body']['error'] for row in commands))
    commands = [row for row in http if row['path'] == '/api/commands']
    check('only_twenty_control_rejections_and_one_create_rejection', len(commands) == 21 and
          all(row['response']['status'] == 409 for row in commands))
    check('native_auth_conflict_still_blocks_new_execution', any(row['request']['command_id'] == 'reject-create' and
          'apiKeyHelper' in row['response']['body']['error'] for row in commands))
    check('all_observation_keeps_native_auth_helper_untouched', (root / 'A/helper-before-read.jsonl').read_bytes() ==
          (root / 'A/helper-calls.jsonl').read_bytes() and len(rows(root / 'A/helper-calls.jsonl')) == 1)
    with sqlite3.connect('file:' + str(root / 'A/application/state/console.sqlite') + '?mode=ro', uri=True) as db:
        check('sqlite_integrity', db.execute('PRAGMA integrity_check').fetchall() == [('ok',)])
        saved = [json.loads(row[0]) for row in db.execute('SELECT body FROM sessions')]
        check('sqlite_keeps_only_original_readonly_binding', len(saved) == 1 and all(saved[0][key] == initial[key]
              for key in ['id', 'scope', 'native_binding', 'control_mode', 'permission', 'status', 'paused', 'generation', 'view']))
        check('sqlite_has_no_execution_operations_or_rejected_receipts', db.execute('SELECT COUNT(*) FROM operations').fetchone()[0] == 0 and
              db.execute('SELECT COUNT(*) FROM command_receipts').fetchone()[0] == 1)
    for name, digest in result['artifacts'].items():
        check('tested_artifact_unchanged_' + name, hashlib.sha256((Path(__file__).resolve().parents[1] / name).read_bytes()).hexdigest() == digest)
    check('counterexample_is_not_reported_as_goal_acceptance', result['input_exclusivity'] == 'violated' and result['goal_acceptance_met'] is False)
    return {'root': str(root), 'passed': all(checks.values()), 'checks': checks, 'native_id': native, 'session_id': sid,
            'main_requests': {key: len(value) for key, value in main.items()},
            'all_requests': {key: len(value) for key, value in requests.items()}, 'http_count': len(http),
            'refusal_native_suffix': suffix, 'scope': 'read-only raw input, provider, native history, process, HTTP and SQLite audit'}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('root', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    outcome = audit(args.root)
    args.output.write_text(json.dumps(outcome, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({key: outcome[key] for key in ['root', 'passed', 'main_requests', 'all_requests', 'http_count']} |
                     {'checks': len(outcome['checks'])}, ensure_ascii=False))
