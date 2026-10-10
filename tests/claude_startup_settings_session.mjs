// Candidate settings files through the existing production Session launch.
// This does not implement an Application source-binding or apply transaction.
import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';

const request=JSON.parse(readFileSync(process.argv[2],'utf8')),id=randomUUID(),checks={},reports={};
const check=(key,ok)=>{checks[key]=Boolean(ok);};
for(const name of ['original','composed']){
  const root=join(request.root,'session-'+name),log=join(root,'startup.jsonl');
  mkdirSync(root);process.env.NETCAP_SETTINGS_LOG=log;
  const config={id,source:id,cwd:request.cwd,settings_file:request[name],executable:'/home/zzw/.local/bin/claude',model:'claude-sonnet-4-6',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false};
  const session=new Session(root,config,true);
  let snapshot;
  try{
    await session.open('fixture-no-model');
    const skills=await session.skillCatalog(),hooks=await session.hookCatalog();
    for(let i=0;i<100;i++){
      snapshot=await session.snapshot();
      const started=snapshot.entries.filter(e=>e.kind==='event'&&e.event.subtype==='hook_started');
      const finished=snapshot.entries.filter(e=>e.kind==='event'&&e.event.subtype==='hook_response');
      if(started.length>0&&started.length===finished.length)break;
      await new Promise(resolve=>setTimeout(resolve,20));
    }
    const started=snapshot.entries.filter(e=>e.kind==='event'&&e.event.subtype==='hook_started').map(e=>e.event);
    const finished=snapshot.entries.filter(e=>e.kind==='event'&&e.event.subtype==='hook_response').map(e=>e.event);
    const startup=existsSync(log)?readFileSync(log,'utf8').trim().split('\n').filter(Boolean).map(line=>JSON.parse(line)):[];
    check(name+'_native_hook_identity',started.length>0&&started.every(e=>e.session_id===id));
    check(name+'_hooks_completed',started.length===finished.length&&started.every(s=>finished.filter(f=>f.hook_id===s.hook_id&&f.exit_code===0&&f.outcome==='success').length===1));
    check(name+'_custom_hook_preserved',startup.some(x=>x.source==='work'&&x.cwd===request.cwd&&x.marker==='custom-preserved'));
    check(name+'_installed_startup_selection',startup.some(x=>x.source==='installed')===(name==='original'));
    check(name+'_installed_skill_selection',skills.inventory.descriptions.some(s=>s.plugin_name==='w0208-selected')===(name==='original'));
    check(name+'_installed_hook_selection',hooks.inventory.items.some(h=>h.plugin_name==='w0208-selected@w0208-local')===(name==='original'));
    check(name+'_no_model_input_result',!snapshot.entries.some(e=>e.kind==='input'||e.event?.type==='result'));
    reports[name]={snapshot,skills,hooks,startup};
  }finally{
    await session.close();
    check(name+'_closed',JSON.parse(readFileSync(join(root,id+'.jsonl'),'utf8').trim().split('\n').at(-1)).kind==='closed');
    writeFileSync(join(request.root,'session-result.json'),JSON.stringify({checks,reports,passed:Object.keys(reports).length===2&&Object.values(checks).every(Boolean),scope:'production Session + SDK + CLI initialization only; fixture-composed settings path, not Application installed-source selection or permission/tool enforcement'},null,2));
  }
}
if(!Object.values(checks).every(Boolean))process.exitCode=1;
