#!/usr/bin/env python3
"""Recheck saved shared-MCP HTTP, SQLite and model evidence without starting a CLI."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import sqlite3


def objects(value):
    """Decode nested JSON text from both native model wire formats."""
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from objects(child)
    elif isinstance(value, list):
        for child in value:
            yield from objects(child)
    elif isinstance(value, str):
        try:
            decoded = json.loads(value)
        except ValueError:
            return
        if isinstance(decoded, (dict, list)):
            yield from objects(decoded)


def audit(root):
    result = json.loads((root / 'result.json').read_text())
    snapshot = json.loads((root / 'snapshot.json').read_text())
    programs = json.loads((root / 'tool-programs.json').read_text())
    checks = {}

    def check(name, condition):
        checks[name] = bool(condition)
        if not condition:
            raise AssertionError(name)

    db = sqlite3.connect('file:' + str(root / 'console/console.sqlite') + '?mode=ro', uri=True)
    try:
        sessions = {row[0]: json.loads(row[1]) for row in db.execute('SELECT id, body FROM sessions')}
        operations = {json.loads(row[0])['command_id']: json.loads(row[0]) for row in db.execute('SELECT body FROM operations')}
    finally:
        db.close()
    check('runner_completed_all_206_checks', result['passed'] and len(result['checks']) == 206 and all(result['checks'].values()))
    check('local_providers_reported_no_error', result['provider_errors'] == {'A': [], 'B': []})
    check('four_profiles_are_distinct', set(result['sessions']) == {'codex-a', 'codex-b', 'claude-a', 'claude-b'} and len(set(result['sessions'].values())) == 4)
    definition = root / 'assets/shared/.mcp.json'
    definition_hash = hashlib.sha256(definition.read_bytes()).hexdigest()
    shared_refs = []
    for profile, sid in result['sessions'].items():
        path = '/api/sessions/' + sid
        initial = result['frames']['initial'][path]['session']
        original = result['frames']['initial'][path + '/execution']['snapshot']
        restored = result['frames']['restored'][path + '/execution']['snapshot']
        disabled = result['frames']['shared-disabled'][path + '/execution']
        enabled = result['frames']['reenabled-used'][path + '/execution']
        check(profile + '_restart_retained_initial_snapshot', original == restored)
        check(profile + '_sqlite_retains_original_native_id', sessions[sid]['scope']['native_id'] == initial['scope']['native_id'])
        check(profile + '_sqlite_matches_final_snapshot', sessions[sid]['execution_snapshot'] == enabled['snapshot'])
        check(profile + '_revision_chain_is_continuous', disabled['snapshot']['revision'] == 2 and enabled['snapshot']['revision'] == 3 and
              disabled['snapshot']['previous_snapshot_id'] == original['id'] and enabled['snapshot']['previous_snapshot_id'] == disabled['snapshot']['id'])
        check(profile + '_both_previous_versions_retained', enabled['history'] == [original, disabled['snapshot']])
        shared_refs.append(next(row for row in original['capability_refs'] if row['asset']['id'] == 'shared'))
        private = next(row for row in original['mcp_selections'] if row['asset_id'] == 'private-' + profile)
        for label, read, mode in [('disabled', disabled, 'disabled'), ('enabled', enabled, 'enabled')]:
            selection = next(row for row in read['snapshot']['mcp_selections'] if row['asset_id'] == 'shared')
            check(profile + '_' + label + '_keeps_definition_source', selection['mode'] == mode and selection['source_path'] == str(definition))
            check(profile + '_' + label + '_keeps_private_binding', next(row for row in read['snapshot']['mcp_selections'] if row['asset_id'] == 'private-' + profile) == private)
            check(profile + '_' + label + '_has_successful_durable_apply', operations['apply-' + label + '-' + profile]['status'] == 'succeeded')
        for number, expected in [(1, original), (6, disabled['snapshot']), (8, enabled['snapshot'])]:
            op = operations['send-' + str(number) + '-' + profile]
            check(profile + '_turn_' + str(number) + '_retains_exact_snapshot', op['status'] == 'succeeded' and op['execution_snapshot_id'] == expected['id'] and
                  op['intent']['scope']['mcp_selections'] == expected['mcp_selections'])
        selection = next(row for row in original['mcp_selections'] if row['asset_id'] == 'shared')
        if profile.startswith('codex'):
            binding = selection['definition']
            check(profile + '_pins_actual_definition_bytes', binding['version'] == 1 and binding['sha256'] == definition_hash)
            expected_values = {'NETCAP_MCP_CONTEXT': profile, 'NETCAP_MCP_LOG': str(root / 'calls' / profile), 'NETCAP_MCP_SECRET': 'fixture-mcp-' + profile}
            expected_bindings = [{'name': name, 'present': True, 'sha256': hashlib.sha256(('netcap-codex-mcp-environment-v1\x00' + name + '\x00true\x00' + value).encode()).hexdigest()}
                                 for name, value in sorted(expected_values.items())]
            check(profile + '_environment_stamps_match_owned_daemon_values', binding['environment'] == expected_bindings)
            check(profile + '_profile_does_not_gain_shared_server_definition', 'mcp_servers' not in (root / profile / 'codex/config.toml').read_text())
        else:
            check(profile + '_does_not_invent_codex_environment_binding', 'definition' not in selection)
    check('one_registered_shared_asset_and_resource_version_for_all_profiles', all(row == shared_refs[0] for row in shared_refs))
    encoded = json.dumps({'snapshot': snapshot, 'frames': result['frames']})
    check('http_and_snapshot_exports_do_not_contain_credential_values', all('fixture-mcp-' + profile not in encoded for profile in result['sessions']))

    request_count = 0
    actual_programs = []
    for letter in ['A', 'B']:
        requests = {int(path.stem.split('-')[-1]): json.loads(path.read_text()) for path in (root / 'joint-providers' / ('provider-' + letter)).glob('request-*.json')}
        request_count += len(requests)
        check(letter + '_all_requests_use_expected_local_protocol_and_credentials', bool(requests) and all(row['auth_matches'] and
              row['path'] in ['/v1/responses', '/v1/messages?beta=true'] for row in requests.values()))
        check(letter + '_tool_call_ids_are_unique', len({row['call_id'] for row in programs[letter]}) == len(programs[letter]))
        for seq, record in requests.items():
            profiles = set(re.findall(r'PROFILE_((?:codex|claude)-[ab])', json.dumps(record['body'])))
            backend = 'codex' if record['path'] == '/v1/responses' else 'claude'
            check(letter + '_request_' + str(seq) + '_has_only_its_profile_history', profiles == {backend + '-' + letter.lower()})
        for program in programs[letter]:
            profile, turn, server = program['profile'], program['turn'], program['server']
            label = profile + '_turn_' + turn
            origin = requests[int(program['call_id'].split('_')[-1])]['body']
            absent = turn in ['6', '99']
            if profile.startswith('codex'):
                returned = [item for record in requests.values() for item in record['body'].get('input', [])
                            if item.get('type') == 'custom_tool_call_output' and item.get('call_id') == program['call_id']]
                decoded = [value for item in returned for value in objects(item)]
                check(label + '_actual_tool_registry_matches_selection', {'server': server, 'available': not absent} in decoded)
            else:
                exposed = any(tool.get('name') == 'mcp__' + server + '__whoami' for tool in origin.get('tools', []))
                check(label + '_actual_model_tools_match_selection', exposed is not absent)
                returned = [block for record in requests.values() for message in record['body'].get('messages', []) for block in message.get('content', [])
                            if isinstance(block, dict) and block.get('type') == 'tool_result' and block.get('tool_use_id') == program['call_id']]
                decoded = [value for item in returned for value in objects(item)]
            if absent:
                continue
            expected = {'profile': profile, 'cwd': str(root / profile / 'work'), 'credential_matches': True, 'asset': 'shared' if server == 'identity' else 'private-' + profile, 'version': 'v1'}
            check(label + '_exact_tool_result_reaches_original_model_call', expected in decoded)
            approval = operations['approve-' + turn + '-' + profile]
            check(label + '_has_successful_single_call_approval', approval['status'] == 'succeeded' and approval['session_id'] == result['sessions'][profile])
            actual_programs.append(program)
    calls = [json.loads(line) for path in (root / 'calls').glob('*/*.jsonl') for line in path.read_text().splitlines()]
    approvals = [row for key, row in operations.items() if key.startswith('approve-')]
    check('every_actual_mcp_call_has_one_successful_approval', len(calls) == len(actual_programs) == len(approvals) == 27)
    for profile in result['sessions']:
        check(profile + '_every_called_tool_used_its_own_cwd_and_credentials', all(row['credential_matches'] and row['cwd'] == str(root / profile / 'work')
              for row in calls if row['profile'] == profile))
    missing = next(row for row in snapshot['sessions'] if row['title'] == 'Excluded MCP without credentials')
    check('disabled_unconfigured_definition_has_explicit_empty_environment_binding', missing['execution_snapshot']['mcp_selections'][0]['definition']['environment'] == [])
    check('unconfigured_definition_does_not_call_mcp', not list((root / 'calls').glob('*/unavailable.jsonl')))
    return {'passed': True, 'checks': checks, 'model_requests': request_count, 'mcp_calls': len(calls), 'approvals': len(approvals),
            'scope': 'read-only HTTP frame, SQLite, definition hash, owned environment stamp, exact model tool result and approval audit'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--native', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    result = audit(args.native.resolve())
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({key: value for key, value in result.items() if key != 'checks'} | {'checks': len(result['checks'])}))


if __name__ == '__main__':
    main()
