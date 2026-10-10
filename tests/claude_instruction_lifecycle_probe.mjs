// Fixed native SDK probe. All model replies come from an owned local provider.
import { query } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { readFileSync, writeFileSync, unlinkSync, realpathSync } from 'node:fs';
import { join, resolve, sep } from 'node:path';
import { randomUUID, createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { NativeHookChannel } from '../bridges/claude/dist/hook_catalog.js';

const request = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const native = randomUUID(), samples = {}, events = [], loads = [], permissions = [], checks = {};
const check = (name, value) => { checks[name] = Boolean(value); if (!value) throw Error(name); };
let stage = 'initializing', hookMutation = false, current;
const writeResult = () => writeFileSync(join(request.root, 'sdk-result.json'), JSON.stringify({native, checks, samples, loads, permissions, events}, null, 2));
const owned = path => resolve(path).startsWith(resolve(request.root) + sep);
const disk = () => Object.fromEntries(Object.entries(request.files).map(([name, path]) => {
  try { return [name, createHash('sha256').update(readFileSync(path)).digest('hex')]; }
  catch (error) { if (error.code === 'ENOENT') return [name, 'absent']; throw error; }
}));
const mutate = () => {
  for (const change of request.changes) {
    check('mutation_is_owned_' + change.path, owned(change.path));
    if (change.action === 'delete') unlinkSync(change.path);
    else writeFileSync(change.path, change.text);
  }
};
async function open(resume) {
  let closing = false, wake, channel;
  const queue = [], results = [];
  const input = async function* () {
    while (!closing) {
      if (queue.length) yield queue.shift();
      else await new Promise(resolve => { wake = resolve; });
    }
  };
  const run = query({prompt: input(), options: {
    cwd: request.cwd, pathToClaudeCodeExecutable: request.executable,
    ...(resume ? {resume: native} : {sessionId: native}),
    settingSources: ['user', 'project', 'local'], settings: request.settings,
    systemPrompt: {type: 'preset', preset: 'claude_code'}, tools: ['Read'],
    permissionMode: 'default', model: 'claude-sonnet-4-6',
    env: {...process.env, ANTHROPIC_BASE_URL: request.endpoint, ANTHROPIC_API_KEY: 'fixture-A'},
    spawnClaudeCodeProcess: options => {
      const child = spawn(options.command, options.args, {cwd: options.cwd, env: options.env, stdio: ['pipe', 'pipe', 'pipe']});
      child.stderr.resume();
      channel = new NativeHookChannel(child);
      return channel.process;
    },
    hooks: {InstructionsLoaded: [{hooks: [async input => {
      loads.push({stage, ...input});
      check('loaded_path_is_owned_' + loads.length, owned(input.file_path) || input.file_path.startsWith('/etc/claude-code/'));
      if (request.mode === 'hook-race' && !hookMutation && input.file_path === request.files.parent) {
        hookMutation = true;
        mutate();
      }
      if (request.mode === 'hook-block' && input.file_path === request.files.parent) return {decision: 'block', reason: 'Owned instruction exclusion probe'};
      return {};
    }]}]},
    canUseTool: async (tool, input) => {
      const allow = tool === 'Read' && input.file_path === request.read_file && owned(realpathSync(input.file_path));
      permissions.push({stage, tool, input, allow});
      return allow ? {behavior: 'allow', updatedInput: input} : {behavior: 'deny', message: 'Only the owned fixture Read is allowed'};
    },
  }});
  let failure;
  const reading = (async () => {
    try { for await (const event of run) { events.push({stage, ...event}); if (event.type === 'result') results.push(event); } }
    catch (error) { if (!closing) failure = error; }
  })();
  const close = async () => { closing = true; wake?.(); run.close(); await reading; };
  current = {run, close, policy: async () => (await channel.read()).policy};
  await run.initializationResult();
  return {run, close, async turn(label) {
    stage = label;
    const count = results.length;
    queue.push({type: 'user', session_id: native, parent_tool_use_id: null,
      message: {role: 'user', content: 'W0209_INSTRUCTION_CASE=' + label}});
    wake?.();
    for (const deadline = Date.now() + 25000; results.length === count;) {
      if (failure) throw failure;
      if (Date.now() > deadline) throw Error('native result timeout: ' + label);
      await new Promise(resolve => setTimeout(resolve, 20));
    }
    check(label + '_same_native_identity', results.at(-1).session_id === native);
    check(label + '_native_success', results.at(-1).is_error === false);
  }};
}
async function sample(label) {
  const usage = await current.run.getContextUsage({detail: 'summary'});
  samples[label] = {memory_files: usage.memoryFiles, load_count: loads.length, disk: disk(), policy: await current.policy()};
  check(label + '_metadata_available', Array.isArray(usage.memoryFiles));
  check(label + '_reported_paths_owned', usage.memoryFiles.every(item => owned(item.path) || item.path.startsWith('/etc/claude-code/')));
  writeResult();
}
try {
  let session = await open(false);
  await sample('initialized');
  if (request.mode === 'startup-race') mutate();
  await session.turn('initial');
  await sample('initial');
  if (!['startup-race', 'hook-race'].includes(request.mode)) mutate();
  await sample('after_disk_change');
  await session.turn('hot');
  await sample('hot');
  await session.close();
  stage = 'reopening';
  session = await open(true);
  await sample('reopened');
  await session.turn('cold');
  await sample('cold');
  if (request.mode === 'hook-race') check('hook_race_was_injected', hookMutation);
} finally {
  if (current) await current.close();
  writeResult();
}
