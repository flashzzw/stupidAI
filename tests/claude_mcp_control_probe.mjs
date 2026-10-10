import { query } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
const config=JSON.parse(readFileSync(process.argv[2],'utf8'));
let wake,queue=[],closing=false,results=[];const events=[],permissions=[];
async function* input(){while(!closing){if(!queue.length)await new Promise(resolve=>wake=resolve);while(queue.length&&!closing)yield queue.shift();}}
const session=randomUUID();
const run=query({prompt:input(),options:{cwd:config.cwd,pathToClaudeCodeExecutable:'/home/zzw/.local/bin/claude',model:'claude-sonnet-4-6',sessionId:session,persistSession:true,systemPrompt:{type:'preset',preset:'claude_code'},settingSources:['user','project','local'],settings:config.settings,strictMcpConfig:false,mcpServers:config.servers,tools:[],permissionMode:'default',env:{...process.env,ANTHROPIC_BASE_URL:config.endpoint,ANTHROPIC_API_KEY:'fixture-A',ENABLE_TOOL_SEARCH:'false'},canUseTool:async(tool,args,options)=>{permissions.push({tool,mcp:options.mcpServer});return {behavior:'allow',updatedInput:args,decisionClassification:'user_temporary'};}}});
const reading=(async()=>{for await(const e of run){events.push(e);if(e.type==='result')results.push(e);}})();
const wait=async(fn)=>{const start=Date.now();while(!fn()){if(Date.now()-start>20000)throw Error('probe timeout');await new Promise(r=>setTimeout(r,50));}};
const status=async()=> (await run.mcpServerStatus()).map(s=>({name:s.name,status:s.status,source:s.source,scope:s.scope,tools:s.tools?.map(t=>t.name),env_present:!!s.config?.env}));
try{
 await run.initializationResult();let initial;await wait(async()=>true);
 for(let n=0;n<100;n++){initial=await status();if(initial.filter(s=>s.status==='connected').length===2)break;await new Promise(r=>setTimeout(r,100));}
 await run.applyFlagSettings({permissions:{ask:['mcp__identity__*']}});
 queue.push({type:'user',uuid:randomUUID(),session_id:session,message:{role:'user',content:'NETCAP_A_TURN_1'},parent_tool_use_id:null});wake?.();await wait(()=>results.length===1);
 const beforeToggle=await status();await run.toggleMcpServer('identity',false);const disabled=await status();
 const removed=await run.setMcpServers({});const afterRemove=await status();
 const added=await run.setMcpServers(config.servers);const afterAdd=await status();
 const output={session,initial,beforeToggle,disabled,removed:{added:removed.added,removed:removed.removed,errors:Object.keys(removed.errors)},afterRemove,added:{added:added.added,removed:added.removed,errors:Object.keys(added.errors)},afterAdd,permissions,terminal:results.map(r=>({subtype:r.subtype,is_error:r.is_error}))};
 writeFileSync(config.output,JSON.stringify(output,null,2));
}finally{closing=true;wake?.();run.close();await reading.catch(()=>{});}
