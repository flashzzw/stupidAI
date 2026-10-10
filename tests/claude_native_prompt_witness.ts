import type { EngineInterface, Register } from 'claude-code';

const root = '__OWNED_CHANNEL_ROOT__';
let sequence = 0;
let generation = 'unstarted';
const configuredMode: string = '__REVOKE_QUEUED__';
const revokeQueued = configuredMode === 'yes';
let manualTurn: string | undefined;

async function record($: EngineInterface, kind: string, data: unknown) {
  const seq = ++sequence;
  const row = {generation, seq, at: await $.clock.now(), kind, data};
  await $.fs.write(`${root}/witness/events/${generation}-${String(seq).padStart(6, '0')}.json`, JSON.stringify(row));
}

export const register: Register = (on) => {
  on('session.start', async ($, e, next) => {
    generation = String(await $.clock.now());
    await record($, 'session.start', {event: e, id: await $.session.id(), pluginRoot: $.plugin.root});
    await $.fs.write(`${root}/witness/ready.json`, JSON.stringify({id: await $.session.id()}));
    return next(e);
  });
  on('prompt.submit', async ($, e, next) => {
    await record($, 'prompt.submit', e);
    if (revokeQueued && e.origin.kind === 'composer' && e.turnId !== undefined) {
      manualTurn = e.turnId;
      await record($, 'manual.running-input', e);
    }
    if (revokeQueued && manualTurn !== undefined && e.origin.kind === 'plugin' && e.origin.name === 'netcap-input-observer-fixture') {
      await record($, 'prompt.withdrawn', {input: e, manualTurn});
      return {drop: 'OWNED_PLUGIN_INPUT_WITHDRAWN_AFTER_MANUAL'};
    }
    const result = await next(e);
    await record($, 'prompt.result', {input: e, result});
    return result;
  });
};
