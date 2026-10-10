import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, existsSync, chmodSync, openSync, closeSync } from 'node:fs';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { validateNativeSettings } from '../bridges/claude/dist/settings_validation.js';

for (const mode of ['valid', 'invalid-schema', 'not-bare', 'unexpected-mcp', 'native-request', 'hook-event', 'hook-progress', 'model-result', 'ignores-term', 'oversized', 'wrong-version']) {
  test(`native settings transport: ${mode}`, async () => {
    const root = mkdtempSync(join(tmpdir(), 'netcap-settings-transport-'));
    const cwd = join(root, 'work'), cli = join(root, 'claude');
    mkdirSync(cwd);
    writeFileSync(cli, `#!/usr/bin/python3
import json,os,signal,sys
from pathlib import Path
root=Path(${JSON.stringify(root)})
mode=${JSON.stringify(mode)}
if '--version' in sys.argv:
 print('other version' if mode=='wrong-version' else '2.1.280 (Claude Code)')
 raise SystemExit(0)
assert '--bare' in sys.argv and '--no-session-persistence' in sys.argv
assert '--setting-sources' in sys.argv and 'user,project,local' in sys.argv
(root/'pid').write_text(str(os.getpid()))
if mode=='ignores-term':signal.signal(signal.SIGTERM,signal.SIG_IGN)
for line in sys.stdin:
 request=json.loads(line)
 assert request['type']=='control_request'
 subtype=request['request']['subtype']
 with (root/'requests.jsonl').open('a') as f:f.write(json.dumps(request)+'\\n')
 if mode in ('native-request','ignores-term'):
  print(json.dumps({'type':'control_request','request_id':'unexpected','request':{'subtype':'can_use_tool'}}),flush=True)
  continue
 if mode in ('hook-event','model-result'):
  print(json.dumps({'type':'result'} if mode=='model-result' else {'type':'system','subtype':'hook_started'}),flush=True)
  continue
 if mode=='oversized':
  print(json.dumps({'padding':'x'*(16<<20)}),flush=True)
  continue
 if subtype=='initialize':result={}
 elif subtype=='get_settings':
  settings={'env':{'PRIVATE_KEY':'PRIVATE_SETTINGS_VALUE'}}
  result={'effective':settings,'sources':[{'source':'userSettings','settings':settings}]}
  if mode=='invalid-schema':result['errors']=[{'file':str(root/'bad.json'),'path':'permissions.allow','message':'PRIVATE_SETTINGS_VALUE'}]
 elif subtype=='get_hooks_listing':result={} if mode=='not-bare' else {'bareMode':{'exitHint':'restart without --bare'}}
 elif subtype=='mcp_status':result={'mcpServers':[{'name':'unexpected'}] if mode=='unexpected-mcp' else []}
 else:raise AssertionError(subtype)
 print(json.dumps({'type':'control_response','response':{'subtype':'success','request_id':request['request_id'],'response':result}}),flush=True)
 if mode=='hook-progress' and subtype=='get_settings':
  print(json.dumps({'type':'system','subtype':'hook_progress','hook_id':'unexpected'}),flush=True)
if mode=='ignores-term':
 while True:signal.pause()
`);
    chmodSync(cli, 0o700);
    const call = validateNativeSettings({ executable: cli, cwd, settings_file: '', model: 'fixture' });
    if (mode === 'valid' || mode === 'invalid-schema') {
      const result = await call;
      assert.equal(result.schema_valid, mode === 'valid');
      assert.equal(result.process_stopped, true);
      assert.equal(result.no_session_persistence, true);
      assert.equal(JSON.stringify(result).includes('PRIVATE_SETTINGS_VALUE'), false);
      if (mode === 'invalid-schema') assert.equal(result.errors[0].field, 'permissions.allow');
      const requests = readFileSync(join(root, 'requests.jsonl'), 'utf8').trim().split('\n').map(JSON.parse);
      assert.deepEqual(requests.map(row => row.request.subtype), ['initialize', 'get_settings', 'get_hooks_listing', 'mcp_status']);
    } else {
      await assert.rejects(call, error => {
        assert.ok(error instanceof Error);
        assert.equal(error.message.includes('PRIVATE_SETTINGS_VALUE'), false);
        return true;
      });
    }
    if (existsSync(join(root, 'pid'))) {
      const pid = Number(readFileSync(join(root, 'pid'), 'utf8'));
      assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' });
    } else assert.equal(mode, 'wrong-version');
  });
}

