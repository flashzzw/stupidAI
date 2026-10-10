import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createRequire } from 'node:module';
import { PassThrough, Writable } from 'node:stream';
import { EventEmitter } from 'node:events';
import { mock, test } from 'node:test';
import { instructionLoadHooks, instructionPromptHooks } from '../bridges/claude/dist/instruction_loads.js';

const context = {native_id: randomUUID(), cwd: '/owned', query_id: randomUUID(), configuration_id: 'a'.repeat(64)};
const input = {hook_event_name: 'InstructionsLoaded', session_id: context.native_id, cwd: context.cwd,
  file_path: '/owned/feature/CLAUDE.md', memory_type: 'Project', load_reason: 'nested_traversal',
  prompt_id: randomUUID(), trigger_file_path: '/owned/feature/data.txt'};
const invoke = async (value, append, failed = () => assert.fail('unexpected metadata failure')) =>
  instructionLoadHooks(context, append, failed).InstructionsLoaded[0].hooks[0](value, undefined, {signal: new AbortController().signal});

test('native metadata retains query and prompt provenance without instruction content', async () => {
  let load;
  assert.deepEqual(await invoke({...input, content: 'PRIVATE_BODY', transcript_path: '/private/transcript', arbitrary: 'PRIVATE_EXTRA'}, value => {load = value;}), {});
  assert.equal(load.query_id, context.query_id);
  assert.equal(load.configuration_id, context.configuration_id);
  assert.equal(load.prompt_id, input.prompt_id);
  assert.equal(load.path, input.file_path);
  assert.equal(load.trigger_path, input.trigger_file_path);
  assert.equal(load.reason, 'nested_traversal');
  assert.ok(load.id && load.at);
  assert.equal(JSON.stringify(load).includes('PRIVATE_'), false);
  assert.equal('transcript_path' in load, false);
  assert.equal('operation' in load, false);
});

test('import, glob and native agent metadata are preserved without reclassifying unknown categories', async () => {
  let load;
  await invoke({...input, cwd: '/owned/feature', memory_type: 'FutureSource', load_reason: 'future_load', agent_id: 'native-worker', agent_type: 'reviewer',
    parent_file_path: '/owned/CLAUDE.md', globs: ['feature/**']}, value => {load = value;});
  assert.equal(load.source, 'FutureSource');
  assert.equal(load.reason, 'future_load');
  assert.equal(load.agent_id, 'native-worker');
  assert.equal(load.parent_path, '/owned/CLAUDE.md');
  assert.deepEqual(load.globs, ['feature/**']);
  assert.equal(load.cwd, context.cwd);
  assert.equal(load.native_cwd, '/owned/feature');
});

for (const [name, override] of Object.entries({
  'wrong-session': {session_id: randomUUID()}, 'relative-cwd': {cwd: 'other'},
  'wrong-event': {hook_event_name: 'PreToolUse'}, 'relative-path': {file_path: 'CLAUDE.md'},
  'nul-path': {file_path: '/owned/\0'}, 'oversize-path': {file_path: '/' + 'a'.repeat(4096)},
  'missing-source': {memory_type: ''}, 'bad-reason': {load_reason: 2}, 'invalid-prompt': {prompt_id: 'another prompt'},
  'relative-parent': {parent_file_path: 'CLAUDE.md'}, 'bad-trigger': {trigger_file_path: {}},
  'bad-agent': {agent_id: null}, 'too-many-globs': {globs: Array(33).fill('**')}, 'empty-glob': {globs: ['']},
})) {
  test(name + ' is not recorded as a valid load or turned into a blocking hook decision', async () => {
    let failed = 0, saved = 0;
    assert.deepEqual(await invoke({...input, ...override}, () => {saved++;}, () => {failed++;}), {});
    assert.equal(saved, 0);
    assert.equal(failed, 1);
  });
}

test('diagnostic write and failure-handler errors do not alter native execution', async () => {
  assert.deepEqual(await invoke(input, () => {throw Error('disk full');}, () => {throw Error('secondary failure');}), {});
});

const promptInput = {hook_event_name: 'PreToolUse', session_id: context.native_id, cwd: context.cwd,
  prompt_id: randomUUID(), tool_use_id: 'owned-read', tool_name: 'Read', tool_input: {file_path: '/PRIVATE_TOOL_INPUT'}};
const invokePrompt = async (value, append, failed = () => assert.fail('unexpected prompt-link failure'), toolID = 'owned-read') =>
  instructionPromptHooks(context, append, failed).PreToolUse[0].hooks[0](value, toolID, {signal: new AbortController().signal});

test('prompt link records only native IDs and owned query metadata', async () => {
  let link;
  assert.deepEqual(await invokePrompt({...promptInput, agent_id: 'native-worker', transcript_path: '/PRIVATE_TRANSCRIPT'}, value => {link = value;}), {});
  assert.equal(link.prompt_id, promptInput.prompt_id);
  assert.equal(link.tool_use_id, 'owned-read');
  assert.equal(link.tool, 'Read');
  assert.equal(link.query_id, context.query_id);
  assert.equal(link.configuration_id, context.configuration_id);
  assert.equal(link.agent_id, 'native-worker');
  assert.equal(JSON.stringify(link).includes('PRIVATE'), false);
  assert.equal('tool_input' in link, false);
  assert.equal('operation' in link, false);
});

