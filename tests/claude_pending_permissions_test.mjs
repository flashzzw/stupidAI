// Production Session and public SDK transport seam; the subprocess is controlled.
import assert from 'node:assert/strict';
import { randomUUID, createHash } from 'node:crypto';
import { EventEmitter } from 'node:events';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, readFileSync, renameSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createRequire } from 'node:module';
import { PassThrough, Writable } from 'node:stream';
import { setImmediate as tick } from 'node:timers/promises';
import { mock, test } from 'node:test';

const require = createRequire(new URL('../bridges/claude/package.json', import.meta.url));
const states = new Map();
const rules = behavior => [{behavior, source: 'policySettings', rule: 'Read(./target.txt)', editability: 'readonly'}];
mock.module('node:child_process', {namedExports: {spawn: (_command, args, options) => {
  const state = states.get(args[0]), child = new EventEmitter();
  Object.assign(child, {pid: 123, killed: false, exitCode: null, signalCode: null, stdout: new PassThrough(), stderr: new PassThrough()});
  child.stdin = new Writable({write(chunk, _encoding, done) {
    const request = JSON.parse(chunk.toString()); state.wire.push(request);
    const send = () => {
      if (child.killed) return;
      const permission = request.request.subtype === 'list_permission_rules';
      const response = permission ? {state: {originalCwd: options.cwd, managedOnly: true, rules: state.rules, workspaceDirectories: state.directories, errors: state.errors}}
        : {hooks: [], policy: {disabledByPolicy: false, managedOnly: false, pluginOnly: false, allDisabled: false, policyHookCount: 0}};
      child.stdout.write(JSON.stringify({type: 'control_response', response: {request_id: request.request_id, subtype: permission && state.failRead ? 'error' : 'success', response, error: 'PRIVATE_ERROR'}})+'\n');
    };
    if (state.holdRead && request.request.subtype === 'list_permission_rules') { state.held.push(send); }
    else queueMicrotask(send);
    done();
  }});
  child.kill = signal => {child.killed = true; child.signalCode = signal; child.stdout.end(); child.stderr.end(); queueMicrotask(() => {child.emit('exit', null, signal); child.emit('close', null, signal);}); return true;};
  return child;
}}});
mock.module(require.resolve('@anthropic-ai/claude-agent-sdk'), {namedExports: {getSessionMessages: async () => [], query: ({prompt, options}) => {
  const state = states.get(options.sessionId); state.options = options; state.starts++;
  const child = options.spawnClaudeCodeProcess({command: process.execPath, args: [options.sessionId], cwd: options.cwd, env: process.env}); child.stdout.resume();
  let closed = false, wake;
  state.emit = event => {state.events.push(event); wake?.();};
  return {initializationResult: async () => ({hooks_applied: true, plugins_applied: true}),
    supportedCommands: async () => [], getContextUsage: async () => ({memoryFiles: [], skills: {skillFrontmatter: []}}),
    mcpServerStatus: async () => [], applyFlagSettings: async () => {},
    async *[Symbol.asyncIterator]() {while (!closed) {if (!state.events.length) await new Promise(resolve => {wake = resolve;}); while (state.events.length) yield state.events.shift();}},
    close() {closed = true; for (const abort of state.aborts) abort.abort(); wake?.(); child.kill('SIGTERM');},
  };
}}});
const {Session} = await import('../bridges/claude/dist/session.js');

