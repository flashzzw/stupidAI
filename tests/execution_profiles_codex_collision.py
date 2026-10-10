#!/usr/bin/env python3
"""Same Codex ID after an owned offline state copy; independent real resumes.

Copies only a synthetic transcript/database between fixture homes, leaving the
B provider config in place. This is a collision test, not a user migration tool.
"""
import json
from pathlib import Path
import shutil
import sqlite3
import subprocess

from execution_profiles_smoke import ProfilesFixture, stop, wait_for


def main():
    with ProfilesFixture(str(Path('.dev/bin/netcap').resolve())) as f:
        print('Evidence directory:',f.root,flush=True)
        profile=f.profiles['codex-a']
        f.command('register-a','register_execution',execution_profile=profile)
        created=f.command('create-a','create',project_id='fixture',route_id=profile['route']['id'],permission='read_only')
        sid=created['session_id'];wait_for(lambda:f.completed(created),'seed created')
        seed=f.command('seed-a','send',session_id=sid,text='COLLISION_BASELINE synthetic transcript')
        wait_for(lambda:f.completed(seed) and f.session(sid)['status']=='idle','seed completed')
        native=f.session(sid)['scope']['native_id']
        stop(f.console)
        for proc in f.natives:stop(proc)
        a=Path(profile['instance']['profile']);b=Path(f.profiles['codex-b']['instance']['profile'])
        assert str(a).startswith(str(f.root)) and str(b).startswith(str(f.root))
        shutil.copytree(a/'sessions',b/'sessions',dirs_exist_ok=True)
        with sqlite3.connect(a/'state_5.sqlite') as source,sqlite3.connect(b/'state_5.sqlite') as dest:
            source.backup(dest)
            rows=dest.execute('select id,rollout_path from threads').fetchall()
            for id,path in rows:dest.execute('update threads set rollout_path=? where id=?',(str(b/Path(path).relative_to(a)),id))
        f.natives=[]
        for p in f.profiles.values():
            if p['instance']['kind']=='codex':Path(p['instance']['socket']).unlink(missing_ok=True)
        f.start_native()
        payload=dict(state_dir=str(f.root/'collision-runtime'),cwd=str(f.cwd),native_id=native,profiles=[f.profiles[x] for x in ('codex-a','codex-b')])
        result=subprocess.run([str(Path('.dev/w0201/collision-fixture').resolve())],input=json.dumps(payload),text=True,env=f.env,capture_output=True,timeout=150)
        (f.root/'collision.stderr').write_text(result.stderr)
        assert result.returncode==0,result.stderr
        evidence=json.loads(result.stdout)
        assert evidence['native_id']==native and all(evidence['checks'].values())
        for letter,p in f.providers.items():
            assert p.requests and all(r['auth_matches'] for r in p.requests)
            other='B' if letter=='A' else 'A'
            for r in p.requests:assert 'SAME_NATIVE_'+other not in json.dumps(r['body'])
            assert all(f'NETCAP_{letter}_TURN_{n}' in json.dumps(p.requests[-1]['body']) for n in (1,2,3))
        evidence['checks']['credentials_and_resumed_model_context_isolated']=True
        evidence['setup']='Offline copy of owned synthetic transcript/state DB only; independent A/B provider configs; production Runtime resumes.'
        (f.root/'result.json').write_text(json.dumps(evidence,indent=2))
        print('PASS',len(evidence['checks']),'same-native-ID Codex collision checks',flush=True)


if __name__=='__main__':main()
