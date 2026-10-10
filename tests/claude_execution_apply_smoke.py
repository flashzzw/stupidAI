#!/usr/bin/env python3
"""Explicit Claude same-ID MCP changes; real CLI and local providers only."""
import argparse
import json
import os
from pathlib import Path
import signal
import time
from urllib.error import HTTPError

from claude_mcp_selection_smoke import MCPFixture
from claude_remote_mcp_smoke import RemoteFixture
from console_fixture import wait_for
from execution_config_probe import write


def fixture_for(binary, transport):
    f = MCPFixture(binary) if transport == 'stdio' else RemoteFixture(binary, transport)
    source = f.root / 'shared-mcp/.mcp.json'
    definitions = json.loads(source.read_text())
    # One asset contributes multiple names. A single UI choice must update all.
    definitions['mcpServers']['companion'] = definitions['mcpServers']['identity']
    write(source, definitions)
    return f


def run(f):
    checks = {}
    sid = f.create_selected('change')
    sibling = f.create_selected('sibling')
    native = f.session(sid)['scope']['native_id']
    sibling_native = f.session(sibling)['scope']['native_id']

    def read(): return f.api(f'/api/sessions/{sid}/execution')
    def operation(receipt): return next(o for o in f.snapshot()['operations'] if o['id'] == receipt['operation_id'])
    def settled(receipt):
        return wait_for(lambda: (o if o['status'] in ('succeeded', 'not_sent', 'failed') else None)
            if (o := operation(receipt)) else None, 'configuration operation settles', timeout=60)
    def command(key, kind, **params):
        def attempt():
            try: return f.command(key, kind, session_id=sid, **params)
            except HTTPError as error:
                body = json.load(error)
                if error.code == 409 and '正在核对' in body.get('error', ''): return None
                raise RuntimeError(body) from error
        return wait_for(attempt, kind + ' accepted', timeout=20)
    def stage(key, mode):
        before = read()['snapshot']
        request = dict(base_snapshot_id=before['id'], pending_change_id='', selections=[dict(asset_id='shared', mode=mode)])
        preview = f.api(f'/api/sessions/{sid}/execution/preview', request)
        assert len(preview['changes']) == 2
        receipt = command('stage-' + key, 'stage_execution', execution_change={**request, 'preview_hash': preview['preview_hash']})
        assert f.command('stage-' + key, 'stage_execution', session_id=sid, execution_change={**request, 'preview_hash': preview['preview_hash']}) == receipt
        plan = read()['pending_change']
        return before, plan, dict(base_snapshot_id=before['id'], pending_change_id=plan['id'])
    def journal(id):
        return [json.loads(x) for x in (f.root / 'console/runtimes/claude-a' / (id + '.jsonl')).read_text().splitlines()]

    first = f.command('initial-send', 'send', session_id=sid, text='NETCAP_A_TURN_1')
    approval = f.pending(sid)
    original, plan, ref = stage('disabled', 'disabled')
    checks['saving_during_approval_keeps_original_version'] = read()['snapshot'] == original and f.pending(sid)['id'] == approval['id'] and not f.calls('A')
    try:
        f.command('prepare-busy', 'prepare_execution', session_id=sid, execution_change=ref)
        checks['preparation_blocks_pending_tool'] = False
    except HTTPError as error: checks['preparation_blocks_pending_tool'] = error.code == 409
    f.command('allow-initial', 'respond', session_id=sid, pending_id=approval['id'], option_id='allow')
    f.idle(sid)
    checks['inflight_tool_retains_original_configuration'] = f.calls('A') == [f.expected_identity('A')]
    history = f.session(sid)['view']['messages']
    count = len(f.provider.requests)
    prep = command('prepare-disabled', 'prepare_execution', execution_change=ref)
    checks['preparation_has_baseline_without_model_call'] = settled(prep)['status'] == 'succeeded' and bool(read()['pending_change']['baseline']) and len(f.provider.requests) == count
    checks['preparation_holds_and_keeps_old_snapshot'] = read()['hold'] and f.session(sid)['paused'] and read()['snapshot'] == original
    for kind in ('send', 'resume'):
        try:
            f.command('held-' + kind, kind, session_id=sid, text='MUST_NOT_SEND')
            checks['hold_blocks_' + kind] = False
        except HTTPError as error: checks['hold_blocks_' + kind] = error.code == 409
    # Another session shares this bridge/profile and keeps an outstanding
    # permission request while the selected session is closed and restarted.
    f.command('sibling-send', 'send', session_id=sibling, text='NETCAP_A_TURN_9')
    sibling_pending = f.pending(sibling)
    sibling_journal = journal(sibling_native)
    applied = command('apply-disabled', 'apply_execution', execution_change=ref)
    checks['explicit_apply_succeeded'] = settled(applied)['status'] == 'succeeded'
    wait_for(lambda: 'pending_change' not in read(), 'candidate promoted')
    after = read()
    checks['same_id_and_exact_history'] = f.session(sid)['scope']['native_id'] == native and f.session(sid)['view']['messages'] == history
    checks['candidate_and_history_preserved_exactly'] = after['snapshot'] == plan['snapshot'] and after['history'] == [original]
    selected = [e for e in after['capability_evidence'] if e.get('asset_id') == 'shared']
    checks['all_asset_servers_disabled'] = len(selected) == 2 and all(e['verified'] and e['mode'] == 'disabled' for e in selected)
    checks['sibling_query_and_pending_permission_untouched'] = journal(sibling_native) == sibling_journal and f.pending(sibling)['id'] == sibling_pending['id']
    f.command('allow-sibling', 'respond', session_id=sibling, pending_id=sibling_pending['id'], option_id='allow')
    f.idle(sibling)
    checks['sibling_still_calls_with_original_binding'] = f.calls('A') == [f.expected_identity('A')] * 2
    checks['old_input_keeps_original_snapshot'] = operation(first)['execution_snapshot_id'] == original['id']
    checks['apply_retry_is_same_operation'] = f.command('apply-disabled', 'apply_execution', session_id=sid, execution_change=ref) == applied
    checks['apply_preserves_pause'] = f.session(sid)['paused'] and not f.session(sid).get('execution_hold', False)
    before_calls = f.calls('A')
    second = f.command('disabled-send', 'send', session_id=sid, text='NETCAP_A_TURN_2')
    f.idle(sid)
    checks['disabled_turn_uses_new_snapshot_without_tool'] = operation(second)['execution_snapshot_id'] == plan['snapshot']['id'] and f.calls('A') == before_calls
    old, enabled_plan, enabled_ref = stage('enabled', 'enabled')
    assert settled(command('prepare-enabled', 'prepare_execution', execution_change=enabled_ref))['status'] == 'succeeded'
    assert settled(command('apply-enabled', 'apply_execution', execution_change=enabled_ref))['status'] == 'succeeded'
    wait_for(lambda: 'pending_change' not in read(), 'enabled candidate promoted')
    checks['third_version_retains_both_prior_snapshots'] = read()['snapshot'] == enabled_plan['snapshot'] and read()['history'] == [original, old]
    third = f.command('reenabled-send', 'send', session_id=sid, text='NETCAP_A_TURN_3')
    approval = f.pending(sid)
    checks['reenabled_call_needs_new_approval'] = f.calls('A') == before_calls
    f.command('allow-reenabled', 'respond', session_id=sid, pending_id=approval['id'], option_id='allow')
    f.idle(sid)
    checks['reenabled_call_uses_pinned_binding_and_version'] = f.calls('A') == before_calls + [f.expected_identity('A')] and operation(third)['execution_snapshot_id'] == enabled_plan['snapshot']['id']
    committed = read()['snapshot']
    f.restart_console(crash=True)
    resumed = f.command('restore-current-version', 'resume', session_id=sid)
    wait_for(lambda: f.completed(resumed) and f.session(sid)['status'] == 'idle', 'same version restore', timeout=60)
    checks['console_restart_keeps_last_committed_version_and_id'] = read()['snapshot'] == committed and f.session(sid)['scope']['native_id'] == native
    fourth = f.command('restored-send', 'send', session_id=sid, text='NETCAP_A_TURN_4')
    approval = f.pending(sid)
    f.command('deny-restored', 'respond', session_id=sid, pending_id=approval['id'], option_id='deny')
    f.idle(sid)
    checks['restored_version_still_uses_single_call_permissions'] = operation(fourth)['execution_snapshot_id'] == committed['id'] and f.calls('A') == before_calls + [f.expected_identity('A')]
    metadata = (f.root / 'console/runtimes/claude-a' / (native + '.json')).read_text()
    checks['durable_outcome_contains_no_expanded_mcp_credentials'] = 'fixture-mcp-A' not in metadata and json.loads(metadata)['_configuration']['status'] == 'verified'
    result = {'scope': f.scope + '; two-server asset v1 enabled -> v2 disabled -> v3 enabled', 'checks': checks,
        'expectations_met': all(checks.values()), 'native_id': native, 'sibling_native_id': sibling_native}
    write(f.root / 'result.json', result)
    print(json.dumps(result, ensure_ascii=False), flush=True)
    assert result['expectations_met'], checks


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--netcap', default='.dev/bin/netcap')
    parser.add_argument('--transport', choices=['stdio','http','sse'], default='stdio')
    parser.add_argument('--hold', action='store_true')
    parser.add_argument('--info-file')
    args = parser.parse_args()
    with fixture_for(str(Path(args.netcap).resolve()), args.transport) as f:
        print('Evidence directory:', f.root, flush=True)
        f.register()
        if not args.hold:
            run(f)
            return
        if args.info_file: write(Path(args.info_file), {**f.info, 'root': str(f.root), 'harness_pid': os.getpid()})
        stopped = False
        def finish(*_):
            nonlocal stopped
            stopped = True
        signal.signal(signal.SIGTERM, finish)
        signal.signal(signal.SIGINT, finish)
        print('READY', f.info['url'], flush=True)
        while not stopped:
            assert f.console.poll() is None
            time.sleep(.2)


if __name__ == '__main__': main()
