#!/usr/bin/env python3
"""Probe per-thread MCP definitions and environment binding in an owned namespace."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

from execution_config_probe import CODEX, SERVER, environment, verify_versions, write
from go_cli_smoke import Proxy
from mcp_binding_probe import shared_definition
from native_mcp_selection_smoke import SelectionProvider, decoded_outputs
from runtime.common import stop, wait_for


def inside(root):
    assert os.path.samefile(root / 'policy', '/etc/codex')
    verify_versions(root, ['codex', 'server'])
    env = environment(root / 'execution')
    env.update(NETCAP_FIXTURE_KEY='fixture-A', NETCAP_MCP_KEY='fixture-mcp-A', NETCAP_SOURCE_TOKEN='fixture-mcp-A')
    cwd = root / 'work'
    cwd.mkdir()
    subprocess.run(['git', 'init', '--quiet', str(cwd)], env=env, check=True)
    definition = shared_definition(root)
    provider = SelectionProvider(root, 'A')
    profile = Path(env['CODEX_HOME'])
    write(profile / 'config.toml', f'''model = "gpt-6-astra"
model_provider = "fixture"
approval_policy = "on-request"
sandbox_mode = "read-only"
[model_providers.fixture]
name = "Fixture"
base_url = "{provider.url}/v1"
env_key = "NETCAP_FIXTURE_KEY"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = false
request_max_retries = 0
stream_max_retries = 0
[features]
apps = false
plugins = false
shell_snapshot = false
''')
    original_config = (profile / 'config.toml').read_bytes()
    socket = root / 'native.sock'
    checks, reports, finished = {}, {}, False
    native = client = None

    def check(name, value):
        checks[name] = bool(value)
        assert value, name

    try:
        with (root / 'native.log').open('w') as log:
            native = subprocess.Popen([SERVER, 'app-server', '--listen', 'unix://' + str(socket)],
                env=env, cwd=cwd, stdout=log, stderr=log, start_new_session=True)
        wait_for(socket.exists, 'owned native MCP definition socket')
        client = Proxy(CODEX, socket, env)
        initial = client.call('config/read', {'cwd': str(cwd), 'includeLayers': True})
        write(root / 'original-config.json', initial)
        check('profile_has_no_identity_server', 'identity' not in initial['config'].get('mcp_servers', {}))
        for mode in ['literal', 'interpolation', 'forwarded']:
            calls = root / (mode + '-calls.jsonl')
            values = {'NETCAP_MCP_PROFILE': 'A', 'NETCAP_MCP_LOG': str(calls)}
            server = {**definition, 'cwd': str(cwd), 'env': values, 'enabled': True}
            if mode == 'literal':
                values['NETCAP_MCP_KEY'] = 'fixture-mcp-A'
            elif mode == 'interpolation':
                values['NETCAP_MCP_KEY'] = '${NETCAP_SOURCE_TOKEN}'
            else:
                server['env_vars'] = ['NETCAP_MCP_KEY']
            overrides = {'mcp_servers.identity.' + key: value for key, value in server.items()}
            started = client.call('thread/start', {'cwd': str(cwd), 'model': 'gpt-6-astra', 'modelProvider': 'fixture',
                'historyMode': 'legacy', 'sandbox': 'read-only', 'approvalPolicy': 'on-request', 'config': overrides})
            sid = started['thread']['id']
            report = {'native_id': sid, 'overrides': overrides, 'approvals': []}
            reports[mode] = report
            status = client.call('mcpServerStatus/list', {'threadId': sid})
            report['inventory'] = status
            check(mode + '_new_definition_starts_without_profile_edit', any(row['name'] == 'identity' and row['runtimeStatus'] == 'connected' and row.get('pluginId') is None for row in status['data']))
            receipt = client.call('turn/start', {'threadId': sid, 'input': [{'type': 'text', 'text': 'NETCAP_A_TURN_1'}]})
            replied = set()

            def completed():
                for event in client.events:
                    if event.get('method') != 'mcpServer/elicitation/request' or event.get('params', {}).get('threadId') != sid or event['id'] in replied:
                        continue
                    check(mode + '_approval_precedes_actual_tool', not calls.exists())
                    check(mode + '_native_approval_has_expected_origin', event['params']['serverName'] == 'identity' and event['params'].get('_meta', {}).get('codex_approval_kind') == 'mcp_tool_call')
                    report['approvals'].append(event)
                    client.send({'id': event['id'], 'result': {'action': 'accept', 'content': {}}})
                    replied.add(event['id'])
                try:
                    history = client.call('thread/read', {'threadId': sid, 'includeTurns': True})['thread']
                except RuntimeError as error:
                    if 'failed to read session metadata' in str(error) and 'rollout' in str(error) and 'is empty' in str(error):
                        return None
                    raise
                report['history'] = history
                turn = next((row for row in history['turns'] if row['id'] == receipt['turn']['id']), None)
                return history if turn and turn['status'] == 'completed' else None

            wait_for(completed, mode + ' tool result', timeout=35)
            actual = [json.loads(line) for line in calls.read_text().splitlines()]
            report['calls'] = actual
            check(mode + '_one_approved_call', len(actual) == 1 and len(report['approvals']) == 1)
            check(mode + '_execution_directory_is_bound', actual[0]['cwd'] == str(cwd) and actual[0]['profile'] == 'A')
            if mode != 'interpolation':
                check(mode + '_credential_is_from_expected_value', actual[0]['credential_matches'])
            returned = []
            requests = [row for row in provider.requests if row['body'].get('client_metadata', {}).get('thread_id') == sid]
            for output in decoded_outputs(requests):
                for block in output.get('content', []):
                    if block.get('type') == 'text':
                        try: returned.append(json.loads(block['text']))
                        except (ValueError, TypeError): pass
            check(mode + '_exact_tool_result_returns_to_model', actual[0] in returned)
            client.call('thread/unsubscribe', {'threadId': sid})
        check('native_does_not_expand_shell_style_environment_references', reports['interpolation']['calls'][0]['credential_matches'] is False)
        check('profile_configuration_bytes_remain_unchanged', (profile / 'config.toml').read_bytes() == original_config)
        check('six_local_model_requests_are_authenticated', len(provider.requests) == 6 and all(row['auth_matches'] for row in provider.requests) and not provider.errors)
        report_files = list(profile.rglob('*.jsonl'))
        check('native_rollouts_do_not_store_literal_mcp_token', all('fixture-mcp-A' not in path.read_text() for path in report_files))
        finished = True
    finally:
        if client:
            write(root / 'native-events.json', client.events)
            client.close()
        stop(native)
        provider.close()
        write(root / 'result.json', {'checks': checks, 'reports': reports, 'passed': finished and bool(checks) and all(checks.values()),
            'scope': 'fixed Codex per-thread config with local stdio MCP and scripted model responses; owned policy and process namespace'})
    print(json.dumps({'checks': len(checks), 'passed': all(checks.values())}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside', type=Path)
    args = parser.parse_args()
    if args.inside:
        return inside(args.inside)
    root = Path(tempfile.mkdtemp(prefix='netcap-codex-mcp-definition-', dir='/var/tmp'))
    (root / 'policy').mkdir()
    print('Evidence directory:', root, flush=True)
    command = ['bwrap', '--unshare-user', '--unshare-pid', '--unshare-net', '--die-with-parent', '--ro-bind', '/', '/',
        '--tmpfs', '/etc', '--ro-bind', str(root / 'policy'), '/etc/codex', '--bind', str(root), str(root),
        '--tmpfs', '/mnt', '--tmpfs', '/tmp', '--tmpfs', '/run', '--proc', '/proc', '--dev', '/dev',
        sys.executable, str(Path(__file__).resolve()), '--inside', str(root)]
    process = subprocess.Popen(command, env=environment(root / 'environment'), text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        out, err = process.communicate(timeout=140)
        write(root / 'runner.stdout', out); write(root / 'runner.stderr', err)
        print(out, end=''); print(err, end='', file=sys.stderr)
    finally:
        stop(process)
    raise SystemExit(process.returncode)


if __name__ == '__main__':
    main()
