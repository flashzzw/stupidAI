import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync, writeFileSync, mkdirSync, existsSync, symlinkSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
const request=JSON.parse(readFileSync(process.argv[2],'utf8')), root=request.root, state=join(root,'journal');
mkdirSync(state);
const checks={},snapshots={},sessions=[];
const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw new Error(name);};
function create(letter,kind='directory',mode='enabled') {
  const plugin='netcap-skill-'+letter.repeat(24),hash='f'.repeat(64),id=randomUUID();
  const source=join(root,kind==='directory'?'shared':'single','SKILL.md');
  const name=plugin+':'+(kind==='directory'?'external-method':'single-method');
  const config={id,source:id,cwd:request.cwd,executable:'/home/zzw/.local/bin/claude',model:'claude-sonnet-4-6',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false,
    skill_selections:[{asset_id:'asset',name,native_key:plugin+':method',mode,source:'plugin',source_path:source,discovery_path:join(state,'skill-resources',plugin,hash,'skills/method/SKILL.md'),delivery:'local_plugin',plugin_name:plugin,resource_root:kind==='directory'?join(root,'shared'):source,resource_kind:kind,resource_hash:hash}]};
  const session=new Session(state,config,true);sessions.push(session);return session;
}
const a=create('a'),b=create('b','directory','disabled'),c=create('c','file');
try {
  for(const [label,s] of [['a',a],['b',b],['c',c]]) {
    await s.open('fixture-no-model');const snap=await s.snapshot();snapshots[label]=snap;
    const pick=s.config.skill_selections[0],on=pick.mode==='enabled';
    check(label+'_native_inventory',snap.skills.available&&snap.skills.commands.some(x=>x.name===pick.name)===on&&snap.skills.descriptions.some(x=>x.name===pick.name&&x.plugin_name===pick.plugin_name)===on);
    check(label+'_inherited_kept',snap.skills.descriptions.some(x=>x.name==='project-method'));
    check(label+'_package_presence',existsSync(pick.discovery_path)===on);
  }
  const inherited=s=>JSON.stringify({commands:s.skills.commands.filter(c=>!c.name.startsWith('netcap-skill-')).sort((a,b)=>a.name.localeCompare(b.name)),descriptions:s.skills.descriptions.filter(c=>!c.name.startsWith('netcap-skill-')).sort((a,b)=>a.name.localeCompare(b.name))});
  check('all_other_inherited_inventory_preserved',inherited(snapshots.a)===inherited(snapshots.b)&&inherited(snapshots.a)===inherited(snapshots.c));
  const original=JSON.stringify(a.config);await a.close();const restored=Session.restore(state,a.config.id);sessions.push(restored);await restored.open('fixture-no-model');snapshots.restored=await restored.snapshot();
  check('restored_original_id_and_binding',JSON.stringify(restored.config)===original);
  check('restored_enabled_reloaded',snapshots.restored.skills.available&&snapshots.restored.skills.descriptions.some(x=>x.name===a.config.skill_selections[0].name));
  // Test the real alias collision, not just names returned by a stub.
  const collision=join(request.cwd,'.claude/skills/collision');mkdirSync(collision);
  writeFileSync(join(collision,'SKILL.md'),'---\nname: external-method\ndescription: conflicting inherited alias\n---\n');
  const d=create('d');let rejected=false;try{await d.open('fixture-no-model');}catch{rejected=true;}
  snapshots.collision=await d.snapshot();check('native_alias_collision_rejected',rejected);
  symlinkSync(join(root,'shared'),join(request.cwd,'.claude/skills/added'));
  for(const [label,s] of [['enabled',restored],['disabled',b]]) {
    let rejected=false;try{await s.send({operation:randomUUID(),text:'MUST NOT REACH MODEL',api_key:'fixture-no-model'});}catch{rejected=true;}
    check(label+'_new_natural_source_blocks_input',rejected);snapshots[label+'_changed']=await s.snapshot();
  }
  check('zero_model_inputs',Object.values(snapshots).every(s=>!s.entries.some(e=>e.kind==='input')));
  check('zero_model_results',Object.values(snapshots).every(s=>!s.entries.some(e=>e.kind==='event'&&e.event.type==='result')));
  check('no_body_in_metadata',sessions.every(s=>!readFileSync(join(state,s.config.id+'.json'),'utf8').includes('PRIVATE_BODY')));
} finally {
  for(const s of sessions)await s.close();
  writeFileSync(join(root,'result.json'),JSON.stringify({checks,snapshots,scope:'real production Session/SDK/CLI; zero model input; original ID metadata restore without a historical model turn'},null,2));
}
