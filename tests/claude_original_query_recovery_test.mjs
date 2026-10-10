// Production Session with real owned files and controlled SDK/process I/O.
// A failed original startup must not be confused with a started candidate.
import assert from 'node:assert/strict';
import {mock, test} from 'node:test';
import {createRequire} from 'node:module';
import {createHash, randomUUID} from 'node:crypto';
import {mkdtempSync, mkdirSync, writeFileSync, readFileSync, statSync, lstatSync, realpathSync, rmSync} from 'node:fs';
import {dirname, join} from 'node:path';
import {tmpdir} from 'node:os';
import {EventEmitter} from 'node:events';
import {PassThrough, Writable} from 'node:stream';

const require = createRequire(new URL('../bridges/claude/package.json', import.meta.url));
const states = new Map();
const rawPolicy = {disabledByPolicy:false, managedOnly:false, pluginOnly:false, allDisabled:false, policyHookCount:0};
mock.module('node:child_process', {namedExports:{spawn:() => {
  const child = new EventEmitter();
  child.pid = 123; child.killed = false; child.exitCode = null; child.signalCode = null;
  child.stdout = new PassThrough(); child.stderr = new PassThrough();
  child.stdin = new Writable({write(chunk, encoding, done) {
    const request = JSON.parse(chunk.toString());
    queueMicrotask(() => child.stdout.write(JSON.stringify({type:'control_response', response:{request_id:request.request_id, subtype:'success', response:{hooks:[], policy:rawPolicy}}}) + '\n'));
    done();
  }});
  child.kill = signal => {child.killed = true; child.signalCode = signal; child.stdout.end(); child.stderr.end(); queueMicrotask(() => {child.emit('exit', null, signal); child.emit('close', null, signal);}); return true;};
  return child;
}}});
mock.module(require.resolve('@anthropic-ai/claude-agent-sdk'), {namedExports:{
  getSessionMessages: async id => states.get(id).history,
  query: ({options}) => {
    const state = states.get(options.resume ?? options.sessionId);
    const metadata = JSON.parse(readFileSync(state.metadata));
    state.starts.push({settings:options.settings ?? null, original:state.config.settings_artifact?.file.path ?? null, metadata});
    const candidate = options.settings === state.target.artifact.file.path;
    if (candidate) {
      assert.ok(state.allowCandidate, 'recovery must never start the failed candidate');
      assert.deepEqual(metadata._configuration.progress, {version:1,phase:'candidate'}, 'candidate phase must be durable before query()');
      throw Error('OWNED_CANDIDATE_STARTUP_FAILURE');
    }
    if (state.failOriginal) throw Error('OWNED_ORIGINAL_STARTUP_FAILURE');
    const child = options.spawnClaudeCodeProcess({command:process.execPath, args:[], cwd:options.cwd, env:process.env});
    child.stdout.resume();
    let stopped = false, wake;
    return {
      initializationResult: async () => ({hooks_applied:true, plugins_applied:true}),
      reloadPlugins: async () => ({plugins:[{name:'selected', path:state.plugin, source:'selected@owned', version:'1'}, ...(state.inheritedDrift ? [{name:'unexpected',path:state.plugin,source:'unexpected@owned',version:'1'}] : [])], error_count:state.failInventory ? 1 : 0}),
      supportedCommands: async () => [], getContextUsage: async () => ({skills:{skillFrontmatter:[]}}),
      supportedAgents: async () => [], mcpServerStatus: async () => [], applyFlagSettings: async () => {},
      async *[Symbol.asyncIterator]() {while (!stopped) await new Promise(resolve => wake = resolve);},
      close() {stopped = true; wake?.(); child.kill('SIGTERM');},
    };
  },
}});
const {Session} = await import('../bridges/claude/dist/session.js');
const sha = body => createHash('sha256').update(body).digest('hex');
const key = stat => [stat.dev, stat.ino, stat.mode, stat.uid, stat.gid, stat.nlink, stat.size, stat.mtimeNs, stat.ctimeNs].map(String).join(':');
const file = path => ({path, resolved_path:realpathSync(path), kind:'file', mode:statSync(path).mode & 0o7777, sha256:sha(readFileSync(path))});
const startup = path => ({path, status:'read', sha256:sha(readFileSync(path)), bytes:statSync(path).size, resolved_path:realpathSync(path), identity_sha256:sha(JSON.stringify([realpathSync(path), key(statSync(path,{bigint:true})), key(lstatSync(path,{bigint:true}))]))});
const write = (path, body) => writeFileSync(path, typeof body === 'string' ? body : JSON.stringify(body), {mode:0o600});

