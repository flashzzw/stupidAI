import test from 'node:test';
import assert from 'node:assert/strict';
import { adoptedSettingsHookSources } from '../bridges/claude/dist/settings_sources.js';
import { inheritedPluginComponents } from '../bridges/claude/dist/plugins.js';

const file=(name,sha256)=>({path:'/fixture/.claude/'+name,resolved_path:'/fixture/.claude/'+name,kind:'file',mode:0o600,sha256});
const inventory=(source,hash,plugin_name='')=>({packages:[],skills:{commands:[],descriptions:[]},agents:[],mcp:{servers:[]},hooks:{items:[{source,plugin_name,event:'SessionStart',definition_hash:hash}],policy:{all_disabled:false,managed_only:false}}});
test('only changed source groups are explicitly adopted',()=>{
 const original=[file('settings.json','project-v1'),file('settings.local.json','local-v1')];
 assert.deepEqual(adoptedSettingsHookSources(original,undefined),[]);
 assert.deepEqual(adoptedSettingsHookSources(original,original),[]);
 assert.deepEqual(adoptedSettingsHookSources(original,[original[0],file('settings.local.json','local-v2')]),['localSettings']);
 assert.deepEqual(adoptedSettingsHookSources(original,[file('settings.json','project-v2'),original[1]]),['projectSettings']);
 assert.throws(()=>adoptedSettingsHookSources(undefined,original));
 assert.throws(()=>adoptedSettingsHookSources(original,[original[0],file('other.json','local-v2')]));
});
test('explicit local hook replacement is allowed while ordinary capability changes preserve it',()=>{
 const a=inventory('localSettings','v1'),b=inventory('localSettings','v2');
 assert.notEqual(inheritedPluginComponents(a,[],[],[]),inheritedPluginComponents(b,[],[],[]));
 assert.equal(inheritedPluginComponents(a,[],[],[],['localSettings']),inheritedPluginComponents(b,[],[],[],['localSettings']));
 assert.notEqual(inheritedPluginComponents(a,[],[],[],['projectSettings']),inheritedPluginComponents(b,[],[],[],['projectSettings']));
});
test('user, managed, unknown and unselected plugin hooks remain protected',()=>{
 for(const source of ['userSettings','policySettings','unknown']){
  assert.notEqual(inheritedPluginComponents(inventory(source,'v1'),[],[],[],[source]),inheritedPluginComponents(inventory(source,'v2'),[],[],[],[source]));
 }
 assert.notEqual(inheritedPluginComponents(inventory('localSettings','v1','unselected@inline'),[],[],[],['localSettings']),inheritedPluginComponents(inventory('localSettings','v2','unselected@inline'),[],[],[],['localSettings']));
});
test('adopting hooks does not bypass native policy comparison',()=>{
 const a=inventory('localSettings','v1'),b=inventory('localSettings','v2');b.hooks.policy.managed_only=true;
 assert.notEqual(inheritedPluginComponents(a,[],[],[],['localSettings']),inheritedPluginComponents(b,[],[],[],['localSettings']));
});
