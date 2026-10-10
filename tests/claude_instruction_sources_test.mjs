import assert from 'node:assert/strict';
import { test } from 'node:test';
import { skillInventory } from '../bridges/claude/dist/skills.js';

test('catalog reports native instruction metadata from the existing summary read', async () => {
  const calls=[];
  const run={supportedCommands:async()=>[],getContextUsage:async options=>{
    calls.push(options);
    return {memoryFiles:[{path:'/parent/CLAUDE.md',type:'Project',tokens:18,content:'PRIVATE_BODY'},
      {path:'/profile/CLAUDE.md',type:'FutureSource',tokens:0}]};
  }};
  const catalog=await skillInventory(run,undefined,true);
  assert.deepEqual(catalog.instructions,{available:true,items:[
    {path:'/parent/CLAUDE.md',source:'Project',tokens:18},{path:'/profile/CLAUDE.md',source:'FutureSource',tokens:0}]});
  assert.deepEqual(calls,[{detail:'summary'}]);
  assert.ok(!JSON.stringify(catalog).includes('PRIVATE_BODY'));
  assert.equal((await skillInventory(run)).instructions,undefined);
});

for(const [name,memoryFiles] of Object.entries({missing:undefined,null:null,empty:[],relative:[{path:'CLAUDE.md',type:'Project',tokens:2}],
  badTokens:[{path:'/x',type:'User',tokens:-1}],fractional:[{path:'/x',type:'User',tokens:1.5}],
  unsafe:[{path:'/x',type:'User',tokens:Number.MAX_SAFE_INTEGER+1}],badPath:[{path:'/x\0y',type:'User',tokens:1}],
  duplicate:[{path:'/x',type:'User',tokens:1},{path:'/x',type:'User',tokens:2}],
  tooMany:Array.from({length:257},(_,i)=>({path:`/file-${i}`,type:'User',tokens:1}))})) {
  test(`instruction sample distinguishes ${name} from a verified empty list`,async()=>{
    const out=await skillInventory({supportedCommands:async()=>[],getContextUsage:async()=>({memoryFiles})},undefined,true);
    assert.equal(out.available,true);
    assert.deepEqual(out.instructions,{available:name==='empty',items:[]});
  });
}