function fixture(t, mode) {
  const persistent = process.env.NETCAP_ORIGINAL_RECOVERY_EVIDENCE;
  const root = mkdtempSync(join(persistent ?? tmpdir(), 'original-recovery-sdk-'));
  const id = randomUUID(), operation = randomUUID(), profile = join(root, 'profile'), plugin = join(root, 'plugin');
  mkdirSync(join(profile,'plugins'), {recursive:true, mode:0o700});
  mkdirSync(join(plugin,'.claude-plugin'), {recursive:true, mode:0o700});
  const savedProfile = process.env.CLAUDE_CONFIG_DIR, savedExec = process.env.NETCAP_CLAUDE_SETTINGS_EXEC;
  process.env.CLAUDE_CONFIG_DIR = profile; process.env.NETCAP_CLAUDE_SETTINGS_EXEC = process.execPath;
  const project = join(root,'settings.json'), local = join(root,'settings.local.json');
  for (const path of [project, local]) write(path, '{}');
  const registry = join(profile,'plugins/installed_plugins.json');
  write(registry, {version:2, plugins:{'selected@owned':[]}});
  const manifest = join(plugin,'.claude-plugin/plugin.json');
  write(manifest, {name:'selected', version:'1'});
  const selection = {asset_id:'', name:'selected', native_source:'selected@owned', mode:'enabled', version:'1', source_path:plugin, source_hash:sha('owned-tree'), root_mode:statSync(plugin).mode & 0o777,
    files:[{path:'.claude-plugin', kind:'directory', mode:statSync(dirname(manifest)).mode & 0o777}, {path:'.claude-plugin/plugin.json', kind:'file', mode:0o600, sha256:sha(readFileSync(manifest))}], components:[]};
  const binding = enabled => {
    const version = randomUUID(), path = join(root, '.netcap-settings-' + version + '.json');
    write(path, {enabledPlugins:{'selected@owned':enabled}});
    return {version:1, artifact:{id:version, original:{kind:'absent'}, file:file(path)}, selections:[{...selection, mode:enabled ? 'enabled' : 'disabled'}], source_files:[file(registry)], startup_files:[startup(project),startup(local)], startup_source_version:sha('original-observed-sources')};
  };
  const original = mode === 'installed' ? binding(true) : undefined;
  const target = binding(false);
  const config = {id, source:id, cwd:root, executable:process.execPath, model:'fixture', endpoint:'http://127.0.0.1:1', permission:'read_only', probe:false, settings_guard_version:1, settings_sources:[file(project),file(local)],
    ...(original ? {installed_settings:original, settings_artifact:original.artifact} : {})};
  const metadata = join(root, id + '.json');
  write(metadata, config);
  write(join(root,id+'.jsonl'), [{kind:'initialized',connection:'prior'},{kind:'closed',connection:'prior'}].map(entry => JSON.stringify(entry)).join('\n') + '\n');
  const state = {config, target, plugin, metadata, starts:[], history:[{type:'user', message:{role:'user',content:'owned original history'}}], failOriginal:true, completed:false, reports:{}};
  states.set(id, state);
  const session = Session.restore(root,id), sessions = [session];
  t.after(async () => {
    for (const session of sessions) await session.close();
    if (persistent) write(join(root,'result.json'), {scenario:t.name, mode, node:process.version, config, target, starts:state.starts, reports:state.reports, completed:state.completed, snapshot:await sessions.at(-1).snapshot()});
    else rmSync(root, {recursive:true, force:true});
    states.delete(id);
    if (savedProfile === undefined) delete process.env.CLAUDE_CONFIG_DIR; else process.env.CLAUDE_CONFIG_DIR = savedProfile;
    if (savedExec === undefined) delete process.env.NETCAP_CLAUDE_SETTINGS_EXEC; else process.env.NETCAP_CLAUDE_SETTINGS_EXEC = savedExec;
  });
  return {root,id,operation,session,sessions,state,target};
}

