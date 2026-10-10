// Owned Session/SDK permission execution. All approvals match scripted inputs.
import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync, writeFileSync, mkdirSync, existsSync, readdirSync, unlinkSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';

const request=JSON.parse(readFileSync(process.argv[2],'utf8'));
const id=randomUUID(),checks={},reports={};
const check=(name,value)=>{checks[name]=Boolean(value);};
const pause=()=>new Promise(resolve=>setTimeout(resolve,20));
const lines=path=>existsSync(path)?readFileSync(path,'utf8').trim().split('\n').filter(Boolean).map(JSON.parse):[];
const blocks=rows=>rows.flatMap(row=>Array.isArray(row.event?.message?.content)?row.event.message.content:[]);
let originalHistory,complete=false,error;
try {
  for(const stage of ['original','composed']) {
    const root=join(request.root,'session-'+stage),startup=join(root,'startup.jsonl');
    mkdirSync(root);process.env.NETCAP_SETTINGS_ACCESS_LOG=startup;
    for(const file of request.reset_files)writeFileSync(file.path,file.content);
    for(const spec of request.cases.filter(row=>row.tool==='Write'))if(existsSync(spec.input.file_path))unlinkSync(spec.input.file_path);
    const config={id,source:id,cwd:request.cwd,settings_file:request[stage],executable:request.executable,
      model:'claude-sonnet-4-6',endpoint:request.endpoint,permission:'read_only',probe:false};
    const session=new Session(root,config,true),report={config,cases:{}};reports[stage]=report;
    try {
      if(stage==='composed')check('original_history_preserved_before_reopen',JSON.stringify((await session.snapshot()).history)===JSON.stringify(originalHistory));
      await session.open('fixture-A');
      let snapshot;
      const startDeadline=Date.now()+7000;
      do {
        snapshot=await session.snapshot();
        const started=snapshot.entries.filter(row=>row.event?.subtype==='hook_started');
        const finished=snapshot.entries.filter(row=>row.event?.subtype==='hook_response');
        if((started.length||!request.ordinary_hooks)&&started.length===finished.length)break;
        await pause();
      } while(Date.now()<startDeadline);
      report.startup=lines(startup);report.hooks=await session.hookCatalog();
      const selected=report.startup.filter(row=>row.source==='installed');
      check(stage+'_selected_startup_count',selected.length===(stage==='original'&&request.ordinary_hooks?1:0));
      check(stage+'_actual_plugin_root',selected.every(row=>row.root===request.plugin_source&&row.root!==request.cache_source));
      check(stage+'_cache_canary_never_runs',!report.startup.some(row=>row.source==='cache-canary'));
      check(stage+'_custom_hook_and_env_preserved',report.startup.filter(row=>row.source==='custom'&&row.value==='managed-access-preserved').length===(request.ordinary_hooks?1:0));
      check(stage+'_full_installed_source_key',report.hooks.inventory.items.filter(row=>row.plugin_name===request.source_key&&!row.disabled).length===(stage==='original'&&request.ordinary_hooks?1:0));
      for(const spec of request.cases) {
        const operation=randomUUID(),approvals=[];
        const prompt=`NETCAP_SETTINGS_ACCESS_${stage}_${spec.name}`;
        await session.send({operation,text:prompt,api_key:'fixture-A'});
        const deadline=Date.now()+20000;let result;
        do {
          snapshot=await session.snapshot();
          for(const pending of snapshot.pending.filter(row=>row.operation===operation&&row.status==='pending')) {
            const matches=pending.tool===spec.tool&&Object.entries(spec.input).every(([key,value])=>JSON.stringify(pending.input[key])===JSON.stringify(value));
            const decision=matches&&spec.expected==='success'?'allow':'deny';
            approvals.push({id:pending.id,connection:pending.connection,tool:pending.tool,input:pending.input,decision,matches_requested_input:matches});
            await session.respond({request_id:pending.id,connection:pending.connection,decision});
          }
          result=snapshot.entries.find(row=>row.operation===operation&&row.event?.type==='result');
          if(result)break;
          if(snapshot.fault)throw Error('session fault: '+stage+'/'+spec.name);
          await pause();
        }while(Date.now()<deadline);
        if(!result)throw Error('missing result: '+stage+'/'+spec.name);
        const rows=snapshot.entries.filter(row=>row.operation===operation);
        const tools=blocks(rows).filter(block=>block.type==='tool_use');
        const returns=blocks(rows).filter(block=>block.type==='tool_result'&&block.tool_use_id===tools[0]?.id);
        const body=returns[0],prefix=stage+'_'+spec.name;
        check(prefix+'_actual_requested_tool',tools.length===1&&tools[0].name===spec.tool&&Object.entries(spec.input).every(([key,value])=>JSON.stringify(tools[0].input[key])===JSON.stringify(value)));
        check(prefix+'_actual_permission_result',returns.length===1&&Boolean(body.is_error)===(spec.expected==='denied'));
        check(prefix+'_approval_count',approvals.length===spec.approvals&&approvals.every(row=>row.matches_requested_input&&row.decision==='allow'));
        check(prefix+'_original_native_identity',result.event.session_id===id&&result.operation===operation);
        if(spec.marker)check(prefix+'_read_content_returned',JSON.stringify(body).includes(spec.marker));
        if(spec.after!==undefined)check(prefix+'_actual_file_content',existsSync(spec.input.file_path)&&readFileSync(spec.input.file_path,'utf8')===spec.after);
        if(spec.absent)check(prefix+'_denied_file_absent',!existsSync(spec.input.file_path));
        report.cases[spec.name]={operation,prompt,tool_use_id:tools[0]?.id,tool:spec.tool,input:spec.input,
          tool_result:body,approvals,result:result.event,file_content:existsSync(spec.input.file_path)?readFileSync(spec.input.file_path,'utf8'):null};
      }
      report.final=await session.snapshot();
      const init=report.final.entries.find(row=>row.event?.type==='system'&&row.event?.subtype==='init')?.event;
      report.native_init=init;
      check(stage+'_native_init_plugin_path',Array.isArray(init?.plugins)&&init.plugins.filter(row=>row.name==='w0208-selected').length===(stage==='original'?1:0)&&init.plugins.filter(row=>row.name==='w0208-selected').every(row=>row.path===request.plugin_source));
      check(stage+'_no_reload_call',!report.final.entries.some(row=>row.kind==='configuration_applied'));
    } finally {
      await session.close();
      check(stage+'_query_closed',lines(join(root,id+'.jsonl')).at(-1)?.kind==='closed');
      if(stage==='original')originalHistory=(await session.snapshot()).history;
    }
  }
  for(const spec of request.cases) {
    const a=reports.original.cases[spec.name],b=reports.composed.cases[spec.name];
    check(spec.name+'_original_derived_parity',Boolean(a.tool_result.is_error)===Boolean(b.tool_result.is_error)&&JSON.stringify(a.approvals.map(row=>[row.tool,row.input,row.decision]))===JSON.stringify(b.approvals.map(row=>[row.tool,row.input,row.decision])));
  }
  const history=JSON.stringify(reports.composed.final.history);
  check('original_history_retained_after_derived_tools',Object.values(reports.original.cases).every(row=>history.includes(row.tool_use_id)&&history.includes(row.prompt)));
  complete=true;
} catch(caught) {error=String(caught);}
finally {
  check('complete_matrix',complete);
  writeFileSync(join(request.root,'session-result.json'),JSON.stringify({id,checks,reports,error,passed:complete&&Object.values(checks).every(Boolean)},null,2));
}
if(!complete||!Object.values(checks).every(Boolean))process.exitCode=1;
