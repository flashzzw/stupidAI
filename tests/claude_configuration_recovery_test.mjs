// Read-only Session API over isolated persisted records and a controlled SDK history reader.
import test,{mock} from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { randomUUID,createHash } from 'node:crypto';
import { mkdtempSync,mkdirSync,writeFileSync,readFileSync,rmSync,readdirSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
const require=createRequire(new URL('../bridges/claude/package.json',import.meta.url)),states=new Map();let starts=0;
mock.module(require.resolve('@anthropic-ai/claude-agent-sdk'),{namedExports:{query:()=>{starts++;throw Error('must not open');},getSessionMessages:async id=>{const s=states.get(id);s.reads++;s.onHistory?.(s.reads);return s.history;}}});
const {Session}=await import('../bridges/claude/dist/session.js');
const {fingerprint}=await import('../bridges/claude/dist/configuration.js');
function fixture(t){
 const root=mkdtempSync(join(tmpdir(),'netcap-recovery-read-')),id=randomUUID(),operation=randomUUID(),file=join(root,'settings.local.json'),metadata=join(root,id+'.json'),journal=join(root,id+'.jsonl');
 writeFileSync(file,'{}',{mode:0o600});
 const sources=[{path:join(root,'settings.json'),resolved_path:join(root,'settings.json'),kind:'absent'},{path:file,resolved_path:file,kind:'file',sha256:createHash('sha256').update('{}').digest('hex'),mode:0o600}];
 const config={id,source:id,cwd:root,executable:'fixture',model:'fixture',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false,settings_guard_version:1,settings_sources:sources};
 const baseline=fingerprint({config,history:[]}),change={operation,baseline,status:'applying',at:'2026-10-06T00:00:00Z',target:[],previous:[],settings_previous:sources,settings_target:[sources[0],{...sources[1],sha256:'b'.repeat(64)}]};
 const failure={version:1,operation,baseline,stage:'open_candidate',reason:'stage_failed',cleanup:'confirmed',at:'2026-10-06T00:00:00Z'};
 const record={...config,_configuration:change};writeFileSync(metadata,JSON.stringify(record));
 const entries=[{kind:'initialized',connection:'old'},{kind:'closed',connection:'old'},{kind:'configuration_failure',failure}];
 const save=()=>writeFileSync(journal,entries.map(e=>JSON.stringify(e)).join('\n')+'\n');save();
 const state={reads:0,history:[]};states.set(id,state);
 t.after(()=>{states.delete(id);rmSync(root,{recursive:true,force:true});});
 return {root,id,operation,baseline,file,metadata,journal,entries,record,state,save};
}
for(const mode of ['matching','settings-drift','history-changed','unfinished-input','unconfirmed-exit','wrong-operation','metadata-race'])test('recovery check '+mode,async t=>{
 const f=fixture(t);
 if(mode==='settings-drift')writeFileSync(f.file,'{"changed":true}');
 if(mode==='history-changed')f.state.history=[{type:'assistant',message:{content:'external history'}}];
 if(mode==='unfinished-input'){f.entries.push({kind:'input',operation:'input-op',text:'not completed'});f.save();}
 if(mode==='unconfirmed-exit'){f.entries[2].failure.cleanup='unconfirmed';f.save();}
 if(mode==='metadata-race')f.state.onHistory=n=>{if(n===2)writeFileSync(f.metadata,JSON.stringify({...f.record,external:true}));};
 const before=readdirSync(f.root).map(n=>[n,readFileSync(join(f.root,n),'utf8')]);
 const session=Session.restore(f.root,f.id),result=await session.configurationRecovery({operation:mode==='wrong-operation'?'different':f.operation,baseline:f.baseline});
 const conditions=Object.fromEntries(result.checks.map(c=>[c.kind,c.status]));
 const expected={'settings-drift':'original_settings','history-changed':'original_history','unfinished-input':'work_clear','unconfirmed-exit':'owned_query_stopped','wrong-operation':'transaction','metadata-race':'read_stable'}[mode];
 if(expected)assert.equal(conditions[expected],'blocked');else assert.ok(result.checks.every(c=>c.status==='passed'));
 if(mode!=='metadata-race')assert.deepEqual(readdirSync(f.root).map(n=>[n,readFileSync(join(f.root,n),'utf8')]),before);
 assert.equal(result.read_only,true);assert.equal(result.snapshot.live,false);assert.equal(result.snapshot.configuration.status,'applying');assert.equal(starts,0);
});
for(const mode of ['settings-drift','unfinished-input','recovering','verified-same','verified-other','startup-fails'])test('explicit recovery '+mode,async t=>{
 const f=fixture(t),operation=randomUUID(),beforeStarts=starts;
 if(mode==='settings-drift')writeFileSync(f.file,'{"changed":true}');
 if(mode==='unfinished-input'){f.entries.push({kind:'input',operation:'unfinished',text:'not completed'});f.save();}
 if(['recovering','verified-same','verified-other'].includes(mode)){
  f.record._configuration.recovery={operation:mode==='verified-other'?randomUUID():operation,status:mode==='recovering'?'recovering':'verified',at:'2026-10-06T00:00:00Z'};
  if(mode!=='recovering')f.record._configuration.status='recovered';
  writeFileSync(f.metadata,JSON.stringify(f.record));
 }
 const session=Session.restore(f.root,f.id),args={operation,original_operation:f.operation,baseline:f.baseline,api_key:'fixture'};
 if(mode==='verified-same')assert.equal((await session.recoverExecution(args)).native_id,f.id);
 else await assert.rejects(session.recoverExecution(args));
 assert.equal(starts-beforeStarts,mode==='startup-fails'?1:0);
 if(mode==='startup-fails'){
  const snapshot=await session.snapshot();
  assert.equal(snapshot.configuration.status,'applying');assert.equal(snapshot.configuration.recovery.operation,operation);
  assert.equal(snapshot.configuration_failure.operation,f.operation);assert.equal(snapshot.configuration_recovery_failure.operation,operation);assert.equal(snapshot.configuration_recovery_failure.cleanup,'confirmed');
  await assert.rejects(session.recoverExecution(args));
  await assert.rejects(Session.restore(f.root,f.id).recoverExecution(args));
  assert.equal(starts-beforeStarts,1);
 }
});
