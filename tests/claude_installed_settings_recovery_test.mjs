// Production Session operations with real files and controlled SDK/process I/O.
import assert from 'node:assert/strict';
import { mock, test } from 'node:test';
import { createRequire } from 'node:module';
import { createHash, randomUUID } from 'node:crypto';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, statSync, lstatSync, realpathSync, rmSync, utimesSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { tmpdir } from 'node:os';
import { EventEmitter } from 'node:events';
import { PassThrough, Writable } from 'node:stream';

const require=createRequire(new URL('../bridges/claude/package.json',import.meta.url)), states=new Map();
const rawPolicy={disabledByPolicy:false,managedOnly:false,pluginOnly:false,allDisabled:false,policyHookCount:0};
mock.module('node:child_process',{namedExports:{spawn:()=>{
  const child=new EventEmitter();child.pid=123;child.killed=false;child.exitCode=null;child.signalCode=null;
  child.stdout=new PassThrough();child.stderr=new PassThrough();
  child.stdin=new Writable({write(chunk,encoding,done){const q=JSON.parse(chunk.toString());queueMicrotask(()=>child.stdout.write(JSON.stringify({type:'control_response',response:{request_id:q.request_id,subtype:'success',response:{hooks:[],policy:rawPolicy}}})+'\n'));done();}});
  child.kill=signal=>{child.killed=true;child.signalCode=signal;child.stdout.end();child.stderr.end();queueMicrotask(()=>{child.emit('exit',null,signal);child.emit('close',null,signal);});return true;};return child;
}}});
mock.module(require.resolve('@anthropic-ai/claude-agent-sdk'),{namedExports:{getSessionMessages:async()=>[],query:({options})=>{
  const state=states.get(options.sessionId);state.starts++;
  const stored=JSON.parse(readFileSync(state.metadata));
  assert.deepEqual(stored._configuration.recovery.installed_settings,state.proposed,'new binding must be durable before query startup');
  if(state.fail)throw Error('OWNED_STARTUP_FAILURE');
  const child=options.spawnClaudeCodeProcess({command:process.execPath,args:[],cwd:options.cwd,env:process.env});child.stdout.resume();
  let stopped=false,wake;
  return {initializationResult:async()=>({hooks_applied:true}),reloadPlugins:async()=>({plugins:[],error_count:0}),
    supportedCommands:async()=>[],getContextUsage:async()=>({skills:{skillFrontmatter:[]}}),supportedAgents:async()=>[],mcpServerStatus:async()=>[],applyFlagSettings:async()=>{},
    async *[Symbol.asyncIterator](){while(!stopped)await new Promise(resolve=>wake=resolve);},
    close(){stopped=true;wake?.();child.kill('SIGTERM');}};
}}});
const {Session}=await import('../bridges/claude/dist/session.js');
const {fingerprint}=await import('../bridges/claude/dist/configuration.js');
const {hookInventory}=await import('../bridges/claude/dist/hook_catalog.js');
const sha=body=>createHash('sha256').update(body).digest('hex');
const key=s=>[s.dev,s.ino,s.mode,s.uid,s.gid,s.nlink,s.size,s.mtimeNs,s.ctimeNs].map(String).join(':');
const file=p=>({path:p,resolved_path:realpathSync(p),kind:'file',mode:statSync(p).mode&0o7777,sha256:sha(readFileSync(p))});
const startup=p=>({path:p,status:'read',sha256:sha(readFileSync(p)),bytes:statSync(p).size,resolved_path:realpathSync(p),identity_sha256:sha(JSON.stringify([realpathSync(p),key(statSync(p,{bigint:true})),key(lstatSync(p,{bigint:true}))]))});
const write=(p,v)=>writeFileSync(p,typeof v==='string'?v:JSON.stringify(v),{mode:0o600});
const rewriteIdentity=(p,v)=>{const before=statSync(p);write(p,v);utimesSync(p,before.atime,new Date(before.mtimeMs+1000));};