for (const mode of ['enabled', 'disabled', 'missing', 'wrong-type', 'close-fd', 'foreign-file', 'bad-key']) {
  test(`plugin flag transport: ${mode}`, async t => {
    const root = mkdtempSync(join(tmpdir(), 'netcap-plugin-settings-transport-'));
    t.diagnostic(`Evidence directory: ${root}`);
    const cli = join(root, 'claude'), settings = join(root, 'settings.json'), held = join(root, 'held.json');
    const sourceKey = 'selected@local', enabled = mode === 'enabled';
    const flags = mode === 'missing' ? {} : { [sourceKey]: mode === 'wrong-type' ? null : enabled };
    writeFileSync(settings, JSON.stringify({ enabledPlugins: flags, env: { PRIVATE_KEY: 'PRIVATE_PREVIEW_SETTING' } }), { mode: 0o600 });
    writeFileSync(held, '{}', { mode: 0o600 });
    writeFileSync(cli, `#!/usr/bin/python3
import json,os,sys
from pathlib import Path
root=Path(${JSON.stringify(root)})
mode=${JSON.stringify(mode)}
if '--version' in sys.argv:
 print('2.1.280 (Claude Code)');raise SystemExit
(root/'pid').write_text(str(os.getpid()))
assert '--bare' in sys.argv and '--no-session-persistence' in sys.argv
if mode=='close-fd':os.close(3)
for line in sys.stdin:
 r=json.loads(line);sub=r['request']['subtype']
 if sub=='initialize':value={}
 elif sub=='get_settings':value={'effective':json.loads((root/'settings.json').read_text()),'sources':[]}
 elif sub=='get_hooks_listing':value={'bareMode':{}}
 elif sub=='mcp_status':value={'mcpServers':[]}
 else:raise AssertionError(sub)
 print(json.dumps({'type':'control_response','response':{'subtype':'success','request_id':r['request_id'],'response':value}}),flush=True)
`, { mode: 0o700 });
    const fd = openSync(mode === 'foreign-file' ? held : settings, 'r');
    const result = await new Promise((resolve, reject) => {
      const child = spawn(process.execPath, [fileURLToPath(new URL('../bridges/claude/dist/settings_validation.js', import.meta.url)), '--stdio'], { cwd: root, stdio: ['pipe', 'pipe', 'pipe', fd] });
      closeSync(fd);
      let stdout = '', stderr = '';
      child.stdout.on('data', data => { stdout += data; });
      child.stderr.on('data', data => { stderr += data; });
      child.on('error', reject);
      child.on('close', code => resolve({ code, stdout, stderr }));
      child.stdin.end(JSON.stringify({ executable: cli, cwd: root, settings_file: settings, model: 'fixture', plugin_settings: [{ source_key: mode === 'bad-key' ? '../invalid' : sourceKey, enabled }] }));
    });
    writeFileSync(join(root, 'result.json'), JSON.stringify(result), { mode: 0o600 });
    assert.equal(result.stdout.includes('PRIVATE_PREVIEW_SETTING'), false);
    if (['enabled', 'disabled', 'missing', 'wrong-type'].includes(mode)) {
      assert.equal(result.code, 0);
      const out = JSON.parse(result.stdout);
      assert.equal(out.file_holder_observed, true);
      assert.equal(out.process_stopped, true);
      assert.deepEqual(out.plugin_settings, [{ source_key: sourceKey, requested_enabled: enabled, actual_enabled: ['missing', 'wrong-type'].includes(mode) ? null : enabled, matches: ['enabled', 'disabled'].includes(mode) }]);
    } else assert.equal(result.code, 1);
    if (existsSync(join(root, 'pid'))) {
      assert.throws(() => process.kill(Number(readFileSync(join(root, 'pid'), 'utf8')), 0), { code: 'ESRCH' });
    } else assert.ok(['foreign-file', 'bad-key'].includes(mode));
  });
}
