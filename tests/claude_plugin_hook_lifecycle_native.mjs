import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
const request=JSON.parse(readFileSync(process.argv[2],'utf8')),state=join(request.root,'journal');mkdirSync(state);
const results={},checks={};
const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
for (const item of request.cases) {
  const id=randomUUID(),session=new Session(state,{id,source:id,cwd:request.cwd,executable:'/home/zzw/.local/bin/claude',model:'claude-sonnet-4-6',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false,plugin_selections:[item.selection]},true);
  try {
    await session.open('fixture-no-model');
    // Observe whether initialization actually runs the hook; do not send an
    // input solely to manufacture a lifecycle result.
    await new Promise(resolve=>setTimeout(resolve,300));
    const snapshot=await session.snapshot();writeFileSync(join(request.root,item.mode+'-snapshot.json'),JSON.stringify(snapshot,null,2));
    results[item.mode]={id,events:snapshot.entries.filter(e=>e.event?.subtype?.startsWith('hook_')),trace:existsSync(item.marker)?readFileSync(item.marker,'utf8'):null,inputs:snapshot.entries.filter(e=>e.kind==='input').length,model_results:snapshot.entries.filter(e=>e.event?.type==='result').length};
  } finally { await session.close(); }
}
for(const [mode,expected] of [['success',0],['error',2],['disabled',null]]) {
  const result=results[mode],responses=result.events.filter(e=>e.event.subtype==='hook_response');
  check(mode+'_zero_model_input',result.inputs===0&&result.model_results===0);
  check(mode+'_execution_presence',(result.trace!==null)===(mode!=='disabled'));
  check(mode+'_native_result',mode==='disabled'?result.events.length===0:responses.length===1&&responses[0].event.exit_code===expected&&responses[0].event.outcome===mode);
  if(mode!=='disabled'){
    check(mode+'_hook_id_continuity',result.events[0].event.hook_id===responses[0].event.hook_id&&result.events.every(e=>e.event.session_id===result.id));
    check(mode+'_no_private_diagnostics',result.events.every(e=>!e.event.hook_name&&!e.event.stdout&&!e.event.stderr&&!e.event.output));
  }
}
writeFileSync(join(request.root,'result.json'),JSON.stringify({checks,results},null,2));
