// Native local-plugin delivery probe; no user prompt, provider or tool call.
import { query } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
const config=JSON.parse(readFileSync(process.argv[2],'utf8'));
let closing=false,wake,modelResults=0;
async function* prompt(){while(!closing)await new Promise(resolve=>{wake=resolve;});}
const run=query({prompt:prompt(),options:{cwd:config.cwd,pathToClaudeCodeExecutable:'/home/zzw/.local/bin/claude',sessionId:randomUUID(),
  settingSources:['user','project','local'],systemPrompt:{type:'preset',preset:'claude_code'},tools:['Skill'],permissionMode:'default',
  plugins:config.plugins,...(config.delivery?{pluginDelivery:config.delivery}:{}),settings:{disableAllHooks:true,skillOverrides:config.overrides??{}},
  env:{...process.env,ANTHROPIC_BASE_URL:'http://127.0.0.1:1',ANTHROPIC_API_KEY:'fixture-no-model'},
  canUseTool:async()=>({behavior:'deny',message:'probe sends no model input'})}});
const reading=(async()=>{for await(const event of run)if(event.type==='result')modelResults++;})();
try {
  const initialized=await run.initializationResult();
  const commands=await run.supportedCommands();
  const context=await run.getContextUsage({detail:'summary'});
  writeFileSync(config.output,JSON.stringify({initialized,commands,descriptions:context.skills?.skillFrontmatter??[],model_results:modelResults},null,2));
} finally{closing=true;wake?.();run.close();await reading.catch(()=>{});}
