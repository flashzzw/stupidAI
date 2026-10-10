import { mkdirSync } from 'node:fs';
import { Session } from './session.js';
import { discover, inspectNative } from './discovery.js';
import { readNativeHistory } from './native-history.js';
import { NotSent, protocolVersion, sessionConfig, text, uuid } from './protocol.js';
import { openController, serveChannel } from './controller.js';

const stateDir = text(process.argv[2], 'bridge state directory');
mkdirSync(stateDir, { recursive: true, mode: 0o700 });
const sessions = new Map<string, Session>();
let stopping = false;
let controller: Awaited<ReturnType<typeof openController>> | undefined;

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
  if (method === 'hello') return { version: protocolVersion, sdk: '0.3.280', settings_file_consumer: 1, installed_settings: 1, installed_settings_recovery: 1, original_query_recovery: 1, pending_permissions: 1,
    ...(controller ? {persistent_control: 1, controller: controller.identity} : {}) };
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

async function close() {
  if (stopping) return;
  stopping = true;
  await Promise.allSettled([...sessions.values()].map(session => session.close()));
  await controller?.close();
  process.exit(0);
}
if (process.argv[3] === '--persistent-control') {
  controller = await openController(stateDir, text(process.env.NETCAP_CLAUDE_CONTROLLER_BINDING, 'controller binding'), handle, () => void close());
}
serveChannel(process.stdin, process.stdout, async (method, params) => {
  if (controller && method !== 'hello') throw new NotSent('persistent bridge requires its controller connection');
  return handle(method, params);
}, () => { if (!controller?.connected) void close(); });
process.on('SIGTERM', () => void close());
process.on('SIGINT', () => void close());
