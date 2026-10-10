// Session public operations against a controlled SDK/process boundary.
// Native loading is covered separately; this fixture injects stream changes.
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createRequire } from 'node:module';
import { PassThrough, Writable } from 'node:stream';
import { EventEmitter } from 'node:events';
import { mock, test } from 'node:test';
const require=createRequire(new URL('../bridges/claude/package.json',import.meta.url)), states=new Map();
mock.module('node:child_process',{namedExports:{spawn:()=>{
  const child=new EventEmitter();child.pid=123;child.killed=false;child.exitCode=null;child.signalCode=null;
  child.stdout=new PassThrough();child.stderr=new PassThrough();
  child.stdin=new Writable({write(chunk,encoding,done){const q=JSON.parse(chunk.toString());queueMicrotask(()=>child.stdout.write(JSON.stringify({type:'control_response',response:{request_id:q.request_id,subtype:'success',response:{hooks:[],policy:{disabledByPolicy:false,managedOnly:false,pluginOnly:false,allDisabled:false,policyHookCount:0}}}})+'\n'));done();}});
  child.kill=signal=>{child.killed=true;child.signalCode=signal;child.stdout.end();child.stderr.end();queueMicrotask(()=>{child.emit('exit',null,signal);child.emit('close',null,signal);});return true;};return child;
}}});
mock.module(require.resolve('@anthropic-ai/claude-agent-sdk'),{namedExports:{getSessionMessages:async()=>[],query:({options})=>{
  const s=states.get(options.sessionId);s.options=options;
  const child=options.spawnClaudeCodeProcess({command:'fixture',args:[],cwd:options.cwd,env:process.env});child.stdout.resume();
  let stopped=false,wake;const events=[];
  s.emit=event=>{events.push(event);wake?.();};
  return {
    initializationResult:async()=>({plugins_applied:true,hooks_applied:true}),
    reloadPlugins:async()=>{s.reloads++;return {plugins:s.packages,error_count:s.errors??0};},
    supportedCommands:async()=>s.commands,
    getContextUsage:async()=>({skills:{skillFrontmatter:s.descriptions}}),
    supportedAgents:async()=>s.agents,
    mcpServerStatus:async()=>s.mcp,
    applyFlagSettings:async settings=>{s.settings=settings;},
    async *[Symbol.asyncIterator](){while(!stopped){if(!events.length)await new Promise(r=>wake=r);while(events.length)yield events.shift();}},
    close(){stopped=true;wake?.();child.kill('SIGTERM');}
  };
}}});
const {Session}=await import('../bridges/claude/dist/session.js');
function fixture(t) {
  const root=mkdtempSync(join(tmpdir(),'netcap-hook-execution-')),id=randomUUID();
  const session=new Session(root,{id,source:id,cwd:root,executable:process.execPath,model:'fixture',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false},true);
  const state={packages:[],commands:[],descriptions:[],agents:[],mcp:[],reloads:0};states.set(id,state);
  t.after(async()=>{await session.close();states.delete(id);rmSync(root,{recursive:true,force:true});});
  const emit=async(subtype,extra={})=>{state.emit({type:'system',subtype,hook_id:'fixture-hook',hook_name:'PRIVATE_COMMAND',hook_event:'PreToolUse',uuid:randomUUID(),session_id:id,...extra});await new Promise(r=>setImmediate(r));};
  return {root,id,session,state,emit};
}
test('hook executions are requested and private hook diagnostics are not journaled or returned',async t=>{
  const f=fixture(t);await f.session.open('fixture');assert.equal(f.state.options.includeHookEvents,true);
  await f.emit('hook_started');await f.emit('hook_progress',{stdout:'PRIVATE_OUT',stderr:'PRIVATE_ERR',output:'PRIVATE_OUTPUT'});
  await f.emit('hook_response',{stdout:'PRIVATE_OUT',stderr:'PRIVATE_ERR',output:'PRIVATE_OUTPUT',exit_code:2,outcome:'error'});
  const snapshot=await f.session.snapshot();assert.equal(JSON.stringify(snapshot).includes('PRIVATE_'),false);
  assert.equal(readFileSync(join(f.root,f.id+'.jsonl'),'utf8').includes('PRIVATE_'),false);
  assert.equal(snapshot.entries.at(-1).event.exit_code,2);assert.equal(snapshot.entries.at(-1).event.outcome,'error');
});
test('unfinished hook blocks new input and configuration preparation even without a model turn',async t=>{
  const f=fixture(t);await f.session.open('fixture');await f.emit('hook_started');
  await assert.rejects(f.session.prepareExecution());
  await assert.rejects(f.session.send({operation:randomUUID(),text:'must not send',api_key:'fixture'}));
  await f.emit('hook_response',{exit_code:2,outcome:'error'});
  assert.ok((await f.session.prepareExecution()).configuration_baseline);
});
test('closing and restoring does not turn unfinished hook execution into a new query',async t=>{
  const f=fixture(t);await f.session.open('fixture');await f.emit('hook_started');await f.session.close();
  const restored=Session.restore(f.root,f.id);
  try { await assert.rejects(restored.open('fixture'), /hook.*尚未/);await assert.rejects(restored.prepareExecution()); } finally { await restored.close(); }
});
test('a terminal hook without a start remains unresolved',async t=>{
  const f=fixture(t);await f.session.open('fixture');await f.emit('hook_response',{outcome:'success',exit_code:0});
  await assert.rejects(f.session.prepareExecution());
});
test('a late hook response keeps the original operation after the model result',async t=>{
  const f=fixture(t);await f.session.open('fixture');const operation=randomUUID();
  await f.session.send({operation,text:'fixture input',api_key:'fixture'});await f.emit('hook_started');
  f.state.emit({type:'result',subtype:'success',session_id:f.id,uuid:randomUUID()});await new Promise(r=>setImmediate(r));
  await f.emit('hook_response',{outcome:'success',exit_code:0});
  assert.equal((await f.session.snapshot()).entries.at(-1).operation,operation);
});
