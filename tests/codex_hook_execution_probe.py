#!/usr/bin/env python3
"""Compare an owned plugin hook with the same command outside Codex.

The app-server is pinned to 0.158.0. The first turn triggers SessionStart and
then targets an intentionally unavailable loopback provider. The probe cancels
that turn; it never counts a model turn, MCP call, or method Gate as successful.
All plugin installation/trust changes use a temporary, isolated profile.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import time

from codex_skill_selection_probe import Stdio
from execution_config_probe import SERVER, environment, verify_versions, write


def run(root, timeout, command, *, client_factory=Stdio):
    env = environment(root)
    env['NETCAP_FIXTURE_KEY'] = 'fixture-no-model'
    cwd = root / 'work'
    cwd.mkdir()
    profile = Path(env['CODEX_HOME'])
    write(profile / 'config.toml', '''model="gpt-6-astra"
model_provider="fixture"
[model_providers.fixture]
name="Intentionally unavailable local hook probe"
base_url="http://127.0.0.1:9/v1"
env_key="NETCAP_FIXTURE_KEY"
wire_api="responses"
requires_openai_auth=false
supports_websockets=false
request_max_retries=0
stream_max_retries=0
[features]
apps=false
plugins=true
codex_hooks=true
plugin_sharing=false
shell_snapshot=false
''')
    source = cwd / 'plugins/fixture'
    write(source / '.codex-plugin/plugin.json', {'name': 'fixture', 'version': '1.0.0'})
    write(source / 'hooks/hooks.json', {'hooks': {'SessionStart': [{'hooks': [
        {'type': 'command', 'command': command, 'timeout': timeout},
    ]}]}})
    market = cwd / '.agents/plugins/marketplace.json'
    write(market, {'name': 'owned', 'plugins': [{'name': 'fixture',
        'source': {'source': 'local', 'path': './plugins/fixture'},
        'policy': {'installation': 'AVAILABLE', 'authentication': 'ON_INSTALL'},
        'category': 'Productivity'}]})
    verify_versions(root, ['server'])
    start = time.monotonic()
    direct = subprocess.run(['bash', '-lc', command], cwd=cwd, env=env,
                            capture_output=True, text=True, timeout=timeout)
    report = {'root': str(root), 'scope': 'native owned plugin SessionStart; '
        'unavailable loopback provider; explicit turn cancellation; no production change',
        'direct': {'exit_code': direct.returncode, 'duration_ms': int((time.monotonic()-start)*1000),
                   'stdout': direct.stdout, 'stderr': direct.stderr},
        'checks': {}, 'cases': {}, 'expectations_met': False, 'command': command,
        'binary_sha256': hashlib.sha256(Path(SERVER).read_bytes()).hexdigest()}
    checks = report['checks']
    checks['direct_command_completes'] = direct.returncode == 0
    client = None
    active = None
    try:
        client = client_factory(root, env, 'native')
        client.call('plugin/install', {'pluginName': 'fixture', 'marketplacePath': str(market)})
        hook = client.call('hooks/list', {'cwds': [str(cwd)]})['data'][0]['hooks'][0]
        report['hook'] = hook
        original = (profile / 'config.toml').read_bytes()
        trust = {'hooks.state': {hook['key']: {'enabled': True, 'trusted_hash': hook['currentHash']}}}
        for label, overrides in [('untrusted', {}), ('trusted', trust),
                                 ('plugin-disabled', {**trust, 'plugins.fixture@owned.enabled': False})]:
            started = client.call('thread/start', {'cwd': str(cwd), 'model': 'gpt-6-astra',
                'modelProvider': 'fixture', 'approvalPolicy': 'on-request', 'sandbox': 'read-only',
                'historyMode': 'legacy', 'config': overrides})
            sid = started['thread']['id']
            # A plain barrier request lets pending notifications be recorded;
            # thread creation alone is not claimed to trigger SessionStart.
            client.call('thread/read', {'threadId': sid, 'includeTurns': False})
            before = [e for e in client.events if e.get('method', '').startswith('hook/')
                      and e.get('params', {}).get('threadId') == sid]
            receipt = client.call('turn/start', {'threadId': sid, 'input': [{'type': 'text',
                'text': 'OWNED HOOK PROBE; provider intentionally unavailable'}]})['turn']
            active = {'threadId': sid, 'turnId': receipt['id']}
            deadline = time.monotonic() + timeout + 5
            while True:
                client.call('thread/read', {'threadId': sid, 'includeTurns': True})
                events = [e for e in client.events if e.get('params', {}).get('threadId') == sid]
                done = any(e.get('method') == 'hook/completed' for e in events)
                provider_attempt = any(e.get('method') == 'error' and
                    e['params'].get('error', {}).get('codexErrorInfo', {}).get('responseStreamDisconnected') is not None
                    for e in events)
                if done or provider_attempt or time.monotonic() >= deadline:
                    break
                time.sleep(.05)
            client.call('turn/interrupt', active)
            end = time.monotonic() + 5
            terminal = False
            terminal_status = None
            while time.monotonic() < end:
                history = client.call('thread/read', {'threadId': sid, 'includeTurns': True})['thread']
                terminal = any(t['id'] == receipt['id'] and t['status'] in ('interrupted', 'failed', 'completed')
                               for t in history['turns'])
                if terminal:
                    terminal_status = next(t['status'] for t in history['turns'] if t['id'] == receipt['id'])
                    break
                time.sleep(.05)
            hooks = [e for e in client.events if e.get('method', '').startswith('hook/')
                     and e.get('params', {}).get('threadId') == sid]
            report['cases'][label] = {'native_id': sid, 'turn_id': receipt['id'],
                'before_first_input': before, 'hook_events': hooks,
                'provider_connection_error_observed': provider_attempt, 'turn_terminal': terminal,
                'turn_status': terminal_status}
            checks[label + '_no_hook_before_first_input'] = not before
            checks[label + '_turn_cancelled'] = terminal_status == 'interrupted'
            if label == 'trusted':
                completions = [e['params']['run'] for e in hooks if e['method'] == 'hook/completed']
                checks['trusted_hook_started_with_cache_source'] = any(e['method'] == 'hook/started'
                    and e['params']['run']['sourcePath'] == hook['sourcePath']
                    and e['params']['run']['source'] == 'plugin' for e in hooks)
                checks['trusted_hook_reports_terminal'] = len(completions) == 1
                checks['trusted_command_completes'] = len(completions) == 1 and completions[0]['status'] == 'completed'
            else:
                checks[label + '_does_not_start_hook_before_provider'] = not hooks and provider_attempt
            if not terminal:
                raise RuntimeError('owned turn did not reach a terminal status after interrupt')
            active = None
        checks['thread_overrides_do_not_write_profile'] = (profile / 'config.toml').read_bytes() == original
        checks['directory_discovery_remains_untrusted'] = client.call('hooks/list', {'cwds': [str(cwd)]})['data'][0]['hooks'][0]['trustStatus'] == 'untrusted'
    finally:
        if client:
            if active:
                try:
                    client.call('turn/interrupt', active)
                except Exception:
                    pass
            client.close()
            checks['owned_server_exited_0'] = client.proc.returncode == 0
        report['expectations_met'] = len(report['cases']) == 3 and all(checks.values())
        write(root / 'result.json', report)
    print(json.dumps({'checks': checks, 'expectations_met': report['expectations_met']}, indent=2))
    return report['expectations_met']


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--timeout', type=int, choices=range(1, 11), default=3)
    parser.add_argument('--command', choices=['true', 'exec-true', 'closed-stdio'], default='true')
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-codex-hook-execution-'))
    print('Evidence directory:', root, flush=True)
    command = {'true': 'true', 'exec-true': 'exec /usr/bin/true',
               'closed-stdio': 'exec /usr/bin/true </dev/null >/dev/null 2>&1'}[args.command]
    raise SystemExit(0 if run(root, args.timeout, command) else 1)


if __name__ == '__main__':
    main()
