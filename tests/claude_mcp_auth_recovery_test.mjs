// Exercise the production Session boundary with a controlled SDK transport.
// This is lifecycle evidence; native OAuth remains an independent CLI check.
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createRequire } from 'node:module';
import { mock, test } from 'node:test';
import { setImmediate as tick } from 'node:timers/promises';

const require = createRequire(new URL('../bridges/claude/package.json', import.meta.url));
const states = new Map();
const sdk = mock.module(require.resolve('@anthropic-ai/claude-agent-sdk'), { namedExports: {
  getSessionMessages: async (id) => states.get(id).history,
  query: ({ prompt, options }) => {
    const state = states.get(options.resume ?? options.sessionId);
    const plan = state.plans.shift();
    assert.ok(plan, 'unexpected native query');
    const events = []; let closed = false, wake;
    const run = {
      options, inputs: [], settings: [], closed: false, readerReleased: Promise.withResolvers(),
      initializationResult: async () => {
        if (plan.background) run.emit({ type: 'system', subtype: 'background_tasks_changed', session_id: options.resume, tasks: [{ task_id: 'owned-background', task_type: 'local_bash' }] });
        if (plan.permission) {
          const abort = new AbortController(); state.aborts.push(abort);
          void options.canUseTool('mcp__identity__whoami', {}, { requestId: randomUUID(), signal: abort.signal });
        }
        await tick();
        if (plan.error) throw plan.error;
        return {hooks_applied:true};
      },
      applyFlagSettings: async settings => { run.settings.push(settings); },
      mcpServerStatus: async () => [{ name: 'identity', status: plan.status, source: 'dynamic', tools: [] }],
      emit: event => { events.push(event); wake?.(); },
      close: () => {
        if (plan.lateBackground) run.emit({ type: 'system', subtype: 'background_tasks_changed', session_id: options.resume, tasks: [{ task_id: 'late-background', task_type: 'local_bash' }] });
        closed = true; run.closed = true; wake?.();
        state.closeSignal.resolve();
        if (run.child && !plan.holdExit) run.child.stdin.end();
      },
      async *[Symbol.asyncIterator]() {
        while (!closed) {
          if (!events.length) await new Promise(resolve => { wake = resolve; });
          while (events.length) yield events.shift();
        }
        if (plan.holdReader) await run.readerReleased.promise;
      },
    };
    if (plan.child) run.child = options.spawnClaudeCodeProcess({ command: process.execPath, args: ['-e', 'process.stdin.resume()'], cwd: options.cwd, env: process.env });
    state.runs.push(run);
    // Only consume prompt input after initialization; closing wakes Session's
    // generator, exactly the ownership boundary used by the SDK.
    run.consume = async () => { for await (const message of prompt) run.inputs.push(message); };
    return run;
  },
} });
const { Session } = await import('../bridges/claude/dist/session.js');
const { NotSent } = await import('../bridges/claude/dist/protocol.js');

function fixture(t, plans, { unfinished = false, fresh = false } = {}) {
  const root = mkdtempSync(join(tmpdir(), 'netcap-claude-auth-recovery-'));
  const id = randomUUID(), previous = randomUUID(), connection = randomUUID();
  const source = join(root, 'mcp.json');
  writeFileSync(source, JSON.stringify({ mcpServers: { identity: { type: 'http', url: 'https://example.invalid/mcp' } } }));
  const config = { id, source: id, cwd: root, executable: '/unused/claude', model: 'fixture', endpoint: 'https://example.invalid', permission: 'read_only', probe: false,
    mcp_selections: [{ asset_id: 'shared', server: 'identity', mode: 'enabled', source_path: source }] };
  const entries = [{ kind: 'initialized', connection }, { kind: 'input', operation: previous, text: 'completed before restart' }];
  if (!unfinished) entries.push({ kind: 'event', operation: previous, event: { type: 'result', session_id: id, subtype: 'success', queued_turn_count: 0 } });
  if (fresh) entries.length = 0;
  writeFileSync(join(root, id + '.json'), JSON.stringify(config));
  writeFileSync(join(root, id + '.jsonl'), entries.map(e => JSON.stringify(e) + '\n').join(''));
  const state = { plans: [...plans], runs: [], aborts: [], closeSignal: Promise.withResolvers(), history: fresh ? [] : [{ type: 'user', uuid: previous, session_id: id, message: { role: 'user', content: 'completed before restart' } }] };
  states.set(id, state);
  const session = Session.restore(root, id);
  t.after(async () => {
    for (const abort of state.aborts) abort.abort();
    for (const run of state.runs) { run.readerReleased.resolve(); run.child?.stdin.end(); }
    await session.close(); states.delete(id); rmSync(root, { recursive: true, force: true });
  });
  return { session, state, config, entries };
}

