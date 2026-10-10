// Test-only preload: retain exact metadata requests/replies from owned native
// children without changing their bytes, descriptors, or process identity.
import cp from 'node:child_process';
import fs from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { StringDecoder } from 'node:string_decoder';

const output = process.env.NETCAP_SETTINGS_WIRE;
if (output) {
  const spawn = cp.spawn;
  cp.spawn = function (command, args, options) {
    const child = spawn.call(this, command, args, options);
    if (!Array.isArray(args) || !(process.env.NETCAP_SETTINGS_WIRE_MODE === 'session' ? args.includes('--input-format') : args.includes('--bare'))) return child;
    const settingsFile = args[args.indexOf('--settings') + 1];
    const pending = { request: '', response: '' };
    const decoders = { request: new StringDecoder('utf8'), response: new StringDecoder('utf8') };
    const capture = (direction, chunk) => {
      pending[direction] += typeof chunk === 'string' ? chunk : decoders[direction].write(chunk);
      let end;
      while ((end = pending[direction].indexOf('\n')) >= 0) {
        const line = pending[direction].slice(0, end);
        pending[direction] = pending[direction].slice(end + 1);
        if (!line) continue;
        const row = { pid: child.pid, settings_file: settingsFile, direction, message: JSON.parse(line) };
        fs.appendFileSync(output, JSON.stringify(row) + '\n', { mode: 0o600 });
      }
    };
    child.stdout.on('data', chunk => capture('response', chunk));
    const write = child.stdin.write;
    child.stdin.write = function (chunk, ...args) { capture('request', chunk); return write.call(this, chunk, ...args); };
    return child;
  };
  syncBuiltinESMExports();
}
