import { query, getSessionMessages } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { randomUUID, createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
const cfg = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const sleep = () => new Promise(r => setTimeout(r, 50));
const history = async id => await getSessionMessages(id, { dir: cfg.cwd, includeSystemMessages: true });
const hash = value => createHash('sha256').update(JSON.stringify(value)).digest('hex');
async function start(id, servers, resume) {
  let wake, child, done = false;
  const queued = [], results = [], approvals = [];
  async function* input() { while (!done) { if (!queued.length) await new Promise(r => wake = r); while (queued.length && !done) yield queued.shift(); } }
  const run = query({ prompt: input(), options: {
    cwd: cfg.cwd, pathToClaudeCodeExecutable: '/home/zzw/.local/bin/claude', model: 'claude-sonnet-4-6',
    ...(resume ? { resume: id } : { sessionId: id }), persistSession: true, settings: cfg.settings,
    settingSources: ['user', 'project', 'local'], mcpServers: servers, strictMcpConfig: false, tools: [],
    env: { ...process.env, ANTHROPIC_BASE_URL: cfg.endpoint, ANTHROPIC_API_KEY: 'fixture-A', ENABLE_TOOL_SEARCH: 'false' },
    canUseTool: async (tool, args) => { approvals.push(tool); return { behavior: 'allow', updatedInput: args, decisionClassification: 'user_temporary' }; },
    spawnClaudeCodeProcess: options => { child = spawn(options.command, options.args, { cwd: options.cwd, env: options.env, signal: options.signal, stdio: ['pipe','pipe','pipe'] }); child.stderr.resume(); return child; }
  } });
  const reading = (async () => { for await (const event of run) if (event.type === 'result') results.push(event); })();
  await run.initializationResult();
  let inventory;
  for (let n=0;n<120;n++) { inventory = await run.mcpServerStatus(); if (!inventory.some(s => s.status === 'pending')) break; await sleep(); }
  await run.applyFlagSettings({ permissions: { ask: ['mcp__identity__*'] } });
  return { id, run, approvals, inventory: inventory.map(s=>({name:s.name,status:s.status,source:s.source})),
    alive: () => child.exitCode === null && child.signalCode === null,
    send: async number => { const count=results.length; queued.push({type:'user',uuid:randomUUID(),session_id:id,message:{role:'user',content:'NETCAP_A_TURN_'+number},parent_tool_use_id:null}); wake?.(); for(let n=0;n<400&&results.length===count;n++)await sleep(); if(results.length!==count+1||results.at(-1).is_error)throw Error('turn failed'); },
    close: async () => { done=true;wake?.();run.close();await reading.catch(()=>{});for(let n=0;n<100&&child.exitCode===null&&child.signalCode===null;n++)await sleep();if(child.exitCode===null&&child.signalCode===null)throw Error('child not exited'); }
  };
}
const id=randomUUID(), sibling=await start(randomUUID(),cfg.servers,false), phases=[];
try {
  for (const [index, enabled] of [true,false,true].entries()) {
    const before=await history(id), current=await start(id,enabled?cfg.servers:{},index>0);
    try {
      const afterStart=await history(id);
      await current.send(index+1);
      const beforeClose=await history(id);
      await current.close();
      const afterClose=await history(id);
      phases.push({enabled,native_id:id,inventory:current.inventory,approvals:current.approvals,
        history_unchanged_by_restart:hash(before)===hash(afterStart),history_unchanged_by_close:hash(beforeClose)===hash(afterClose),child_exited:!current.alive(),sibling_alive:sibling.alive()});
    } finally { if(current.alive())await current.close(); }
  }
  await sibling.send(4);
  writeFileSync(cfg.output,JSON.stringify({phases,sibling_native_id:sibling.id,sibling_approvals:sibling.approvals},null,2));
} finally {await sibling.close();}