test('reauthentication retries the same closed session without restarting the bridge or replaying input', async t => {
  const { session, state, config, entries } = fixture(t, [{ status: 'needs-auth' }, { status: 'connected' }]);
  await assert.rejects(session.send({ operation: randomUUID(), text: 'blocked before authentication', api_key: 'fixture' }), e => e instanceof NotSent && /OAuth/.test(e.message));
  await tick();
  const failed = await session.snapshot();
  assert.equal(failed.live, false);
  assert.equal(failed.entries.filter(e => e.kind === 'input').length, 1);
  const next = randomUUID();
  const receipt = await session.send({ operation: next, text: 'after authentication', api_key: 'fixture' });
  assert.equal(receipt.native_id, config.id);
  assert.equal(receipt.native_turn, next);
  assert.equal(state.runs.length, 2);
  assert.equal(state.runs[0].closed, true);
  assert.equal(state.runs[1].options.resume, config.id);
  assert.equal(state.runs[1].options.sessionId, undefined);
  assert.deepEqual(session.entries.slice(0, entries.length), entries);
  assert.deepEqual(session.config, config);
  const consuming = state.runs[1].consume();
  await tick();
  assert.deepEqual(state.runs[1].inputs.map(x => x.uuid), [next]);
  assert.ok(state.runs[1].settings.some(s => s.permissions.ask.includes('mcp__identity__*')));
  await session.close(); await consuming;
});

test('non-authentication initialization failures remain blocked', async t => {
  const { session, state } = fixture(t, [{ error: new Error('transport failure') }, { status: 'connected' }]);
  await assert.rejects(session.open('fixture'), /transport failure/);
  await tick();
  await assert.rejects(session.open('fixture'), /SDK initialization failed|SDK event stream/);
  assert.equal(state.runs.length, 1);
});

test('unfinished original input cannot be retried by authentication recovery', async t => {
  const { session, state } = fixture(t, [{ status: 'connected' }], { unfinished: true });
  await assert.rejects(session.open('fixture'), /no verified terminal result/);
  assert.equal(state.runs.length, 0);
});

test('authentication recovery waits for the reader and owned process to exit', async t => {
  const { session, state } = fixture(t, [{ status: 'needs-auth', child: true, holdExit: true, holdReader: true }, { status: 'connected' }]);
  let settled = false;
  const failed = session.open('fixture');
  void failed.then(() => { settled = true; }, () => { settled = true; });
  await state.closeSignal.promise; await tick();
  assert.equal(settled, false, 'must not release fault before the event reader ends');
  assert.equal((await session.snapshot()).live, true);
  state.runs[0].readerReleased.resolve(); await tick();
  assert.equal(settled, false, 'must not release fault before the native child exits');
  assert.notEqual((await session.snapshot()).fault, '');
  await assert.rejects(session.open('fixture'), /SDK initialization failed/);
  assert.equal(state.runs.length, 1);
  state.runs[0].child.stdin.end();
  await assert.rejects(failed, /OAuth/);
  await session.open('fixture');
  assert.equal(state.runs.length, 2);
  assert.equal((await session.snapshot()).fault, '');
});

for (const condition of ['background', 'lateBackground', 'permission']) {
  test(`authentication recovery preserves unresolved ${condition}`, async t => {
    const { session, state } = fixture(t, [{ status: 'needs-auth', [condition]: true }, { status: 'connected' }]);
    await assert.rejects(session.open('fixture'), /OAuth/);
    await tick();
    await assert.rejects(session.open('fixture'), /SDK initialization failed/);
    assert.equal(state.runs.length, 1);
    assert.notEqual((await session.snapshot()).fault, '');
  });
}

test('a failed first creation does not become an authenticated or recoverable session', async t => {
  const { session, state } = fixture(t, [{ status: 'needs-auth' }, { status: 'connected' }], { fresh: true });
  await assert.rejects(session.open('fixture'), /OAuth/);
  await tick();
  await assert.rejects(session.open('fixture'), /SDK initialization failed/);
  assert.equal(state.runs.length, 1);
  assert.equal(session.entries.some(e => e.kind === 'initialized'), false);
});

test('retrying one profile preserves a sibling query and its permission request', async t => {
  const a = fixture(t, [{ status: 'needs-auth' }, { status: 'connected' }]);
  const b = fixture(t, [{ status: 'connected', permission: true }]);
  await b.session.open('fixture');
  const original = await b.session.snapshot();
  await assert.rejects(a.session.open('fixture'), /OAuth/);
  await a.session.open('fixture');
  const sibling = await b.session.snapshot();
  assert.equal(sibling.connection, original.connection);
  assert.deepEqual(sibling.pending, original.pending);
  assert.equal(sibling.pending[0].status, 'pending');
  assert.equal(b.state.runs.length, 1);
  assert.equal(b.state.runs[0].closed, false);
});

test('a candidate authentication failure retains the existing configuration transaction outcome', async t => {
  const { session, state, config } = fixture(t, [{ status: 'connected' }, { status: 'needs-auth' }]);
  await session.open('fixture');
  const prepared = await session.prepareExecution();
  const source = join(config.cwd, 'replacement.json');
  writeFileSync(source, JSON.stringify({ mcpServers: { identity: { type: 'sse', url: 'https://example.invalid/replacement' } } }));
  const operation = randomUUID();
  await assert.rejects(session.applyExecution({ operation, baseline: prepared.configuration_baseline, api_key: 'fixture',
    mcp_selections: [{ ...config.mcp_selections[0], source_path: source }] }), /original binding retained/);
  const observed = await session.snapshot();
  assert.deepEqual(observed.config, config);
  assert.equal(observed.configuration.operation, operation);
  assert.equal(observed.configuration.status, 'not_sent');
  assert.equal(state.runs.length, 2);
  assert.equal(state.runs.every(run => run.closed), true);
});

process.on('exit', () => sdk.restore());
