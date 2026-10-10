import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync,writeFileSync,mkdirSync,realpathSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
const request=JSON.parse(readFileSync(process.argv[2],'utf8')),state=join(request.root,'journal');mkdirSync(state);
const checks={},snapshots={},sessions=[];
const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
const plugin='netcap-skill-'+'a'.repeat(24),hash='f'.repeat(64);
const selection={asset_id:'method',name:plugin+':method',native_key:plugin+':method',mode:'enabled',source:'plugin',source_path:join(request.source,'SKILL.md'),discovery_path:join(state,'skill-resources',plugin,hash,'skills/method/SKILL.md'),delivery:'local_plugin',plugin_name:plugin,resource_root:request.source,resource_kind:'directory',resource_hash:hash};
const config=mode=>{const id=randomUUID();return{id,source:id,cwd:request.cwd,executable:'/home/zzw/.local/bin/claude',model:'claude-sonnet-4-6',endpoint:request.endpoint,permission:'read_only',probe:false,skill_selections:[{...selection,mode}]};};
const a=new Session(state,config('enabled'),true),b=new Session(state,config('disabled'),true);sessions.push(a,b);
async function turn(session,label) {
  const operation=randomUUID();await session.send({operation,text:'W0207_METHOD_CASE='+label,api_key:'fixture-A'});
  const deadline=Date.now()+55000;
  while(Date.now()<deadline) {
    const snap=await session.snapshot();
    for(const pending of snap.pending.filter(p=>p.status==='pending')) {
      const allowed=pending.tool==='Skill'&&pending.input.skill===selection.name||pending.tool==='Read'&&realpathSync(pending.input.file_path)===join(request.source,'data.txt');
      await session.respond({request_id:pending.id,connection:pending.connection,decision:allowed?'allow':'deny'});
    }
    const result=snap.entries.find(e=>e.kind==='event'&&e.operation===operation&&e.event.type==='result');
    if(result){check(label+'_native_result',!result.event.is_error);snapshots[label]=snap;return snap;}
    await new Promise(resolve=>setTimeout(resolve,30));
  }
  throw Error(label+' did not complete');
}
try {
  await a.open('fixture-A');await b.open('fixture-A');
  const enabled=await turn(a,'enabled');
  check('read_tool_result_in_history',enabled.entries.some(e=>e.kind==='event'&&e.event.type==='assistant'&&e.event.message.content?.some(c=>c.type==='tool_use'&&c.name==='Read')));
  await turn(b,'disabled');
  const id=a.config.id;await a.close();const restored=Session.restore(state,id);sessions.push(restored);await restored.open('fixture-A');
  check('restore_keeps_original_history',(await restored.snapshot()).history_digest===enabled.history_digest);
  await turn(restored,'restored');
  const before=await restored.snapshot(),prepared=await restored.prepareExecution();
  await restored.applyExecution({operation:randomUUID(),baseline:prepared.configuration_baseline,mcp_selections:[],skill_selections:[{...selection,mode:'disabled'}],api_key:'fixture-A'});
  const after=await restored.snapshot();snapshots.changed=after;
  check('disable_same_id_with_real_history',after.config.id===id&&after.history_digest===before.history_digest&&!after.skills.commands.some(s=>s.name.startsWith(plugin+':')));
  // Skill attachments live in the original event journal; the SDK's message
  // history is a separate projection and omits the injected body.
  check('historical_body_retained',JSON.stringify(before.entries).includes('W0207_BODY_TOKEN=')&&JSON.stringify(after.entries)===JSON.stringify(before.entries));
} finally {
  for(const session of sessions)await session.close();
  writeFileSync(join(request.root,'result.json'),JSON.stringify({checks,snapshots,method_gate_verified:false},null,2));
}
