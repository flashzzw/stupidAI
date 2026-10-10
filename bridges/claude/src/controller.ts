import { chmodSync, closeSync, fstatSync, lstatSync, openSync, readFileSync, realpathSync, unlinkSync } from 'node:fs';
import { createConnection, createServer, type Socket } from 'node:net';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import type { Readable, Writable } from 'node:stream';
import { NotSent, protocolVersion, record, text } from './protocol.js';

type Handler = (method: string, params: Record<string, unknown>) => Promise<unknown>;

// A disconnected channel can still have an accepted operation settling. Keep
// its ownership until that operation ends; never replay it on the next channel.
export function serveChannel(input: Readable, output: Writable, handle: Handler, onClosed: () => void) {
  const lines = createInterface({input, crlfDelay: Infinity});
  let pending = 0, ended = false, released = false;
  const release = () => {
    if (ended && pending === 0 && !released) { released = true; onClosed(); }
  };
  const write = (value: unknown) => {
    if (!output.destroyed && !output.writableEnded) output.write(JSON.stringify(value) + '\n');
  };
  output.on('error', () => { input.destroy(); });
  input.on('error', () => { lines.close(); });
  input.on('close', () => { lines.close(); });
  lines.on('close', () => { ended = true; release(); });
  lines.on('line', line => {
    pending++;
    void (async () => {
      let id: unknown;
      try {
        if (Buffer.byteLength(line) > 2_000_000) throw new NotSent('bridge request exceeds limit');
        const request = record(JSON.parse(line));
        id = request.id;
        if (request.v !== protocolVersion || typeof id !== 'string') throw new NotSent('incompatible bridge envelope');
        const result = await handle(text(request.method, 'method'), record(request.params));
        write({v: protocolVersion, id, result});
      } catch (error) {
        write({v: protocolVersion, id, error: {not_sent: error instanceof NotSent,
          message: error instanceof NotSent ? error.message : 'SDK bridge operation failed; reconcile original execution'}});
      } finally { pending--; release(); }
    })();
  });
}

// Linux/WSL socket permissions and SO_PEERCRED on the Go side bind this endpoint
// to its owner. A directory FD keeps long state paths within sockaddr_un limits.
export async function openController(root: string, binding: string, handle: Handler, shutdown: () => void) {
  if (!/^[a-f0-9]{64}$/.test(binding)) throw new NotSent('missing controller instance binding');
  root = realpathSync(root);
  const inherited = fstatSync(3), lock = lstatSync(join(root, 'bridge.lock'));
  if (!inherited.isFile() || inherited.dev !== lock.dev || inherited.ino !== lock.ino) throw new NotSent('controller has no inherited instance lock');
  const dir = openSync(root, 'r');
  const path = join(root, 'controller.sock');
  const address = `/proc/self/fd/${dir}/controller.sock`;
  const peers = new Set<Socket>();
  let owner: Socket | undefined, connected = false, closing = false;
  const stat = readFileSync('/proc/self/stat', 'utf8');
  const birth = stat.slice(stat.lastIndexOf(') ') + 2).split(' ')[19];
  const identity = {version: 1, binding, pid: process.pid, birth, state_dir: root};
  const server = createServer(socket => {
    peers.add(socket);
    let authenticated = false;
    socket.setTimeout(3000, () => socket.destroy());
    serveChannel(socket, socket, async (method, params) => {
      if (closing) throw new NotSent('controller is closing');
      if (!authenticated) {
        if (method !== 'hello' || params.controller_binding !== binding || owner) {
          setImmediate(() => socket.destroy());
          throw new NotSent('controller is occupied or its instance binding does not match');
        }
        owner = socket;
        authenticated = true;
        connected = true;
        socket.setTimeout(0);
      }
      if (method === 'shutdown') {
        closing = true;
        setImmediate(shutdown);
        return {closing: true};
      }
      return handle(method, params);
    }, () => { peers.delete(socket); if (owner === socket) owner = undefined; });
  });
  try {
    let previous;
    try { previous = lstatSync(path); } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error; }
    if (previous) {
      if (!previous.isSocket() || previous.uid !== process.getuid!()) throw new NotSent('controller endpoint is not an owned socket');
      const unused = await new Promise<boolean>((resolve, reject) => {
        const probe = createConnection(address);
        probe.setTimeout(1000, () => { probe.destroy(); reject(new NotSent('existing controller endpoint remains unverified')); });
        probe.once('connect', () => { probe.destroy(); resolve(false); });
        probe.once('error', error => { if ((error as NodeJS.ErrnoException).code === 'ECONNREFUSED') resolve(true); else reject(error); });
      });
      const current = lstatSync(path);
      if (!unused || current.dev !== previous.dev || current.ino !== previous.ino) throw new NotSent('existing controller endpoint is still live or changed');
      unlinkSync(path);
    }
    await new Promise<void>((resolve, reject) => {
      server.once('error', reject);
      server.listen(address, () => { server.off('error', reject); resolve(); });
    });
    chmodSync(path, 0o600);
  } catch (error) { server.close(); closeSync(dir); throw error; }
  server.on('error', shutdown);
  return {
    identity,
    get connected() { return connected; },
    async close() {
      closing = true;
      await new Promise<void>(resolve => {
        server.close(() => resolve());
        for (const socket of peers) socket.destroy();
      });
      closeSync(dir);
    },
  };
}
