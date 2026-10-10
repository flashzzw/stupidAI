import { isAbsolute } from 'node:path';
import { NotSent, record } from './protocol.js';

export const permissionRulesUnavailable = () => new NotSent('当前原生权限规则报告不可用');

// Preserve the CLI's stored rule spelling and ignored markers. In particular,
// a live policy transition can report both active and ignored copies. This is
// an observation, never a permission evaluator or a reason to merge the rows.
export function permissionRuleInventory(value: unknown, cwd: string) {
  const fail = permissionRulesUnavailable;
  const state = record(record(value).state);
  if (state.originalCwd !== cwd || typeof state.managedOnly !== 'boolean' ||
      !Array.isArray(state.rules) || state.rules.length > 4096 ||
      !Array.isArray(state.workspaceDirectories) || state.workspaceDirectories.length > 256 ||
      state.errors !== undefined && (!Array.isArray(state.errors) || state.errors.length > 64)) throw fail();
  let bytes = 0;
  const string = (value: unknown, max: number, empty = false, controls = false): string => {
    if (typeof value !== 'string' || !empty && !value || Buffer.byteLength(value) > max ||
        Buffer.from(value).toString('utf8') !== value || !controls && /[\x00-\x1f\x7f]/.test(value)) throw fail();
    bytes += Buffer.byteLength(value);
    if (bytes > 8 << 20) throw fail();
    return value;
  };
  const items = state.rules.map(value => {
    const row = record(value);
    if (!['allow', 'deny', 'ask'].includes(String(row.behavior)) ||
        !['persistent', 'session', 'readonly'].includes(String(row.editability)) ||
        row.notInEffect !== undefined && typeof row.notInEffect !== 'boolean') throw fail();
    return { behavior: row.behavior as 'allow' | 'deny' | 'ask', source: string(row.source, 300),
      rule: string(row.rule, 1 << 20, false, true), editability: row.editability as 'persistent' | 'session' | 'readonly',
      not_in_effect: row.notInEffect === true };
  });
  const directories = state.workspaceDirectories.map(value => {
    const row = record(value), path = string(row.path, 8192);
    if (!isAbsolute(path)) throw fail();
    return { path, source: string(row.source, 300) };
  });
  // Parse errors may quote setting values. Only their location leaves here;
  // native descriptions are also omitted since the exact rule is available.
  const errors = ((state.errors ?? []) as unknown[]).map(value => {
    const row = record(value);
    return { file: string(row.file, 8192), field: string(row.path ?? '', 1024, true) };
  });
  return { version: 1, available: true, at: new Date().toISOString(), cwd,
    managed_only: state.managedOnly, items, directories, errors };
}
