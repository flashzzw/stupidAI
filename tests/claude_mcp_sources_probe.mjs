// Real native startup and inventory; no model prompt or listening endpoint.
import { query } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { mcpDefinitions, mcpInventory, mcpMatches, verifyMCP } from '../bridges/claude/dist/mcp.js';
import { readFileSync, writeFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
const config=JSON.parse(readFileSync(process.argv[2],'utf8'));
let closing=false,wake,modelResults=0;
async function* input(){while(!closing)await new Promise(resolve=>{wake=resolve;});}
const run=query({prompt:input(),options:{cwd:config.cwd,pathToClaudeCodeExecutable:'/home/zzw/.local/bin/claude',sessionId:randomUUID(),
 ...(config.debug_file?{debugFile:config.debug_file}:{}),
 settingSources:['user','project','local'],settings:config.settings,mcpServers:mcpDefinitions(config.selections),strictMcpConfig:config.strict??false,
 ...(config.managed?{managedSettings:config.managed}:{}),...(config.plugins?{plugins:config.plugins}:{}),systemPrompt:{type:'preset',preset:'claude_code'},tools:[],permissionMode:'default',
 env:{...process.env,ANTHROPIC_BASE_URL:'http://127.0.0.1:1',ANTHROPIC_API_KEY:'fixture-no-model'},
 canUseTool:async()=>({behavior:'deny',message:'probe sends no input'})}});
const reading=(async()=>{for await(const e of run)if(e.type==='result')modelResults++;})();
try{
 await run.initializationResult();
 if(config.ask)await run.applyFlagSettings({permissions:{...(config.flagPermissions??{}),ask:['mcp__identity__*']}});
 let inventory;
 for(let n=0;n<100;n++){
  inventory=await mcpInventory(run);
  if(inventory.available && inventory.servers.every(s=>!['pending','connecting'].includes(s.status)))break;
  await new Promise(resolve=>setTimeout(resolve,100));
 }
 let error='';try{await verifyMCP(run,config.selections);}catch(e){error=e.message;}
 writeFileSync(config.output,JSON.stringify({inventory,selection_matches:mcpMatches(config.selections,inventory),verification_error:error,model_results:modelResults},null,2));
}finally{closing=true;wake?.();run.close();await reading.catch(()=>{});}
