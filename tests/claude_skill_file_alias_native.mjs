import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync, writeFileSync, mkdirSync, symlinkSync, linkSync, unlinkSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';

const request=JSON.parse(readFileSync(process.argv[2],'utf8'));
const { root, kind, mode }=request, state=join(root,'journal'), source=join(root,'source/SKILL.md');
mkdirSync(state);mkdirSync(join(root,'profile/skills'),{recursive:true});
const id=randomUUID(),plugin='netcap-skill-'+'a'.repeat(24),hash='f'.repeat(64);
if(kind==='natural')symlinkSync(join(root,'source'),join(root,'profile/skills/native-key'));
const selection={asset_id:'shared',name:'alias-method',native_key:'native-key',mode,source:'userSettings',source_path:source,discovery_path:join(root,'profile/skills/native-key/SKILL.md')};
if(kind==='external')Object.assign(selection,{name:plugin+':alias-method',native_key:plugin+':method',source:'plugin',discovery_path:join(state,'skill-resources',plugin,hash,'skills/method/SKILL.md'),delivery:'local_plugin',plugin_name:plugin,resource_root:join(root,'source'),resource_kind:'directory',resource_hash:hash});
const config={id,source:id,cwd:request.cwd,executable:'/home/zzw/.local/bin/claude',model:'claude-sonnet-4-6',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false,skill_selections:[selection]};
const checks={},snapshots={},sessions=[];
const check=(key,value)=>{checks[key]=Boolean(value);};
let session=new Session(state,config,true);sessions.push(session);
const aliasDir=join(request.cwd,'.claude/skills/other-key');mkdirSync(aliasDir,{recursive:true});
const alias=join(aliasDir,'SKILL.md');
try {
  await session.open('fixture-no-model');snapshots.before=await session.snapshot();
  check('original_native_selection_ready',snapshots.before.skills.available&&snapshots.before.skills.descriptions.some(d=>d.name===selection.name)===(mode==='enabled'));
  linkSync(source,alias);snapshots.aliased=await session.snapshot();
  check('same_file_alias_withdraws_inventory',!snapshots.aliased.skills.available);
  let preparationRejected=false;try{await session.prepareExecution();}catch{preparationRejected=true;}
  check('alias_blocks_preparation',preparationRejected);
  let inputRejected=false;try{await session.send({operation:randomUUID(),text:'MUST NOT REACH MODEL THROUGH FILE ALIAS',api_key:'fixture-no-model'});}catch{inputRejected=true;}
  check('alias_blocks_input',inputRejected);snapshots.after=await session.snapshot();
  check('alias_keeps_original_identity',JSON.stringify(session.config)===JSON.stringify(config));
  check('zero_model_inputs',!snapshots.after.entries.some(e=>e.kind==='input'));
  check('zero_model_results',!snapshots.after.entries.some(e=>e.kind==='event'&&e.event.type==='result'));
  // Finish the red run after capturing a wrongly admitted input; it must not
  // be mistaken for the no-input restoration control below.
  if(inputRejected){
    unlinkSync(alias);snapshots.cleared=await session.snapshot();
    check('removing_alias_restores_inventory',snapshots.cleared.skills.available);
    await session.close();linkSync(source,alias);
    const restored=Session.restore(state,id);sessions.push(restored);
    let reopenRejected=false;try{await restored.open('fixture-no-model');}catch{reopenRejected=true;}
    check('alias_blocks_same_id_reopen',reopenRejected&&JSON.stringify(restored.config)===JSON.stringify(config));
    await restored.close();unlinkSync(alias);
    const healthy=Session.restore(state,id);sessions.push(healthy);await healthy.open('fixture-no-model');snapshots.restored=await healthy.snapshot();
    check('same_id_reopens_after_source_repaired',healthy.config.id===id&&snapshots.restored.skills.available);
  }
} finally {
  for(const s of sessions)await s.close();
  const passed=Object.values(checks).every(Boolean)&&Object.keys(checks).length===10;
  writeFileSync(join(root,'result.json'),JSON.stringify({kind,mode,checks,snapshots,passed,scope:'owned real production Session/SDK/CLI; native hard-link alias before input and metadata-only same-ID reopen; no actual method Gate'},null,2));
  if(!passed)process.exitCode=1;
}
