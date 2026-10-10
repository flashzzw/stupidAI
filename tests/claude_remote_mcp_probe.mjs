import { query } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
const config = JSON.parse(readFileSync(process.argv[2], 'utf8'));
let wake, closing = false;
const queued = [], results = [], permissions = [];
async function* input() {
  while (!closing) {
    if (!queued.length) await new Promise(resolve => wake = resolve);
    while (queued.length && !closing) yield queued.shift();
  }
}
const session = randomUUID();
const run = query({ prompt: input(), options: {
  cwd: config.cwd, pathToClaudeCodeExecutable: '/home/zzw/.local/bin/claude', model: 'claude-sonnet-4-6', sessionId: session,
  persistSession: true, systemPrompt: { type: 'preset', preset: 'claude_code' }, settingSources: ['user', 'project', 'local'],
  settings: config.settings, strictMcpConfig: false, mcpServers: config.servers, tools: [], permissionMode: 'default',
  env: { ...process.env, ANTHROPIC_BASE_URL: config.endpoint, ANTHROPIC_API_KEY: 'fixture-' + config.profile, ENABLE_TOOL_SEARCH: 'false' },
  canUseTool: async (tool, args) => { permissions.push(tool); return { behavior: 'allow', updatedInput: args, decisionClassification: 'user_temporary' }; }
} });
const reading = (async () => { for await (const e of run) if (e.type === 'result') results.push({ subtype: e.subtype, is_error: e.is_error }); })();
const statuses = async () => (await run.mcpServerStatus()).map(s => ({ name: s.name, status: s.status, source: s.source, tools: s.tools?.map(t => t.name) }));
try {
  await run.initializationResult();
  let inventory;
  for (let attempt = 0; attempt < 100; attempt++) {
    inventory = await statuses();
    if (inventory.some(s => s.name === 'identity' && s.status === 'connected')) break;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  if (!inventory.some(s => s.name === 'identity' && s.status === 'connected')) throw Error('remote MCP never connected');
  await run.applyFlagSettings({ permissions: { ask: ['mcp__identity__*'] } });
  queued.push({ type: 'user', uuid: randomUUID(), session_id: session, message: { role: 'user', content: 'NETCAP_' + config.profile + '_TURN_1' }, parent_tool_use_id: null });
  wake?.();
  for (let attempt = 0; attempt < 200 && !results.length; attempt++) await new Promise(resolve => setTimeout(resolve, 100));
  if (!results.length) throw Error('no terminal result');
  writeFileSync(config.output, JSON.stringify({ session, inventory, permissions, results }, null, 2));
} finally { closing = true; wake?.(); run.close(); await reading.catch(() => {}); }
