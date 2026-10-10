// Production Session/SDK with isolated real CLI; no model input or listeners.
import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
const request=JSON.parse(readFileSync(process.argv[2],'utf8'));
const checks={}, snapshots={};
const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw new Error(name);};
const dir=join(request.root,'journal');mkdirSync(dir,{recursive:true});
const selected={asset_id:'shared',name:'displayed-method',native_key:'native-key',source:'userSettings',source_path:request.source,discovery_path:request.entry};
const config=mode=>{const id=randomUUID();return {id,source:id,cwd:request.cwd,executable:'/home/zzw/.local/bin/claude',model:'claude-sonnet-4-6',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false,skill_selections:[{...selected,mode}]};};
let enabled=new Session(dir,config('enabled'),true),disabled=new Session(dir,config('disabled'),true),restored;
const modeMatches=(s,mode)=>s.skills.available && (mode==='enabled'
 ? s.skills.commands.filter(c=>c.name==='displayed-method').length===1 && s.skills.descriptions.filter(d=>d.name==='displayed-method'&&d.source==='userSettings').length===1
 : !s.skills.commands.some(c=>c.name==='displayed-method') && !s.skills.descriptions.some(d=>d.name==='displayed-method'));
try {
  await enabled.open('fixture-no-model');await disabled.open('fixture-no-model');
  snapshots.enabled=await enabled.snapshot();snapshots.disabled=await disabled.snapshot();
  check('enabled_native_selection',modeMatches(snapshots.enabled,'enabled'));
  check('disabled_native_selection',modeMatches(snapshots.disabled,'disabled'));
  const inherited=s=>s.skills.descriptions.filter(d=>d.name!=='displayed-method').map(d=>JSON.stringify(d)).sort();
  check('other_sources_preserved',JSON.stringify(inherited(snapshots.enabled))===JSON.stringify(inherited(snapshots.disabled)));
  check('same_project_distinct_ids',enabled.config.cwd===disabled.config.cwd&&enabled.config.id!==disabled.config.id);
  const original=JSON.stringify(disabled.config),id=disabled.config.id;
  await disabled.close();restored=Session.restore(dir,id);await restored.open('fixture-no-model');
  snapshots.restored=await restored.snapshot();
  check('restored_original_binding',JSON.stringify(restored.config)===original);
  check('restored_disabled_reapplied',modeMatches(snapshots.restored,'disabled'));
  check('sibling_enabled_unchanged',modeMatches(await enabled.snapshot(),'enabled'));
  const collision=join(request.cwd,'.claude/skills/native-key');mkdirSync(collision,{recursive:true});
  writeFileSync(join(collision,'SKILL.md'),'---\nname: other-source\ndescription: collision fixture\n---\n');
  let rejected=false;
  try{await restored.send({operation:randomUUID(),text:'MUST NOT REACH MODEL',api_key:'fixture-no-model'});}catch{rejected=true;}
  check('new_same_key_blocks_input',rejected);
  snapshots.collision=await restored.snapshot();check('collision_inventory_unverified',!snapshots.collision.skills.available);
  check('no_model_inputs',Object.values(snapshots).every(s=>!s.entries.some(e=>e.kind==='input')));
  check('no_model_results',Object.values(snapshots).every(s=>!s.entries.some(e=>e.kind==='event'&&e.event.type==='result')));
  check('no_skill_body_in_metadata',[enabled.config.id,disabled.config.id].every(id=>!readFileSync(join(dir,id+'.json'),'utf8').includes('PRIVATE_BODY')));
} finally {
  await enabled.close();await disabled.close();if(restored)await restored.close();
  writeFileSync(join(request.root,'result.json'),JSON.stringify({checks,snapshots,scope:'production Session/SDK and real CLI; zero model input, no Application/HTTP listener or method Gate'},null,2));
}
