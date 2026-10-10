import assert from 'node:assert/strict';
import { test } from 'node:test';
import { verifySkills, skillChangeMatches } from '../bridges/claude/dist/skills.js';
const old={asset_id:'old',name:'package:method',native_key:'package:method',mode:'enabled',source:'plugin',delivery:'local_plugin',plugin_name:'package'};
for (const mode of ['absent','name','alias','descriptor','unavailable']) test(`released plugin ${mode} cannot masquerade as inherited`,async()=>{
  const command={name:mode==='alias'?'other':'package:method',aliases:mode==='alias'?['package:method']:[],builtin:false};
  const inventory={available:mode!=='unavailable',at:'now',commands:mode==='name'||mode==='alias'?[command]:[],descriptions:mode==='descriptor'?[{name:'changed-name',source:'plugin',plugin_name:'package'}]:[]};
  assert.equal(skillChangeMatches([], [old], inventory),mode==='absent');
  const run={supportedCommands:async()=>inventory.commands,getContextUsage:async()=>({skills:{skillFrontmatter:inventory.descriptions.map(s=>({...s,pluginName:s.plugin_name}))}})};
  if(mode!=='unavailable') {
    if(mode==='absent')await verifySkills(run,{skill_selections:[]},[old]);
    else await assert.rejects(verifySkills(run,{skill_selections:[]},[old]),/不一致/);
  }
});
test('released natural visibility is taken from original native sources',()=>{
  const natural={...old,name:'method',native_key:'method',delivery:undefined,source:'userSettings'};
  assert.equal(skillChangeMatches([],[natural],{available:true,at:'now',commands:[{name:'method',aliases:[],builtin:false}],descriptions:[{name:'method',source:'userSettings'}]}),true);
});
