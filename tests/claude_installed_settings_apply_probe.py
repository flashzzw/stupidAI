#!/usr/bin/env python3
"""Real Application installed-plugin changes, in owned native namespaces."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

from execution_config_probe import CLAUDE, environment, verify_versions, write
from runtime.common import Provider, stop
from mcp_binding_probe import ToolProvider

REPO = Path(__file__).resolve().parents[1]


class InstalledProvider(Provider):
    def __init__(self, root, route):
        self.issued = set()
        super().__init__(root, route)

    def anthropic(self, payload, answer, seq):
        markers = re.findall(r'NETCAP_[AB]_TURN_\d+_INSTALL_MCP', json.dumps(payload.get('messages', [])))
        marker = markers[-1] if markers else ''
        if marker and marker not in self.issued:
            self.issued.add(marker)
            tool = 'mcp__plugin_selected_identity__whoami'
            return ToolProvider.anthropic(self, {**payload, 'messages': [], 'tools': [t for t in payload.get('tools', []) if t.get('name') == tool]}, answer, seq)
        return super().anthropic(payload, answer, seq)


def foreign_instruction_sources(payload, root):
    sources = set()

    def visit(value):
        if isinstance(value, str):
            sources.update(re.findall(r'Contents of ([^\n]+?) \((?:project|user) instructions', value))
        elif isinstance(value, dict):
            for child in value.values():
                visit(child)
        elif isinstance(value, list):
            for child in value:
                visit(child)

    visit(payload)
    return sorted(source for source in sources if not Path(source).is_relative_to(root))


def setup(root, providers, settings_source):
    market = root / 'market'
    write(market / '.claude-plugin/marketplace.json', {'name': 'owned', 'owner': {'name': 'Fixture'},
        'plugins': [{'name': name, 'source': './' + name} for name in ['selected', 'sibling']]})
    for name in ['selected', 'sibling']:
        plugin = market / name
        write(plugin / '.claude-plugin/plugin.json', {'name': name, 'version': '1.0.0'})
        write(plugin / 'skills/method/SKILL.md', '---\nname: method\ndescription: Owned method fixture\n---\nInspect only the owned workspace.\n')
        write(plugin / 'commands/action.md', '---\ndescription: Owned command\n---\nOwned command.\n')
        write(plugin / 'agents/check.md', '---\nname: checker\ndescription: Owned checker\n---\nInspect only the fixture.\n')
        write(plugin / 'startup.py', r'''import json,os,sys
from pathlib import Path
event=json.load(sys.stdin)
metadata=Path(os.environ['NETCAP_FIXTURE_METADATA'])/(event['session_id']+'.json')
transaction=json.loads(metadata.read_text()).get('_configuration',{})
with Path(os.environ['NETCAP_FIXTURE_LOG']).open('a') as f:
 f.write(json.dumps({'name':sys.argv[1],'root':os.environ.get('CLAUDE_PLUGIN_ROOT'),'sentinel':os.environ.get('NETCAP_FIXTURE_SENTINEL'),'adopted':os.environ.get('NETCAP_ADOPTED_SETTING'),'native_transaction':transaction.get('status'),'native_operation':transaction.get('operation'),'input':event})+'\n')
fault=Path(os.environ['NETCAP_FIXTURE_FAULT'])
if fault.exists():
 fault.unlink()
 (Path(__file__).parent.parent/'selected/candidate-drift.txt').write_text('owned candidate startup changed a bound resource')
 fault.with_name('candidate-started').write_text('observed')
''')
        write(plugin / 'hooks/hooks.json', {'hooks': {'SessionStart': [{'hooks': [{'type': 'command', 'command': 'python3 "${CLAUDE_PLUGIN_ROOT}/startup.py" ' + name}]}]}})
        write(plugin / 'server.py', r'''import json,os,sys
from pathlib import Path
for line in sys.stdin:
 r=json.loads(line)
 if 'id' not in r:continue
 if r['method']=='initialize':result={'protocolVersion':r['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'owned-identity','version':'1'}}
 elif r['method']=='tools/list':result={'tools':[{'name':'whoami','description':'Return the owned execution identity','inputSchema':{'type':'object','properties':{}}}]}
 elif r['method']=='tools/call':
  identity={'route':os.environ['NETCAP_FIXTURE_ROUTE'],'root':os.environ.get('CLAUDE_PLUGIN_ROOT'),'cwd':os.getcwd()}
  with Path(os.environ['NETCAP_FIXTURE_MCP_LOG']).open('a') as f:f.write(json.dumps(identity)+'\n')
  result={'content':[{'type':'text','text':json.dumps(identity)}]}
 else:result={}
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':result}),flush=True)
''')
        write(plugin / '.mcp.json', {'mcpServers': {'identity': {'command': 'python3', 'args': ['${CLAUDE_PLUGIN_ROOT}/server.py']}}})
    cfg = {'version': 1, 'state_dir': str(root / 'application'), 'projects': [], 'instances': [], 'routes': []}
    plain = root / 'plain-mcp.json'
    write(plain, {'mcpServers': {'plain': {'command': 'python3', 'args': [str(market / 'selected/server.py')]}}})
    cfg['capability_assets'] = [{'id': 'plain', 'name': 'Owned plain MCP', 'kind': 'mcp', 'version': 'v1', 'source': str(plain), 'backends': ['claude']}]
    env = environment(root / 'execution')
    env.update(NODE_OPTIONS='--import=' + str(REPO / 'tests/claude_settings_wire_capture.mjs'),
        NETCAP_SETTINGS_WIRE=str(root / 'native-wire.jsonl'), NETCAP_SETTINGS_WIRE_MODE='session')
    for letter in ['A', 'B']:
        name = letter.lower()
        directory = root / name
        local_env = environment(directory)
        profile = Path(local_env['CLAUDE_CONFIG_DIR'])
        cwd = directory / 'work'
        cwd.mkdir()
        subprocess.run(['git', 'init', '--quiet', str(cwd)], env=local_env, check=True)
        for index, args in enumerate([['plugin', 'marketplace', 'add', str(market)], *[['plugin', 'install', p + '@owned', '--scope', 'user', '--json'] for p in ['selected', 'sibling']]]):
            result = subprocess.run([CLAUDE, *args], cwd=cwd, env=local_env, text=True, capture_output=True, timeout=20)
            write(directory / f'install-{index}.json', {'args': args, 'exit_code': result.returncode, 'stdout': result.stdout, 'stderr': result.stderr})
            assert result.returncode == 0, 'owned plugin installation failed'
        registry = json.loads((profile / 'plugins/installed_plugins.json').read_text())
        for package in registry['plugins'].values():
            cache = Path(package[0]['installPath'])
            write(cache / 'startup.py', (cache / 'startup.py').read_text().replace("'name':sys.argv[1]", "'name':'CACHE_CANARY'"))
        custom = directory / 'custom/original.json'
        write(custom, {'env': {'NETCAP_FIXTURE_ROUTE': letter, 'NETCAP_FIXTURE_SENTINEL': 'custom-' + letter,
            'NETCAP_FIXTURE_LOG': str(directory / 'startup.jsonl'), 'NETCAP_FIXTURE_MCP_LOG': str(directory / 'mcp.jsonl'),
            'NETCAP_FIXTURE_METADATA': str(root / 'application/runtimes' / name),
            'NETCAP_FIXTURE_FAULT': str(directory / 'fail-next-start')},
            'permissions': {'deny': ['Read(/denied/**)', 'Edit(/denied/**)']}})
        key = 'NETCAP_INSTALLED_KEY_' + letter
        env[key] = 'fixture-' + letter
        cfg['projects'].append({'id': name, 'name': 'Owned ' + letter, 'cwd': str(cwd)})
        cfg['instances'].append({'id': name, 'name': 'Owned Claude ' + letter, 'kind': 'claude', 'executable': CLAUDE,
            'profile': str(profile), 'settings_file': str(custom), 'bridge': str(REPO / 'bridges/claude/dist/main.js'),
            'env_allow': ['XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC',
                'DISABLE_AUTOUPDATER', 'DISABLE_TELEMETRY', 'DISABLE_ERROR_REPORTING', 'NODE_OPTIONS', 'NETCAP_SETTINGS_WIRE', 'NETCAP_SETTINGS_WIRE_MODE']})
        cfg['routes'].append({'id': name, 'instance_id': name, 'endpoint': providers[letter].url,
            'model': 'claude-sonnet-4-6', 'credential_ref': 'env:' + key})
        if settings_source == 'user':
            user_file = profile / 'settings.json'
            user_settings = json.loads(user_file.read_text())
            user_settings.update(json.loads(custom.read_text()))
            write(user_file, user_settings)
            del cfg['instances'][-1]['settings_file']
    return cfg, env


def inside(root, binary, mode, settings_source):
    assert os.path.samefile(root / 'policy', '/etc/claude-code')
    verify_versions(root, ['claude'])
    assert subprocess.check_output(['node', '--version'], text=True).strip() == 'v24.13.0'
    assert json.loads((REPO / 'bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/package.json').read_text())['version'] == '0.3.280'
    providers = {letter: InstalledProvider(root, letter) for letter in ['A', 'B']}
    try:
        cfg, env = setup(root, providers, settings_source)
        if mode.startswith('original-recovery'):
            wrapper = root / 'original-start-wrapper.py'
            write(wrapper, '''#!/usr/bin/python3
import json,os,sys
from pathlib import Path
fault=Path(%s)
def option(name):
 for index,arg in enumerate(sys.argv[1:],1):
  if arg.startswith(name+'='):return arg[len(name)+1:]
  if arg==name and index+1<len(sys.argv):return sys.argv[index+1]
 return None
resume=option('--resume')
bare=any(arg=='--bare' or arg.startswith('--bare=') for arg in sys.argv[1:])
if fault.exists() and not bare and option('--input-format')=='stream-json':
 metadata=fault.parent.parent/'application/runtimes/a'/((resume or 'missing-resume')+'.json')
 change=json.loads(metadata.read_text()).get('_configuration',{}) if metadata.is_file() else {}
 with fault.with_name('original-start-attempts.jsonl').open('a') as observed:
  observed.write(json.dumps({'flags':[arg.split('=')[0] for arg in sys.argv if arg.startswith('--')],'resume':resume,'metadata_exists':metadata.is_file(),'status':change.get('status'),'progress':change.get('progress'),'has_recovery':'recovery' in change})+'\\n')
if fault.exists() and resume and not bare and option('--input-format')=='stream-json':
 native_id=resume
 metadata=fault.parent.parent/'application/runtimes/a'/(native_id+'.json')
 change=json.loads(metadata.read_text()).get('_configuration',{})
 if change.get('status')=='applying' and change.get('progress')=={'version':1,'phase':'original'} and 'recovery' not in change:
  fault.unlink()
  fault.with_name('original-start-failed.json').write_text(json.dumps({'owned_wrapper_exit':72,'candidate_started':False,'native_id':native_id,'progress':change['progress'],'pid':os.getpid()}))
  raise SystemExit(72)
os.execv(%s,[%s,*sys.argv[1:]])
''' % (json.dumps(str(root / 'a/fail-original-start')), json.dumps(CLAUDE), json.dumps(CLAUDE)))
            wrapper.chmod(0o700)
            cfg['instances'][0]['executable'] = str(wrapper)
        if mode.startswith('adoption-recovery'):
            write(root / 'a/work/.claude/settings.local.json', {})
        if mode in ['legacy', 'abandoned']:
            bridge = root / (mode + '-bridge')
            script = '''import { createRequire } from 'node:module';
const write = process.stdout.write.bind(process.stdout);
process.stdout.write = function(chunk, ...args) {
  try { const value=JSON.parse(String(chunk)); if(value.result?.sdk==='0.3.280'){delete value.result.settings_file_consumer;delete value.result.installed_settings;chunk=JSON.stringify(value)+'\\n';} } catch {}
  return write(chunk,...args);
};
await import(%s);
''' % json.dumps((REPO / 'bridges/claude/dist/main.js').as_uri())
            if mode == 'abandoned':
                script = '''const emit=process.stdin.emit.bind(process.stdin);let pending='';
process.stdin.emit=function(event,...args){
 if(event!=='data')return emit(event,...args);
 pending+=String(args[0]);const parts=pending.split('\\n');pending=parts.pop();
 for(const line of parts){const request=JSON.parse(line);if(request.method==='apply_execution')request.params.baseline='b'.repeat(64);emit('data',Buffer.from(JSON.stringify(request)+'\\n'));}
 return true;
};
await import(%s);
''' % json.dumps((REPO / 'bridges/claude/dist/main.js').as_uri())
            write(bridge / 'main.mjs', script)
            for name in ['settings_preflight.js', 'settings_validation.js']:
                (bridge / name).symlink_to(REPO / 'bridges/claude/dist' / name)
            cfg['instances'][0]['bridge'] = str(bridge / 'main.mjs')
        request = root / 'request.json'
        write(request, {'root': str(root), 'config': cfg, 'mode': mode})
        env['NETCAP_INSTALLED_SETTINGS_REQUEST'] = str(request)
        command = [str(binary), '-test.run=^TestClaudeNativeInstalledSettingsApplication$', '-test.v', '-test.timeout=240s']
        result = subprocess.run(command, cwd=REPO / 'internal/runtime', env=env, text=True, capture_output=True, timeout=250)
        write(root / 'native.stdout', result.stdout)
        write(root / 'native.stderr', result.stderr)
        print(result.stdout, end='')
        print(result.stderr, end='', file=sys.stderr)
        provider_result = {letter: {'requests': len(p.requests), 'errors': p.errors,
            'authenticated': all(row['auth_matches'] for row in p.requests), 'issued': sorted(p.issued),
            'foreign_instruction_sources': foreign_instruction_sources(p.requests, root)} for letter, p in providers.items()}
        write(root / 'provider-result.json', provider_result)
        isolated = all(not item['foreign_instruction_sources'] for item in provider_result.values())
        if not isolated:
            print('Native fixture loaded instructions outside its owned directory; see provider-result.json', file=sys.stderr)
        return result.returncode or (0 if isolated else 1)
    finally:
        for provider in providers.values():
            provider.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--case', default='happy', choices=['happy', 'lost', 'failure', 'legacy', 'adoption', 'adoption-recovery', 'adoption-recovery-lost', 'recovery', 'recovery-installed', 'recovery-lost', 'original-recovery', 'original-recovery-installed', 'original-recovery-lost', 'cold-apply', 'abandoned', 'source-drift'])
    parser.add_argument('--settings-source', default='custom', choices=['custom', 'user'])
    parser.add_argument('--policy', default='ordinary', choices=['ordinary', 'managed-only'])
    parser.add_argument('--inside', type=Path)
    args = parser.parse_args()
    if args.inside:
        raise SystemExit(inside(args.inside, args.binary, args.case, args.settings_source))
    # Native project instructions walk cwd ancestors, even with a private HOME.
    # Keep the fixture outside repository/home ancestors and the masked /tmp.
    root = Path(tempfile.mkdtemp(prefix='installed-apply-', dir='/var/tmp'))
    print('Evidence directory:', root, flush=True)
    policy = {'allowManagedPermissionRulesOnly': True, 'permissions': {'allow': ['mcp__plugin_selected_identity__whoami']}} if args.policy == 'managed-only' else {}
    write(root / 'policy/managed-settings.json', policy)
    registry = root / 'windows/Windows/System32/reg.exe'
    write(registry, '#!/usr/bin/python3\nimport sys\nsys.stderr.write("ERROR: The system was unable to find the specified registry key or value.\\n")\nraise SystemExit(1)\n')
    registry.chmod(0o700)
    argv = ['bwrap', '--unshare-user', '--unshare-pid', '--unshare-net', '--die-with-parent', '--ro-bind', '/', '/',
        '--tmpfs', '/etc', '--ro-bind', str(root / 'policy'), '/etc/claude-code', '--bind', str(root), str(root),
        '--tmpfs', '/mnt', '--ro-bind', str(root / 'windows'), '/mnt/c', '--tmpfs', '/tmp', '--tmpfs', '/run',
        '--proc', '/proc', '--dev', '/dev', 'python3', str(Path(__file__).resolve()), '--inside', str(root), '--binary', str(args.binary.resolve()), '--case', args.case, '--settings-source', args.settings_source, '--policy', args.policy]
    write(root / 'invocation.json', {'argv': argv, 'registry': 'controlled absent', 'network': 'isolated',
        'namespaces': {name: os.readlink('/proc/self/ns/' + name) for name in ['user', 'pid', 'net', 'mnt']}})
    proc = subprocess.Popen(argv, cwd=REPO, env=environment(root / 'environment'), text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        stdout, stderr = proc.communicate(timeout=290)
        write(root / 'runner.stdout', stdout)
        write(root / 'runner.stderr', stderr)
        print(stdout, end='')
        print(stderr, end='', file=sys.stderr)
    finally:
        stop(proc)
    raise SystemExit(proc.returncode)


if __name__ == '__main__':
    main()
