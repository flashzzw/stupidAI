#!/usr/bin/env python3
"""Fixed native SDK HTTP/SSE header expansion with two isolated accounts."""
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import subprocess
import tempfile

from console_fixture import stop
from execution_config_probe import environment, write, verify_versions
from mcp_binding_probe import ToolProvider
from remote_mcp_fixture import RemoteMCP


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-claude-remote-mcp-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    remote = RemoteMCP(root)
    try:
        def run(case):
            transport, profile = case
            path = root / (transport + '-' + profile)
            path.mkdir()
            env = environment(path)
            cwd = path / 'work'
            cwd.mkdir()
            settings = path / 'custom.json'
            write(settings, {'env': {'NETCAP_REMOTE_KEY': 'fixture-mcp-' + profile, 'NETCAP_REMOTE_PROFILE': profile, 'NETCAP_REMOTE_WORKSPACE': str(cwd)}})
            provider = ToolProvider(path, profile)
            proc = None
            try:
                config = {'cwd': str(cwd), 'profile': profile, 'settings': str(settings), 'endpoint': provider.url, 'output': str(path / 'result.json'),
                    'servers': {'identity': {'type': transport, 'url': remote.url + '/' + transport, 'headers': {
                        'Authorization': 'Bearer ${NETCAP_REMOTE_KEY}', 'X-Netcap-Profile': '${NETCAP_REMOTE_PROFILE}', 'X-Netcap-Workspace': '${NETCAP_REMOTE_WORKSPACE}'}}}}
                write(path / 'input.json', config)
                proc = subprocess.Popen(['node', str(Path('tests/claude_remote_mcp_probe.mjs').resolve()), str(path / 'input.json')], env=env,
                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
                _, stderr = proc.communicate(timeout=60)
                (path / 'stderr.log').write_text(stderr)
                assert proc.returncode == 0, 'probe failed; see isolated stderr'
                result = json.loads((path / 'result.json').read_text())
                result['model_auth_matches'] = bool(provider.requests) and all(r['auth_matches'] for r in provider.requests)
                return transport + '-' + profile, result
            finally:
                stop(proc)
                provider.close()
        with ThreadPoolExecutor(max_workers=4) as pool:
            results = dict(pool.map(run, [(t, p) for t in ('http', 'sse') for p in ('A', 'B')]))
        checks = {'all_four_calls': len(remote.calls) == 4,
            'all_remote_auth': all(r['credential_matches'] for r in remote.requests),
            'workspace_bindings': all(call['workspace'] == str(root / (call['transport'] + '-' + call['profile']) / 'work') for call in remote.calls),
            'all_model_auth': all(r['model_auth_matches'] for r in results.values()),
            'single_call_permissions': all(r['permissions'] == ['mcp__identity__whoami'] for r in results.values()),
            'all_native_success': all(r['results'] == [{'subtype': 'success', 'is_error': False}] for r in results.values())}
        result = {'checks': checks, 'expectations_met': all(checks.values()), 'results': results, 'calls': remote.calls, 'requests': remote.requests}
        write(root / 'result.json', result)
        print(json.dumps(result), flush=True)
        assert result['expectations_met']
    finally: remote.close()


if __name__ == '__main__': main()