function fixture(t,mode){
  const persistent=process.env.NETCAP_INSTALLED_RECOVERY_EVIDENCE;
  const root=mkdtempSync(join(persistent??tmpdir(),'installed-recovery-sdk-')),id=randomUUID(),operation=randomUUID(),profile=join(root,'profile'),plugin=join(root,'plugin');
  mkdirSync(join(profile,'plugins'),{recursive:true,mode:0o700});mkdirSync(join(plugin,'.claude-plugin'),{recursive:true,mode:0o700});
  const oldProfile=process.env.CLAUDE_CONFIG_DIR,oldExec=process.env.NETCAP_CLAUDE_SETTINGS_EXEC;
  process.env.CLAUDE_CONFIG_DIR=profile;process.env.NETCAP_CLAUDE_SETTINGS_EXEC=process.execPath;
  const local=join(root,'settings.local.json'),project=join(root,'settings.json'),user=join(profile,'settings.json'),registry=join(profile,'plugins/installed_plugins.json');
  for(const p of [local,project,user])write(p,'{}');write(registry,{version:2,plugins:{'selected@owned':[]}});
  const manifest=join(plugin,'.claude-plugin/plugin.json');write(manifest,{name:'selected',version:'1'});
  const selection={asset_id:'',name:'selected',native_source:'selected@owned',mode:'disabled',version:'1',source_path:plugin,source_hash:sha('fixture-tree'),root_mode:statSync(plugin).mode&0o777,files:[{path:'.claude-plugin',kind:'directory',mode:statSync(dirname(manifest)).mode&0o777},{path:'.claude-plugin/plugin.json',kind:'file',mode:0o600,sha256:sha(readFileSync(manifest))}],components:[]};
  const version=randomUUID(),settings=join(root,'.netcap-settings-'+version+'.json');write(settings,{enabledPlugins:{'selected@owned':false}});
  const original={version:1,artifact:{id:version,original:{kind:'absent'},file:file(settings)},selections:[selection],source_files:[file(registry)],startup_files:[startup(project),startup(local),startup(user)],startup_source_version:sha('original-observation')};
  const sources=[file(project),file(local)];
  const config={id,source:id,cwd:root,executable:process.execPath,model:'fixture',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false,settings_guard_version:1,settings_sources:sources,settings_artifact:original.artifact,installed_settings:original};
  const baseline=fingerprint({config,history:[]});
  write(local,'{"env":{"OWNED_LOCAL":"candidate"}}');
  const failed={...original,startup_files:[startup(project),startup(local),startup(user)],startup_source_version:sha('candidate-observation')};
  const change={operation,baseline,status:'applying',at:'2026-10-10T00:00:00Z',target:[],previous:[],skill_target:[],skill_previous:[],plugin_target:[],plugin_previous:[],settings_previous:sources,settings_target:[sources[0],file(local)],installed_previous:original,installed_target:failed,original_plugins:{available:true,packages:[],skills:{available:true,commands:[],descriptions:[]},agents:[],hooks:hookInventory({hooks:[],policy:rawPolicy}),mcp:{available:true,servers:[]}}};
  const failure={version:1,operation,baseline,stage:'open_candidate',reason:'stage_failed',cleanup:'confirmed',at:'2026-10-10T00:00:00Z'};
  const metadata=join(root,id+'.json');write(metadata,{...config,_configuration:change});
  write(join(root,id+'.jsonl'),[{kind:'initialized',connection:'old'},{kind:'closed',connection:'old'},{kind:'configuration_failure',failure}].map(r=>JSON.stringify(r)).join('\n')+'\n');
  rewriteIdentity(local,'{}');
  assert.notEqual(startup(local).identity_sha256,original.startup_files[1].identity_sha256,'restoration must change the recorded identity');
  const recoveredID=randomUUID(),recoveredFile=join(root,'.netcap-settings-'+recoveredID+'.json');
  const proposed={...original,artifact:{...original.artifact,id:recoveredID,file:{...original.artifact.file,path:recoveredFile,resolved_path:recoveredFile}},startup_files:[startup(project),startup(local),startup(user)],startup_source_version:sha('restored-observation')};
  const session=Session.restore(root,id),sessions=[session],state={starts:0,metadata,proposed,fail:false};states.set(id,state);
  const read={operation,baseline,installed_recovery:proposed},recover={operation:randomUUID(),original_operation:operation,baseline,installed_recovery:proposed,api_key:'fixture'};
  const publish=()=>write(recoveredFile,readFileSync(settings).toString());
  t.after(async()=>{
    for(const s of sessions)await s.close();
    if(persistent)write(join(root,'result.json'),{mode,starts:state.starts,node:process.version,original,proposed,snapshot:await sessions.at(-1).snapshot()});
    else rmSync(root,{recursive:true,force:true});
    states.delete(id);
    if(oldProfile===undefined)delete process.env.CLAUDE_CONFIG_DIR;else process.env.CLAUDE_CONFIG_DIR=oldProfile;
    if(oldExec===undefined)delete process.env.NETCAP_CLAUDE_SETTINGS_EXEC;else process.env.NETCAP_CLAUDE_SETTINGS_EXEC=oldExec;
  });
  return {root,id,local,project,user,registry,metadata,session,sessions,state,original,proposed,read,recover,publish};
}