async function fixture(t, {source = false, send = true} = {}) {
  const root = mkdtempSync(join(tmpdir(), 'netcap-pending-permissions-')), id = randomUUID();
  const state = {rules: rules('ask'), directories: [], errors: [], events: [], aborts: [], wire: [], starts: 0, decisions: [], held: []};
  states.set(id, state);
  const config = {id, source: id, cwd: root, executable: process.execPath, model: 'fixture', endpoint: 'http://127.0.0.1:1', permission: 'read_only', probe: false};
  const settings = join(root, '.claude/settings.local.json');
  if (source) {
    mkdirSync(join(root, '.claude')); writeFileSync(settings, '{}', {mode: 0o600});
    config.settings_sources = [{path: settings, resolved_path: settings, kind: 'file', sha256: createHash('sha256').update('{}').digest('hex'), mode: 0o600}];
  }
  const session = new Session(root, config, true), operation = randomUUID();
  t.after(async () => {await session.close(); states.delete(id); rmSync(root, {recursive: true, force: true});});
  await session.open('fixture');
  if (send) await session.send({operation, text: 'one original input', api_key: 'fixture'});
  const ask = async () => {
    const requestId = randomUUID(), abort = new AbortController(); state.aborts.push(abort);
    const result = state.options.canUseTool('Read', {file_path: join(root, 'target.txt')}, {requestId, signal: abort.signal});
    void result.then(value => state.decisions.push({requestId, value}));
    let request;
    for (let n = 0; n < 100; n++) {
      request = (await session.snapshot()).pending.find(p => p.id === requestId);
      if (request) break;
      await tick();
    }
    assert.ok(request, 'original approval becomes observable');
    return {request, abort, result, reply: decision => session.respond({request_id: requestId, connection: request.connection, decision})};
  };
  return {session, state, ask, root, id, operation, settings};
}

test('changed native policy cannot be approved through the original request; explicit deny completes it once', async t => {
  const f = await fixture(t), p = await f.ask();
  f.state.rules = rules('deny');
  await assert.rejects(p.reply('allow'), /权限规则/);
  assert.equal(f.state.decisions.length, 0);
  const current = (await f.session.snapshot()).pending[0];
  assert.equal(current.id, p.request.id); assert.equal(current.status, 'pending');
  assert.equal(current.permission_guard.state, 'changed');
  await p.reply('deny'); assert.equal((await p.result).behavior, 'deny');
  await assert.rejects(p.reply('allow'), /expired/);
  assert.equal(f.state.decisions.length, 1); assert.equal(f.state.starts, 1);
  assert.equal((await f.session.snapshot()).entries.filter(e => e.kind === 'input').length, 1);
});

test('unchanged, reordered native rows preserve one-time approval and its original input', async t => {
  const f = await fixture(t);
  // Capture both rows before the next original input, then change only menu order.
  f.state.rules = [rules('ask')[0], {behavior:'deny', source:'futureSource', rule:'Bash(echo 中文🦊\n\u202E)', editability:'readonly'}];
  f.state.emit({type:'result', session_id:f.id, subtype:'success', queued_turn_count:0}); await tick();
  await f.session.send({operation:randomUUID(),text:'second explicit input',api_key:'fixture'});
  const p = await f.ask(); f.state.rules.reverse();
  await p.reply('allow');
  assert.deepEqual(await p.result, {behavior:'allow', updatedInput:p.request.input, decisionClassification:'user_temporary'});
  const journal = readFileSync(join(f.root, f.id+'.jsonl'), 'utf8');
  assert.ok(journal.includes('permission_check'));
  assert.equal(f.state.decisions.length, 1);
});

test('a change observed through the catalog stays invalid after exact rule restoration', async t => {
  const f = await fixture(t), p = await f.ask();
  f.state.rules = rules('deny'); await f.session.permissionCatalog();
  f.state.rules = rules('ask'); await f.session.permissionCatalog();
  await assert.rejects(p.reply('allow'), /权限规则/);
  assert.equal((await f.session.snapshot()).pending[0].permission_guard.state, 'changed');
  await p.reply('deny'); assert.equal((await p.result).behavior, 'deny');
});

test('an approval inherits the original input observation even when policy changes before the callback', async t => {
  const f = await fixture(t); f.state.rules = rules('deny');
  const p = await f.ask(); await assert.rejects(p.reply('allow'), /权限规则/);
  await p.reply('deny'); assert.equal((await p.result).behavior, 'deny');
});

