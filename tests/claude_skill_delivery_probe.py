#!/usr/bin/env python3
"""Characterize Skill-only local plugin delivery through the fixed native SDK."""
import json
from pathlib import Path
import subprocess
import tempfile
from execution_config_probe import environment, verify_versions, write
from runtime.common import stop


def run(root):
    verify_versions(root,['claude'])
    reports={}
    for mode in ['directory-link','file-link','off-key','off-display','omitted','initialize']:
        case=root/mode;env=environment(case);cwd=case/'work';cwd.mkdir()
        resource=case/'shared'
        write(resource/'SKILL.md','---\nname: display-method\ndescription: W0207_EXTERNAL_DESCRIPTION\n---\nPRIVATE_SKILL_BODY\n[helper](helper.py)\n')
        write(resource/'helper.py','print("fixture helper")\n')
        plugin=case/'wrapper'
        write(plugin/'.claude-plugin/plugin.json',{'name':'netcap-probe','version':'0.0.0-fixture'})
        (plugin/'skills').mkdir()
        if mode=='file-link':
            (plugin/'skills/native-key').mkdir()
            (plugin/'skills/native-key/SKILL.md').symlink_to(resource/'SKILL.md')
        else:
            (plugin/'skills/native-key').symlink_to(resource,target_is_directory=True)
        write(cwd/'.claude/skills/inherited/SKILL.md','---\nname: inherited\ndescription: keep original project skill\n---\n')
        overrides={}
        if mode=='off-key':overrides={'netcap-probe:native-key':'off'}
        if mode=='off-display':overrides={'netcap-probe:display-method':'off'}
        config={'cwd':str(cwd),'plugins':[] if mode=='omitted' else [{'type':'local','path':str(plugin)}],'overrides':overrides,'output':str(case/'result.json')}
        if mode=='initialize':config['delivery']='initialize'
        write(case/'request.json',config)
        proc=subprocess.Popen(['node',str(Path(__file__).with_suffix('.mjs')),str(case/'request.json')],env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
        try:
            stdout,stderr=proc.communicate(timeout=40)
            write(case/'stdout.log',stdout);write(case/'stderr.log',stderr)
        finally:stop(proc)
        result=json.loads((case/'result.json').read_text()) if (case/'result.json').exists() else {}
        commands=[c for c in result.get('commands',[]) if c['name'].startswith('netcap-probe:')]
        descriptions=[d for d in result.get('descriptions',[]) if d['source']=='plugin']
        reports[mode]={'exit_code':proc.returncode,'commands':commands,'descriptions':descriptions,'model_results':result.get('model_results'),
                       'plugins_applied':result.get('initialized',{}).get('plugins_applied'),
                       'inherited_present':any(c['name']=='inherited' for c in result.get('commands',[]))}
        print(mode,json.dumps(reports[mode],ensure_ascii=False),flush=True)
        write(root/'result.json',reports)
    return all(r['exit_code']==0 and r['model_results']==0 and r['inherited_present'] for r in reports.values())


if __name__=='__main__':
    root=Path(tempfile.mkdtemp(prefix='netcap-w0207-skill-delivery-'))
    print('Evidence directory:',root,flush=True)
    raise SystemExit(0 if run(root) else 1)
