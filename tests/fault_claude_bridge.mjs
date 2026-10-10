// Test-only NDJSON wrapper around the real production bridge.
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { readFileSync, writeFileSync, appendFileSync, existsSync } from 'node:fs';
import { join, resolve } from 'node:path';
const root = process.env.NETCAP_CLAUDE_FAULT_DIR;
if (!root?.startsWith('/tmp/netcap-console-')) throw Error('isolated fixture required');
const bridge = resolve(import.meta.dirname, '../bridges/claude/dist/main.js');
const child = spawn(process.execPath, [bridge, process.argv[2]], { env: process.env, stdio: ['pipe','pipe','ignore',3] });
writeFileSync(join(root,'owner.json'),JSON.stringify({wrapper:process.pid,bridge:child.pid}));
const methods = new Map();
const armed = () => existsSync(join(root,'armed.json')) ? JSON.parse(readFileSync(join(root,'armed.json'),'utf8')) : {};
const input = createInterface({input:process.stdin});
input.on('line', line => {
  const request = JSON.parse(line);
  methods.set(request.id, request.method);
  if (request.method === 'apply_execution') {
    appendFileSync(join(root,'applications.jsonl'),JSON.stringify({operation:request.params.operation,native_id:request.params.native_id})+'\n');
    if (armed().mode === 'crash_applying') {
      const timer = setInterval(() => {
        const metadata = join(process.argv[2],request.params.native_id+'.json');
        if (!existsSync(metadata)) return;
        const stored = JSON.parse(readFileSync(metadata,'utf8'));
        if (stored._configuration?.status !== 'applying') return;
        clearInterval(timer);
        process.kill(child.pid,'SIGSTOP');
        writeFileSync(join(root,'intercepted.json'),JSON.stringify({phase:'applying',operation:request.params.operation}));
      },1);
    }
  }
  child.stdin.write(line+'\n');
});
createInterface({input:child.stdout}).on('line', line => {
  const response=JSON.parse(line),method=methods.get(response.id),fault=armed();
  if (method==='apply_execution' && response.result && fault.mode==='drop_verified') {
    writeFileSync(join(root,'armed.json'),JSON.stringify({mode:'hide_proof'}));
    writeFileSync(join(root,'intercepted.json'),JSON.stringify({phase:'verified'}));
    process.stdout.write(JSON.stringify({v:1,id:response.id,error:{not_sent:false,message:'fixture: verified apply reply lost'}})+'\n');
    return;
  }
  if (method==='snapshot' && response.result && fault.mode==='hide_proof') delete response.result.configuration;
  process.stdout.write(JSON.stringify(response)+'\n');
});
input.on('close',()=>child.stdin.end());
process.on('SIGTERM',()=>child.stdin.end());
child.on('exit',code=>process.exit(code??0));
