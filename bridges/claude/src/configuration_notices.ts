import { randomUUID } from 'node:crypto';
import { isAbsolute } from 'node:path';
import type { HookCallback, HookEvent, HookCallbackMatcher } from '@anthropic-ai/claude-agent-sdk';
import { record } from './protocol.js';

export type ConfigurationNotice = {
  version: 1; id: string; native_id: string; cwd: string; native_cwd: string;
  query_id: string; configuration_id: string; at: string; source: string; path: string;
  prompt_id?: string; agent_id?: string; agent_type?: string;
};
type Context = { native_id: string; cwd: string; query_id: string; configuration_id: string };

const identifier = /^[a-f\d]{8}-[a-f\d]{4}-[a-f\d]{4}-[a-f\d]{4}-[a-f\d]{12}$/i;
function string(value: unknown, max: number): string {
  if (typeof value !== 'string' || !value || Buffer.byteLength(value) > max || value.includes('\0')) throw Error('invalid configuration notification metadata');
  return value;
}
function path(value: unknown): string {
  const result = string(value, 4096);
  if (!isAbsolute(result)) throw Error('invalid configuration notification path');
  return result;
}

// A ConfigChange is a notification, not a loaded version or a permission
// decision. Keep only metadata and leave every native configuration decision
// to the existing callbacks and CLI, even when recording fails.
export function configurationNoticeHooks(context: Context, append: (notice: ConfigurationNotice) => void, failed: () => void): Partial<Record<HookEvent, HookCallbackMatcher[]>> {
  const callback: HookCallback = async input => {
    try {
      const raw = record(input);
      if (raw.hook_event_name !== 'ConfigChange' || raw.session_id !== context.native_id) throw Error('configuration notification identity mismatch');
      const notice: ConfigurationNotice = {version: 1, id: randomUUID(), ...context, at: new Date().toISOString(),
        native_cwd: path(raw.cwd), source: string(raw.source, 100), path: raw.file_path === undefined ? '' : path(raw.file_path)};
      if (raw.prompt_id !== undefined) {
        notice.prompt_id = string(raw.prompt_id, 36);
        if (!identifier.test(notice.prompt_id)) throw Error('invalid configuration prompt');
      }
      if (raw.agent_id !== undefined) notice.agent_id = string(raw.agent_id, 200);
      if (raw.agent_type !== undefined) notice.agent_type = string(raw.agent_type, 200);
      append(notice);
    } catch { try { failed(); } catch { /* Observation failure must not change the native config decision. */ } }
    return {};
  };
  return {ConfigChange: [{hooks: [callback]}]};
}