for (const [name, override] of Object.entries({
  'wrong-session': {session_id: randomUUID()}, 'wrong-event': {hook_event_name: 'InstructionsLoaded'},
  'invalid-prompt': {prompt_id: 'another prompt'}, 'missing-tool-id': {tool_use_id: undefined},
  'invalid-tool-id': {tool_use_id: 'owned\0read'}, 'missing-tool': {tool_name: ''}, 'bad-agent': {agent_id: null},
})) {
  test('prompt link with ' + name + ' leaves permission unchanged and reports a diagnostic gap', async () => {
    let failed = 0, saved = 0;
    assert.deepEqual(await invokePrompt({...promptInput, ...override}, () => {saved++;}, () => {failed++;}), {});
    assert.equal(saved, 0);
    assert.equal(failed, 1);
  });
}

test('missing native prompt remains uncorrelated without inventing an error or decision', async () => {
  assert.deepEqual(await invokePrompt({...promptInput, prompt_id: undefined}, () => assert.fail('missing native identity was saved')), {});
});

test('a mismatched callback tool ID cannot establish a prompt link', async () => {
  let failed = 0;
  assert.deepEqual(await invokePrompt(promptInput, () => assert.fail('mismatched identity was saved'), () => {failed++;}, 'other-tool'), {});
  assert.equal(failed, 1);
});

test('prompt-link write failures do not make a permission decision', async () => {
  assert.deepEqual(await invokePrompt(promptInput, () => {throw Error('disk full');}, () => {throw Error('secondary failure');}), {});
});

const require = createRequire(new URL('../bridges/claude/package.json', import.meta.url)), states = new Map();
mock.module('node:child_process', {namedExports: {spawn: () => {
  const child = new EventEmitter();
  Object.assign(child, {pid: 123, killed: false, exitCode: null, signalCode: null, stdout: new PassThrough(), stderr: new PassThrough()});
  child.stdin = new Writable({write(chunk, encoding, done) {
    const q = JSON.parse(chunk.toString());
    queueMicrotask(() => child.stdout.write(JSON.stringify({type: 'control_response', response: {request_id: q.request_id, subtype: 'success', response: {hooks: [], policy: {disabledByPolicy: false, managedOnly: false, pluginOnly: false, allDisabled: false, policyHookCount: 0}}}}) + '\n'));
    done();
  }});
  child.kill = signal => { child.killed = true; child.signalCode = signal; child.stdout.end(); child.stderr.end(); queueMicrotask(() => {child.emit('exit', null, signal); child.emit('close', null, signal);}); return true; };
  return child;
}}});
mock.module(require.resolve('@anthropic-ai/claude-agent-sdk'), {namedExports: {getSessionMessages: async () => [], query: ({options}) => {
  const state = states.get(options.sessionId); state.options = options;
  const child = options.spawnClaudeCodeProcess({command: 'fixture', args: [], cwd: options.cwd, env: process.env}); child.stdout.resume();
  let stopped = false, wake;
  const events = [];
  state.emit = event => {events.push(event); wake?.();};
  return {initializationResult: async () => ({plugins_applied: true, hooks_applied: true}),
    supportedCommands: async () => [], getContextUsage: async () => ({memoryFiles: [], skills: {skillFrontmatter: []}}),
    mcpServerStatus: async () => [], applyFlagSettings: async () => {},
    async *[Symbol.asyncIterator]() {while (!stopped) {if (!events.length) await new Promise(resolve => {wake = resolve;}); while (events.length) yield events.shift();}},
    close() {stopped = true; wake?.(); child.kill('SIGTERM');}};
}}});
const {Session} = await import('../bridges/claude/dist/session.js');
function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'netcap-instruction-loads-')), id = randomUUID(), state = {};
  const session = new Session(root, {id, source: id, cwd: root, executable: process.execPath, model: 'fixture', endpoint: 'http://127.0.0.1:1', permission: 'read_only', probe: false}, true);
  states.set(id, state);
  t.after(async () => {await session.close(); states.delete(id); rmSync(root, {recursive: true, force: true});});
  const notify = value => state.options.hooks.InstructionsLoaded[0].hooks[0]({hook_event_name: 'InstructionsLoaded', session_id: id, cwd: root,
    file_path: join(root, 'CLAUDE.md'), memory_type: 'Project', load_reason: 'session_start', ...value}, undefined, {signal: new AbortController().signal});
  return {root, id, state, session, notify};
}

