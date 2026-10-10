// Fixed SDK inventory and local context estimates, without model input.
import { query } from '../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
const config = JSON.parse(readFileSync(process.argv[2], 'utf8'));
let closing = false, wake, modelResults = 0;
async function* input() { while (!closing) await new Promise(resolve => { wake = resolve; }); }
const run = query({ prompt: input(), options: {
  cwd: config.cwd, pathToClaudeCodeExecutable: '/home/zzw/.local/bin/claude', sessionId: randomUUID(),
  settingSources: ['user', 'project', 'local'], settings: config.settings,
  ...(config.managed ? { managedSettings: config.managed } : {}),
  ...(config.skills !== undefined ? { skills: config.skills } : {}),
  systemPrompt: { type: 'preset', preset: 'claude_code' }, tools: ['Skill'], permissionMode: 'default',
  env: { ...process.env, ANTHROPIC_BASE_URL: 'http://127.0.0.1:1', ANTHROPIC_API_KEY: 'fixture-no-model' },
  canUseTool: async () => ({ behavior: 'deny', message: 'probe sends no input' }),
} });
const reading = (async () => { for await (const event of run) if (event.type === 'result') modelResults++; })();
try {
  await run.initializationResult();
  const commands = await run.supportedCommands();
  // Full is the SDK default and can call the token-count API. This experiment
  // explicitly uses local summary estimates; token counts are not usage proof.
  const context = await run.getContextUsage({ detail: 'summary' });
  const reloaded = await run.reloadSkills();
  const refreshed = await run.supportedCommands();
  let changed;
  if (config.change) {
    await run.applyFlagSettings(config.change);
    await run.reloadSkills();
    const current = await run.getContextUsage({ detail: 'summary' });
    changed = { commands: await run.supportedCommands(), skills: current.skills?.skillFrontmatter ?? [] };
  }
  writeFileSync(config.output, JSON.stringify({ commands, refreshed, reloaded, skills: context.skills?.skillFrontmatter ?? [],
    skill_counts: context.skills ? { total: context.skills.totalSkills, included: context.skills.includedSkills } : null,
    memory_files: context.memoryFiles, changed, model_results: modelResults }, null, 2));
} finally { closing = true; wake?.(); run.close(); await reading.catch(() => {}); }
