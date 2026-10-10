import test from 'node:test';
import assert from 'node:assert/strict';
import { localSettingsHooks, settingsGuardEvidence } from '../bridges/claude/dist/settings_guard.js';
import { sessionConfig } from '../bridges/claude/dist/protocol.js';

const input={hook_event_name:'ConfigChange',session_id:'original',source:'local_settings',file_path:'/owned/.claude/settings.local.json'};
const policy={all_disabled:false,managed_only:false,disabled_by_policy:false,plugin_only:false,policy_unreadable:false,safe_mode:false,bare_mode:false,settings_error_count:0};
test('probe and unsupported guard versions are rejected by the public protocol',()=>{
  assert.throws(()=>sessionConfig({settings_guard_version:2}),/invalid local settings guard/);
  assert.throws(()=>sessionConfig({settings_guard_version:1,probe:true}),/invalid local settings guard/);
});
test('ordinary settings block is independent of diagnostic persistence',async()=>{
  let failure=false;
  const hooks=localSettingsHooks('original',()=>{throw Error('disk full');},()=>{failure=true;});
  assert.equal(hooks.ConfigChange[0].matcher,'project_settings|local_settings');
  assert.deepEqual(await hooks.ConfigChange[0].hooks[0](input,undefined,{signal:new AbortController().signal}),{decision:'block'});
  assert.equal(failure,true);
});
test('even diagnostic error handling cannot turn block into callback failure',async()=>{
  const hooks=localSettingsHooks('original',()=>{throw Error('disk full');},()=>{throw Error('diagnostic failure');});
  assert.deepEqual(await hooks.ConfigChange[0].hooks[0](input,undefined,{signal:new AbortController().signal}),{decision:'block'});
});
test('callback records source identity without copying arbitrary payload',async()=>{
  let saved;
  const hooks=localSettingsHooks('original',v=>{saved=v;},()=>assert.fail('unexpected persistence failure'));
  await hooks.ConfigChange[0].hooks[0]({...input,secret:'DO_NOT_COPY'},undefined,{signal:new AbortController().signal});
  assert.equal(saved.path,input.file_path);assert.equal(saved.source,'local_settings');assert.equal(saved.decision,'block_requested');assert.ok(saved.at);
  assert.equal('secret' in saved,false);
});
test('wrong identity is recorded as unknown and still blocked',async()=>{
  let saved;const hooks=localSettingsHooks('different',v=>{saved=v;},()=>{});
  assert.deepEqual(await hooks.ConfigChange[0].hooks[0](input,undefined,{signal:new AbortController().signal}),{decision:'block'});
  assert.equal(saved.source,'unknown');
});
test('legacy and unavailable policy never claim registration',()=>{
  assert.equal(settingsGuardEvidence(undefined,true,policy,[],false),undefined);
  assert.equal(settingsGuardEvidence(1,true,policy,[],false).state,'registered');
  assert.equal(settingsGuardEvidence(1,false,policy,[],false).state,'unavailable');
  assert.equal(settingsGuardEvidence(1,true,undefined,[],false).state,'unavailable');
  assert.equal(settingsGuardEvidence(1,true,policy,[],true).state,'unavailable');
  for(const field of Object.keys(policy)) assert.equal(settingsGuardEvidence(1,true,{...policy,[field]:field==='settings_error_count'?1:true},[],false).state,'unavailable');
});
