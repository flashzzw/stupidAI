import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
const request=JSON.parse(readFileSync(process.argv[2],'utf8')),state=join(request.root,'journal');mkdirSync(state);
const checks={},reports={},sessions=[];
let complete=false;
const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
const id=randomUUID(),a=new Session(state,{id,source:id,cwd:request.cwd,executable:'/home/zzw/.local/bin/claude',model:'claude-sonnet-4-6',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false,plugin_selections:[request.selection]},true);sessions.push(a);
try {
  await a.open('fixture-no-model');reports.before=await a.snapshot();
  const prep=await a.prepareExecution();
  await a.applyExecution({operation:randomUUID(),baseline:prep.configuration_baseline,mcp_selections:[],skill_selections:[],plugin_selections:[{...request.selection,mode:'disabled'}],api_key:'fixture-no-model'});
  reports.disabled=await a.snapshot();
  check('disabled_same_id',reports.disabled.config.id===id&&reports.disabled.config.plugin_selections[0].mode==='disabled');
  check('disabled_package_and_components_absent',!reports.disabled.plugins.packages.some(p=>p.name===request.selection.name)&&!reports.disabled.plugins.skills.commands.some(c=>c.name.startsWith(request.selection.name+':'))&&!reports.disabled.plugins.hooks.items.some(h=>h.plugin_name.startsWith(request.selection.name+'@')));
  check('inherited_project_preserved',reports.disabled.plugins.skills.commands.some(c=>c.name==='project-method'));
  check('history_and_input_unchanged',reports.disabled.history_digest===reports.before.history_digest&&!reports.disabled.entries.some(e=>e.kind==='input'));
  const change=async(label,plugins)=>{
    const before=await a.snapshot(),prep=await a.prepareExecution(),params={operation:randomUUID(),baseline:prep.configuration_baseline,mcp_selections:[],skill_selections:[],plugin_selections:plugins,api_key:'fixture-no-model'};
    await a.applyExecution(params);const after=await a.snapshot();reports[label]=after;
    check(label+'_same_id_history',after.config.id===id&&after.history_digest===before.history_digest);
    check(label+'_durable_target',after.configuration.status==='verified'&&JSON.stringify(after.configuration.plugin_target)===JSON.stringify(plugins));
    check(label+'_inherited_kept',after.plugins.skills.commands.some(c=>c.name==='project-method'));
    await a.applyExecution(params);check(label+'_duplicate_no_reopen',(await a.snapshot()).connection===after.connection);
  };
  await change('enable',[request.selection]);
  await change('inherit',[]);
  check('inherit_all_components_absent',reports.inherit.plugins.packages.every(p=>p.name!==request.selection.name)&&reports.inherit.plugins.skills.commands.every(c=>!c.name.startsWith(request.selection.name+':'))&&reports.inherit.plugins.agents.every(c=>!c.name.startsWith(request.selection.name+':'))&&reports.inherit.plugins.hooks.items.every(h=>!h.plugin_name.startsWith(request.selection.name+'@')));
  await change('add',[request.selection]);
  await change('upgrade',[request.v2]);
  check('upgrade_exact_version_and_retired_command',reports.upgrade.plugins.packages.some(p=>p.version==='2.0.0'&&p.path===request.v2.source_path)&&!reports.upgrade.plugins.skills.commands.some(c=>c.name===request.selection.name+':action'));
  const current=JSON.stringify(a.config),beforeFailure=await a.prepareExecution();let rejected=false;
  try{await a.applyExecution({operation:randomUUID(),baseline:beforeFailure.configuration_baseline,mcp_selections:[],plugin_selections:[{...request.v2,components:request.v2.components.filter(c=>c.kind!=='agent')}],api_key:'fixture-no-model'});}catch{rejected=true;}
  reports.failed=await a.snapshot();
  check('failed_candidate_retains_original',rejected&&JSON.stringify(a.config)===current&&reports.failed.configuration.status==='not_sent');
  await a.open('fixture-no-model');await change('release-after-failure',[]);
  await a.close();const restored=Session.restore(state,id);sessions.push(restored);await restored.open('fixture-no-model');reports.restored=await restored.snapshot();
  check('restored_release_keeps_id_and_history',restored.config.id===id&&reports.restored.history_digest===reports.before.history_digest&&restored.config.plugin_selections.length===0);
  check('restored_retired_components_absent',!reports.restored.plugins.packages.some(p=>p.name===request.selection.name));
  check('zero_model_inputs_and_results',Object.values(reports).every(s=>!s.entries.some(e=>e.kind==='input'||e.event?.type==='result')));
  complete=true;
} finally {
  for(const session of sessions)await session.close();
  writeFileSync(join(request.root,'result.json'),JSON.stringify({checks,reports,expectations_met:complete&&Object.values(checks).every(Boolean),scope:'production Session and fixed CLI; no model/tool execution or model-turn history'},null,2));
}
