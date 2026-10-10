// Public SDK mirror of the pinned raw-wire plugin experiment. No model input.
import { query } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';

const inputPath = process.argv[2], config = JSON.parse(readFileSync(inputPath, 'utf8'));
const sdk = JSON.parse(readFileSync(new URL('../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/package.json', import.meta.url), 'utf8'));
if (sdk.version !== '0.3.280') throw new Error('revalidate the SDK version before updating this pin');
let closing = false, wake, child, modelResults = 0, permissionCalls = 0;
const outgoing = [], result = { sdk_version: sdk.version };
async function* prompt() { while (!closing) await new Promise(resolve => { wake = resolve; }); }
const run = query({ prompt: prompt(), options: {
  cwd: config.cwd, pathToClaudeCodeExecutable: '/home/zzw/.local/bin/claude', sessionId: randomUUID(),
  plugins: config.plugins, pluginDelivery: 'initialize', settings: config.settings,
  settingSources: ['user', 'project', 'local'], systemPrompt: { type: 'preset', preset: 'claude_code' },
  model: 'claude-sonnet-4-6', permissionMode: 'default',
  env: { ...process.env, ANTHROPIC_API_KEY: 'fixture-no-model', ANTHROPIC_BASE_URL: 'http://127.0.0.1:1' },
  canUseTool: async () => { permissionCalls++; return { behavior: 'deny', message: 'No tools in this experiment' }; },
  spawnClaudeCodeProcess: options => {
    child = spawn(options.command, options.args, { cwd: options.cwd, env: options.env, signal: options.signal, stdio: ['pipe', 'pipe', 'pipe'] });
    child.stderr.resume();
    const write = child.stdin.write.bind(child.stdin);
    child.stdin.write = (...args) => { outgoing.push(args[0].toString()); return write(...args); };
    return child;
  },
} });
const reading = (async () => { for await (const event of run) if (event.type === 'result') modelResults++; })();
try {
  const initialized = await run.initializationResult();
  result.initialized = { commands: initialized.commands, agents: initialized.agents, plugins_applied: initialized.plugins_applied };
  result.before = await run.getContextUsage({ detail: 'summary' });
  result.reloaded = await run.reloadPlugins();
  result.after = await run.getContextUsage({ detail: 'summary' });
} catch (error) {
  result.error = String(error);
  process.exitCode = 1;
} finally {
  closing = true; wake?.(); run.close(); await reading.catch(() => {});
  if (child && child.exitCode === null && child.signalCode === null) await new Promise(resolve => child.once('exit', resolve));
  const messages = outgoing.join('').split('\n').filter(Boolean).map(line => JSON.parse(line));
  result.no_user_input = messages.length > 0 && messages.every(m => m.type === 'control_request');
  result.requests = messages;
  result.child_stopped = !!child && (child.exitCode !== null || child.signalCode !== null);
  result.model_results = modelResults; result.permission_calls = permissionCalls;
  writeFileSync(join(dirname(inputPath), 'sdk-result.json'), JSON.stringify(result, null, 2));
}
