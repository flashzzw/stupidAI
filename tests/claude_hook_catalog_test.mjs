// Production Session at the public SDK spawn/stream boundary. Process and SDK
// are controlled in-memory fixtures; native CLI coverage is a separate test.
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createRequire } from 'node:module';
import { createInterface } from 'node:readline';
import { PassThrough, Writable } from 'node:stream';
import { EventEmitter } from 'node:events';
import { mock, test } from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';

const require = createRequire(new URL('../bridges/claude/package.json', import.meta.url));
const states = new Map();

mock.module('node:child_process', { namedExports: { spawn: (_command, args, options) => {
  const mode=args[0], child=new EventEmitter(); let count=0;
  child.pid=123; child.killed=false; child.exitCode=null; child.signalCode=null;
  child.stdout=new PassThrough(); child.stderr=new PassThrough();
  const send=(message,fragment=false)=>{
    const bytes=Buffer.from(JSON.stringify(message)+'\n');
    if(fragment){for(let n=0;n<80;n++)child.stdout.write(bytes.subarray(n,n+1));child.stdout.write(bytes.subarray(80));}
    else child.stdout.write(bytes);
  };
  child.stdin=new Writable({write(chunk,_encoding,done){
    const request=JSON.parse(chunk.toString()), id=request.request_id;
    queueMicrotask(()=>{
      if(request.request.subtype==='list_permission_rules'){
        if(mode==='permission-close')return;
        const state={originalCwd:options.cwd,managedOnly:true,rules:[
          {behavior:'deny',source:'userSettings',rule:'Read(./private/**)',editability:'persistent'},
          {behavior:'deny',source:'userSettings',rule:'Read(./private/**)',editability:'readonly',notInEffect:true},
          {behavior:'ask',source:'futureSource',rule:'Bash(echo 中文🦊\n\u202E)',editability:'readonly',description:{prefix:'PRIVATE_DESCRIPTION'}},
        ],workspaceDirectories:[{path:options.cwd,source:'session'}],errors:[{file:options.cwd+'/settings.json',path:'permissions.allow',message:'PRIVATE_PARSE_ERROR'}]};
        if(mode==='permission-invalid')delete state.managedOnly;
        if(mode==='permission-wrong-cwd')state.originalCwd='/foreign';
        send({type:'fixture_notification',text:'permission frame before'});
        send({type:'control_response',response:{request_id:id,subtype:mode==='permission-error'?'error':'success',response:{state},error:'PRIVATE_NATIVE_PERMISSION_ERROR'}},true);
        send({type:'fixture_notification',text:'permission frame after'});
        return;
      }
      if(request.request.subtype!=='get_hooks_listing'){
        send({type:'control_response',response:{request_id:id,subtype:'success',response:{hooks_applied:mode==='missing-hook-ack'?undefined:mode!=='rejected-hook-ack',marker:'中文🦊'+'.'.repeat(180000)}}},true); return;
      }
      count++;
      const result={hooks:[{event:'PreToolUse',matcher:'Read',source:'pluginHook',pluginName:'fixture@inline',type:'command',commandText:'echo PRIVATE_COMMAND',editable:{config:{token:'PRIVATE_EDIT'}}}],policy:{disabledByPolicy:false,managedOnly:false,pluginOnly:false,allDisabled:false,policyHookCount:0}};
      if(mode==='invalid-policy')delete result.policy.allDisabled;
      if(mode==='invalid-row')result.hooks.push({event:'broken'});
      if(mode==='modes'){result.safeMode={managedHooksStillApply:true,exitHint:'PRIVATE_HINT'};result.bareMode={exitHint:'PRIVATE_HINT'};result.errors=[{message:'PRIVATE_ERROR'}];result.policy.policyUnreadable=true;}
      if(mode==='close')return;
      const message={type:'control_response',response:{request_id:id,subtype:mode==='error'?'error':'success',response:result,error:'PRIVATE_NATIVE_ERROR'}};
      if(mode==='late'&&count===1){setTimeout(()=>send(message,true),5100);return;}
      send({type:'fixture_notification',text:'中文🦊',id:'not-owned'});send(message,true);send({type:'fixture_notification',text:'after',id:'not-owned'});
    });
    done();
  }});
  child.kill=signal=>{child.killed=true;child.signalCode=signal;child.stdout.end();child.stderr.end();queueMicrotask(()=>{child.emit('exit',null,signal);child.emit('close',null,signal);});return true;};
  return child;
} } });
mock.module(require.resolve('@anthropic-ai/claude-agent-sdk'), { namedExports: {
  getSessionMessages: async () => [],
  query: ({ options }) => {
    const state = states.get(options.sessionId);
    const child = options.spawnClaudeCodeProcess({ command: process.execPath, args: [state.mode], cwd: options.cwd, env: process.env });
    const replies = new Map();
    createInterface({ input: child.stdout }).on('line', line => {
      const message = JSON.parse(line); state.seen.push(message);
      const id = message.response?.request_id;
      if (replies.has(id)) { replies.get(id)(message.response.response); replies.delete(id); }
    });
    let release;
    state.roundtrip = () => new Promise(resolve => {
      const id = 'sdk-' + randomUUID(); replies.set(id, resolve);
      child.stdin.write(JSON.stringify({ type: 'control_request', request_id: id, request: { subtype: 'initialize' } }) + '\n');
    });
    state.finishClose = () => { release?.(); child.kill('SIGTERM'); };
    return {
      initializationResult: state.roundtrip,
      async *[Symbol.asyncIterator]() { await new Promise(resolve => { release = resolve; }); },
      close() { if (!state.deferClose) state.finishClose(); },
    };
  },
} });
const { Session } = await import('../bridges/claude/dist/session.js');

