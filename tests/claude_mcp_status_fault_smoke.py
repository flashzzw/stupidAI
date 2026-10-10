#!/usr/bin/env python3
"""A real SDK status RPC error blocks production input until fresh recovery."""
import json
from pathlib import Path
import shutil
from urllib.error import HTTPError

from claude_remote_mcp_smoke import RemoteFixture
from console_fixture import wait_for
from execution_config_probe import write


def main():
    f = RemoteFixture(str(Path('.dev/bin/netcap').resolve()), 'http')
    marker = f.root / 'status-fault'
    proxy = f.root / 'claude-proxy.py'
    shutil.copyfile('tests/claude_status_fault_proxy.py', proxy)
    proxy.chmod(0o700)
    f.env['NETCAP_STATUS_FAULT'] = str(marker)
    profile = f.profiles['claude-a']['instance']
    profile['executable'] = str(proxy)
    profile['env_allow'].append('NETCAP_STATUS_FAULT')
    with f:
        print('Evidence directory:', f.root, flush=True)
        f.register()
        sid = f.create_selected('fault')
        original = f.session(sid)['scope']['native_id']
        marker.touch()
        # Only the status RPC is intercepted. The SDK, CLI and remote MCP
        # remain alive, and no model input is sent while evidence is missing.
        wait_for(lambda: marker.with_suffix('.observed').exists() and f.session(sid)['status'] == 'unknown', 'native status error observed', timeout=45)
        observed = f.session(sid)
        checks = {'status_unreadable_is_unknown': not observed['view']['evidence']['complete'] and not observed['view']['execution_clear'],
            'selection_not_falsely_verified': not any(e.get('verified') for e in observed['view']['capability_evidence'])}
        count = len(f.provider.requests)
        try:
            f.command('send-while-unreadable', 'send', session_id=sid, text='MUST_NOT_REACH_MODEL')
            checks['input_blocked'] = False
        except HTTPError as error: checks['input_blocked'] = error.code == 409
        checks['no_provider_or_tool_call'] = len(f.provider.requests) == count and not f.remote.calls
        marker.unlink()
        wait_for(lambda: f.session(sid)['status'] == 'idle' and any(e.get('verified') for e in f.session(sid)['view']['capability_evidence']), 'fresh native inventory restored', timeout=45)
        f.command('send-after-recovery', 'send', session_id=sid, text='NETCAP_A_TURN_1')
        pending = f.pending(sid)
        checks['fresh_permission_after_recovery'] = not f.remote.calls
        f.command('allow-after-recovery', 'respond', session_id=sid, pending_id=pending['id'], option_id='allow')
        f.idle(sid)
        checks['recovery_keeps_original_id'] = f.session(sid)['scope']['native_id'] == original
        checks['recovered_call_uses_original_binding'] = f.calls('A') == [f.expected_identity('A')]
        result = {'checks': checks, 'expectations_met': all(checks.values()), 'native_id': original,
            'fault': 'owned CLI stream proxy rejects mcp_status RPC; actual SDK catches error, native MCP and query remain alive'}
        write(f.root / 'result.json', result)
        print(json.dumps(result), flush=True)
        assert result['expectations_met']


if __name__ == '__main__': main()
