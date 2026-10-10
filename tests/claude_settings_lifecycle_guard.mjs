// Public SDK ConfigChange callback experiment, sharing the production read-only hook channel.
import { query } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { NativeHookChannel } from '../bridges/claude/dist/hook_catalog.js';
import { localSettingsHooks } from '../bridges/claude/dist/settings_guard.js';
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { readFileSync, writeFileSync, existsSync, unlinkSync, renameSync, mkdirSync, realpathSync } from 'node:fs';
import { join, dirname } from 'node:path';

const request = JSON.parse(readFileSync(process.argv[2], 'utf8')), id = randomUUID();
const checks = {}, callbacks = [], events = [], reports = {};
const diagnosticFailureHooks = localSettingsHooks(id, () => { throw Error('fixture persistence failure'); }, () => { reports.diagnostic_failure_recorded = true; });
let child, channel, stopping = false, wake, error = '';
const markers = () => existsSync(join(request.root,'hooks.jsonl'))
  ? readFileSync(join(request.root,'hooks.jsonl'),'utf8').trim().split('\n').filter(Boolean).map(JSON.parse) : [];
async function* input() { while (!stopping) await new Promise(resolve => { wake = resolve; }); }
const run = query({prompt:input(),options:{cwd:request.cwd,pathToClaudeCodeExecutable:'/home/zzw/.local/bin/claude',
  sessionId:id,model:'claude-sonnet-4-6',settingSources:['user','project','local'],includeHookEvents:true,
  env:{...process.env,ANTHROPIC_BASE_URL:'http://127.0.0.1:1',ANTHROPIC_API_KEY:'fixture-no-model'},
  ...(request.source === 'custom' ? {settings:request.path} : request.guard_case === 'managed-only-flag' ? {settings:{allowManagedHooksOnly:true}} : {}),
  hooks:{ConfigChange:[{hooks:[async (value, toolID, options)=>{
    callbacks.push({event:value.hook_event_name,source:value.source,file_path:value.file_path,session_id:value.session_id,decision:request.sdk_hook});
    if (request.guard_case === 'callback-error') throw Error('fixture callback failure');
    if (request.guard_case === 'diagnostic-error') return diagnosticFailureHooks.ConfigChange[0].hooks[0](value, toolID, options);
    return request.sdk_hook === 'block' ? {decision:'block',reason:'Owned version experiment requires explicit restart'} : {};
  }]}]},
  spawnClaudeCodeProcess:options=>{
    child=spawn(options.command,options.args,{cwd:options.cwd,env:options.env,signal:options.signal,stdio:['pipe','pipe','pipe']});
    child.stderr.resume();channel=new NativeHookChannel(child);return channel.process;
  },
}});
const reading=(async()=>{try{for await(const e of run)events.push({type:e.type,subtype:e.subtype,hook_event:e.hook_event,hook_id:e.hook_id,session_id:e.session_id,exit_code:e.exit_code,outcome:e.outcome});}catch(e){if(!stopping)error=e.message;}})();
try {
  await run.initializationResult();
  reports.before_hooks=await channel.read();reports.before_markers=markers();
  if(request.guard_case==='delete')unlinkSync(request.path);
  else if(request.guard_case==='replace'){
    writeFileSync(request.path+'.new',JSON.stringify(request.target,null,2));renameSync(request.path+'.new',request.path);
  }else{
    mkdirSync(dirname(request.path),{recursive:true});
    writeFileSync(request.guard_case==='symlink'?realpathSync(request.path):request.path,JSON.stringify(request.target,null,2));
  }
  await new Promise(resolve=>setTimeout(resolve,request.wait_ms));
  reports.after_hooks=await channel.read();reports.before_close_markers=markers();
}catch(e){error=e.message;}
finally{
  stopping=true;wake?.();run.close();await reading;
  if(child&&child.exitCode===null&&child.signalCode===null)await new Promise((resolve,reject)=>{
    const timer=setTimeout(()=>{child.off('close',done);reject(Error('owned CLI did not close'));},8000);
    function done(){clearTimeout(timer);resolve();}child.once('close',done);
  });
  reports.final_markers=markers();reports.events=events;reports.callbacks=callbacks;
  reports.exit_code=child?.exitCode;reports.signal_code=child?.signalCode;
  const hooksDisabled=request.guard_case==='disabled'||request.guard_case==='managed-only-flag';
  checks.initialized_v1=hooksDisabled ? reports.before_hooks?.policy.all_disabled===true||reports.before_hooks?.policy.managed_only===true : reports.before_markers?.some(h=>h.event==='SessionStart'&&h.version==='v1');
  checks.callback_payload=request.source==='custom'?callbacks.length===0:callbacks.some(c=>c.event==='ConfigChange'&&c.session_id===id&&c.file_path===request.path);
  const ends=reports.final_markers.filter(h=>h.event==='SessionEnd');
  checks.original_hook_retained=hooksDisabled ? ends.length===0&&JSON.stringify(reports.before_hooks?.policy)===JSON.stringify(reports.after_hooks?.policy) : ends.length===1&&ends[0].version==='v1'&&ends[0].env==='v1';
  checks.no_model_result=!events.some(e=>e.type==='result');
  checks.closed=child&&(child.exitCode!==null||child.signalCode!==null);
  checks.no_error=!error;
  const result={checks,reports,error,native_id:id,passed:Object.values(checks).every(Boolean),scope:'public SDK ConfigChange decision callback; no model input, no managed policy, no Application binding/adoption'};
  writeFileSync(join(request.root,'result.json'),JSON.stringify(result,null,2));
  process.stdout.write(JSON.stringify({checks,callbacks,markers:reports.final_markers,error})+'\n');
  if(!result.passed)process.exitCode=1;
}
