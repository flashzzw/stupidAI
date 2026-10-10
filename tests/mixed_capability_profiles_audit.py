#!/usr/bin/env python3
"""Audit saved mixed-profile SQLite, native journals and model requests offline."""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import re
import sqlite3

from shared_mcp_profiles_audit import objects


def audit(root, checks):
    result = json.loads((root / 'mixed-result.json').read_text())
    programs = json.loads((root / 'mixed-programs.json').read_text())

    def check(name, condition):
        checks[name] = bool(condition)
        if not condition:
            raise AssertionError(name)

    def execution(label, profile):
        return result['frames'][label]['/api/sessions/' + result['sessions'][profile] + '/execution']

    def initial_session(profile):
        return result['frames']['initial']['/api/sessions/' + result['sessions'][profile]]['session']

    db = sqlite3.connect('file:' + str(root / 'console/console.sqlite') + '?mode=ro', uri=True)
    try:
        sessions = {row[0]: json.loads(row[1]) for row in db.execute('SELECT id, body FROM sessions')}
        operations = {row['id']: row for row in (json.loads(value[0]) for value in db.execute('SELECT body FROM operations'))}
    finally:
        db.close()
    commands = {row['command_id']: row for row in operations.values()}
    check('runner_finished_without_claiming_a_method_gate', result['passed'] and all(result['checks'].values()) and result['method_gate_verified'] is False)
    check('all_four_independent_profiles_are_present', set(result['sessions']) == {'codex-a', 'codex-b', 'claude-a', 'claude-b'} and len(set(result['sessions'].values())) == 4)
    check('all_twelve_rounds_have_one_input_per_profile', Counter((row['profile'], row['round']) for row in result['rounds']) ==
          Counter((profile, number) for profile in result['sessions'] for number in range(1, 13)))
    sends = [row for row in operations.values() if row['intent']['kind'] == 'send']
    check('rejected_drift_and_unknown_observations_did_not_duplicate_inputs', len(sends) == 48 and all(row['status'] == 'succeeded' for row in sends))
    check('owned_providers_recorded_no_error', result['provider_errors'] == {'A': [], 'B': []})

    snapshots, aliases, journals = {}, {}, {}
    shared_references = []
    for profile, sid in result['sessions'].items():
        initial = execution('initial', profile)['snapshot']
        disabled = execution('disabled', profile)
        reenabled = execution('reenabled', profile)
        final = execution('versions-restored', profile)
        expected_version = 'v2' if profile in ['codex-a', 'claude-b'] else 'v1'
        snapshots.update({row['id']: row for row in final['history'] + [final['snapshot']]})
        check(profile + '_sqlite_and_final_http_preserve_original_identity', sessions[sid]['execution_snapshot'] == final['snapshot'] and
              sessions[sid]['scope']['native_id'] == initial_session(profile)['scope']['native_id'])
        check(profile + '_sqlite_and_http_keep_the_same_history', sessions[sid]['execution_history'] == final['history'])
        versions = final['history'] + [final['snapshot']]
        check(profile + '_snapshot_revision_chain_is_continuous', [row.get('revision', 1) for row in versions] == list(range(1, len(versions) + 1)) and
              all(versions[index]['previous_snapshot_id'] == versions[index - 1]['id'] for index in range(1, len(versions))))
        check(profile + '_first_restart_preserves_mixed_snapshot', execution('restored', profile)['snapshot'] == reenabled['snapshot'])
        check(profile + '_catalog_update_does_not_upgrade_old_execution', execution('registered-v2', profile)['snapshot'] == reenabled['snapshot'])
        check(profile + '_explicit_version_survives_second_restart', next(row['asset']['version'] for row in final['snapshot']['capability_refs'] if row['asset']['id'] == 'shared-method') == expected_version)
        mcp_bindings = lambda snapshot: {(row['asset_id'], row['server']): row for row in snapshot['mcp_selections']}
        check(profile + '_all_mcp_bindings_remain_original', all(mcp_bindings(row) == mcp_bindings(initial) for row in versions))
        private = next(row for row in initial['capability_refs'] if row['asset']['id'] == 'method-' + profile)
        check(profile + '_private_method_is_never_replaced', all(next(row for row in version['capability_refs'] if row['asset']['id'] == 'method-' + profile) == private for version in versions))
        shared_references.append(next(row for row in initial['capability_refs'] if row['asset']['id'] == 'shared-method'))
        for label, mode in [('disabled', 'disabled'), ('enabled', 'enabled')]:
            read = disabled if label == 'disabled' else reenabled
            selection = read['snapshot'].get('skill_selections', []) + read['snapshot'].get('plugin_selections', [])
            targets = {'shared-method', 'suite-' + profile.split('-')[0]}
            check(profile + '_' + label + '_changes_only_the_two_selected_assets', all(row['mode'] == mode for row in selection if row['asset_id'] in targets) and
                  {row['asset_id'] for row in result['previews'][label + '/' + profile]['changes']} == targets and
                  commands['apply-' + label + '-' + profile]['status'] == 'succeeded')
        for label, affected in [('drift-shared-method', True), ('drift-method-claude-a', profile == 'claude-a')]:
            check(profile + '_' + label + '_has_exact_recorded_scope', execution(label, profile)['status'] == ('drifted' if affected else 'unchanged'))
        bundle = next(row['server'] for row in initial_session(profile)['view']['capability_evidence'] if row.get('asset_id') == 'suite-' + profile.split('-')[0])
        aliases[profile] = bundle.replace(':', '_') if profile.startswith('claude') else bundle
        if profile.startswith('claude'):
            path = root / 'console/runtimes' / profile / (sessions[sid]['scope']['native_id'] + '.jsonl')
            journals[profile] = [json.loads(line) for line in path.read_text().splitlines()]
            inputs = [row['text'] for row in journals[profile] if row['kind'] == 'input']
        else:
            files = list((root / profile / 'codex/sessions').rglob('*' + sessions[sid]['scope']['native_id'] + '.jsonl'))
            check(profile + '_one_native_rollout_is_retained', len(files) == 1)
            journals[profile] = [json.loads(line) for line in files[0].read_text().splitlines()]
            inputs = [row['payload']['message'] for row in journals[profile] if row['type'] == 'event_msg' and row['payload']['type'] == 'user_message']
        expected_inputs = [row['intent']['text'] for row in sends if row['session_id'] == sid]
        check(profile + '_native_journal_confirms_twelve_original_inputs', Counter(inputs) == Counter(expected_inputs) and len(inputs) == 12)
    check('all_profiles_start_with_one_shared_method_reference', all(row == shared_references[0] for row in shared_references))
    for key, form in result['form_previews'].items():
        check(key + '_full_form_and_partial_api_agree', form['preview']['changes'] == result['previews'][key]['changes'] and
              all(row['asset_id'] in ['shared-method', 'suite-codex', 'suite-claude'] for row in form['preview']['changes']))
    encoded = json.dumps(result['frames'])
    check('http_frames_do_not_contain_mcp_credential_values', all('fixture-mcp-' + profile not in encoded for profile in result['sessions']))

    for asset, versions in result['methods'].items():
        for version, method in versions.items():
            source = Path(method['source'])
            check(asset + '_' + version + '_markers_match_saved_resources', method['body'] in (source / 'SKILL.md').read_text() and
                  (source / 'references/guide.md').read_text() == 'MIXED_REFERENCE=' + method['reference'] + '\n')

    requests = {}
    for letter in ['A', 'B']:
        requests[letter] = {int(path.stem.split('-')[-1]): json.loads(path.read_text()) for path in (root / 'joint-providers' / ('provider-' + letter)).glob('request-*.json')}
        for seq, request in requests[letter].items():
            backend = 'codex' if request['path'] == '/v1/responses' else 'claude'
            profile = backend + '-' + letter.lower()
            text = json.dumps(request['body'])
            check(letter + '_request_' + str(seq) + '_uses_only_its_profile_and_authentication', request['auth_matches'] and
                  request['path'] in ['/v1/responses', '/v1/messages?beta=true'] and set(re.findall(r'PROFILE_((?:codex|claude)-[ab])', text)) == {profile} and
                  all(result['methods']['method-' + other]['v1']['body'] not in text for other in result['sessions'] if other != profile))

    for row in result['rounds']:
        profile, number = row['profile'], row['round']
        label = profile + '_round_' + str(number)
        backend, letter = profile.split('-')[0], profile[-1].upper()
        operation = operations[row['receipt']['operation_id']]
        snapshot = snapshots[row['snapshot_id']]
        ref = next(value for value in snapshot['capability_refs'] if value['asset']['id'] == row['asset'])
        method = result['methods'][row['asset']][ref['asset']['version']]
        check(label + '_durable_input_uses_its_original_snapshot_and_selections', operation['execution_snapshot_id'] == snapshot['id'] and
              operation['command_id'] == 'round-' + str(number) + '-' + profile and row['version'] == ref['asset']['version'] and
              all(operation['intent']['scope'].get(key, []) == snapshot.get(key, []) for key in ['mcp_selections', 'skill_selections', 'plugin_selections']))
        program = [value for value in programs[letter]['mcp'] if value['profile'] == profile and value['turn'] == str(number)]
        check(label + '_exactly_one_mcp_program_is_bound', len(program) == 1)
        program = program[0]
        own = []
        for seq, request in requests[letter].items():
            matches = re.findall(r'NETCAP_([AB])_TURN_(\d+) PROFILE_((?:codex|claude)-[ab]) TOOL_([a-z]+)', json.dumps(request['body'].get('input' if backend == 'codex' else 'messages', [])))
            if seq > row['request_start'] and matches and matches[-1] == (letter, str(number), profile, row['server']):
                own.append(request)
        check(label + '_selected_body_reaches_its_model', bool(own) and any(method['body'] in json.dumps(request['body']) for request in own))
        if row['asset'] == 'shared-method' and row['version'] == 'v1':
            check(label + '_unselected_v2_body_is_absent', all(result['methods']['shared-method']['v2']['body'] not in json.dumps(request['body']) for request in own))
        if backend == 'codex':
            outputs = [item for request in requests[letter].values() for item in request['body'].get('input', [])
                       if item.get('type') == 'custom_tool_call_output' and item.get('call_id') == program['call_id']]
        else:
            outputs = [block for request in requests[letter].values() for message in request['body'].get('messages', []) for block in message.get('content', [])
                       if isinstance(block, dict) and block.get('type') == 'tool_result' and block.get('tool_use_id') == program['call_id']]
        decoded = [value for item in outputs for value in objects(item)]
        expected = {'profile': profile, 'cwd': str(root / profile / 'work'), 'credential_matches': True,
                    'asset': 'shared' if row['server'] == 'identity' else 'private-' + profile if row['server'] == 'exclusive' else 'suite-' + backend, 'version': 'v1'}
        check(label + '_exact_mcp_result_returns_to_original_call', expected in decoded)
        names = {'mcp__identity__whoami', 'mcp__exclusive__whoami'}
        if number != 4:
            names.add('mcp__' + aliases[profile] + '__whoami')
        if backend == 'codex':
            registries = [set(value['mcp_tools']) for value in decoded if 'mcp_tools' in value]
        else:
            origin = requests[letter][int(program['call_id'].split('_')[-1])]['body']
            registries = [{value['name'] for value in origin['tools'] if value['name'].startswith('mcp__')}]
        check(label + '_native_tool_exposure_matches_combined_selection', bool(registries) and all(value == names for value in registries))
        approvals = [value for value in result['approvals'] if value['profile'] == profile and value['round'] == number]
        check(label + '_one_approval_belongs_to_this_native_input', len(approvals) == 1 and approvals[0]['pending']['native_id'] == operation['receipt']['native_id'] and
              approvals[0]['pending']['native_turn'] == operation['receipt']['native_turn'])
        approval = operations[approvals[0]['receipt']['operation_id']]
        check(label + '_approval_is_durable_and_single_use', approval['status'] == 'succeeded' and approval['session_id'] == operation['session_id'] and
              approval['intent']['pending']['id'] == approvals[0]['pending']['id'] and approval['intent']['option_id'] == ('decision-0' if backend == 'codex' else 'allow'))
        if backend == 'codex':
            context = result['contexts'][str(number) + '/' + profile]
            evidence = next(value['input_evidence'] for value in context['frames'] if value['turn_id'] == operation['receipt']['native_turn'])
            check(label + '_body_hash_and_loaded_evidence_belong_to_original_input', evidence['operation_id'] == operation['id'] and evidence['snapshot_id'] == snapshot['id'] and len(evidence['items']) == 1 and
                  evidence['items'][0]['status'] == 'loaded_exact' and evidence['items'][0]['asset_version'] == row['version'] and
                  evidence['items'][0]['expected_sha256'] == hashlib.sha256((Path(method['source']) / 'SKILL.md').read_bytes()).hexdigest())
        else:
            skill = next(value for value in programs[letter]['skills'] if value['profile'] == profile and value['turn'] == str(number))
            entries = journals[profile]
            check(label + '_native_input_and_skill_invocation_are_recorded', any(value['kind'] == 'input' and value['operation'] == operation['id'] and value['text'] == operation['intent']['text'] for value in entries) and
                  any(block.get('type') == 'tool_use' and block.get('id') == skill['call_id'] and block.get('name') == 'Skill' and block.get('input', {}).get('skill') == row['method_name']
                      for value in entries if value['kind'] == 'event' for block in value['event'].get('message', {}).get('content', []) if isinstance(block, dict)))
    calls = [json.loads(line) for path in (root / 'calls').glob('*/*.jsonl') for line in path.read_text().splitlines()]
    expected_calls = Counter((row['profile'], 'shared' if row['server'] == 'identity' else 'private-' + row['profile'] if row['server'] == 'exclusive' else 'suite-' + row['profile'].split('-')[0]) for row in result['rounds'])
    check('raw_tool_logs_have_exactly_one_call_per_input', Counter((row['profile'], row['asset']) for row in calls) == expected_calls and len(calls) == 48)
    check('all_actual_tools_use_their_own_cwd_and_credentials', all(row['cwd'] == str(root / row['profile'] / 'work') and row['credential_matches'] for row in calls))
    check('approval_and_actual_call_totals_match', len(result['approvals']) == len([row for row in operations.values() if row['intent']['kind'] == 'respond']) == len(calls))
    return {'passed': True, 'model_requests': sum(len(value) for value in requests.values()), 'mcp_calls': len(calls), 'approvals': len(result['approvals']),
            'native_inputs': len(sends), 'method_gate_verified': False,
            'scope': 'read-only SQLite, HTTP, source bytes, exact model call/result, native journal and per-input approval audit'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--native', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    checks = {}
    try:
        result = audit(args.native.resolve(), checks)
    except Exception as error:
        result = {'passed': False, 'error': type(error).__name__ + ': ' + str(error)}
    args.output.write_text(json.dumps({**result, 'checks': checks}, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({**result, 'checks': len(checks)}, ensure_ascii=False))
    raise SystemExit(0 if result['passed'] else 1)


if __name__ == '__main__':
    main()