for (const mode of ['read-error','parse-error','conflicting','directory','duplicate','no-original-input']) test(`unusable approval observation: ${mode}`, async t => {
  const f = await fixture(t, {send: mode !== 'no-original-input'}), p = await f.ask();
  if (mode === 'read-error') f.state.failRead = true;
  if (mode === 'parse-error') f.state.errors = [{file:join(f.root,'settings.json'),path:'permissions.allow',message:'PRIVATE_VALUE'}];
  if (mode === 'conflicting') f.state.rules.push({...f.state.rules[0],notInEffect:true});
  if (mode === 'duplicate') f.state.rules.push({...f.state.rules[0]});
  if (mode === 'directory') f.state.directories = [{path:join(f.root,'extra'),source:'localSettings'}];
  await assert.rejects(p.reply('allow'), error => /权限规则/.test(error.message) && !error.message.includes('PRIVATE'));
  assert.equal(f.state.decisions.length, 0);
  await p.reply('deny'); assert.equal((await p.result).behavior, 'deny');
});

test('two simultaneous pending tools keep their identities while sharing the original observation', async t => {
  const f = await fixture(t), requests = await Promise.all([f.ask(), f.ask()]);
  await Promise.all(requests.map(p => p.reply('allow')));
  assert.equal(f.state.decisions.length, 2);
  assert.equal(new Set(f.state.decisions.map(d=>d.requestId)).size, 2);
  for (const p of requests) assert.equal((await p.result).behavior, 'allow');
});

test('denial wins a concurrent delayed approval without waiting for a permission report', async t => {
  const f = await fixture(t), p = await f.ask(); f.state.holdRead = true;
  const stale = assert.rejects(p.reply('allow'), /expired/);
  for (let n=0; n<100 && !f.state.held.length; n++) await tick();
  assert.equal(f.state.held.length, 1);
  await p.reply('deny'); assert.equal((await p.result).behavior, 'deny');
  f.state.holdRead = false; f.state.held.splice(0).forEach(send=>send()); await stale;
  assert.equal(f.state.decisions.length, 1);
});

test('two concurrent approvals resolve the original request once', async t => {
  const f = await fixture(t), p = await f.ask();
  const results = await Promise.allSettled([p.reply('allow'), p.reply('allow')]);
  assert.equal(results.filter(r=>r.status==='fulfilled').length, 1);
  assert.equal(f.state.decisions.length, 1);
});

test('changed local source blocks approval but does not prevent refusing the same original request', async t => {
  const f = await fixture(t, {source:true}), p = await f.ask(); writeFileSync(f.settings, '{"env":{"OWNED":"changed"}}');
  await assert.rejects(p.reply('allow'));
  const before = f.state.wire.length;
  await p.reply('deny'); assert.equal((await p.result).behavior, 'deny');
  assert.equal(f.state.wire.length, before); assert.equal(f.state.starts, 1);
});

test('closing while approval is checked cannot hand back allow', async t => {
  const f = await fixture(t), p = await f.ask(); f.state.holdRead = true;
  const rejected = assert.rejects(p.reply('allow'));
  await tick(); await f.session.close(); await rejected;
  assert.equal((await p.result).behavior, 'deny'); assert.equal(f.state.starts, 1);
});

test('permission evidence write failure blocks allow while refusal still resolves the original request', async t => {
  const f = await fixture(t), p = await f.ask(), journal = join(f.root, f.id+'.jsonl');
  renameSync(journal, journal+'.saved'); mkdirSync(journal);
  try {
    await assert.rejects(p.reply('allow'), /权限规则/);
    assert.equal(f.state.decisions.length, 0);
    await p.reply('deny'); assert.equal((await p.result).behavior, 'deny');
  } finally {rmSync(journal,{recursive:true});renameSync(journal+'.saved',journal);}
});

test('a later explicit input can be approved in the same query after refusing an invalidated request', async t => {
  const f = await fixture(t), old = await f.ask();
  f.state.rules = rules('deny'); await f.session.permissionCatalog();
  f.state.rules = rules('ask'); await f.session.permissionCatalog();
  await old.reply('deny'); await old.result;
  f.state.emit({type:'result',session_id:f.id,subtype:'success',queued_turn_count:0}); await tick();
  const operation = randomUUID(); await f.session.send({operation,text:'new explicit input',api_key:'fixture'});
  const next = await f.ask(); await next.reply('allow');
  assert.equal((await next.result).behavior,'allow');
  assert.equal(next.request.operation,operation); assert.equal(next.request.connection,old.request.connection);
  assert.equal(f.state.starts,1); assert.equal(f.state.decisions.length,2);
});
