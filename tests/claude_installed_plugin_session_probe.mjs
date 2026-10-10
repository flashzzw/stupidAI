// Pinned public SDK with the production hook read channel; no model input.
import { query } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { NativeHookChannel } from '../bridges/claude/dist/hook_catalog.js';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';

const config=JSON.parse(readFileSync(process.argv[2],'utf8')),id=randomUUID();
const selected='w0208-selected',sibling='w0208-sibling',key=selected+'@w0208-local';
const checks={},reports={};
const check=(name,ok)=>{checks[name]=Boolean(ok);};
const sdk=JSON.parse(readFileSync(new URL('../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/package.json',import.meta.url),'utf8'));
if(sdk.version!=='0.3.280')throw Error('SDK pin changed');
async function stage(label,settings,late,extraArgs,appendSettings){
  let closing=false,wake,child,channel,modelResults=0,permissionRequests=0,spawnArgs=[];
  const wire=[],events=[],log=join(config.root,label+'-startup.jsonl');
  async function* prompt(){while(!closing)await new Promise(resolve=>{wake=resolve;});}
  const run=query({prompt:prompt(),options:{
    cwd:config.cwd,pathToClaudeCodeExecutable:'/home/zzw/.local/bin/claude',sessionId:id,
    settingSources:['user','project','local'],systemPrompt:{type:'preset',preset:'claude_code'},
    model:'claude-sonnet-4-6',permissionMode:'default',persistSession:true,includeHookEvents:true,
    ...(settings?{settings}:{}),
    ...(extraArgs?{extraArgs}:{}),
    env:{...process.env,ANTHROPIC_API_KEY:'fixture-no-model',ANTHROPIC_BASE_URL:'http://127.0.0.1:1',NETCAP_INSTALLED_STARTUP_LOG:log},
    canUseTool:async()=>{permissionRequests++;return {behavior:'deny',message:'No model input in this fixture'};},
    spawnClaudeCodeProcess:options=>{
      spawnArgs=[...options.args,...(appendSettings?['--settings',JSON.stringify(appendSettings)]:[])];
      child=spawn(options.command,spawnArgs,{cwd:options.cwd,env:options.env,signal:options.signal,stdio:['pipe','pipe','pipe']});
      child.stderr.resume();
      const original=child.stdin.write.bind(child.stdin);
      child.stdin.write=(...args)=>{wire.push(args[0].toString());return original(...args);};
      channel=new NativeHookChannel(child);return channel.process;
    },
  }});
  const reading=(async()=>{for await(const event of run){events.push(event);if(event.type==='result')modelResults++;}})();
  let result;
  try{
    const initial=await run.initializationResult();
    if(late)await run.applyFlagSettings({enabledPlugins:{[key]:false}});
    const loaded=await run.reloadPlugins();
    const commands=await run.supportedCommands(),context=await run.getContextUsage({detail:'summary'}),agents=await run.supportedAgents();
    const hooks=await channel.read();
    result={initial,loaded,commands,descriptions:context.skills?.skillFrontmatter??[],agents,hooks,events};
  }finally{
    closing=true;wake?.();run.close();await reading.catch(()=>{});
    if(child&&child.exitCode===null&&child.signalCode===null)await new Promise(resolve=>child.once('exit',resolve));
  }
  const requests=wire.join('').split('\n').filter(Boolean).map(line=>JSON.parse(line));
  result.requests=requests;
  result.spawn_args=spawnArgs;
  result.startup=existsSync(log)?readFileSync(log,'utf8').trim().split('\n').filter(Boolean).map(line=>JSON.parse(line)):[];
  result.child_stopped=child.exitCode!==null||child.signalCode!==null;
  result.exit_code=child.exitCode;result.signal=child.signalCode;
  result.model_results=modelResults;result.id=id;
  reports[label]=result;
  check(label+'_native_identity',events.some(e=>e.session_id===id)&&events.every(e=>!e.session_id||e.session_id===id));
  check(label+'_native_source_key',result.loaded.plugins.filter(p=>p.name===selected).every(p=>p.source===key&&p.path===join(config.market,selected)&&p.version==='1.0.0'));
  check(label+'_sibling_preserved',result.loaded.plugins.some(p=>p.name===sibling&&p.source===sibling+'@w0208-local'&&p.path===join(config.market,sibling)&&p.version==='1.0.0')&&result.commands.some(c=>c.name===sibling+':action')&&result.descriptions.some(d=>d.pluginName===sibling)&&result.agents.some(a=>a.name===sibling+':'+sibling+'-checker')&&result.hooks.items.some(h=>h.plugin_name===sibling+'@w0208-local'&&!h.disabled));
  const started=events.filter(e=>e.subtype==='hook_started'),finished=events.filter(e=>e.subtype==='hook_response');
  check(label+'_hook_lifecycle_finished',started.length>0&&started.length===finished.length&&started.every(s=>finished.filter(f=>f.hook_id===s.hook_id&&f.exit_code===0&&f.outcome==='success').length===1));
  check(label+'_no_permission_request',permissionRequests===0);
  check(label+'_only_control_requests',requests.length>0&&requests.every(r=>r.type==='control_request'));
  check(label+'_no_model_result',modelResults===0);
  check(label+'_child_closed',result.child_stopped);
  return result;
}
let complete=false;
try{
  const enabled=await stage('enabled',undefined,false);
  check('enabled_startup_hook_ran',enabled.startup.some(x=>x.plugin===selected));
  const disabled=await stage('disabled',{enabledPlugins:{[key]:false}},false);
  check('disabled_package_and_all_components_absent',!disabled.loaded.plugins.some(p=>p.name===selected)&&!disabled.commands.some(c=>c.name.startsWith(selected+':'))&&!disabled.descriptions.some(c=>c.name.startsWith(selected+':'))&&!disabled.agents.some(a=>a.name.startsWith(selected+':'))&&!disabled.hooks.items.some(h=>h.plugin_name===key));
  check('disabled_startup_hook_did_not_run',!disabled.startup.some(x=>x.plugin===selected));
  const reenabled=await stage('reenabled',{enabledPlugins:{[key]:true}},false);
  check('reenabled_same_id_and_hook',reenabled.id===enabled.id&&reenabled.startup.some(x=>x.plugin===selected));
  const late=await stage('custom-late-disable',config.custom,true);
  check('late_disable_clears_inventory',!late.loaded.plugins.some(p=>p.name===selected));
  check('late_disable_does_not_prevent_startup',late.startup.some(x=>x.plugin===selected));
  check('custom_file_effect_observed',late.startup.some(x=>x.plugin===sibling&&x.custom==='custom-source-preserved'));
  const combined=await stage('custom-startup-override',config.custom,false,{settings:JSON.stringify({enabledPlugins:{[key]:false}})});
  check('combined_settings_exclude_before_startup',!combined.startup.some(x=>x.plugin===selected)&&!combined.loaded.plugins.some(p=>p.name===selected));
  check('combined_settings_preserve_custom_file',combined.startup.some(x=>x.plugin===sibling&&x.custom==='custom-source-preserved'));
  const repeated=await stage('custom-repeated-flag',config.custom,false,undefined,{enabledPlugins:{[key]:false}});
  check('repeated_flags_exclude_before_startup',!repeated.startup.some(x=>x.plugin===selected)&&!repeated.loaded.plugins.some(p=>p.name===selected));
  check('repeated_flags_preserve_custom_file',repeated.startup.some(x=>x.plugin===sibling&&x.custom==='custom-source-preserved'));
  complete=true;
}finally{
  check('complete_matrix',complete);
  writeFileSync(join(config.root,'result.json'),JSON.stringify({checks,reports,passed:complete&&Object.values(checks).every(Boolean),sdk_version:sdk.version,scope:'owned installed plugins and real SDK query reopen under one ID; no model-turn history, model input, MCP or method Gate; startup hook writes only fixture marker'},null,2));
}
if(!Object.values(checks).every(Boolean))process.exitCode=1;
