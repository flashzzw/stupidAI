import { createHash, randomUUID } from 'node:crypto';
import type { ChildProcessWithoutNullStreams } from 'node:child_process';
import { Transform } from 'node:stream';
import type { SpawnedProcess } from '@anthropic-ai/claude-agent-sdk';
import { NotSent, record } from './protocol.js';
import { permissionRuleInventory, permissionRulesUnavailable } from './permission_rules.js';

const unavailable = () => new NotSent('当前原生 hook 来源清单不可用');

// The SDK's public spawn seam owns the stdio transport. Consume only replies
// to this channel's read-only requests; forward all other bytes to the SDK.
// No SDK private methods, second process, reload or generic control API.
export class NativeHookChannel {
  readonly process: SpawnedProcess;
  private readonly prefix = `netcap-hooks-${randomUUID()}-`;
  private counter = 0;
  private stopped = false;
  private queue: Promise<unknown> = Promise.resolve();
  private pending?: { id: string; resolve: (value: unknown) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout>; failure: () => Error };

  constructor(private readonly child: ChildProcessWithoutNullStreams) {
    let parts: Buffer[] = [], size = 0;
    const owner = this;
    const output = new Transform({
      transform(chunk: Buffer, _encoding, done) {
        let offset = 0, end: number;
        while ((end = chunk.indexOf(10, offset)) >= 0) {
          const tail = chunk.subarray(offset, end + 1);
          parts.push(tail); size += tail.length;
          const line = parts.length === 1 ? parts[0]! : Buffer.concat(parts, size);
          parts = []; size = 0; offset = end + 1;
          if (!owner.consume(line)) this.push(line);
        }
        if (offset < chunk.length) { const tail = chunk.subarray(offset); parts.push(tail); size += tail.length; }
        done();
      },
      flush(done) { for (const part of parts) this.push(part); owner.stop(); done(); },
    });
    child.stdout.pipe(output);
    child.stdout.on('error', () => { owner.stop(); output.destroy(unavailable()); });
    child.on('exit', () => owner.stop());
    child.on('error', () => owner.stop());
    this.process = {
      stdin: child.stdin, stdout: output,
      get killed() { return child.killed; }, get exitCode() { return child.exitCode; },
      get signalCode() { return child.signalCode; },
      kill: child.kill.bind(child), on: child.on.bind(child), once: child.once.bind(child), off: child.off.bind(child),
    };
  }

  private consume(line: Buffer): boolean {
    if (!line.includes(this.prefix)) return false;
    let value: Record<string, unknown>;
    try { value = record(JSON.parse(line.toString('utf8'))); } catch { return false; }
    if (value.type !== 'control_response') return false;
    let response: Record<string, unknown>;
    try { response = record(value.response); } catch { return false; }
    if (typeof response.request_id !== 'string' || !response.request_id.startsWith(this.prefix)) return false;
    // Late or duplicate owned replies never enter the SDK's request registry.
    const pending = this.pending;
    if (pending?.id === response.request_id) {
      this.pending = undefined; clearTimeout(pending.timer);
      if (response.subtype === 'success') pending.resolve(response.response);
      else pending.reject(pending.failure());
    }
    return true;
  }

  private stop() {
    this.stopped = true;
    if (this.pending) {
      clearTimeout(this.pending.timer); this.pending.reject(this.pending.failure()); this.pending = undefined;
    }
  }

  private request(subtype: 'get_hooks_listing' | 'list_permission_rules', failure: () => Error) {
    // Approval checks and read-only panels share the original transport. Queue
    // their owned reads so concurrent tool prompts do not invalidate each other.
    const result = this.queue.then(() => this.dispatch(subtype, failure));
    this.queue = result.catch(() => {});
    return result;
  }

