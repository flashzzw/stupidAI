// Real Session/SDK tools, with fixture-only approvals and a scripted provider.
import { Session } from '../bridges/claude/dist/session.js';
import { readFileSync, writeFileSync, mkdirSync, existsSync, readdirSync, unlinkSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';

const request = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const id = randomUUID(), checks = {}, reports = {};
const check = (name, ok) => { checks[name] = Boolean(ok); };
const pause = () => new Promise(resolve => setTimeout(resolve, 20));
const readLines = path => existsSync(path) ? readFileSync(path, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse) : [];
const blocks = rows => rows.flatMap(row => Array.isArray(row.event?.message?.content) ? row.event.message.content : []);
let originalHistory, complete = false, error;

try {
  for (const stage of ['original', 'composed']) {
    const root = join(request.root, 'session-' + stage), startupLog = join(root, 'startup.jsonl');
    mkdirSync(root);
    process.env.NETCAP_SETTINGS_ACCESS_LOG = startupLog;
    for (const spec of request.cases.filter(c => c.tool === 'Write' && c.expected === 'success')) {
      const output = spec.input;
      if (existsSync(output.file_path)) {
        if (readFileSync(output.file_path, 'utf8') !== output.content) throw Error('unexpected owned output before reset');
        unlinkSync(output.file_path);
      }
    }
    const config = { id, source: id, cwd: request.cwd, settings_file: request[stage],
      executable: '/home/zzw/.local/bin/claude', model: 'claude-sonnet-4-6', endpoint: request.endpoint,
      permission: 'read_only', probe: false };
    const session = new Session(root, config, true), report = { config, cases: {} };
    reports[stage] = report;
    try {
      const before = await session.snapshot();
      if (stage === 'composed') check('original_history_present_before_reopen', JSON.stringify(before.history) === JSON.stringify(originalHistory));
      const count = readdirSync(request.provider_log).filter(name => /^request-\d+\.json$/.test(name)).length;
      await session.open('fixture-A');
      const deadline = Date.now() + 5000;
      let snapshot;
      do {
        snapshot = await session.snapshot();
        const started = snapshot.entries.filter(row => row.event?.subtype === 'hook_started');
        const finished = snapshot.entries.filter(row => row.event?.subtype === 'hook_response');
        if (started.length > 0 && started.length === finished.length) break;
        await pause();
      } while (Date.now() < deadline);
      check(stage + '_opening_has_no_model_request', readdirSync(request.provider_log).filter(name => /^request-\d+\.json$/.test(name)).length === count);
      const startup = readLines(startupLog), hooks = await session.hookCatalog();
      report.startup = startup; report.hooks = hooks;
      check(stage + '_custom_hook_preserved_once', startup.filter(row => row.source === 'cwd' && row.cwd === request.cwd && row.marker === 'custom-preserved').length === 1);
      check(stage + '_installed_startup_selection', startup.filter(row => row.source === 'installed').length === (stage === 'original' ? 1 : 0));
      check(stage + '_installed_full_source_key', hooks.inventory.items.filter(row => row.plugin_name === 'w0208-selected@w0208-local').length === (stage === 'original' ? 1 : 0));
      for (const spec of request.cases) {
        const operation = randomUUID(), approvals = [];
        const text = `NETCAP_SETTINGS_ACCESS_${stage}_${spec.name}`;
        await session.send({ operation, text, api_key: 'fixture-A' });
        const until = Date.now() + 20000;
        let result;
        do {
          snapshot = await session.snapshot();
          for (const pending of snapshot.pending.filter(item => item.operation === operation && item.status === 'pending')) {
            const matches = pending.tool === spec.tool && pending.input.file_path === spec.input.file_path &&
              (spec.tool !== 'Write' || pending.input.content === spec.input.content);
            const decision = matches && spec.expected === 'success' ? 'allow' : 'deny';
            approvals.push({ id: pending.id, tool: pending.tool, input: pending.input, decision, matches_requested_input: matches });
            await session.respond({ request_id: pending.id, connection: pending.connection, decision });
          }
          result = snapshot.entries.find(row => row.operation === operation && row.event?.type === 'result');
          if (result) break;
          if (snapshot.fault) throw Error('native query fault during ' + stage + '/' + spec.name);
          await pause();
        } while (Date.now() < until);
        if (!result) throw Error('no terminal result for ' + stage + '/' + spec.name);
        const rows = snapshot.entries.filter(row => row.operation === operation);
        const calls = blocks(rows).filter(block => block.type === 'tool_use');
        const returned = blocks(rows).filter(block => block.type === 'tool_result' && block.tool_use_id === calls[0]?.id);
        const body = returned[0], prefix = stage + '_' + spec.name;
        check(prefix + '_single_requested_tool', calls.length === 1 && calls[0].name === spec.tool && calls[0].input.file_path === spec.input.file_path);
        check(prefix + '_single_native_result', returned.length === 1 && Boolean(body.is_error) === (spec.expected === 'denied'));
        check(prefix + '_same_native_id_and_operation', result.event.session_id === id && result.operation === operation);
        if (spec.expected === 'denied') check(prefix + '_native_denial_without_fixture_decision', approvals.length === 0);
        else check(prefix + '_approvals_only_for_owned_exact_input', approvals.every(row => row.matches_requested_input && row.decision === 'allow'));
        if (spec.marker) check(prefix + '_actual_read_content', JSON.stringify(body).includes(spec.marker));
        if (spec.tool === 'Write' && spec.expected === 'success') check(prefix + '_actual_write_after_one_approval', approvals.length === 1 && existsSync(spec.input.file_path) && readFileSync(spec.input.file_path, 'utf8') === spec.input.content);
        if (spec.unchanged) check(prefix + '_file_unchanged', readFileSync(spec.input.file_path, 'utf8') === spec.unchanged);
        if (spec.absent) check(prefix + '_file_not_created', !existsSync(spec.input.file_path));
        report.cases[spec.name] = { operation, prompt: text, tool_use_id: calls[0]?.id, tool_result: body,
          approvals, result: result.event, native_id: id };
      }
      report.final = await session.snapshot();
      check(stage + '_all_cases_completed', Object.keys(report.cases).length === request.cases.length);
    } finally {
      await session.close();
      const journal = readLines(join(root, id + '.jsonl'));
      check(stage + '_query_closed', journal.at(-1)?.kind === 'closed');
      report.closed = journal.at(-1);
      if (stage === 'original') originalHistory = (await session.snapshot()).history;
    }
  }
  for (const spec of request.cases) {
    const a = reports.original.cases[spec.name], b = reports.composed.cases[spec.name];
    check(spec.name + '_admission_parity', Boolean(a.tool_result.is_error) === Boolean(b.tool_result.is_error) &&
      JSON.stringify(a.approvals.map(row => ({tool:row.tool,input:row.input,decision:row.decision}))) ===
      JSON.stringify(b.approvals.map(row => ({tool:row.tool,input:row.input,decision:row.decision}))));
  }
  const finalHistory = JSON.stringify(reports.composed.final.history);
  check('original_tool_history_retained_after_composed_calls', Object.values(reports.original.cases).every(row =>
    finalHistory.includes(row.tool_use_id) && finalHistory.includes(row.prompt)));
  complete = true;
} catch (caught) { error = String(caught); }
finally {
  check('complete_matrix', complete);
  writeFileSync(join(request.root, 'session-result.json'), JSON.stringify({id, checks, reports, error,
    passed: complete && Object.values(checks).every(Boolean)}, null, 2));
}
if (!complete || !Object.values(checks).every(Boolean)) process.exitCode = 1;
