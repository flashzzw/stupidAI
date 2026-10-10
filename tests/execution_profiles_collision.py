#!/usr/bin/env python3
"""Real native ID collision in two Claude profiles via production adapters."""
import json
from pathlib import Path
import subprocess

from execution_profiles_smoke import ProfilesFixture


def main():
    f=ProfilesFixture(str(Path('.dev/bin/netcap').resolve()))
    print('Evidence directory:',f.root,flush=True)
    try:
        payload=dict(state_dir=str(f.root/'collision-runtime'),cwd=str(f.cwd),profiles=[f.profiles[x] for x in ('claude-a','claude-b')])
        result=subprocess.run([str(Path('.dev/w0201/collision-fixture').resolve())],input=json.dumps(payload),text=True,env=f.env,capture_output=True,timeout=150)
        (f.root/'collision.stderr').write_text(result.stderr)
        assert result.returncode==0,result.stderr
        evidence=json.loads(result.stdout)
        assert len(set(evidence['state_namespaces']))==2 and all(evidence['checks'].values())
        for letter,p in f.providers.items():
            assert p.requests and all(r['auth_matches'] for r in p.requests)
            other='B' if letter=='A' else 'A'
            for r in p.requests:
                assert 'SAME_NATIVE_'+other not in json.dumps(r['body'])
            assert all(f'NETCAP_{letter}_TURN_{n}' in json.dumps(p.requests[-1]['body']) for n in (1,2,3))
        evidence['checks']['credentials_and_resumed_model_context_isolated']=True
        (f.root/'result.json').write_text(json.dumps(evidence,indent=2))
        print('PASS',len(evidence['checks']),'same-native-ID Claude collision checks',flush=True)
    finally:f.close()


if __name__=='__main__':main()
