import { mkdirSync } from 'node:fs';
import { createInterface } from 'node:readline';
import { Session } from './session.js';
import { discover, inspectNative } from './discovery.js';
import { readNativeHistory } from './native-history.js';
import { NotSent, protocolVersion, record, sessionConfig, text, uuid } from './protocol.js';

const stateDir = text(process.argv[2], 'bridge state directory');
mkdirSync(stateDir, { recursive: true, mode: 0o700 });
const sessions = new Map<string, Session>();
let stopping = false;

function existing(params: Record<string, unknown>): Session {
  const id = uuid(params.native_id);
  let session = sessions.get(id);
  if (!session) {
    session = Session.restore(stateDir, id);
    sessions.set(id, session);
  }
  return session;
}

async function handle(method: string, params: Record<string, unknown>) {
  if (stopping) throw new NotSent('bridge is closing');
  if (method === 'hello') return { version: protocolVersion, sdk: '0.3.280', settings_file_consumer: 1, installed_settings: 1, installed_settings_recovery: 1, original_query_recovery: 1 };
  if (method === 'discover') return discover(params);
  if (method === 'inspect_native') return inspectNative(params);
  if (method === 'read_native_history') return readNativeHistory(params);
  if (method === 'create') {
    const config = sessionConfig(params);
    if (sessions.has(config.id)) return { native_id: config.id };
    const session = new Session(stateDir, config, true);
    sessions.set(config.id, session);
    await session.open(text(params.api_key, 'credential'));
    return { native_id: config.id };
  }
  const session = existing(params);
  switch (method) {
    case 'send': return session.send(params);
    case 'snapshot': return session.snapshot();
    case 'configuration_recovery': return session.configurationRecovery(params);
    case 'recover_execution': return session.recoverExecution(params);
    case 'skill_catalog': return session.skillCatalog();
    case 'hook_catalog': return session.hookCatalog();
    case 'permission_catalog': return session.permissionCatalog();
    case 'respond': return session.respond(params);
    case 'interrupt': return session.interrupt(params);
    case 'prepare_execution': return session.prepareExecution(params.settings_target, params.installed_target);
    case 'apply_execution': return session.applyExecution(params);
    default: throw new NotSent('unsupported bridge method');
  }
}

const input = createInterface({ input: process.stdin, crlfDelay: Infinity });
input.on('line', line => {
  void (async () => {
    let id: unknown;
    try {
      if (line.length > 2_000_000) throw new NotSent('bridge request exceeds limit');
      const request = record(JSON.parse(line));
      id = request.id;
      if (request.v !== protocolVersion || typeof id !== 'string') throw new NotSent('incompatible bridge envelope');
      const result = await handle(text(request.method, 'method'), record(request.params));
      process.stdout.write(JSON.stringify({ v: protocolVersion, id, result }) + '\n');
    } catch (error) {
      process.stdout.write(JSON.stringify({ v: protocolVersion, id, error: { not_sent: error instanceof NotSent,
        message: error instanceof NotSent ? error.message : 'SDK bridge operation failed; reconcile original execution' } }) + '\n');
    }
  })();
});

async function close() {
  if (stopping) return;
  stopping = true;
  await Promise.allSettled([...sessions.values()].map(session => session.close()));
  process.exit(0);
}
input.on('close', () => void close());
process.on('SIGTERM', () => void close());
process.on('SIGINT', () => void close());
