// Session public operations against a controlled SDK/process boundary.
// Native loading is covered separately; this fixture injects stream changes.
import assert from 'node:assert/strict';
import { randomUUID, createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, readdirSync, statSync, rmSync, chmodSync, symlinkSync, renameSync } from 'node:fs';
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
  const s=states.get(options.sessionId);s.options=options;s.onQuery?.(options);
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
function fixture(t,mode='enabled'){
  const root=mkdtempSync(join(tmpdir(),'netcap-plugin-sdk-')),source=join(root,'plugin'),profile=join(root,'profile'),id=randomUUID();
  mkdirSync(join(source,'.claude-plugin'),{recursive:true});mkdirSync(profile);mkdirSync(join(root,'journal'));
  writeFileSync(join(source,'.claude-plugin/plugin.json'),'{"name":"fixture","version":"1"}');
  writeFileSync(join(source,'.mcp.json'),'{"mcpServers":{"identity":{"command":"fixture"}}}');
  const files=[];const walk=relative=>{for(const name of readdirSync(join(source,relative))){const p=relative?relative+'/'+name:name,full=join(source,p),st=statSync(full);files.push({path:p,kind:st.isDirectory()?'directory':'file',mode:st.mode&0o777,...(st.isFile()?{sha256:createHash('sha256').update(readFileSync(full)).digest('hex')}:{})});if(st.isDirectory())walk(p);}};walk('');
  const selection={asset_id:'plugin',name:'fixture',version:'1',mode,source_path:source,source_hash:'a'.repeat(64),root_mode:statSync(source).mode&0o777,files,components:[{kind:'mcp',name:'identity',native_name:'plugin:fixture:identity',path:'.mcp.json',count:1}]};
  const previous=process.env.CLAUDE_CONFIG_DIR;process.env.CLAUDE_CONFIG_DIR=profile;
  const session=new Session(join(root,'journal'),{id,source:id,cwd:root,executable:process.execPath,model:'fixture',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false,plugin_selections:[selection]},true);
  const state={packages:mode==='enabled'?[{name:'fixture',version:'1',path:source,source:'fixture@inline'}]:[],commands:[],descriptions:[],agents:[],mcp:mode==='enabled'?[{name:'plugin:fixture:identity',source:'plugin',status:'connected',tools:[]}]:[],reloads:0};states.set(id,state);
  t.after(async()=>{await session.close();states.delete(id);if(previous===undefined)delete process.env.CLAUDE_CONFIG_DIR;else process.env.CLAUDE_CONFIG_DIR=previous;rmSync(root,{recursive:true,force:true});});return {root,source,profile,selection,session,state};
}
test('mixed plugin and explicit MCP retain both ask rules plus read-only rules',async t=>{
  const f=fixture(t);const file=join(f.root,'mcp.json');writeFileSync(file,'{"mcpServers":{"plain":{"command":"fixture"}}}');
  f.session.config.mcp_selections=[{asset_id:'plain',server:'plain',mode:'enabled',source_path:file}];f.state.mcp.push({name:'plain',source:'dynamic',status:'connected',tools:[]});
  await f.session.open('fixture');
  for(const rule of ['Bash','Write','Edit','NotebookEdit','mcp__plain__*','mcp__plugin_fixture_identity__*'])assert.ok(f.state.settings.permissions.ask.includes(rule),rule);
});
test('cold prepare preserves plugin binding without opening a query',async t=>{
  const f=fixture(t);const before=JSON.stringify(f.session.config);assert.ok((await f.session.prepareExecution()).configuration_baseline);assert.equal(f.state.reloads,0);assert.equal(JSON.stringify(f.session.config),before);
});
test('a later native init with a different plugin version invalidates input admission',async t=>{
  const f=fixture(t);await f.session.open('fixture');
  f.state.emit({type:'system',subtype:'init',session_id:f.session.config.id,plugins:[{name:'fixture',path:f.source,version:'2'}]});await new Promise(r=>setImmediate(r));
  await assert.rejects(f.session.send({operation:randomUUID(),text:'must not send',api_key:'fixture'}));
  assert.equal((await f.session.snapshot()).entries.some(e=>e.kind==='input'),false);assert.equal(f.state.reloads,1);
});
for(const change of ['missing-package','wrong-source','extra-agent','missing-mcp','failed-mcp','disabled-loaded','load-errors'])test(`unverified ${change} prevents query admission`,async t=>{
  const f=fixture(t,change==='disabled-loaded'?'disabled':'enabled');
  if(change==='missing-package')f.state.packages=[];
  if(change==='wrong-source')f.state.packages[0].source='fixture@other';
  if(change==='extra-agent')f.state.agents=[{name:'fixture:unexpected'}];
  if(change==='missing-mcp')f.state.mcp=[];
  if(change==='failed-mcp')f.state.mcp[0].status='failed';
  if(change==='disabled-loaded')f.state.mcp=[{name:'plugin:fixture:identity',source:'plugin',status:'connected',tools:[]}];
  if(change==='load-errors')f.state.errors=1;
  await assert.rejects(f.session.open('fixture'));assert.equal((await f.session.snapshot()).entries.some(e=>e.kind==='input'),false);
});
for(const change of ['content','extra-file','mode','symlink','installed-collision'])test(`source ${change} blocks input after verified loading`,async t=>{
  const f=fixture(t);await f.session.open('fixture');
  if(change==='content')writeFileSync(join(f.source,'.mcp.json'),'{}');
  if(change==='extra-file')writeFileSync(join(f.source,'extra.txt'),'new');
  if(change==='mode')chmodSync(join(f.source,'.mcp.json'),0);
  if(change==='symlink')symlinkSync(f.profile,join(f.source,'link'));
  if(change==='installed-collision'){mkdirSync(join(f.profile,'plugins'));writeFileSync(join(f.profile,'plugins/installed_plugins.json'),'{"plugins":{"fixture@market":[]}}');}
  await assert.rejects(f.session.send({operation:randomUUID(),text:'must not send',api_key:'fixture'}));assert.equal((await f.session.snapshot()).entries.some(e=>e.kind==='input'),false);
});
test('a normalized MCP tool namespace collision cannot be accepted as plugin binding',async t=>{
  const f=fixture(t);f.state.mcp.push({name:'plugin_fixture_identity',source:'project',status:'connected',tools:[]});
  await assert.rejects(f.session.open('fixture'));
});
test('plugin change cannot drop an unrelated native agent',async t=>{
  const f=fixture(t);f.state.agents=[{name:'inherited-agent'}];await f.session.open('fixture');
  f.state.onQuery=()=>{f.state.packages=[];f.state.mcp=[];f.state.agents=[];};
  const prepared=await f.session.prepareExecution();
  await assert.rejects(f.session.applyExecution({operation:randomUUID(),baseline:prepared.configuration_baseline,mcp_selections:[],plugin_selections:[{...f.selection,mode:'disabled'}],api_key:'fixture'}));
  const snapshot=await f.session.snapshot();assert.equal(snapshot.config.plugin_selections[0].mode,'enabled');assert.equal(snapshot.configuration.status,'not_sent');
  assert.equal(snapshot.configuration_failure.stage,'check_components');assert.equal(snapshot.configuration_failure.cleanup,'confirmed');
});

