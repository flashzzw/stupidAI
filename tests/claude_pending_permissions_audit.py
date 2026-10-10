#!/usr/bin/env python3
"""Read-only audit of pending-approval wire, journal, HTTP and provider evidence."""
import argparse
import hashlib
import json
from pathlib import Path


def compact(value):
    return json.dumps(value, ensure_ascii=False, separators=(',', ':'))


def digest_state(state):
    # Independently reconstruct the observation from the captured CLI response.
    # JS string ordering is UTF-16; duplicate native rows remain in the digest.
    ordered = lambda rows: sorted([compact(row) for row in rows], key=lambda s: s.encode('utf-16-be'))
    value = [state['originalCwd'], state['managedOnly'],
             ordered([[r['behavior'], r['source'], r['rule'], r['editability'], r.get('notInEffect') is True] for r in state['rules']]),
             ordered([[r['path'], r['source']] for r in state['workspaceDirectories']]),
             ordered([[r['file'], r.get('path', '')] for r in state.get('errors', [])])]
    return hashlib.sha256(('permission_approval_observation_v1\0' + compact(value)).encode()).hexdigest()


def audit(root, mode):
    root = Path(root)
    checks, facts = {}, {}
    check = lambda name, value: checks.update({mode + '/' + name: bool(value)})
    read = lambda p: json.loads((root / p).read_text())
    captured_root = Path(read('application-request.json')['Root'])
    app = read('application-result.json')
    sid, sibling = app['sessions']['A'], app['sessions']['B']
    frame = lambda name, id=sid: app['frames'][name]['/api/sessions/' + id]['session']
    initial, final = frame('pending'), frame('finished')
    native = initial['scope']['native_id']
    request = app['observations']['original-pending']
    operation = request['native_turn']
    request_id = request['request_id']
    journal_path = Path('application/runtimes/A') / (native + '.jsonl')
    journal = [json.loads(s) for s in (root / journal_path).read_text().splitlines()]
    inputs = [e for e in journal if e['kind'] == 'input']
    baseline = inputs[0]['permission_baseline']
    wire = [json.loads(s) for s in (root / 'wire-A.jsonl').read_text().splitlines()]
    input_frames = [(i, r['message']) for i, r in enumerate(wire) if r['direction'] == 'request' and r['message'].get('type') == 'user']
    prompts = [(i, r['message']) for i, r in enumerate(wire) if r['direction'] == 'response' and r['message'].get('request', {}).get('subtype') == 'can_use_tool']
    answers = [(i, r['message']['response']) for i, r in enumerate(wire) if r['direction'] == 'request' and r['message'].get('type') == 'control_response' and r['message'].get('response', {}).get('request_id') == request_id]
    samples = []
    requests = {r['message']['request_id']: r['message']['request']['subtype'] for r in wire if r['direction'] == 'request' and r['message'].get('type') == 'control_request'}
    for i, row in enumerate(wire):
        reply = row['message'].get('response', {})
        if row['direction'] == 'response' and reply.get('subtype') == 'success' and requests.get(reply.get('request_id')) == 'list_permission_rules':
            state = reply['response']['state']
            samples.append({'wire_index': i, 'hash': digest_state(state), 'state': state})
    check('one_original_input_in_journal', len(inputs) == 1 and inputs[0]['operation'] == operation)
    check('one_original_input_on_wire', len(input_frames) == 1 and input_frames[0][1].get('uuid') == operation and input_frames[0][1].get('session_id') == native)
    check('input_and_pending_share_observation', request['permission_guard']['query_id'] == inputs[0]['query_id'] == baseline['query_id'] and request['permission_guard']['baseline_hash'] == baseline['baseline_hash'])
    check('baseline_precedes_native_input', any(s['hash'] == baseline['baseline_hash'] and s['wire_index'] < input_frames[0][0] for s in samples))
    metadata = read(str(Path('application/runtimes/A') / (native + '.json')))
    # The saved configuration uses recursively sorted schema keys (arrays keep
    # their original order), unlike the native-rule observation's tuple digest.
    metadata.pop('_configuration', None)
    check('configuration_digest_matches_saved_config', hashlib.sha256(json.dumps(metadata, sort_keys=True, ensure_ascii=False, separators=(',', ':')).encode()).hexdigest() == baseline['configuration_id'])
    check('one_original_native_approval', len(prompts) == 1 and prompts[0][1]['request_id'] == request_id and prompts[0][1]['request']['tool_name'] == 'Read' and prompts[0][1]['request']['input'] == request['request'])
    check('approval_came_from_native_rule', len(prompts) == 1 and prompts[0][1]['request'].get('decision_reason_type') == 'rule')
    check('original_connection_and_request_kept', request['id'] == 'A/' + request['connection_id'] + '/' + request_id and final['scope']['native_id'] == native)
    check('snapshot_unchanged', final['execution_snapshot'] == initial['execution_snapshot'])
    check('sibling_snapshot_unchanged', frame('pending', sibling)['execution_snapshot'] == frame('finished', sibling)['execution_snapshot'])
    check('no_pending_request_after_completion', all(p['status'] == 'expired' for p in final['view']['pending']))
    check('baseline_never_claimed_complete_permission', baseline['complete'] is False and request['permission_guard']['complete'] is False)
    entries = [e for e in journal if e['kind'] == 'permission_check']
    sample_hashes = {s['hash'] for s in samples}
    check('recorded_checks_match_captured_native_reports', all(e['request_id'] == request_id and e['operation'] == operation and e['guard']['baseline_hash'] == baseline['baseline_hash'] and (not e['guard']['checked_hash'] or e['guard']['checked_hash'] in sample_hashes) for e in entries))
    check('only_one_native_query_process', len({r['pid'] for r in wire if r['message'].get('session_id') == native}) == 1)
    allowed = [reply for _, reply in answers if reply.get('response', {}).get('behavior') == 'allow']
    denied = [reply for _, reply in answers if reply.get('response', {}).get('behavior') == 'deny']
    check('native_approval_matches_case', len(allowed) == (1 if mode == 'unchanged' else 0))
    if mode != 'interrupt':
        check('one_original_native_response', len(answers) == 1 and len(denied) == (0 if mode == 'unchanged' else 1))
        check('decision_recorded_before_handoff', any(e['action'] == ('checked_allow' if mode == 'unchanged' else 'deny') for e in entries))
    else:
        check('interrupt_uses_original_query', any(r['direction'] == 'request' and r['message'].get('request', {}).get('subtype') == 'interrupt' for r in wire))
    posts = [json.loads(p.read_text()) for p in sorted((root / 'provider-A').glob('request-*.json'))]
    check('only_expected_local_model_requests', len(posts) == (1 if mode == 'interrupt' else 2))
    check('owned_auth_verified', bool(posts) and all(p['auth_matches'] is True for p in posts))
    check('no_host_instructions_in_requests', all('全局协作约定' not in compact(p['body']) for p in posts))
    check('no_sibling_model_request', not list((root / 'provider-B').glob('request-*.json')))
    check('no_token_count_request', all('count_tokens' not in p['path'] for p in posts))
    calls, outputs = {}, {}
    for row in wire:
        event = row['message']
        if row['direction'] != 'response' or event.get('type') not in ['assistant', 'user']:
            continue
        content = event.get('message', {}).get('content')
        for block in content if isinstance(content, list) else []:
            if block.get('type') == 'tool_use':
                calls[block['id']] = block
            elif block.get('type') == 'tool_result':
                outputs[block['tool_use_id']] = block
    check('one_exact_read_and_correlated_result', len(calls) == 1 and set(outputs) == set(calls) and all(call['name'] == 'Read' and call['input']['file_path'] == str(captured_root / 'work/target-A.txt') for call in calls.values()))
    check('tool_bytes_only_after_unchanged_approval', ('OWNED_PENDING_POLICY_CONTENT_A' in compact(list(outputs.values()))) == (mode == 'unchanged'))
    check('tool_error_after_refusal_or_interrupt', len(outputs) == 1 and all(bool(v.get('is_error')) == (mode != 'unchanged') for v in outputs.values()))
    if mode != 'unchanged':
        after = frame('after-allow')
        pending = next(p for p in after['view']['pending'] if p['id'] == request['id'])
        guard = pending['permission_guard']
        check('failed_approval_keeps_live_original_request', pending['status'] == 'pending' and after['connection'] == 'ready' and pending['native_turn'] == operation)
        check('allow_receipt_not_sent', app['observations']['allow-http-status'] >= 400 or app['observations']['allow-operation']['status'] == 'not_sent')
        visible_notices = {n['id'] for n in after['view'].get('configuration_notices', {}).get('items', [])}
        original_notices = [e['notice'] for e in journal if e['kind'] == 'configuration_notice' and e['notice']['id'] in visible_notices
                            and e['notice']['query_id'] == baseline['query_id']]
        expected = 'unavailable' if mode == 'user-invalid' or mode.endswith('-notice') else ('unavailable' if original_notices else 'observed') if mode == 'local-drift' else 'changed'
        check('original_guard_state_visible', guard['state'] == expected and guard['complete'] is False)
        if mode == 'restore':
            check('restoration_does_not_reauthorize_old_request', guard['checked_hash'] == guard['baseline_hash'] and guard['state'] == 'changed')
        if mode == 'invalid':
            report = app['observations']['changed-report']
            check('invalid_managed_rules_disappear_without_parse_error', not report['errors'] and not any(r['source'] == 'policySettings' and r['rule'] == 'Read(./target-A.txt)' for r in report['items']))
        if mode == 'user-invalid':
            check('native_parser_error_remains_visible', any(e['file'] == str(captured_root / 'execution-A/claude/settings.json') for e in app['observations']['changed-report']['errors']))
    final_op = next(o for o in app['snapshot']['operations'] if o['id'] == operation)
    check('original_input_reaches_expected_terminal', final_op['status'] == ('interrupted' if mode == 'interrupt' else 'succeeded'))
    check('network_and_policy_namespace_were_isolated', '--unshare-net' in read('invocation.json')['command'] and str(captured_root / 'policy') in read('invocation.json')['command'])
    check('fixed_native_version', read('versions.json')['claude']['version'] == '2.1.280 (Claude Code)')
    facts.update(root=str(root), captured_root=str(captured_root), native_id=native, operation=operation, approval_request=request_id, native_samples=len(samples), model_requests=len(posts), native_calls=len(calls), native_results=len(outputs))
    return checks, facts


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument('--matrix', type=Path)
    source.add_argument('--native', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    checks, cases = {}, {}
    items = json.loads(args.matrix.read_text()) if args.matrix else [{'root': str(args.native), 'case': json.loads((args.native / 'application-request.json').read_text())['Mode']}]
    for item in items:
        found, facts = audit(item['root'], item['case'])
        checks.update(found)
        cases[item['case']] = facts
    result = {'checks': checks, 'cases': cases, 'passed': bool(checks) and all(checks.values()), 'scope': 'read-only raw-evidence audit; no CLI, model input, tool approval or state repair'}
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'checks': len(checks), 'failed': [k for k, v in checks.items() if not v], 'cases': len(cases)}))
    if not result['passed']:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
