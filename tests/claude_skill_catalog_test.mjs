import assert from 'node:assert/strict';
import { test } from 'node:test';
import { skillInventory } from '../bridges/claude/dist/skills.js';

test('description text is read only for the catalog, not persisted selection evidence',async()=>{
  const calls=[];
  const query={supportedCommands:async()=>[{name:'method',description:'catalog metadata',aliases:['key']}],getContextUsage:async options=>{calls.push(options);return {skills:{skillFrontmatter:[{name:'method',source:'userSettings'}]}};}};
  const execution=await skillInventory(query);
  assert.equal(execution.available,true);assert.equal('description' in execution.commands[0],false);
  const catalog=await skillInventory(query,undefined,true);
  assert.equal(catalog.commands[0].description,'catalog metadata');
  assert.deepEqual(calls,[{detail:'summary'},{detail:'summary'}]);
});
test('catalog source-query failure never returns partial available metadata',async()=>{
  const query={supportedCommands:async()=>[{name:'method',description:'private metadata'}],getContextUsage:async()=>{throw Error('private failure');}};
  const catalog=await skillInventory(query,undefined,true);
  assert.equal(catalog.available,false);assert.deepEqual(catalog.commands,[]);
});
