import { query } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
const config=JSON.parse(readFileSync(process.argv[2],'utf8'));
const id=config.session??randomUUID(), queued=[], permissions=[], results=[];
let wake,closing=false;
async function* input(){while(!closing){if(!queued.length)await new Promise(resolve=>wake=resolve);while(queued.length&&!closing)yield queued.shift();}}
const run=query({prompt:input(),options:{cwd:config.cwd,pathToClaudeCodeExecutable:'/home/zzw/.local/bin/claude',model:'claude-sonnet-4-6',
  ...(config.resume?{resume:id}:{sessionId:id}),persistSession:true,settingSources:['user','project','local'],settings:config.settings,
  mcpServers:config.servers,strictMcpConfig:false,tools:[],permissionMode:'default',
  env:{...process.env,ANTHROPIC_BASE_URL:config.endpoint,ANTHROPIC_API_KEY:'fixture-'+config.profile,ENABLE_TOOL_SEARCH:'false'},
  canUseTool:async(tool,args)=>{permissions.push(tool);return {behavior:'allow',updatedInput:args,decisionClassification:'user_temporary'};}
}});
const reading=(async()=>{for await(const event of run)if(event.type==='result')results.push({subtype:event.subtype,is_error:event.is_error});})();
try{
  await run.initializationResult();
  let inventory;
  for(let i=0;i<100;i++){
    inventory=(await run.mcpServerStatus()).map(s=>({name:s.name,status:s.status,source:s.source,tools:s.tools?.map(t=>t.name)}));
    if(inventory.some(s=>s.name==='identity'&&s.status===config.expected_status))break;
    await new Promise(resolve=>setTimeout(resolve,100));
  }
  if(!inventory.some(s=>s.name==='identity'&&s.status===config.expected_status))throw Error('unexpected sanitized status '+JSON.stringify(inventory));
  if(config.turn){
    await run.applyFlagSettings({permissions:{ask:['mcp__identity__*']}});
    queued.push({type:'user',uuid:randomUUID(),session_id:id,message:{role:'user',content:'NETCAP_'+config.profile+'_TURN_'+config.turn},parent_tool_use_id:null});wake?.();
    for(let i=0;i<300&&!results.length;i++)await new Promise(resolve=>setTimeout(resolve,100));
    if(!results.length||results.some(r=>r.is_error))throw Error('native turn did not succeed');
  }
  writeFileSync(config.output,JSON.stringify({session:id,inventory,permissions,results},null,2));
}finally{closing=true;wake?.();run.close();await reading.catch(()=>{});}