for (const mode of ['inherited','installed']) test('cold original query failure can explicitly recover ' + mode, async t => {
  const f = fixture(t,mode);
  const prepared = await f.session.prepareExecution(undefined,f.target);
  await assert.rejects(f.session.applyExecution({operation:f.operation, baseline:prepared.configuration_baseline, mcp_selections:[], skill_selections:[], plugin_selections:[], installed_settings:f.target, api_key:'fixture'}));
  const failed = await f.session.snapshot();
  f.state.reports.failed = failed;
  assert.equal(failed.configuration_failure.stage, 'open_original');
  assert.equal(failed.configuration_failure.cleanup, 'confirmed');
  assert.equal(failed.configuration.original_plugins, undefined);
  assert.deepEqual(failed.config, f.state.config);
  assert.equal(f.state.starts.length, 1);
  const check = await f.session.configurationRecovery({operation:f.operation, baseline:prepared.configuration_baseline});
  f.state.reports.check = check;
  assert.equal(check.checks.find(condition => condition.kind === 'original_settings').status, 'passed', 'failure before candidate startup needs its own recoverable original-query evidence');
  assert.ok(check.original_query_retry);
  f.state.failOriginal = false;
  const command = {operation:randomUUID(), original_operation:f.operation, baseline:prepared.configuration_baseline, original_query_retry:true, api_key:'fixture'};
  const receipt = await f.session.recoverExecution(command);
  f.state.reports.receipt = receipt;
  assert.equal(receipt.native_id, f.id);
  assert.ok(receipt.original_query_retry);
  const recovered = await f.session.snapshot();
  assert.equal(recovered.configuration.status, 'recovered');
  assert.equal(recovered.configuration.original_plugins, undefined, 'do not backfill an original inventory that was never observed');
  assert.deepEqual(recovered.config, f.state.config);
  assert.equal(recovered.configuration.recovery.plugins.packages[0].source, 'selected@owned');
  assert.equal(f.state.starts.length, 2);
  await f.session.close();
  const cold = Session.restore(f.root, f.id); f.sessions.push(cold);
  assert.deepEqual(await cold.recoverExecution(command), receipt);
  assert.equal(f.state.starts.length, 2, 'durable recovery must not replay');
  await cold.open('fixture');
  assert.equal(f.state.starts.length, 3);
  const originalSettings = mode === 'installed' ? f.state.config.settings_artifact.file.path : {permissions:{ask:['Bash','Write','Edit','NotebookEdit']}};
  for (const start of f.state.starts) assert.deepEqual(start.settings, originalSettings);
  assert.deepEqual(f.state.starts[0].metadata._configuration.progress, {version:1,phase:'original'});
  assert.equal(f.state.starts[1].metadata._configuration.recovery.status, 'recovering', 'persist recovery before opening the original');
  assert.deepEqual(f.state.starts[1].metadata._configuration.recovery.original_query_retry.failure, failed.configuration_failure);
  assert.equal((await cold.snapshot()).entries.some(entry => entry.kind === 'input'), false);
  f.state.completed = true;
});

async function originalFailure(t, mode = 'inherited') {
  const f = fixture(t, mode);
  const prepared = await f.session.prepareExecution(undefined, f.target);
  const apply = {operation:f.operation, baseline:prepared.configuration_baseline, mcp_selections:[], skill_selections:[], plugin_selections:[], installed_settings:f.target, api_key:'fixture'};
  await assert.rejects(f.session.applyExecution(apply));
  f.state.reports.failed = await f.session.snapshot();
  return {...f, apply, command:{operation:randomUUID(), original_operation:f.operation, baseline:prepared.configuration_baseline, original_query_retry:true, api_key:'fixture'}};
}

function restoreFixture(f, mutate) {
  const metadata = JSON.parse(readFileSync(f.state.metadata));
  const path = join(f.root, f.id + '.jsonl');
  const entries = readFileSync(path, 'utf8').trim().split('\n').map(line => JSON.parse(line));
  mutate(metadata._configuration, entries);
  write(f.state.metadata, metadata);
  write(path, entries.map(entry => JSON.stringify(entry)).join('\n') + '\n');
  const cold = Session.restore(f.root, f.id); f.sessions.push(cold);
  return cold;
}

