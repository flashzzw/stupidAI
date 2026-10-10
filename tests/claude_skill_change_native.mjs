import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
const request=JSON.parse(readFileSync(process.argv[2],'utf8')),state=join(request.root,'journal');
mkdirSync(state);
const checks={},snapshots={},sessions=[];
const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw new Error(name);};
const natural={asset_id:'natural',name:'natural-method',native_key:'natural-key',mode:'enabled',source:'userSettings',source_path:request.source,discovery_path:request.entry};
const config=()=>{const id=randomUUID();return {id,source:id,cwd:request.cwd,executable:'/home/zzw/.local/bin/claude',model:'claude-sonnet-4-6',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false,skill_selections:[natural]};};
const a=new Session(state,config(),true),b=new Session(state,config(),true);sessions.push(a,b);
try {
  await a.open('fixture-no-model');await b.open('fixture-no-model');snapshots.before=await a.snapshot();
  check('initial_natural_enabled',snapshots.before.skills.descriptions.some(s=>s.name==='natural-method'));
  const prepared=await a.prepareExecution(),operation=randomUUID();
  await a.applyExecution({operation,baseline:prepared.configuration_baseline,mcp_selections:[],skill_selections:[{...natural,mode:'disabled'}],api_key:'fixture-no-model'});
  snapshots.disabled=await a.snapshot();
  check('same_id_disabled_applied',snapshots.disabled.config.id===snapshots.before.config.id&&!snapshots.disabled.skills.descriptions.some(s=>s.name==='natural-method')&&!snapshots.disabled.skills.commands.some(s=>s.name==='natural-method'));
  const disabled={...natural,mode:'disabled'},originalID=a.config.id,firstConnection=snapshots.disabled.connection;
  const firstRequest={operation,baseline:prepared.configuration_baseline,mcp_selections:[],skill_selections:[disabled],api_key:'fixture-no-model'};
  await a.applyExecution(firstRequest);check('duplicate_operation_keeps_query',(await a.snapshot()).connection===firstConnection);
  let rejected=false;try{await a.applyExecution({...firstRequest,skill_selections:[natural]});}catch{rejected=true;}check('duplicate_operation_different_skill_rejected',rejected);
  const plugin='netcap-skill-'+'a'.repeat(24);
  const external=(version,mode='enabled')=>({asset_id:'external',name:plugin+':external-method',native_key:plugin+':method',mode,source:'plugin',source_path:join(request.root,version,'SKILL.md'),discovery_path:join(state,'skill-resources',plugin,(version==='v1'?'b':'c').repeat(64),'skills/method/SKILL.md'),delivery:'local_plugin',plugin_name:plugin,resource_root:join(request.root,version),resource_kind:'directory',resource_hash:(version==='v1'?'b':'c').repeat(64)});
  async function change(label,target) {
    const before=await a.snapshot(),prep=await a.prepareExecution();
    await a.applyExecution({operation:randomUUID(),baseline:prep.configuration_baseline,mcp_selections:[],skill_selections:target,api_key:'fixture-no-model'});
    const after=await a.snapshot();snapshots[label]=after;
    check(label+'_same_id_history',after.config.id===originalID&&after.history_digest===before.history_digest);
    check(label+'_durable_target',after.configuration.status==='verified'&&JSON.stringify(after.config.skill_selections)===JSON.stringify(target)&&JSON.stringify(after.configuration.skill_target)===JSON.stringify(target));
    check(label+'_inherited_kept',after.skills.descriptions.some(s=>s.name==='inherited-method'));
    return after;
  }
  await change('add_external',[disabled,external('v1')]);
  const off=await change('disable_external',[disabled,external('v1','disabled')]);check('external_namespace_absent_when_disabled',!off.skills.commands.some(s=>s.name.startsWith(plugin+':')));
  await change('enable_external',[disabled,external('v1')]);
  const upgraded=await change('upgrade',[disabled,external('v2')]);check('upgrade_binds_new_resource',upgraded.config.skill_selections[1].resource_root===join(request.root,'v2'));
  const inheritedNatural=await change('inherit_natural',[external('v2')]);check('released_natural_inherits_original',inheritedNatural.skills.descriptions.some(s=>s.name==='natural-method'&&s.source==='userSettings'));
  const inheritedAll=await change('inherit_external',[]);check('released_plugin_absent',!inheritedAll.skills.commands.some(s=>s.name.startsWith(plugin+':')));
  await a.close();const restored=Session.restore(state,originalID);sessions.push(restored);
  const cold=await restored.prepareExecution();
  await restored.applyExecution({operation:randomUUID(),baseline:cold.configuration_baseline,mcp_selections:[],skill_selections:[external('v2')],api_key:'fixture-no-model'});
  snapshots.cold=await restored.snapshot();check('cold_apply_opens_original_then_applies',snapshots.cold.config.id===originalID&&snapshots.cold.configuration.status==='verified'&&snapshots.cold.skills.descriptions.some(s=>s.name===plugin+':external-method'));
  const coldRelease=await restored.prepareExecution();
  await restored.applyExecution({operation:randomUUID(),baseline:coldRelease.configuration_baseline,mcp_selections:[],skill_selections:[],api_key:'fixture-no-model'});
  snapshots.restored=await restored.snapshot();
  check('restored_released_selection',snapshots.restored.config.id===originalID&&!snapshots.restored.skills.commands.some(s=>s.name.startsWith(plugin+':'))&&snapshots.restored.history_digest===snapshots.before.history_digest);
  const bad={...external('v2'),name:plugin+':inherited-method',resource_root:join(request.root,'collision'),source_path:join(request.root,'collision/SKILL.md'),resource_hash:'d'.repeat(64),discovery_path:join(state,'skill-resources',plugin,'d'.repeat(64),'skills/method/SKILL.md')};
  const prep=await restored.prepareExecution(),previous=JSON.stringify(restored.config);rejected=false;
  try{await restored.applyExecution({operation:randomUUID(),baseline:prep.configuration_baseline,mcp_selections:[],skill_selections:[bad],api_key:'fixture-no-model'});}catch{rejected=true;}
  snapshots.failed=await restored.snapshot();check('failed_candidate_retains_original',rejected&&JSON.stringify(restored.config)===previous&&snapshots.failed.configuration.status==='not_sent');
  snapshots.sibling=await b.snapshot();check('sibling_query_unchanged',snapshots.sibling.skills.descriptions.some(s=>s.name==='natural-method')&&snapshots.sibling.config.skill_selections[0].mode==='enabled');
  check('zero_model_inputs',Object.values(snapshots).every(s=>!s.entries.some(e=>e.kind==='input')));
  check('zero_model_results',Object.values(snapshots).every(s=>!s.entries.some(e=>e.kind==='event'&&e.event.type==='result')));

} finally {
  for(const s of sessions)await s.close();
  writeFileSync(join(request.root,'result.json'),JSON.stringify({checks,snapshots,scope:'production Session/SDK/CLI; zero model input; no model-turn history'},null,2));
}
