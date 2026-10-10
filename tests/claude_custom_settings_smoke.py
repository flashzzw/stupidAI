#!/usr/bin/env python3
"""Native SDK path settings, preserved inheritance/permissions and route conflicts."""
import json
import os
from pathlib import Path
import tempfile
import uuid

from claude_bridge_smoke import Bridge
from claude_interaction_smoke import ClaudeInteractionProvider
from console_fixture import isolated_env, wait_for


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-custom-settings-'))
    print('Evidence directory:', root, flush=True)
    env = isolated_env(root)
    env.update(CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1', DISABLE_AUTOUPDATER='1', DISABLE_TELEMETRY='1', DISABLE_ERROR_REPORTING='1')
    work = root / 'work'; work.mkdir(); (work / '.claude').mkdir()
    (work / 'CLAUDE.md').write_text('NETCAP_CUSTOM_PROJECT_RULE\n')
    (Path(env['CLAUDE_CONFIG_DIR']) / 'CLAUDE.md').write_text('NETCAP_CUSTOM_USER_RULE\n')
    sender = root / 'sender.py'
    sender.write_text('import json,os,sys\nfrom pathlib import Path\nx=json.load(sys.stdin)\nwith Path(' + repr(str(root / 'hooks.jsonl')) + ").open('a') as f:f.write(json.dumps({'source':sys.argv[1],'marker':os.getenv('NETCAP_CUSTOM_MARKER'),'session':x['session_id']})+'\\n')\n")
    hook = lambda source: {'hooks': {'SessionStart': [{'hooks': [{'type': 'command', 'command': f'python3 {sender} {source}'}]}]}}
    (work / '.claude/settings.json').write_text(json.dumps(hook('project')))
    custom = root / 'custom-named.json'
    settings = {**hook('custom'), 'env': {'NETCAP_CUSTOM_MARKER':'CUSTOM_LOADED'}, 'permissions': {'allow':['Bash'], 'deny':['Read(./forbidden.txt)']}}
    custom.write_text(json.dumps(settings)); original = custom.read_bytes()
    providers = [ClaudeInteractionProvider(root, x) for x in ('A','B')]
    bridge = None
    try:
        bridge = Bridge(Path('bridges/claude/dist/main.js').resolve(), root, env)
        sid = str(uuid.uuid4())
        config = dict(native_id=sid, source=sid, cwd=str(work), executable='/home/zzw/.local/bin/claude', model='claude-sonnet-4-6', endpoint=providers[0].url, permission='read_only', api_key='fixture-A', settings_file=str(custom))
        bridge.call('create', config)
        assert not any(p.requests for p in providers), 'initialization made a model call'
        operation = str(uuid.uuid4())
        bridge.call('send', dict(native_id=sid, operation=operation, text='NETCAP_CLAUDE_APPROVE', api_key='fixture-A'))
        pending = wait_for(lambda: next(iter(bridge.call('snapshot', {'native_id':sid})['pending']), None), 'custom allow must still require read_only permission')
        assert not (work / 'approve.txt').exists()
        bridge.call('respond', dict(native_id=sid, request_id=pending['id'], connection=pending['connection'], decision='allow'))
        def finished():
            entries=bridge.call('snapshot', {'native_id':sid})['entries']
            return any(e['kind']=='event' and e['operation']==operation and e['event']['type']=='result' for e in entries)
        wait_for(finished, 'custom settings result')
        assert (work / 'approve.txt').exists()
        assert all(r['auth_matches'] for r in providers[0].requests) and not providers[1].requests
        payload = json.dumps(providers[0].requests[-1]['body'])
        assert 'NETCAP_CUSTOM_USER_RULE' in payload and 'NETCAP_CUSTOM_PROJECT_RULE' in payload
        hooks = [json.loads(x) for x in (root / 'hooks.jsonl').read_text().splitlines()]
        assert {h['source'] for h in hooks} == {'custom','project'}, hooks
        assert all(h['marker']=='CUSTOM_LOADED' and h['session']==sid for h in hooks), hooks
        assert custom.read_bytes()==original
        print('PASS custom path/env/hooks, inherited user/project rules, native permission callback and route credentials', flush=True)
        (root/'result.json').write_text(json.dumps({'native_id':sid,'custom_loaded':True,'inheritance_preserved':True,'read_only_approval':True,'custom_file_unchanged':True}, indent=2))
        # Deliberately conflicting settings use only the other local fixture.
        # Record the real precedence before deciding how production diagnoses it.
        conflict = root/'conflict.json'
        conflict.write_text(json.dumps({'env':{'ANTHROPIC_BASE_URL':providers[1].url,'ANTHROPIC_API_KEY':'fixture-B'}}))
        sid2=str(uuid.uuid4());config.update(native_id=sid2,source=sid2,settings_file=str(conflict))
        counts=[len(p.requests) for p in providers]
        try:
            bridge.call('create',config)
        except AssertionError as error:
            assert 'conflicts with the registered' in str(error), str(error)
        else:
            raise AssertionError('custom env silently overrode explicit route')
        assert [len(p.requests) for p in providers] == counts
        print('PASS conflicting custom route/credential env rejected before model input', flush=True)

    finally:
        if bridge: bridge.close()
        for p in providers: p.close()


if __name__=='__main__': main()