test('failed candidate diagnostics survive restore and never copy native errors',async t=>{
 const f=fixture(t);await f.session.open('fixture');
 const prepared=await f.session.prepareExecution(),operation=randomUUID();
 f.state.onQuery=()=>{throw Error('PRIVATE_FAILURE_CREDENTIAL');};
 await assert.rejects(f.session.applyExecution({operation,baseline:prepared.configuration_baseline,mcp_selections:[],plugin_selections:[{...f.selection,mode:'disabled'}],api_key:'fixture'}));
 const snapshot=await f.session.snapshot();assert.equal(snapshot.configuration.status,'not_sent');
 assert.deepEqual(Object.keys(snapshot.configuration_failure).sort(),['at','baseline','cleanup','operation','reason','stage','version']);
 assert.equal(snapshot.configuration_failure.operation,operation);assert.equal(snapshot.configuration_failure.stage,'open_candidate');assert.equal(snapshot.configuration_failure.cleanup,'confirmed');
 const directory=join(f.root,'journal');const restored=Session.restore(directory,f.session.config.id);
 assert.deepEqual((await restored.snapshot()).configuration_failure,snapshot.configuration_failure);
 for(const name of readdirSync(directory))assert.ok(!readFileSync(join(directory,name),'utf8').includes('PRIVATE_FAILURE_CREDENTIAL'));
});

test('unwritable failure journal cannot produce a safely unsent outcome',async t=>{
 const f=fixture(t);await f.session.open('fixture');const prepared=await f.session.prepareExecution();
 const journal=join(f.root,'journal',f.session.config.id+'.jsonl'),backup=journal+'.backup';
 f.state.onQuery=()=>{renameSync(journal,backup);mkdirSync(journal);throw Error('PRIVATE_FAILURE_CREDENTIAL');};
 await assert.rejects(f.session.applyExecution({operation:randomUUID(),baseline:prepared.configuration_baseline,mcp_selections:[],plugin_selections:[{...f.selection,mode:'disabled'}],api_key:'fixture'}));
 rmSync(journal,{recursive:true});renameSync(backup,journal);
 const stored=Session.restore(join(f.root,'journal'),f.session.config.id),snapshot=await stored.snapshot();
 assert.equal(snapshot.configuration.status,'applying');assert.equal(snapshot.configuration_failure,undefined);
 assert.equal(snapshot.entries.some(e=>e.kind==='input'),false);
});
