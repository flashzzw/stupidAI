// Probe the existing production Session close/reopen path after owned file edits.
// Direct empty-selection apply is a fixture, not Application settings adoption.
import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID, createHash } from 'node:crypto';

const request = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const id = randomUUID(), checks = {}, reports = {};
const config = {id,source:id,cwd:request.cwd,executable:'/home/zzw/.local/bin/claude',
  model:'claude-sonnet-4-6',endpoint:'http://127.0.0.1:1',permission:'read_only',probe:false,
  ...(request.source === 'custom' ? {settings_file:request.path} : {})};
const journal = join(request.root, 'journal'); mkdirSync(journal);
const session = new Session(journal, config, true);
const check = (name, ok) => { checks[name] = Boolean(ok); };
const markers = () => existsSync(join(request.root, 'hooks.jsonl'))
  ? readFileSync(join(request.root, 'hooks.jsonl'),'utf8').trim().split('\n').filter(Boolean).map(JSON.parse) : [];
const hash = () => createHash('sha256').update(readFileSync(request.path)).digest('hex');
let error = '';
try {
  await session.open('fixture-no-model');
  reports.before = await session.snapshot();
  reports.before_hooks = await session.hookCatalog();
  reports.before_markers = markers();
  reports.before_hash = hash();
  writeFileSync(request.path, JSON.stringify(request.target, null, 2));
  await new Promise(resolve => setTimeout(resolve, request.wait_ms));
  reports.changed_hooks = await session.hookCatalog();
  reports.changed_hash = hash();
  reports.prepared = await session.prepareExecution();
  reports.before_apply_markers = markers();
  reports.receipt = await session.applyExecution({operation:randomUUID(),baseline:reports.prepared.configuration_baseline,
    mcp_selections:[],skill_selections:[],plugin_selections:[],api_key:'fixture-no-model'});
  reports.after = await session.snapshot();
  reports.after_hooks = await session.hookCatalog();
  reports.after_apply_markers = markers();
  check('same_native_id', reports.receipt.native_id === id && session.config.id === id);
  check('file_changed', reports.before_hash !== reports.changed_hash);
  check('prepare_does_not_restart', JSON.stringify(reports.before_markers.filter(h=>h.event==='SessionStart')) === JSON.stringify(reports.before_apply_markers.filter(h=>h.event==='SessionStart')));
  if (request.target.hooks.ConfigChange) check('no_config_hook_before_explicit_action', !reports.before_apply_markers.some(h=>h.event==='ConfigChange'));
  check('new_query_connection', reports.before.connection !== reports.after.connection);
  check('history_unchanged', reports.before.history_digest === reports.after.history_digest);
  check('durable_verified_restart', reports.after.configuration?.status === 'verified');
  check('startup_v1_then_v2', reports.after_apply_markers.filter(h=>h.event==='SessionStart').map(h=>h.version).join(',') === 'v1,v2');
  const ends = reports.after_apply_markers.filter(h=>h.event==='SessionEnd');
  check('close_uses_original_hook', ends.length === 1 && ends[0].version === 'v1');
  check('no_model_input_or_result', !reports.after.entries.some(e=>e.kind==='input'||e.event?.type==='result'));
} catch (e) { error = e.message; check('session_sequence_completed', false); }
finally {
  try { await session.close(); check('closed', true); } catch (e) { error += '; close: '+e.message; check('closed', false); }
  reports.final_markers = markers();
  reports.journal = readFileSync(join(journal,id+'.jsonl'),'utf8').trim().split('\n').filter(Boolean).map(JSON.parse);
  check('startup_hooks_have_native_success', reports.journal.filter(e=>e.kind==='event'&&e.event.subtype==='hook_started').every(e=>reports.journal.some(r=>r.kind==='event'&&r.event.subtype==='hook_response'&&r.event.hook_id===e.event.hook_id&&r.event.exit_code===0&&r.event.outcome==='success'&&r.event.session_id===id)));
  const result = {checks,reports,error,native_id:id,passed:Object.values(checks).every(Boolean),
    scope:'existing production Session/SDK/native query lifecycle; empty selection restart fixture, no Application adoption or model input'};
  writeFileSync(join(request.root,'result.json'),JSON.stringify(result,null,2));
  process.stdout.write(JSON.stringify({checks,error,markers:reports.final_markers})+'\n');
  if (!result.passed) process.exitCode = 1;
}
