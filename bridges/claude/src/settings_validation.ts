// A separate, nonpersistent native process parses the same settings sources.
// --bare must be a CLI flag: settings.env can override CLAUDE_CODE_SIMPLE.
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createHash, randomUUID } from 'node:crypto';
import { fstatSync, statSync } from 'node:fs';
import { isAbsolute } from 'node:path';
import { record } from './protocol.js';

type PluginSetting = { source_key: string; enabled: boolean };
type Request = { executable: string; cwd: string; settings_file: string; model: string; plugin_settings?: PluginSetting[]; baseline_settings_file?: string };
type SettingsSignature = { effective_sha256: string; sources_sha256: string; applied_sha256: string; permissions_sha256: string };
type SettingsPreservation = {
  version: 1; baseline_settings_file: string; source_keys: string[];
  baseline_schema_valid: boolean; baseline_process_stopped: boolean;
  baseline: SettingsSignature; candidate: SettingsSignature; differences: string[]; matches: boolean;
};
type Reply = { version: 1; source: string; cli_version: string; cwd: string; settings_file: string;
  schema_valid: boolean; errors: { file: string; field: string }[];
  effective_sha256: string; sources: { source: string; sha256: string }[];
  bare_mode: boolean; no_session_persistence: boolean; process_stopped: boolean;
  file_holder_observed?: boolean;
  preservation?: SettingsPreservation;
  plugin_settings?: { source_key: string; requested_enabled: boolean; actual_enabled: boolean | null; matches: boolean }[] };
const failure = () => Error('native settings validation did not complete');
const digest = (value: unknown) => createHash('sha256').update(JSON.stringify(value)).digest('hex');
const canonical = (value: unknown): unknown => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object'
  ? Object.fromEntries(Object.entries(value).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0).map(([key, item]) => [key, canonical(item)])) : value;
const signatureDigest = (value: unknown) => digest(canonical(value));
const text = (value: unknown, limit: number, empty = false): string => {
  if (typeof value !== 'string' || (!empty && !value) || value.length > limit || value.includes('\0')) throw failure();
  return value;
};