const invalidEvidence = {
  'missing phase': change => delete change.progress,
  'unknown phase version': change => change.progress.version = 2,
  'candidate phase': change => change.progress.phase = 'candidate',
  'unavailable original inventory': change => change.original_plugins = {available:false},
  'null original inventory': change => change.original_plugins = null,
  'late failure stage': (_, entries) => entries.filter(e => e.kind === 'configuration_failure').forEach(e => e.failure.stage = 'open_candidate'),
  'unconfirmed cleanup': (_, entries) => entries.filter(e => e.kind === 'configuration_failure').forEach(e => e.failure.cleanup = 'unconfirmed'),
  'different failure operation': (_, entries) => entries.filter(e => e.kind === 'configuration_failure').forEach(e => e.failure.operation = randomUUID()),
  'different failure baseline': (_, entries) => entries.filter(e => e.kind === 'configuration_failure').forEach(e => e.failure.baseline = sha('different baseline')),
};
for (const [name, mutate] of Object.entries(invalidEvidence)) test('original query recovery rejects ' + name, async t => {
  const f = await originalFailure(t);
  const cold = restoreFixture(f, mutate);
  f.state.failOriginal = false;
  const check = await cold.configurationRecovery({operation:f.operation,baseline:f.apply.baseline});
  f.state.reports.check = check;
  assert.equal(check.original_query_retry, undefined);
  assert.ok(check.checks.some(c => c.status !== 'passed'));
  await assert.rejects(cold.recoverExecution(f.command));
  assert.equal(f.state.starts.length, 1);
  assert.equal((await cold.snapshot()).configuration.recovery, undefined);
  f.state.completed = true;
});

for (const value of [undefined, false, 'true']) test('original query recovery requires explicit boolean mode: ' + value, async t => {
  const f = await originalFailure(t);
  f.state.failOriginal = false;
  await assert.rejects(f.session.recoverExecution({...f.command,original_query_retry:value}));
  assert.equal(f.state.starts.length, 1);
  assert.equal((await f.session.snapshot()).configuration.recovery, undefined);
  f.state.completed = true;
});

test('a started candidate cannot become an original-query retry after deleting its inventory', async t => {
  const f = fixture(t, 'inherited');
  f.state.failOriginal = false; f.state.allowCandidate = true;
  const prepared = await f.session.prepareExecution(undefined, f.target);
  await assert.rejects(f.session.applyExecution({operation:f.operation,baseline:prepared.configuration_baseline,mcp_selections:[],skill_selections:[],plugin_selections:[],installed_settings:f.target,api_key:'fixture'}));
  assert.equal(f.state.starts.length, 2);
  const failed = await f.session.snapshot();
  f.state.reports.failed = failed;
  assert.equal(failed.configuration_failure.stage, 'open_candidate');
  assert.ok(failed.configuration.original_plugins.available);
  const cold = restoreFixture(f, change => delete change.original_plugins);
  const check = await cold.configurationRecovery({operation:f.operation,baseline:prepared.configuration_baseline});
  f.state.reports.check = check;
  assert.equal(check.original_query_retry, undefined);
  assert.equal(check.checks.find(c => c.kind === 'original_settings').status, 'blocked');
  await assert.rejects(cold.recoverExecution({operation:randomUUID(),original_operation:f.operation,baseline:prepared.configuration_baseline,original_query_retry:true,api_key:'fixture'}));
  assert.equal(f.state.starts.length, 2);
  f.state.completed = true;
});

test('recovery failure is durable and cannot replay after restart', async t => {
  const f = await originalFailure(t);
  await assert.rejects(f.session.recoverExecution(f.command));
  const failed = await f.session.snapshot();
  f.state.reports.recoveryFailed = failed;
  assert.equal(failed.configuration.recovery.status, 'recovering');
  assert.equal(failed.configuration_recovery_failure.operation, f.command.operation);
  assert.equal(failed.configuration_failure.operation, f.operation);
  assert.equal(f.state.starts.length, 2);
  f.state.failOriginal = false;
  const cold = restoreFixture(f, () => {});
  for (const command of [f.command, {...f.command,operation:randomUUID()}]) await assert.rejects(cold.recoverExecution(command));
  await assert.rejects(cold.open('fixture'));
  assert.equal(f.state.starts.length, 2);
  assert.deepEqual((await cold.snapshot()).config, f.state.config);
  f.state.completed = true;
});

