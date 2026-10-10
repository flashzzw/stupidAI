// No model input or listening endpoint: measure only native helper invocation.
import { query } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { mcpDefinitions } from '../bridges/claude/dist/mcp.js';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
const config=JSON.parse(readFileSync(process.argv[2],'utf8'));
let closing=false,wake,results=0,trustWarning='';
async function* input(){while(!closing)await new Promise(resolve=>{wake=resolve;});}
const servers=config.source==='project'?{}:mcpDefinitions([{asset_id:'shared',server:'identity',mode:config.mode,source_path:config.definition}]);
const run=query({prompt:input(),options:{cwd:config.cwd,pathToClaudeCodeExecutable:'/home/zzw/.local/bin/claude',sessionId:randomUUID(),
 settingSources:['user','project','local'],settings:config.settings,mcpServers:servers,strictMcpConfig:false,tools:[],
 env:{...process.env,ANTHROPIC_BASE_URL:'http://127.0.0.1:1',ANTHROPIC_API_KEY:'fixture-no-model'},
 stderr:data=>{trustWarning ||= data.split('\n').find(line=>line.includes('headersHelper not run'))??'';},
 canUseTool:async()=>({behavior:'deny',message:'probe sends no input'})}});
const reading=(async()=>{for await(const e of run)if(e.type==='result')results++;})();
try{
 await run.initializationResult();
 let statuses=[];
 for(let n=0;n<100;n++){
  statuses=(await run.mcpServerStatus()).map(s=>({name:s.name,status:s.status,source:s.source}));
  if(statuses.every(s=>!['pending','connecting'].includes(s.status)))break;
  await new Promise(resolve=>setTimeout(resolve,100));
 }
 writeFileSync(config.output,JSON.stringify({helper_ran:existsSync(config.marker),statuses,model_results:results,trust_warning:!!trustWarning,trust_warning_detail:trustWarning},null,2));
}finally{closing=true;wake?.();run.close();await reading.catch(()=>{});}