test('production Session journals observations and preserves them across original-ID reopening', async t => {
  const f = fixture(t);
  await f.session.open('fixture');
  await f.notify({content: 'PRIVATE_BODY'});
  const before = await f.session.snapshot(), original = before.entries.find(entry => entry.kind === 'instruction_load');
  assert.equal(before.instruction_observer.current_query_id, original.load.query_id);
  assert.equal(before.instruction_observer.recording_failed, false);
  assert.equal(readFileSync(join(f.root, f.id + '.jsonl'), 'utf8').includes('PRIVATE_BODY'), false);
  await f.session.close();
  const restored = Session.restore(f.root, f.id);
  try {
    await restored.open('fixture');
    await f.notify({load_reason: 'include', parent_file_path: join(f.root, 'parent.md')});
    const after = await restored.snapshot(), loads = after.entries.filter(entry => entry.kind === 'instruction_load');
    assert.equal(loads.length, 2);
    assert.deepEqual(loads[0], original);
    assert.notEqual(loads[0].load.query_id, after.instruction_observer.current_query_id);
    assert.equal(loads[1].load.query_id, after.instruction_observer.current_query_id);
    assert.equal(after.entries.filter(entry => entry.kind === 'input').length, 0);
    assert.equal(after.config.id, f.id);
  } finally {await restored.close();}
});

test('late notification keeps its native prompt instead of the active operation', async t => {
  const f = fixture(t); await f.session.open('fixture');
  const first = randomUUID(), second = randomUUID(), nativePrompt = randomUUID();
  await f.session.send({operation: first, text: 'first', api_key: 'fixture'});
  f.state.emit({type: 'result', subtype: 'success', is_error: false, session_id: f.id, uuid: randomUUID()});
  await new Promise(resolve => setImmediate(resolve));
  await f.session.send({operation: second, text: 'second', api_key: 'fixture'});
  await f.notify({prompt_id: nativePrompt});
  const snapshot = await f.session.snapshot(), load = snapshot.entries.findLast(entry => entry.kind === 'instruction_load').load;
  assert.equal(snapshot.active, second);
  assert.equal(load.prompt_id, nativePrompt);
  assert.equal('operation' in load, false);
});

test('production Session persists the independent input, event and native tool identities', async t => {
  const f = fixture(t); await f.session.open('fixture');
  const operation = randomUUID(), nativePrompt = randomUUID();
  await f.session.send({operation, text: 'read owned fixture', api_key: 'fixture'});
  f.state.emit({type: 'assistant', session_id: f.id, uuid: randomUUID(), user_message_uuid: operation,
    parent_tool_use_id: null, message: {role: 'assistant', content: [{type: 'tool_use', id: 'owned-read', name: 'Read', input: {}}]}});
  await new Promise(resolve => setImmediate(resolve));
  const observe = f.state.options.hooks.PreToolUse.find(value => !value.matcher).hooks[0];
  assert.deepEqual(await observe({...promptInput, session_id: f.id, prompt_id: nativePrompt}, 'owned-read', {signal: new AbortController().signal}), {});
  const before = await f.session.snapshot();
  const savedInput = before.entries.find(entry => entry.kind === 'input');
  const event = before.entries.find(entry => entry.kind === 'event');
  const link = before.entries.find(entry => entry.kind === 'instruction_prompt');
  assert.equal(savedInput.operation, operation);
  assert.equal(event.event.user_message_uuid, operation);
  assert.equal(link.link.prompt_id, nativePrompt);
  assert.equal(link.link.tool_use_id, 'owned-read');
  assert.equal(savedInput.query_id, before.instruction_observer.current_query_id);
  assert.equal(event.query_id, savedInput.query_id);
  assert.equal(link.link.query_id, savedInput.query_id);
  assert.equal(readFileSync(join(f.root, f.id + '.jsonl'), 'utf8').includes('PRIVATE_TOOL_INPUT'), false);
  await f.session.close();
  const restored = Session.restore(f.root, f.id);
  assert.deepEqual((await restored.snapshot()).entries.find(entry => entry.kind === 'instruction_prompt'), link);
});

test('production Session adds the observer while retaining required ask callbacks', async t => {
  const f = fixture(t); await f.session.open('fixture');
  const hooks = f.state.options.hooks.PreToolUse;
  assert.equal(hooks.length, 2);
  const value = {...promptInput, session_id: f.id, tool_name: 'Edit'};
  const results = [];
  for (const matcher of hooks) {
    if (!matcher.matcher || new RegExp(matcher.matcher).test(value.tool_name)) {
      for (const callback of matcher.hooks) results.push(await callback(value, 'owned-read', {signal: new AbortController().signal}));
    }
  }
  assert.equal(results[0].hookSpecificOutput.permissionDecision, 'ask');
  assert.deepEqual(results[1], {});
  const snapshot = await f.session.snapshot();
  assert.equal(snapshot.instruction_observer.recording_failed, false);
  assert.equal(snapshot.fault, '');
});

test('invalid notification is visible as a diagnostic gap without changing input permission', async t => {
  const f = fixture(t); await f.session.open('fixture');
  assert.deepEqual(await f.notify({session_id: randomUUID()}), {});
  const snapshot = await f.session.snapshot();
  assert.equal(snapshot.instruction_observer.recording_failed, true);
  assert.equal(snapshot.entries.some(entry => entry.kind === 'instruction_load'), false);
  assert.equal(snapshot.fault, '');
  await f.session.send({operation: randomUUID(), text: 'still uses original permission policy', api_key: 'fixture'});
});