function fixture(t, mode) {
  const root = mkdtempSync(join(tmpdir(), 'netcap-hook-channel-')), id = randomUUID();
  const state = { mode, seen: [] }; states.set(id, state);
  const session = new Session(root, { id, source: id, cwd: root, executable: process.execPath,
    model: 'fixture', endpoint: 'http://127.0.0.1:1', permission: 'read_only', probe: false }, true);
  t.after(async () => { await session.close(); states.delete(id); rmSync(root, { recursive: true, force: true }); });
  return { session, state };
}

for (const mode of ['missing-hook-ack', 'rejected-hook-ack']) test(`tool approval ${mode} prevents initialization and input`, async t => {
  const {session,state}=fixture(t,mode);
  state.deferClose=true;
  try {
    await assert.rejects(session.open('fixture'), /未确认逐次工具审批约束/);
    await assert.rejects(session.send({operation:randomUUID(),text:'must remain unsent',api_key:'fixture'}), /SDK initialization failed/);
  } finally {
    state.deferClose=false;state.finishClose?.();await session.close();
  }
  const snapshot=await session.snapshot();
  assert.equal(snapshot.entries.some(row=>row.kind==='initialized'||row.kind==='input'),false);
});

test('hook catalog preserves SDK frames, Unicode, and SDK control responses while stripping sensitive content', { timeout: 12000 }, async t => {
  const { session, state } = fixture(t, 'valid');
  await session.open('fixture');
  const before = await session.snapshot(), result = await session.hookCatalog(), after = await session.snapshot();
  assert.equal(result.inventory.items[0].plugin_name, 'fixture@inline');
  assert.match(result.inventory.items[0].definition_hash, /^[a-f0-9]{64}$/);
  assert.equal(JSON.stringify(result).includes('PRIVATE'), false);
  assert.deepEqual(before.entries, after.entries); assert.equal(before.connection, after.connection);
  const sdk = await state.roundtrip(); assert.equal(sdk.marker, '中文🦊' + '.'.repeat(180000));
  assert.ok(state.seen.some(m => m.text === '中文🦊'));
  assert.ok(state.seen.every(m => !m.response?.request_id?.startsWith('netcap-hooks-')));
});
for (const mode of ['error', 'invalid-policy', 'invalid-row']) test(`hook catalog ${mode} returns no partial rows or private error`, { timeout: 12000 }, async t => {
  const { session } = fixture(t, mode); await session.open('fixture');
  await assert.rejects(session.hookCatalog(), error => !String(error).includes('PRIVATE'));
});
test('native safe/bare modes, policy uncertainty and skipped settings remain observable without their raw contents', { timeout: 12000 }, async t => {
  const { session } = fixture(t, 'modes'); await session.open('fixture');
  const result = await session.hookCatalog();
  assert.equal(result.inventory.policy.safe_mode, true); assert.equal(result.inventory.policy.bare_mode, true);
  assert.equal(result.inventory.policy.policy_unreadable, true); assert.equal(result.inventory.policy.settings_error_count, 1);
  assert.equal(JSON.stringify(result).includes('PRIVATE'), false);
});
test('closing the query cancels an outstanding hook read and never restores it', { timeout: 12000 }, async t => {
  const { session } = fixture(t, 'close'); await session.open('fixture');
  const rejected = assert.rejects(session.hookCatalog()); await session.close(); await rejected;
  await assert.rejects(session.hookCatalog(), /原生查询未开启/);
});
test('late hook response is consumed after timeout, and a later read still works with the same SDK process', { timeout: 12000 }, async t => {
  const { session, state } = fixture(t, 'late'); await session.open('fixture');
  const before = await session.snapshot();
  await assert.rejects(session.hookCatalog(), /清单不可用/); await delay(150);
  assert.equal((await session.hookCatalog()).inventory.items.length, 1);
  const sdk = await state.roundtrip(); assert.ok(sdk.marker.startsWith('中文🦊'));
  assert.equal((await session.snapshot()).connection, before.connection);
  assert.ok(state.seen.every(m => !m.response?.request_id?.startsWith('netcap-hooks-')));
});

