import assert from 'node:assert/strict';
import { PassThrough } from 'node:stream';
import { test } from 'node:test';
import { setImmediate as tick } from 'node:timers/promises';
import { serveChannel } from '../bridges/claude/dist/controller.js';

test('destroying a channel releases its connection record', async () => {
  const input = new PassThrough(), output = new PassThrough();
  let closed = 0;
  serveChannel(input, output, async () => ({}), () => { closed++; });
  input.destroy(); output.destroy();
  await tick();
  assert.equal(closed, 1);
});

test('a destroyed channel retains ownership until its accepted operation settles', async () => {
  const input = new PassThrough(), output = new PassThrough();
  const started = Promise.withResolvers(), result = Promise.withResolvers();
  let closed = 0, calls = 0;
  serveChannel(input, output, async () => { calls++; started.resolve(); return result.promise; }, () => { closed++; });
  input.write(JSON.stringify({v: 1, id: 'owned-command', method: 'send', params: {}}) + '\n');
  await started.promise;
  input.destroy(); output.destroy();
  await tick();
  assert.equal(closed, 0);
  result.resolve({native_id: 'original'});
  await tick();
  assert.equal(closed, 1);
  assert.equal(calls, 1);
});
