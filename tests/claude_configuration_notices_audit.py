#!/usr/bin/env python3
"""Independently join captured ConfigChange callbacks, saved journals and views."""
import argparse
from collections import Counter
import json
from pathlib import Path

from claude_pending_permissions_audit import audit as approval_audit, compact


def signature(value, native=False):
    pairs = [('native_id', 'session_id'), ('native_cwd', 'cwd'), ('path', 'file_path'),
             ('source', 'source'), ('prompt_id', 'prompt_id'), ('agent_id', 'agent_id'), ('agent_type', 'agent_type')]
    return tuple(value.get(raw if native else saved, '') for saved, raw in pairs)


def audit(root, mode):
    root = Path(root)
    checks, facts = approval_audit(root, mode)
    check = lambda name, value: checks.update({mode + '/' + name: bool(value)})
    read = lambda name: json.loads((root / name).read_text())
    app = read('application-result.json')
    sid = app['sessions']['A']
    frame = lambda stage, label: app['frames'][stage]['/api/sessions/' + app['sessions'][label]]['session']
    pending = app['observations']['original-pending']
    operation = pending['native_turn']
    after = frame('after-allow', 'A')
    guard = next(p['permission_guard'] for p in after['view']['pending'] if p['id'] == pending['id'])
    before_config, after_config = read('notice-original.json'), read('notice-target.json')
    check('only_env_changed_in_owned_configuration', before_config != after_config and
          {k: v for k, v in before_config.items() if k != 'env'} == {k: v for k, v in after_config.items() if k != 'env'})
    check('native_permission_report_unchanged', app['observations']['initial-report']['sample_hash'] == app['observations']['notification-report']['sample_hash'])
    if mode == 'policy-disabled-notice':
        check('native_disable_all_hooks_preserved', before_config.get('disableAllHooks') is True and after_config.get('disableAllHooks') is True)
    if mode == 'policy-managed-notice':
        check('native_managed_hooks_policy_preserved', before_config.get('allowManagedHooksOnly') is True and after_config.get('allowManagedHooksOnly') is True and after_config.get('allowManagedPermissionRulesOnly') is True)
    for label in ['A', 'B']:
        original, final = frame('pending', label), frame('finished', label)
        native = original['scope']['native_id']
        wire = [json.loads(line) for line in (root / ('wire-' + label + '.jsonl')).read_text().splitlines()]
        journal = [json.loads(line) for line in (root / 'application/runtimes' / label / (native + '.jsonl')).read_text().splitlines()]
        registration = [row['message'] for row in wire if row['direction'] == 'request' and row['message'].get('request', {}).get('hooks', {}).get('ConfigChange')]
        check(label + '_one_original_observer_registration', len(registration) == 1)
        hooks = registration[0]['request']['hooks']['ConfigChange']
        observer_ids = {id for match in hooks if not match.get('matcher') for id in match['hookCallbackIds']}
        local_ids = {id for match in hooks if match.get('matcher') == 'project_settings|local_settings' for id in match['hookCallbackIds']}
        check(label + '_observer_does_not_replace_local_block_registration', len(observer_ids) == 1 and len(local_ids) == 1 and observer_ids.isdisjoint(local_ids))
        registration_reply = [r['message']['response'] for r in wire if r['direction'] == 'response' and
                              r['message'].get('response', {}).get('request_id') == registration[0]['request_id']]
        check(label + '_native_registration_acknowledged', len(registration_reply) == 1 and registration_reply[0].get('response', {}).get('hooks_applied') is True)
        callbacks = [(i, row['message']) for i, row in enumerate(wire) if row['direction'] == 'response' and
                     row['message'].get('request', {}).get('input', {}).get('hook_event_name') == 'ConfigChange']
        observed = [(i, m) for i, m in callbacks if m['request']['callback_id'] in observer_ids]
        blocked = [(i, m) for i, m in callbacks if m['request']['callback_id'] in local_ids]
        notices = [(i, row['notice']) for i, row in enumerate(journal) if row['kind'] == 'configuration_notice']
        replies = {r['message']['response']['request_id']: r['message']['response'] for r in wire
                   if r['direction'] == 'request' and r['message'].get('type') == 'control_response'}
        check(label + '_every_observer_reply_is_passive', all(replies.get(m['request_id'], {}).get('response') == {} for _, m in observed))
        check(label + '_every_local_callback_still_blocks', all(replies.get(m['request_id'], {}).get('response') == {'decision': 'block'} for _, m in blocked))
        check(label + '_journal_metadata_matches_native_callback_multiset', Counter(signature(n) for _, n in notices) == Counter(signature(m['request']['input'], True) for _, m in observed))
        check(label + '_no_body_or_transcript_in_new_journal_records', all(not any(k in n for k in ['content', 'transcript_path', 'version_hash', 'operation']) for _, n in notices))
        history = final['view']['configuration_notices']
        saved_ids = {n['id'] for _, n in notices}
        check(label + '_view_preserves_only_received_notice_ids', {n['id'] for n in history['items']} == saved_ids)
        check(label + '_registration_not_promoted_to_complete_coverage', history['registered'] is True and history['complete'] is False and not history['recording_failed'])
        check(label + '_current_count_matches_own_query', history['current_count'] == len(notices) and all(n['query_id'] == history['current_query_id'] and n['native_id'] == native for _, n in notices))
        expected_count = 0 if label == 'B' and mode == 'user-notice' else 1
        check(label + '_expected_source_notification_received', len(observed) == expected_count)
        if mode in ['local-notice', 'project-notice']:
            check(label + '_both_callbacks_executed_for_local_source', len(observed) == 1 and len(blocked) == 1)
        if label == 'A':
            notice_index, notice = notices[0]
            input_indices = [i for i, row in enumerate(journal) if row['kind'] == 'input' and row['operation'] == operation]
            result_indices = [i for i, row in enumerate(journal) if row['kind'] == 'event' and row.get('operation') == operation and row.get('event', {}).get('type') == 'result']
            check('notice_received_between_original_input_and_terminal', len(input_indices) == len(result_indices) == 1 and input_indices[0] < notice_index < result_indices[0])
            check('guard_refers_to_saved_original_query_notice', guard.get('configuration_notice_id') == notice['id'] and notice['query_id'] == guard['query_id'] and notice['configuration_id'] == guard['configuration_id'])
            check('native_prompt_is_not_sdk_input_uuid', notice.get('prompt_id') not in [None, '', operation])
            view_notice = next(n for n in history['items'] if n['id'] == notice['id'])
            check('receipt_snapshot_is_original_operation_snapshot', view_notice.get('receipt_operation') == operation and view_notice.get('receipt_snapshot_id') == original['execution_snapshot']['id'] and view_notice.get('receipt_correlation') == 'journal_in_flight')
            check('invalidated_approval_journal_precedes_refusal', any(row['kind'] == 'permission_check' and row.get('action') == 'invalidated' and row['guard'].get('configuration_notice_id') == notice['id'] for row in journal[notice_index + 1:]))
        else:
            check('sibling_received_no_input_and_claims_no_receipt_snapshot', not any(row['kind'] == 'input' for row in journal) and
                  all(not n.get('receipt_operation') and not n.get('receipt_snapshot_id') for n in history['items']))
            check('idle_sibling_native_notification_has_no_prompt_id', all(not m['request']['input'].get('prompt_id') for _, m in observed))
        facts[label + '_notices'] = len(notices)
        facts[label + '_local_block_callbacks'] = len(blocked)
    if mode == 'policy-notice':
        reopened = frame('reopened', 'A')
        h = reopened['view']['configuration_notices']
        old = after['view']['configuration_notices']['items']
        same = lambda n: {k: v for k, v in n.items() if k != 'current'}
        check('sqlite_reopen_preserves_original_notice_metadata_and_snapshot', list(map(same, h['items'])) == list(map(same, old)) and reopened['scope']['native_id'] == after['scope']['native_id'])
        check('sqlite_reopen_does_not_claim_registered_current_query', h['registered'] is False and not h.get('current_query_id') and h['current_count'] == 0 and all(n['current'] is False for n in h['items']))
    return checks, facts


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--matrix', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    checks, cases = {}, {}
    for item in json.loads(args.matrix.read_text()):
        found, facts = audit(item['root'], item['case'])
        checks.update(found)
        cases[item['case']] = facts
    result = {'checks': checks, 'cases': cases, 'passed': bool(checks) and all(checks.values()),
              'scope': 'read-only audit of original wire, journal, handler and provider records; no new native execution'}
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'checks': len(checks), 'failed': [k for k, v in checks.items() if not v], 'cases': len(cases)}))
    if not result['passed']:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
