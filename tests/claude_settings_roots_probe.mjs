// Compare only owned marker settings. Registry replies are controlled here;
// the native CLI comparison is separate and uses its own startup path.
import cp from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { promisify } from 'node:util';
import { EventEmitter } from 'node:events';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';

const request = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const reads = [];
cp.execFile = (command, ...args) => {
  const callback = args.pop();
  if (typeof callback !== 'function' || !String(command).toLowerCase().endsWith('reg.exe')) throw Error('unexpected resolver subprocess');
  reads.push({command:'reg.exe', hive:JSON.stringify(args).includes('HKLM')?'hklm':'hkcu'});
  const error = Object.assign(new Error('controlled registry absence'), {code:1});
  queueMicrotask(()=>callback(error,'','ERROR: The system was unable to find the specified registry key or value.'));
  return new EventEmitter();
};
Object.defineProperty(cp.execFile,promisify.custom,{value:(...args)=>new Promise((resolve,reject)=>cp.execFile(...args,(error,stdout,stderr)=>error?reject(Object.assign(error,{stdout,stderr})):resolve({stdout,stderr})))});
syncBuiltinESMExports();
const {resolveSettings} = await import('../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs');
const sdk = JSON.parse(readFileSync(new URL('../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/package.json',import.meta.url),'utf8'));
if (sdk.version !== '0.3.280') throw Error('revalidate SDK pin');
const value = await resolveSettings({cwd:request.cwd,settingSources:['user','project','local']});
const markers = env => Object.fromEntries(Object.entries(env ?? {}).filter(([key])=>key.startsWith('NETCAP_ROOTS_')));
writeFileSync(request.output,JSON.stringify({sdk:sdk.version,hook_executed:existsSync(request.trace),markers:markers(value.effective.env),sources:value.sources.map(s=>({source:s.source,path:s.path,markers:markers(s.settings.env)})),registry_reads:reads},null,2));
