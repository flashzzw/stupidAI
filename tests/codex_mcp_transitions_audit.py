#!/usr/bin/env python3
"""Independently recompute saved MCP launch bindings and native call provenance."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import sqlite3
import stat

from shared_mcp_profiles_audit import objects


def encoded(value):
    return json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode()


def source_digest(root):
    digest = hashlib.sha256()

    def walk(path):
        relative = '.' if path == root else path.relative_to(root).as_posix()
        digest.update((json.dumps(relative, ensure_ascii=False) + ' ' + stat.filemode(path.stat().st_mode) + '\n').encode())
        if path.is_dir():
            for child in sorted(path.iterdir()):
                walk(child)
        else:
            digest.update((hashlib.sha256(path.read_bytes()).hexdigest() + '\n').encode())
    walk(root)
    return digest.hexdigest()


def audit(root):
    report = json.loads((root / 'result.json').read_text())
    raw = json.loads((root / 'native-readbacks.json').read_text())
    exported = json.loads((root / 'snapshot.json').read_text())
    programs = json.loads((root / 'tool-programs.json').read_text())
    checks = {}

    def check(name, value):
        checks[name] = bool(value)
        if not value:
            raise AssertionError(name)

    db = sqlite3.connect('file:' + str(root / 'console/console.sqlite') + '?mode=ro', uri=True)
    try:
        sessions = {sid: json.loads(body) for sid, body in db.execute('SELECT id,body FROM sessions')}
        operations = {json.loads(body)['command_id']: json.loads(body) for (body,) in db.execute('SELECT body FROM operations')}
    finally:
        db.close()
    check('native_runner_finished_all_observed_cases', report['passed'] and len(report['checks']) >= 209 and all(report['checks'].values()))
    check('four_profiles_and_two_same_daemon_peers_are_distinct', len(report['sessions']) == len(set(report['sessions'].values())) == 6)
    for session in exported['sessions']:
        # HTTP adds derived access fields that are absent from SQLite. Compare
        # persisted execution identities/history, not that display projection.
        check('sqlite_preserves_exported_bindings_' + session['id'], all(sessions[session['id']].get(key) == session.get(key) for key in [
            'id', 'scope', 'execution_snapshot', 'execution_history', 'pending_execution_change', 'execution_change_attempts']))

    snapshots = {}
    for frame in report['frames'].values():
        for path, value in frame.items():
            if not path.endswith('/execution'):
                continue
            candidates = [value['snapshot'], *value.get('history', [])]
            if value.get('pending_change'):
                candidates.append(value['pending_change']['snapshot'])
            for snapshot in candidates:
                snapshots[snapshot['id']] = snapshot
    planned = {}
    for snapshot_id, snapshot in snapshots.items():
        if snapshot['instance']['kind'] != 'codex':
            continue
        for selection in snapshot.get('mcp_selections', []):
            definition = selection['definition']
            launch = definition['binding']
            label = snapshot_id + '/' + selection['server']
            reference = next(row for row in snapshot['capability_refs'] if row['asset']['id'] == selection['asset_id'])
            source = Path(selection['source_path'])
            check(label + '_registered_tree_and_definition_bytes', definition['version'] == 2 and definition['source_hash'] == reference['source']['sha256'] == source_digest(Path(reference['source']['resolved_path'])) and definition['sha256'] == hashlib.sha256(source.read_bytes()).hexdigest())
            execution_profile = snapshot['instance']['id']
            values = {'NETCAP_MCP_CONTEXT': execution_profile, 'NETCAP_MCP_LOG': str(root / 'calls' / execution_profile), 'NETCAP_MCP_SECRET': 'fixture-mcp-' + execution_profile}
            environment = [{'name': name, 'present': True, 'sha256': hashlib.sha256(('netcap-codex-mcp-environment-v1\0' + name + '\0true\0' + value).encode()).hexdigest()} for name, value in sorted(values.items())]
            check(label + '_actual_reference_versions', definition['environment'] == environment)
            instance = snapshot['instance']
            context = hashlib.sha256(b'netcap-codex-mcp-scope-v1\0' + encoded([snapshot['session_id'], snapshot['state_namespace'], instance['id'], instance['profile'], instance['socket'], str(source), definition['source_hash'], definition['sha256'], environment])).hexdigest()
            native = json.loads(source.read_text())['mcpServers'][selection['server']]
            expand = lambda text: re.sub(r'\$\{([^}]+)\}', lambda match: values[match[1]], text)
            resource = lambda text: str(source.parent / text) if text.startswith(('./', '../', 'scripts/')) else text
            args = [resource(expand(value)) for value in native.get('args', [])]
            env = {name: expand(value) for name, value in sorted(native.get('env', {}).items())}
            identity = {'version': 1, 'digest': '', 'asset_id': selection['asset_id'], 'asset_version': reference['asset']['version'], 'server': selection['server']}
            digest = hashlib.sha256(b'netcap-mcp-stdio-v1\0' + encoded({'identity': identity, 'context': context, 'cwd': snapshot['cwd'], 'command': resource(expand(native['command'])), 'args': args, 'environment': env})).hexdigest()
            expected = {**identity, 'digest': digest, 'context': context, 'cwd': snapshot['cwd'], 'environment': sorted(env)}
            check(label + '_launch_digest_matches_actual_expansion', launch == expected)
            planned.setdefault(snapshot['session_id'], {})[digest] = {key: launch[key] for key in identity}
    for tag, transition in report['transitions'].items():
        command = operations['apply-' + tag]
        candidate = transition['pending']['snapshot']
        before = transition['before']['snapshot']
        check(tag + '_durable_intent_preserves_original_and_candidate', command['intent']['previous_scope']['mcp_selections'] == before.get('mcp_selections', []) and command['intent']['scope'].get('mcp_selections', []) == candidate.get('mcp_selections', []) and command['execution_snapshot_id'] == candidate['id'])
        check(tag + '_configuration_status_matches_actual_outcome', command['status'] == ('failed' if tag == 'second_subscriber' else 'succeeded'))
    native_calls = 0
    for label in ['codex-a', 'peer', 'last']:
        sid = report['sessions'][label]
        thread = raw[label + '_final_history']['thread']
        check(label + '_native_id_matches_sqlite', thread['id'] == sessions[sid]['scope']['native_id'])
        for turn in thread['turns']:
            for item in turn['items']:
                if item['type'] != 'mcpToolCall' or item['status'] != 'completed' or not item.get('result'):
                    continue
                native_calls += 1
                result = item['result']
                payload = json.loads(result['content'][0]['text'])
                identity = result.get('_meta', {}).get('netcap/binding')
                key = label + '/' + turn['id'] + '/' + item['id']
                if item['server'] == 'keep':
                    check(key + '_inherited_server_has_no_invented_binding', identity is None and payload['asset'] == 'inherited')
                    continue
                check(key + '_native_result_keeps_exact_launch_identity', identity == planned[sid].get(identity['digest']) and identity['server'] == item['server'])
                check(key + '_actual_result_version_matches_launch', payload['asset'] == identity['asset_id'] and payload['version'] == identity['asset_version'])
                if identity['asset_id'] in ['shared', 'added']:
                    check(key + '_actual_version_marker_and_upstream_metadata', payload['marker'] == report['versions'][payload['asset'] + '/' + payload['version']]['marker'] and result['_meta']['original'] == 'preserved')
    stale = next(row for row in raw['stale_inventory']['data'] if row['name'] == 'identity')
    check('same_name_same_server_info_exposes_old_loaded_version', {key: value for key, value in stale['serverInfo'].items() if value is not None} == {'name': 'owned-mcp', 'version': 'unchanged'} and stale['serverCapabilities']['experimental']['netcap/binding']['asset_version'] == 'v2' and stale['serverCapabilities']['experimental']['original'] == {'fixture': 'preserved'})
    for phase in ['stale', 'restarted_stale']:
        read = report['frames'][phase]['/api/sessions/' + report['sessions']['codex-a'] + '/execution']
        check(phase + '_v3_is_only_a_pending_candidate', next(row for row in read['snapshot']['mcp_selections'] if row['asset_id'] == 'shared')['definition']['asset_version'] == 'v2' and next(row for row in read['pending_change']['snapshot']['mcp_selections'] if row['asset_id'] == 'shared')['definition']['asset_version'] == 'v3')
    check('original_recovery_has_durable_confirmation', operations['recover-original']['status'] == 'succeeded' and raw['recovery_check']['satisfied'])
    attempts = sessions[report['sessions']['codex-a']]['execution_change_attempts']
    failed = report['transitions']['second_subscriber']['pending']
    check('failed_candidate_remains_separate_from_effective_history', len(attempts) == 1 and attempts[0]['change']['status'] == 'recovered' and attempts[0]['change']['snapshot'] == failed['snapshot'] and attempts[0]['change']['recovery_operation_id'] == operations['recover-original']['id'] and failed['snapshot']['id'] not in [row['id'] for row in sessions[report['sessions']['codex-a']]['execution_history']])

    requests_count, programs_count = 0, 0
    for letter in ['A', 'B']:
        requests = [json.loads(path.read_text()) for path in (root / 'joint-providers' / ('provider-' + letter)).glob('request-*.json')]
        requests_count += len(requests)
        check(letter + '_requests_use_only_owned_protocol_and_credentials', requests and all(row['auth_matches'] and row['path'] in ['/v1/responses', '/v1/messages?beta=true'] for row in requests))
        for program in programs[letter]:
            programs_count += 1
            returned = [value for row in requests for value in objects(row['body']) if value.get('call_id') == program['call_id'] and value.get('type') == 'custom_tool_call_output' or value.get('tool_use_id') == program['call_id'] and value.get('type') == 'tool_result']
            results = [value for item in returned for value in objects(item) if value.get('profile') == program['profile'] and value.get('credential_matches')]
            number = program['turn']
            check(letter + '/' + number + '_original_tool_result_returns_to_model', bool(results))
            approval, sent = operations['approval-' + number], operations['input-' + number]
            check(letter + '/' + number + '_one_successful_approval_for_original_input', approval['status'] == sent['status'] == 'succeeded' and approval['session_id'] == sent['session_id'] and sent['execution_snapshot_id'] in snapshots)
    calls = [json.loads(line) for path in (root / 'calls').glob('*/*.jsonl') for line in path.read_text().splitlines()]
    approvals = [row for key, row in operations.items() if key.startswith('approval-')]
    check('every_tool_call_has_exactly_one_approval', len(calls) == len(approvals) == programs_count)
    check('candidate_v3_was_never_called', all(row['version'] != 'v3' for row in calls))
    public = json.dumps({'frames': report['frames'], 'snapshot': exported})
    check('public_snapshots_do_not_copy_credential_values', all('fixture-mcp-' + profile not in public for profile in ['codex-a', 'codex-b', 'claude-a', 'claude-b']))
    return {'passed': True, 'checks': checks, 'snapshots': len(snapshots), 'bound_native_calls': native_calls, 'model_requests': requests_count, 'mcp_calls': len(calls), 'approvals': len(approvals)}


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