  private async dispatch(subtype: 'get_hooks_listing' | 'list_permission_rules', failure: () => Error) {
    if (this.stopped || this.pending || this.child.exitCode !== null || this.child.signalCode !== null) throw failure();
    return new Promise<unknown>((resolve, reject) => {
      const id = this.prefix + (++this.counter);
      const timer = setTimeout(() => {
        if (this.pending?.id === id) { this.pending = undefined; reject(failure()); }
      }, 5000);
      this.pending = { id, resolve, reject, timer, failure };
      this.child.stdin.write(JSON.stringify({ type: 'control_request', request_id: id, request: { subtype } }) + '\n', error => {
        if (error && this.pending?.id === id) { clearTimeout(timer); this.pending = undefined; reject(failure()); }
      });
    });
  }

  async read() {
    return hookInventory(await this.request('get_hooks_listing', unavailable));
  }

  async readPermissions(cwd: string) {
    return permissionRuleInventory(await this.request('list_permission_rules', permissionRulesUnavailable), cwd);
  }
}

export function hookInventory(value: unknown) {
  const raw = record(value), policy = record(raw.policy);
  const fields = ['disabledByPolicy', 'managedOnly', 'pluginOnly', 'allDisabled'];
  if (!Array.isArray(raw.hooks) || raw.hooks.length > 2048 || fields.some(k => typeof policy[k] !== 'boolean') ||
      !Number.isSafeInteger(policy.policyHookCount) || Number(policy.policyHookCount) < 0 ||
      policy.policyUnreadable !== undefined && typeof policy.policyUnreadable !== 'boolean' ||
      raw.errors !== undefined && !Array.isArray(raw.errors) ||
      raw.safeMode !== undefined && (!raw.safeMode || typeof raw.safeMode !== 'object' || Array.isArray(raw.safeMode)) ||
      raw.bareMode !== undefined && (!raw.bareMode || typeof raw.bareMode !== 'object' || Array.isArray(raw.bareMode))) throw unavailable();
  const string = (value: unknown, max: number, empty = false): string => {
    if (typeof value !== 'string' || !empty && !value || value.length > max || value.includes('\0')) throw unavailable();
    return value;
  };
  const optionalBoolean = (r: Record<string, unknown>, key: string) => {
    if (r[key] !== undefined && typeof r[key] !== 'boolean') throw unavailable();
    return r[key] === true;
  };
  const items = raw.hooks.map(value => {
    const h = record(value);
    const event = string(h.event, 100), matcher = string(h.matcher, 1000, true), source = string(h.source, 100), type = string(h.type, 100);
    const plugin = h.pluginName === undefined ? '' : string(h.pluginName, 300);
    const command = string(h.commandText, 200_000, true);
    const condition = h.condition === undefined ? '' : string(h.condition, 20_000, true);
    if (h.timeout !== undefined && (typeof h.timeout !== 'number' || !Number.isFinite(h.timeout) || h.timeout <= 0)) throw unavailable();
    // Keep command/prompt/URL/header/editable content inside this boundary.
    // The digest can identify a changed definition without exposing its body.
    return { event, matcher, source, plugin_name: plugin, type,
      disabled: optionalBoolean(h, 'disabled'), runs_once: optionalBoolean(h, 'runsOnce'),
      background: optionalBoolean(h, 'runsInBackground'), conditional: !!condition,
      ...(h.timeout === undefined ? {} : { timeout_seconds: h.timeout }),
      definition_hash: createHash('sha256').update(JSON.stringify({ event, matcher, source, plugin, type, command, condition,
        timeout: h.timeout, once: h.runsOnce, background: h.runsInBackground })).digest('hex') };
  });
  return { available: true, at: new Date().toISOString(), items, policy: {
    disabled_by_policy: policy.disabledByPolicy, managed_only: policy.managedOnly, plugin_only: policy.pluginOnly,
    all_disabled: policy.allDisabled, policy_hook_count: policy.policyHookCount, policy_unreadable: policy.policyUnreadable === true,
    safe_mode: raw.safeMode !== undefined, bare_mode: raw.bareMode !== undefined,
    settings_error_count: Array.isArray(raw.errors) ? raw.errors.length : 0,
  } };
}