test('pre-candidate original-query recovery can also bind a reviewed restored file identity',async t=>{
  const f=fixture(t,'original-query-with-restored-identity');
  const metadata=JSON.parse(readFileSync(f.metadata));
  delete metadata._configuration.original_plugins;
  metadata._configuration.progress={version:1,phase:'original'};
  write(f.metadata,metadata);
  const journal=join(f.root,f.id+'.jsonl');
  const entries=readFileSync(journal,'utf8').trim().split('\n').map(line=>JSON.parse(line));
  for(const entry of entries)if(entry.kind==='configuration_failure')entry.failure.stage='open_original';
  write(journal,entries.map(entry=>JSON.stringify(entry)).join('\n')+'\n');
  const cold=Session.restore(f.root,f.id);f.sessions.push(cold);
  const check=await cold.configurationRecovery(f.read);
  assert.ok(check.checks.every(c=>c.status==='passed'));
  assert.ok(check.original_query_retry);
  assert.deepEqual(check.installed_recovery,f.proposed);
  f.publish();
  await assert.rejects(cold.recoverExecution(f.recover));
  assert.equal(f.state.starts,0,'new identity does not imply the separate original-query mode');
  const receipt=await cold.recoverExecution({...f.recover,original_query_retry:true});
  assert.deepEqual(receipt.installed_settings,f.proposed);
  assert.deepEqual(receipt.original_query_retry,check.original_query_retry);
  const recovered=await cold.snapshot();
  assert.equal(recovered.configuration.original_plugins,undefined);
  assert.deepEqual(recovered.configuration.installed_previous,f.original);
  assert.deepEqual(recovered.config.installed_settings,f.proposed);
  assert.ok(recovered.configuration.recovery.plugins.available);
});

test('restored local identity is read-only until explicit recovery; durable result keeps original history and supports cold reopen',async t=>{
  const f=fixture(t,'success'),before=readFileSync(f.metadata);
  const old=await f.session.configurationRecovery({operation:f.read.operation,baseline:f.read.baseline});
  assert.equal(old.checks.find(c=>c.kind==='original_settings').status,'blocked');
  const check=await f.session.configurationRecovery(f.read);
  assert.ok(check.checks.every(c=>c.status==='passed'));assert.deepEqual(check.installed_recovery,f.proposed);
  assert.ok(before.equals(readFileSync(f.metadata)));assert.equal(f.state.starts,0);
  f.publish();
  const receipt=await f.session.recoverExecution(f.recover);
  assert.equal(receipt.native_id,f.id);assert.deepEqual(receipt.installed_settings,f.proposed);
  const after=await f.session.snapshot();assert.equal(after.configuration.status,'recovered');assert.deepEqual(after.config.installed_settings,f.proposed);assert.deepEqual(after.configuration.installed_previous,f.original);
  assert.equal(after.entries.some(e=>e.kind==='input'),false);
  await f.session.close();
  const cold=Session.restore(f.root,f.id);f.sessions.push(cold);
  assert.deepEqual(await cold.recoverExecution(f.recover),receipt);assert.equal(f.state.starts,1,'durable receipt must not replay recovery');
  await assert.rejects(cold.recoverExecution({...f.recover,installed_recovery:{...f.proposed,startup_source_version:'f'.repeat(64)}}));
  await cold.open('fixture');assert.equal(f.state.starts,2);
  rewriteIdentity(f.local,'{}');await assert.rejects(cold.send({operation:randomUUID(),text:'must not send after another identity change',api_key:'fixture'}));
  assert.equal((await cold.snapshot()).entries.some(e=>e.kind==='input'),false);
});

for(const mode of ['content','unadopted-local','nonlocal','registry','selection','stale','missing-target'])test('installed restoration rejects '+mode,async t=>{
  const f=fixture(t,mode);
  if(mode==='content')write(f.local,'{"unexpected":true}');
  if(mode==='unadopted-local')rewriteIdentity(f.project,'{}');
  if(mode==='nonlocal')rewriteIdentity(f.user,'{}');
  if(mode==='registry')write(f.registry,{version:2,plugins:{'selected@other':[]}});
  if(mode==='selection')f.proposed.selections=[{...f.proposed.selections[0],mode:'enabled'}];
  if(mode==='stale')rewriteIdentity(f.local,'{}');
  if(['content','unadopted-local','nonlocal'].includes(mode))f.proposed.startup_files=[startup(f.project),startup(f.local),startup(f.user)];
  if(mode==='missing-target'){delete f.read.installed_recovery;delete f.recover.installed_recovery;}
  const before=readFileSync(f.metadata),check=await f.session.configurationRecovery(f.read);
  assert.equal(check.checks.find(c=>c.kind==='original_settings').status,'blocked');
  f.publish();await assert.rejects(f.session.recoverExecution(f.recover));
  assert.equal(f.state.starts,0);assert.ok(before.equals(readFileSync(f.metadata)));
});

test('failed startup retains the new recovery target and refuses replay after restart',async t=>{
  const f=fixture(t,'startup-fails');f.publish();f.state.fail=true;
  await assert.rejects(f.session.recoverExecution(f.recover));
  const snapshot=await f.session.snapshot();
  assert.equal(snapshot.configuration.status,'applying');assert.equal(snapshot.configuration.recovery.operation,f.recover.operation);
  assert.deepEqual(snapshot.configuration.recovery.installed_settings,f.proposed);
  assert.equal(snapshot.configuration_failure.operation,f.read.operation);
  assert.equal(snapshot.configuration_recovery_failure.operation,f.recover.operation);assert.equal(snapshot.configuration_recovery_failure.cleanup,'confirmed');
  const cold=Session.restore(f.root,f.id);f.sessions.push(cold);
  await assert.rejects(cold.recoverExecution(f.recover));assert.equal(f.state.starts,1);
});