test('successful original-query retry supplies the baseline for subsequent inherited components', async t => {
  const f = await originalFailure(t);
  f.state.failOriginal = false;
  await f.session.recoverExecution(f.command);
  await f.session.close();
  f.state.inheritedDrift = true;
  const cold = restoreFixture(f, () => {});
  await assert.rejects(cold.open('fixture'), 'new inherited packages must not bypass the saved recovery inventory');
  assert.equal((await cold.snapshot()).configuration.original_plugins, undefined);
  f.state.completed = true;
});

test('original component collection failure can recover before any candidate start', async t => {
  const f = fixture(t, 'inherited');
  f.state.failOriginal = false; f.state.failInventory = true;
  const prepared = await f.session.prepareExecution(undefined, f.target);
  await assert.rejects(f.session.applyExecution({operation:f.operation,baseline:prepared.configuration_baseline,mcp_selections:[],skill_selections:[],plugin_selections:[],installed_settings:f.target,api_key:'fixture'}));
  const failed = await f.session.snapshot();
  f.state.reports.failed = failed;
  assert.equal(failed.configuration_failure.stage, 'check_original_components');
  assert.equal(failed.configuration.original_plugins, undefined);
  const check = await f.session.configurationRecovery({operation:f.operation,baseline:prepared.configuration_baseline});
  f.state.reports.check = check;
  assert.ok(check.checks.every(c => c.status === 'passed'));
  assert.ok(check.original_query_retry);
  f.state.failInventory = false;
  await f.session.recoverExecution({operation:randomUUID(),original_operation:f.operation,baseline:prepared.configuration_baseline,original_query_retry:true,api_key:'fixture'});
  assert.equal(f.state.starts.length, 2);
  assert.equal((await f.session.snapshot()).configuration.original_plugins, undefined);
  f.state.completed = true;
});

test('changed original sources or history still block pre-candidate recovery', async t => {
  const f = await originalFailure(t, 'installed');
  const original = readFileSync(f.state.config.settings_sources[0].path);
  write(f.state.config.settings_sources[0].path, '{"unreviewed":true}');
  const changed = await f.session.configurationRecovery({operation:f.operation,baseline:f.apply.baseline});
  f.state.reports.changedSources = changed;
  assert.equal(changed.checks.find(c => c.kind === 'original_settings').status, 'blocked');
  await assert.rejects(f.session.recoverExecution(f.command));
  writeFileSync(f.state.config.settings_sources[0].path, original);
  f.state.history.push({type:'user',message:{role:'user',content:'unreviewed extra history'}});
  const history = await f.session.configurationRecovery({operation:f.operation,baseline:f.apply.baseline});
  f.state.reports.changedHistory = history;
  assert.equal(history.checks.find(c => c.kind === 'original_history').status, 'blocked');
  await assert.rejects(f.session.recoverExecution(f.command));
  assert.equal(f.state.starts.length, 1);
  f.state.completed = true;
});

test('later explicit input remains bound to the recovered original query', async t => {
  const f = await originalFailure(t);
  f.state.failOriginal = false;
  await f.session.recoverExecution(f.command);
  const operation = randomUUID();
  const receipt = await f.session.send({operation,text:'owned input after explicit recovery',api_key:'fixture'});
  f.state.reports.inputReceipt = receipt;
  assert.equal(receipt.native_id, f.id);
  assert.equal(receipt.native_turn, operation);
  assert.equal(f.state.starts.length, 2, 'input uses the existing original query');
  const snapshot = await f.session.snapshot();
  assert.deepEqual(snapshot.config, f.state.config);
  assert.equal(snapshot.entries.filter(e => e.kind === 'input').length, 1);
  assert.equal(snapshot.configuration.original_plugins, undefined);
  f.state.completed = true;
});