test('permission rules preserve conflicting native rows and exact spelling without writing state or consuming SDK frames', async t => {
  const {session,state}=fixture(t,'valid');
  await assert.rejects(session.permissionCatalog(), /原生查询未开启/);
  await session.open('fixture');
  const before=await session.snapshot(), first=await session.permissionCatalog(), second=await session.permissionCatalog();
  assert.equal(first.query_id,second.query_id);
  assert.equal(first.configuration_id,second.configuration_id);
  assert.equal(first.inventory.managed_only,true);
  assert.equal(first.inventory.items.length,3);
  assert.deepEqual(first.inventory.items.slice(0,2).map(row=>row.not_in_effect),[false,true]);
  assert.equal(first.inventory.items[2].source,'futureSource');
  assert.equal(first.inventory.items[2].rule,'Bash(echo 中文🦊\n\u202E)');
  assert.deepEqual(first.inventory.errors,[{file:session.config.cwd+'/settings.json',field:'permissions.allow'}]);
  assert.equal(JSON.stringify(first).includes('PRIVATE'),false);
  assert.deepEqual((await session.snapshot()).entries,before.entries);
  assert.equal((await session.snapshot()).history_digest,before.history_digest);
  assert.equal((await state.roundtrip()).marker,'中文🦊'+'.'.repeat(180000));
  assert.ok(state.seen.some(row=>row.text==='permission frame after'));
  assert.ok(state.seen.every(row=>!row.response?.request_id?.startsWith('netcap-hooks-')));
});
for(const mode of ['permission-invalid','permission-wrong-cwd','permission-error'])test(`permission ${mode} rejects the complete read without native diagnostic text`,async t=>{
  const {session}=fixture(t,mode);await session.open('fixture');
  await assert.rejects(session.permissionCatalog(),error=>!String(error).includes('PRIVATE'));
});
test('permission read cannot return after query binding changes or closure',async t=>{
  const {session}=fixture(t,'valid');await session.open('fixture');
  const pending=session.permissionCatalog();
  session.config={...session.config,model:'changed'};
  await assert.rejects(pending,/查询或配置已变化/);
  await session.close();
  await assert.rejects(session.permissionCatalog(),/原生查询未开启/);
});
test('closing during a permission read cancels the request without reopening the query',async t=>{
  const {session}=fixture(t,'permission-close');await session.open('fixture');
  const rejected=assert.rejects(session.permissionCatalog(),/权限规则报告不可用/);
  await session.close();await rejected;
  await assert.rejects(session.permissionCatalog(),/原生查询未开启/);
});

const {permissionRuleInventory}=await import('../bridges/claude/dist/permission_rules.js');
for(const mode of ['missing-rows','missing-directories','invalid-mode','invalid-behavior','invalid-source','invalid-rule','invalid-location','invalid-marker','invalid-directory','invalid-errors','invalid-error-row'])test(`native permission boundary rejects ${mode}`,()=>{
  const state={originalCwd:'/owned',managedOnly:false,rules:[{behavior:'deny',source:'policySettings',rule:'Read(./private)',editability:'readonly'}],workspaceDirectories:[]};
  if(mode==='missing-rows')delete state.rules;
  if(mode==='missing-directories')delete state.workspaceDirectories;
  if(mode==='invalid-mode')state.managedOnly=null;
  if(mode==='invalid-behavior')state.rules[0].behavior='maybe';
  if(mode==='invalid-source')state.rules[0].source='';
  if(mode==='invalid-rule')state.rules[0].rule='bad\ud800';
  if(mode==='invalid-location')state.rules[0].editability='custom';
  if(mode==='invalid-marker')state.rules[0].notInEffect=null;
  if(mode==='invalid-directory')state.workspaceDirectories=[{path:'relative',source:'session'}];
  if(mode==='invalid-errors')state.errors={};
  if(mode==='invalid-error-row')state.errors=[{message:'PRIVATE_ERROR'}];
  assert.throws(()=>permissionRuleInventory({state},'/owned'));
});
test('empty native rules remain a valid empty report, distinct from a missing list',()=>{
  const report=permissionRuleInventory({state:{originalCwd:'/owned',managedOnly:false,rules:[],workspaceDirectories:[]}},'/owned');
  assert.deepEqual(report.items,[]);assert.deepEqual(report.errors,[]);assert.equal(report.available,true);
});
