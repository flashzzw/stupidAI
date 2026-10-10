#!/usr/bin/env python3
"""Production HTTP/SSE MCP selections and header-auth isolation."""
import argparse
import json
import os
from pathlib import Path
import signal
import time
from urllib.error import HTTPError

from claude_mcp_selection_smoke import MCPFixture, run
from console_fixture import wait_for
from execution_config_probe import write, verify_versions
from remote_mcp_fixture import RemoteMCP


class RemoteFixture(MCPFixture):
    def __init__(self, binary, transport, headers_helper=False):
        super().__init__(binary)
        self.transport = transport
        self.remote = RemoteMCP(self.root)
        self.headers_helper = headers_helper
        self.scope = f'real Claude 2.1.280 / SDK 0.3.280, production HTTP, loopback {transport} MCP; header-auth accounts and requested workspace bindings, same-ID restore'
        definition = {'type': transport, 'url': self.remote.url + '/' + transport, 'headers': {
            'Authorization': 'Bearer ${NETCAP_MCP_KEY}', 'X-Netcap-Profile': '${NETCAP_MCP_PROFILE}', 'X-Netcap-Workspace': '${NETCAP_MCP_WORKSPACE}'}}
        if headers_helper:
            self.scope += '; native dynamic headersHelper with invalid static fallback'
            definition['headers']['Authorization'] = 'Bearer ${NETCAP_MCP_BAD_KEY}'
            definition['headersHelper'] = '/usr/bin/python3 "${NETCAP_HELPER_SCRIPT}"'
            write(self.root / 'shared-mcp/headers-helper.py', '''import json,os,sys
from pathlib import Path
p=os.environ['NETCAP_MCP_PROFILE']
with Path(os.environ['NETCAP_HELPER_LOG']).open('a') as f:f.write(json.dumps({'profile':p,'cwd':os.getcwd()})+'\\n')
token='invalid-fixture-key' if '--invalid' in sys.argv else os.environ['NETCAP_MCP_KEY']
print(json.dumps({'Authorization':'Bearer '+token,'X-Netcap-Profile':p,'X-Netcap-Workspace':os.environ['NETCAP_MCP_WORKSPACE']}))
''')
        write(self.root / 'shared-mcp/.mcp.json', {'mcpServers': {'identity': definition}})
        bad = {**definition, 'headers': {**definition['headers'], 'Authorization': 'Bearer ${NETCAP_MCP_BAD_KEY}'}}
        if headers_helper:bad['headersHelper'] += ' --invalid'
        write(self.root / 'wrong-auth.json', {'mcpServers': {'wrongauth': bad}})
        self.config['capability_assets'].append(dict(id='wrongauth', name='失效认证 MCP', kind='mcp', version='v1', source=str(self.root / 'wrong-auth.json'), backends=['claude']))
        for id, profile in self.profiles.items():
            settings = Path(profile['instance']['settings_file'])
            data = json.loads(settings.read_text())
            data['env'].update(NETCAP_MCP_WORKSPACE=str(self.root / id / 'work'), NETCAP_MCP_BAD_KEY='invalid-fixture-key')
            if headers_helper:data['env'].update(NETCAP_HELPER_SCRIPT=str(self.root/'shared-mcp/headers-helper.py'),NETCAP_HELPER_LOG=str(self.root/'helper-invocations.jsonl'))
            write(settings, data)

    def calls(self, letter):
        with self.remote.lock: return [dict(c) for c in self.remote.calls if c['profile'] == letter]

    def expected_identity(self, letter):
        return {'profile': letter, 'workspace': str(self.root / ('claude-' + letter.lower()) / 'work'), 'credential_matches': True, 'transport': self.transport}

    def additional_checks(self):
        counts = (len(self.remote.calls), sum(len(p.requests) for p in self.providers.values()))
        sid = self.create_selected('bad-auth', asset='wrongauth', success=False)
        try:
            receipt = self.command('send-bad-auth', 'send', session_id=sid, text='AUTH_FAILURE_MUST_NOT_SEND')
            wait_for(lambda: self.completed(receipt), 'bad auth rejected', timeout=45)
            status = next(o['status'] for o in self.snapshot()['operations'] if o['id'] == receipt['operation_id'])
            rejected = status in ('not_sent', 'failed')
        except HTTPError as error: rejected = error.code == 409
        write(self.root / 'remote-requests.json', self.remote.requests)
        checks = {'remote_auth_failure_blocks_input': rejected,
            'remote_auth_failure_no_model_or_tool': counts == (len(self.remote.calls), sum(len(p.requests) for p in self.providers.values())),
            'remote_server_observed_invalid_auth': any(not r['credential_matches'] for r in self.remote.requests),
            'all_completed_remote_calls_authenticated': all(c['credential_matches'] for c in self.remote.calls)}
        if self.headers_helper:
            invocations=[json.loads(line) for line in (self.root/'helper-invocations.jsonl').read_text().splitlines()]
            checks['helper_keeps_profile_and_cwd']=all(row['profile'] in ('A','B') and row['cwd']==str(self.root/('claude-'+row['profile'].lower())/'work') for row in invocations)
            checks['helper_reruns_for_each_profile']=all(sum(row['profile']==p for row in invocations)>=2 for p in ('A','B'))
        return checks

    def close(self):
        super().close()
        if hasattr(self, 'remote'): self.remote.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--netcap', default='.dev/bin/netcap')
    parser.add_argument('--transport', choices=['http', 'sse'], default='http')
    parser.add_argument('--hold', action='store_true')
    parser.add_argument('--info-file')
    parser.add_argument('--headers-helper', action='store_true')
    args = parser.parse_args()
    with RemoteFixture(str(Path(args.netcap).resolve()), args.transport, args.headers_helper) as f:
        print('Evidence directory:', f.root, flush=True)
        verify_versions(f.root, ['claude'])
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
