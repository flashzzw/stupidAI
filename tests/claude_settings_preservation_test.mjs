import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, openSync, closeSync, existsSync } from 'node:fs';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

for (const mode of ['preserved', 'key-order', 'other-plugin', 'effective', 'source-only', 'applied', 'permission-rule', 'permission-directory', 'permission-managed', 'missing-permissions', 'invalid-rule', 'invalid-directory', 'foreign-cwd', 'permission-errors', 'missing-applied', 'baseline-schema', 'baseline-fatal', 'candidate-fatal', 'candidate-close-fd']) {
  test(`native settings preservation: ${mode}`, async t => {
    const root = mkdtempSync(join(tmpdir(), 'netcap-settings-preservation-'));
    t.diagnostic(`Evidence directory: ${root}`);
    const cli = join(root, 'claude'), original = join(root, 'original.json'), candidate = join(root, 'candidate.json');
    const settings = { enabledPlugins: { 'selected@local': true, 'retained@local': false }, env: { OWNED_PRIVATE: 'PRIVATE_PRESERVATION_VALUE' } };
    writeFileSync(original, JSON.stringify(settings), { mode: 0o600 });
    writeFileSync(candidate, JSON.stringify({ ...settings, enabledPlugins: { ...settings.enabledPlugins, 'selected@local': false } }), { mode: 0o600 });
    writeFileSync(cli, `#!/usr/bin/python3
import json,os,sys
from pathlib import Path
root=Path(${JSON.stringify(root)})
mode=${JSON.stringify(mode)}
if '--version' in sys.argv:
 print('2.1.280 (Claude Code)');raise SystemExit
path=Path(sys.argv[sys.argv.index('--settings')+1]);candidate=path.name=='candidate.json'
with (root/'pids.jsonl').open('a') as f:f.write(json.dumps(os.getpid())+'\\n')
assert '--bare' in sys.argv and '--no-session-persistence' in sys.argv
for line in sys.stdin:
 r=json.loads(line);sub=r['request']['subtype']
 if sub=='get_settings' and (mode=='baseline-fatal' and not candidate or mode=='candidate-fatal' and candidate):
  print(json.dumps({'type':'control_request','request_id':'unexpected','request':{'subtype':'can_use_tool'}}),flush=True)
  continue
 if sub=='initialize':value={}
 elif sub=='get_settings':
  effective=json.loads(path.read_text());source=json.loads(path.read_text())
  applied={'model':'fixture','effort':'high'}
  if candidate and mode=='other-plugin':effective['enabledPlugins']['retained@local']=True
  if candidate and mode=='effective':effective['env']['OWNED_PRIVATE']='PRIVATE_CHANGED_VALUE'
  if candidate and mode=='source-only':source['permissions']={'deny':['Read(/owned/**)']}
  if candidate and mode=='applied':applied['effort']='low'
  if candidate and mode=='key-order':
   effective=dict(reversed(list(effective.items())));source=dict(reversed(list(source.items())))
  value={'effective':effective,'sources':[{'source':'flagSettings','settings':source}],'applied':applied}
  if mode=='missing-applied':value.pop('applied')
  if not candidate and mode=='baseline-schema':value['errors']=[{'file':str(path),'path':'permissions.allow','message':'PRIVATE_PRESERVATION_VALUE'}]
 elif sub=='get_hooks_listing':value={'bareMode':{}}
 elif sub=='mcp_status':value={'mcpServers':[]}
 elif sub=='list_permission_rules':
  state={'rules':[{'behavior':'deny','source':'flagSettings','rule':'Read(/owned/**)','editability':'readonly'}],'workspaceDirectories':[],'originalCwd':str(root),'managedOnly':False}
  if candidate and mode=='permission-rule':state['rules'][0]['behavior']='allow'
  if candidate and mode=='permission-directory':state['workspaceDirectories']=[{'path':'/owned/extra'}]
  if candidate and mode=='permission-managed':state['managedOnly']=True
  if mode=='invalid-rule':state['rules']=[42]
  if mode=='invalid-directory':state['workspaceDirectories']=[None]
  if candidate and mode=='candidate-close-fd':os.close(3)
  if mode=='foreign-cwd':state['originalCwd']='/foreign'
  if mode=='permission-errors':state['errors']=[{'file':str(path),'path':'permissions','message':'PRIVATE_PRESERVATION_VALUE'}]
  value={} if mode=='missing-permissions' else {'state':state}
 else:raise AssertionError(sub)
 response={'type':'control_response','response':{'subtype':'success','request_id':r['request_id'],'response':value}}
 with (root/'wire.jsonl').open('a') as f:f.write(json.dumps({'candidate':candidate,'request':r,'response':response})+'\\n')
 print(json.dumps(response),flush=True)
`, { mode: 0o700 });
    const fd = openSync(candidate, 'r');
    const result = await new Promise((resolve, reject) => {
      const child = spawn(process.execPath, [fileURLToPath(new URL('../bridges/claude/dist/settings_validation.js', import.meta.url)), '--stdio'], { cwd: root, stdio: ['pipe', 'pipe', 'pipe', fd] });
      closeSync(fd);
      let stdout = '', stderr = '';
      child.stdout.on('data', data => { stdout += data; });
      child.stderr.on('data', data => { stderr += data; });
      child.on('error', reject);
      child.on('close', code => resolve({ code, stdout, stderr }));
      child.stdin.end(JSON.stringify({ executable: cli, cwd: root, settings_file: candidate, model: 'fixture', baseline_settings_file: original, plugin_settings: [{ source_key: 'selected@local', enabled: false }] }));
    });
    writeFileSync(join(root, 'result.json'), JSON.stringify(result, null, 2));
    if (existsSync(join(root, 'pids.jsonl'))) {
      for (const pid of readFileSync(join(root, 'pids.jsonl'), 'utf8').trim().split('\n').map(JSON.parse)) assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' });
    }
    assert.equal(/PRIVATE_PRESERVATION_VALUE|PRIVATE_CHANGED_VALUE|Read\(\/owned/.test(result.stdout), false);
    if (['missing-permissions', 'invalid-rule', 'invalid-directory', 'foreign-cwd', 'permission-errors', 'missing-applied', 'baseline-fatal', 'candidate-fatal', 'candidate-close-fd'].includes(mode)) {
      assert.equal(result.code, 1);
      return;
    }
    assert.equal(result.code, 0);
    const output = JSON.parse(result.stdout), comparison = output.preservation;
    assert.ok(comparison, 'original and derived native observations are required');
    assert.equal(comparison.baseline_settings_file, original);
    assert.deepEqual(comparison.source_keys, ['selected@local']);
    assert.equal(comparison.matches, ['preserved', 'key-order'].includes(mode));
    const expected = { 'other-plugin': 'effective_settings', effective: 'effective_settings', 'source-only': 'source_settings', applied: 'applied_settings', 'permission-rule': 'permission_rules', 'permission-directory': 'permission_rules', 'permission-managed': 'permission_rules', 'baseline-schema': 'schema' }[mode];
    assert.deepEqual(comparison.differences, expected ? [expected] : []);
    assert.equal(output.plugin_settings[0].matches, true);
    const wire = readFileSync(join(root, 'wire.jsonl'), 'utf8').trim().split('\n').map(JSON.parse);
    assert.equal(wire.filter(row => row.request.request.subtype === 'list_permission_rules').length, 2);
    assert.ok(wire.every(row => row.request.type === 'control_request'));
  });
}
