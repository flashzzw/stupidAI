import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
const request=JSON.parse(readFileSync(process.argv[2],'utf8')),state=join(request.root,'journal');mkdirSync(state);
const checks={},reports={},sessions=[];
const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
const create=mode=>{
  const id=randomUUID(),session=new Session(state,{id,source:id,cwd:request.cwd,executable:'/home/zzw/.local/bin/claude',model:'claude-sonnet-4-6',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false,plugin_selections:[{...request.selection,mode}]},true);
  sessions.push(session);return session;
};
try {
  const on=create('enabled'),off=create('disabled');
  for(const [label,session] of [['enabled',on],['disabled',off]]) {
    await session.open('fixture-no-model');const snap=await session.snapshot();reports[label]=snap;
    check(label+'_native_plugin_inventory',snap.plugins?.available===true);
    check(label+'_native_package_presence',snap.plugins.packages.some(p=>p.name==='workbench-plugin')===(label==='enabled'));
    check(label+'_inherited_project_kept',snap.plugins.skills.commands.some(c=>c.name==='project-method'));
  }
  check('native_path_and_version',reports.enabled.plugins.packages.some(p=>p.name==='workbench-plugin'&&p.path===request.selection.source_path&&p.version==='1.0.0'));
  check('native_hook_source',reports.enabled.plugins.hooks.items.some(h=>h.plugin_name==='workbench-plugin@inline'));
  const before=JSON.stringify(on.config);await on.close();const restored=Session.restore(state,on.config.id);sessions.push(restored);await restored.open('fixture-no-model');reports.restored=await restored.snapshot();
  check('restored_id_and_binding',before===JSON.stringify(restored.config));
  check('restored_plugin_loaded',reports.restored.plugins.available&&reports.restored.plugins.packages.some(p=>p.name==='workbench-plugin'));
  writeFileSync(join(request.selection.source_path,'commands/action.md'),'changed source');
  let rejected=false;try{await restored.send({operation:randomUUID(),text:'MUST NOT REACH MODEL',api_key:'fixture-no-model'});}catch{rejected=true;}
  check('source_change_blocks_input',rejected);
  reports.drift=await restored.snapshot();check('drift_inventory_unavailable',!reports.drift.plugins.available);
  check('no_model_inputs_or_results',Object.values(reports).every(s=>!s.entries.some(e=>e.kind==='input'||e.event?.type==='result')));
  check('no_hook_or_skill_body_in_snapshot',!JSON.stringify(reports).includes('PRIVATE_'));
} finally {
  for(const s of sessions)await s.close();
  writeFileSync(join(request.root,'result.json'),JSON.stringify({checks,reports,scope:'production Session and fixed CLI, local plugin components; no model/tool execution; metadata-only original ID restore'},null,2));
}