export async function validateNativeSettings(input: unknown): Promise<Reply> {
  const request = record(input) as unknown as Request;
  if (!isAbsolute(text(request.executable, 16384)) || !isAbsolute(text(request.cwd, 16384)) ||
      !isAbsolute(request.settings_file || request.cwd)) throw failure();
  text(request.settings_file, 16384, true); text(request.model, 300);
  const selection = request.plugin_settings;
  if (selection !== undefined && (!Array.isArray(selection) || selection.length === 0 || selection.length > 16 ||
      selection.some(s => !s || !/^[A-Za-z0-9_-]{1,100}@[A-Za-z0-9_-]{1,100}$/.test(s.source_key) || typeof s.enabled !== 'boolean') ||
      new Set(selection.map(s => s.source_key)).size !== selection.length)) throw failure();
  const compare = request.baseline_settings_file !== undefined;
  if (compare && (!selection || !isAbsolute(text(request.baseline_settings_file, 16384, true) || request.cwd))) throw failure();
  const holder = selection ? fstatSync(3) : undefined;
  if (holder) {
    const named = statSync(request.settings_file);
    if (!holder.isFile() || (holder.mode & 0o777) !== 0o600 || named.dev !== holder.dev || named.ino !== holder.ino) throw failure();
  }
  const env = { ...process.env, ANTHROPIC_API_KEY: 'netcap-settings-validation-no-model',
    ANTHROPIC_BASE_URL: 'http://127.0.0.1:9', CLAUDE_CODE_RESUME_INTERRUPTED_TURN: '0',
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_AUTOUPDATER: '1',
    DISABLE_TELEMETRY: '1', DISABLE_ERROR_REPORTING: '1' };
  const version = await promisify(execFile)(request.executable, ['--version'], {
    cwd: request.cwd, env, timeout: 5000, maxBuffer: 4096,
  });
  if (version.stdout.trim() !== '2.1.280 (Claude Code)') throw failure();
  const inspect = async (settingsFile: string, selected: boolean): Promise<{ reply: Reply; signature?: SettingsSignature }> => {
    const fileHolder = selected ? holder : undefined;
    const args = ['-p', '--bare', '--no-session-persistence', '--input-format', 'stream-json',
      '--output-format', 'stream-json', '--verbose', '--await-initialize', '--session-id', randomUUID(),
      '--setting-sources', 'user,project,local', '--model', request.model,
      '--settings', settingsFile || '{}'];
    // Inherit the metadata parent's process group so Go cancellation also stops
    // this child. No detached worker, daemon, model input or tool permission.
    const stdio: ['pipe', 'pipe', 'pipe', ...number[]] = ['pipe', 'pipe', 'pipe'];
    if (fileHolder) stdio.push(3);
    const child = spawn(request.executable, args, { cwd: request.cwd, env, stdio });
    const { stdin, stdout, stderr } = child;
    if (!stdin || !stdout || !stderr) { child.kill('SIGKILL'); throw failure(); }
    let exited = false, fatal: Error | undefined, size = 0, sequence = 0, lineParts: Buffer[] = [];
    let pending: { id: string; resolve: (value: unknown) => void; reject: (error: Error) => void } | undefined;
    const stopped = new Promise<void>(resolve => {
      child.once('exit', () => { exited = true; pending?.reject(failure()); pending = undefined; resolve(); });
      child.once('error', () => { exited = true; pending?.reject(failure()); pending = undefined; resolve(); });
    });
    const fail = () => {
      fatal = failure(); pending?.reject(fatal); pending = undefined; child.kill('SIGTERM');
    };
    const consume = (line: Buffer) => {
      try {
        const message = record(JSON.parse(line.toString('utf8')));
        if (message.type === 'control_request' || message.type === 'result' ||
            ['hook_started', 'hook_progress', 'hook_response'].includes(String(message.subtype))) { fail(); return; }
        if (message.type !== 'control_response') return;
        const response = record(message.response);
        if (!pending || response.request_id !== pending.id) { fail(); return; }
        const p = pending; pending = undefined;
        if (response.subtype !== 'success') p.reject(failure());
        else p.resolve(response.response);
      } catch { fail(); }
    };
    stderr.resume();
    stdout.on('data', (chunk: Buffer) => {
      let offset = 0, end: number;
      while ((end = chunk.indexOf(10, offset)) >= 0) {
        const part = chunk.subarray(offset, end); lineParts.push(part); size += part.length;
        if (size > 16 << 20) { fail(); return; }
        consume(lineParts.length === 1 ? lineParts[0]! : Buffer.concat(lineParts, size));
        lineParts = []; size = 0; offset = end + 1;
      }
      if (offset < chunk.length) { lineParts.push(chunk.subarray(offset)); size += chunk.length - offset; }
      if (size > 16 << 20) fail();
    });
    stdout.on('error', fail); stdin.on('error', fail);
    const deadline = setTimeout(fail, 12000);
    const call = (subtype: string) => new Promise<unknown>((resolve, reject) => {
      if (pending || fatal || exited) { reject(failure()); return; }
      const id = `netcap-settings-${++sequence}`;
      pending = { id, resolve, reject };
      stdin.write(JSON.stringify({ type: 'control_request', request_id: id, request: { subtype } }) + '\n', error => { if (error) fail(); });
    });
    let result: Reply, signature: SettingsSignature | undefined;
    try {
      await call('initialize');
      const settings = record(await call('get_settings'));
      const hooks = record(await call('get_hooks_listing'));
      const mcp = record(await call('mcp_status'));
      record(hooks.bareMode);
      if (!Array.isArray(mcp.mcpServers) || mcp.mcpServers.length !== 0 || !Array.isArray(settings.sources) || settings.sources.length > 32 ||
          settings.errors !== undefined && !Array.isArray(settings.errors)) throw failure();
      const errors = (settings.errors ?? []) as unknown[];
      if (errors.length > 64) throw failure();
      const effective = record(settings.effective);
      result = { version: 1, source: 'claude.bare.settings_validation', cli_version: '2.1.280',
        cwd: request.cwd, settings_file: settingsFile, schema_valid: errors.length === 0,
        errors: errors.map(value => {
          const e = record(value);
          // Native messages may quote setting values; only paths leave here.
          return { file: text(e.file, 8192), field: text(e.path ?? '', 512, true) };
        }), effective_sha256: digest(effective),
        sources: settings.sources.map(value => { const s = record(value); return { source: text(s.source, 100), sha256: digest(record(s.settings)) }; }),
        bare_mode: true, no_session_persistence: true, process_stopped: false };
      if (compare && selection) {
        const permissions = record(record(await call('list_permission_rules')).state);
        if (!Array.isArray(permissions.rules) || permissions.rules.length > 4096 ||
            !Array.isArray(permissions.workspaceDirectories) || permissions.workspaceDirectories.length > 256 ||
            permissions.originalCwd !== request.cwd || typeof permissions.managedOnly !== 'boolean' ||
            permissions.errors !== undefined && (!Array.isArray(permissions.errors) || permissions.errors.length > 64 || errors.length === 0 && permissions.errors.length > 0)) throw failure();
        for (const value of permissions.rules) {
          const rule = record(value);
          if (!['allow', 'ask', 'deny'].includes(String(rule.behavior))) throw failure();
          text(rule.rule, 1 << 20); text(rule.source, 100);
        }
        for (const directory of permissions.workspaceDirectories) record(directory);
        // Only the selected keys in flagSettings may be introduced or changed.
        // Other plugin flags, inherited layers and permission metadata remain
        // part of the comparison. Values never leave this metadata process.
        const withoutSelection = (value: unknown) => {
          const settings = { ...record(value) };
          if (settings.enabledPlugins !== undefined) {
            const flags = { ...record(settings.enabledPlugins) };
            for (const item of selection) delete flags[item.source_key];
            if (Object.keys(flags).length) settings.enabledPlugins = flags;
            else delete settings.enabledPlugins;
          }
          return settings;
        };
        const sources = settings.sources.map(value => {
          const source = record(value);
          return { ...source, source: source.source, settings: source.source === 'flagSettings' ? withoutSelection(source.settings) : record(source.settings) };
        }).filter(source => source.source !== 'flagSettings' || Object.keys(source.settings).length > 0);
        signature = { effective_sha256: signatureDigest(withoutSelection(effective)), sources_sha256: signatureDigest(sources),
          applied_sha256: signatureDigest(record(settings.applied)), permissions_sha256: signatureDigest(permissions) };
      }
      if (fileHolder && selection) {
        // Check the actual native child while it is alive, not merely spawn's
        // requested stdio mapping. This preview is Linux/WSL only.
        const inherited = statSync(`/proc/${child.pid}/fd/3`);
        if (inherited.dev !== fileHolder.dev || inherited.ino !== fileHolder.ino) throw failure();
        result.file_holder_observed = true;
        const flags = effective.enabledPlugins === undefined ? {} : record(effective.enabledPlugins);
        result.plugin_settings = selection.map(s => {
          const value = flags[s.source_key];
          const actual = typeof value === 'boolean' ? value : null;
          return { source_key: s.source_key, requested_enabled: s.enabled, actual_enabled: actual, matches: actual === s.enabled };
        });
      }
    } finally {
      clearTimeout(deadline);
      stdin.end();
      if (!exited) child.kill('SIGTERM');
      const force = setTimeout(() => child.kill('SIGKILL'), 1000);
      await stopped; clearTimeout(force);
    }
    if (fatal) throw fatal;
    result.process_stopped = true;
    return { reply: result, signature };
  };
  const baseline = compare ? await inspect(request.baseline_settings_file!, false) : undefined;
  const candidate = await inspect(request.settings_file, true);
  if (baseline && selection) {
    if (!baseline.signature || !candidate.signature) throw failure();
    const differences: string[] = [];
    for (const [key, label] of [['effective_sha256', 'effective_settings'], ['sources_sha256', 'source_settings'],
      ['applied_sha256', 'applied_settings'], ['permissions_sha256', 'permission_rules']] as const) {
      if (baseline.signature[key] !== candidate.signature[key]) differences.push(label);
    }
    if (!baseline.reply.schema_valid || !candidate.reply.schema_valid) differences.push('schema');
    candidate.reply.preservation = { version: 1, baseline_settings_file: request.baseline_settings_file!,
      source_keys: selection.map(item => item.source_key), baseline_schema_valid: baseline.reply.schema_valid,
      baseline_process_stopped: baseline.reply.process_stopped, baseline: baseline.signature, candidate: candidate.signature,
      differences, matches: differences.length === 0 };
  }
  return candidate.reply;
}

if (process.argv[2] === '--stdio') {
  let input = '';
  for await (const chunk of process.stdin) { input += chunk; if (input.length > 65536) throw failure(); }
  try { process.stdout.write(JSON.stringify(await validateNativeSettings(JSON.parse(input))) + '\n'); }
  catch { process.stdout.write(JSON.stringify({ error: 'native settings validation unavailable' }) + '\n'); process.exitCode = 1; }
}
