import type { EngineInterface, Register, Timer } from 'claude-code';

// This private file channel belongs to the probe, not to production control.
const root = '__OWNED_CHANNEL_ROOT__';
let sequence = 0;
let command = 1;
let busy = false;
let timer: Timer | undefined;
let activeTurn: string | null = null;
let generation = 'unstarted';

type Command = {id: number; op: string; text?: string};

async function record($: EngineInterface, kind: string, data: unknown) {
  const seq = ++sequence;
  const row = {generation, seq, at: await $.clock.now(), kind, data};
  await $.fs.write(`${root}/events/${generation}-${String(seq).padStart(6, '0')}.json`, JSON.stringify(row));
}

async function inspect($: EngineInterface) {
  const startedAt = await $.clock.now();
  const id = await $.session.id();
  const draft = await $.prompt.read();
  const cwd = await $.session.cwd();
  const projectRoot = await $.session.root();
  const surfaces = await $.session.surfaces();
  const turns = await $.session.turns();
  const messages = await $.session.messages();
  return {startedAt, finishedAt: await $.clock.now(), id, lastId: await $.session.id(),
    draft, cwd, projectRoot, surfaces, turns, messages, activeTurn};
}

async function execute($: EngineInterface, input: Command) {
  try {
    let result: unknown;
    if (input.op === 'inspect') result = await inspect($);
    else if (input.op === 'submit' && typeof input.text === 'string') {
      result = await $.prompt.submit({text: input.text});
    } else throw new Error('unknown owned command');
    await record($, 'command.return', {id: input.id, result});
    await $.fs.write(`${root}/responses/${input.id}.json`, JSON.stringify({id: input.id, result}));
  } catch (error) {
    const detail = {id: input.id, error: String(error)};
    await record($, 'command.error', detail);
    await $.fs.write(`${root}/responses/${input.id}.json`, JSON.stringify(detail));
  }
}

async function poll($: EngineInterface) {
  if (busy) return;
  busy = true;
  let accepted: number | undefined;
  try {
    const path = `${root}/commands/${command}.json`;
    if (!(await $.fs.exists(path))) return;
    const input = JSON.parse(await $.fs.read(path));
    if (input.id !== command) throw new Error('owned command sequence mismatch');
    accepted = command++;
    await record($, 'command.begin', input);
    // A queued native call may remain pending. Keep read-only inspection live
    // while its independent receipt is unknown; do not resubmit that command.
    void execute($, input);
  } catch (error) {
    const detail = {id: accepted, error: String(error)};
    await record($, 'command.error', detail);
    if (accepted !== undefined) {
      await $.fs.write(`${root}/responses/${accepted}.json`, JSON.stringify(detail));
    }
  } finally {
    busy = false;
  }
}

export const register: Register = (on) => {
  on('session.start', async ($, e, next) => {
    generation = String(await $.clock.now());
    await record($, 'session.start', {event: e, id: await $.session.id(), pluginRoot: $.plugin.root});
    timer?.cancel();
    timer = $.clock.every(50, () => { void poll($); });
    await $.fs.write(`${root}/ready.json`, JSON.stringify({id: await $.session.id()}));
    return next(e);
  });
  on('prompt.edit', async ($, e, next) => {
    await record($, 'prompt.edit', e);
    return next(e);
  });
  on('prompt.submit', async ($, e, next) => {
    await record($, 'prompt.submit', e);
    if (e.text === 'NETCAP_NATIVE_INPUT=hook_drop') return {drop: 'OWNED_HOOK_REFUSED'};
    if (e.text === 'NETCAP_NATIVE_INPUT=hook_throw') throw new Error('OWNED_HOOK_THROW');
    const result = await next(e);
    await record($, 'prompt.result', {input: e, result});
    return result;
  });
  on('turn.start', async ($, e, next) => {
    activeTurn = e.turnId;
    await record($, 'turn.start', e);
    return next(e);
  });
  on('turn.complete', async ($, e, next) => {
    await record($, 'turn.complete', e);
    if (activeTurn === e.turnId) activeTurn = null;
    return next(e);
  });
};
