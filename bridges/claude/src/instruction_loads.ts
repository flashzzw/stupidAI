import { randomUUID } from 'node:crypto';
import { isAbsolute } from 'node:path';
import type { HookCallback, HookEvent, HookCallbackMatcher } from '@anthropic-ai/claude-agent-sdk';
import { record } from './protocol.js';

export type InstructionLoad = {
  version: 1; id: string; native_id: string; cwd: string; native_cwd: string;
  query_id: string; configuration_id: string; at: string;
  path: string; source: string; reason: string;
  prompt_id?: string; agent_id?: string; agent_type?: string;
  parent_path?: string; trigger_path?: string; globs?: string[];
};
type Context = { native_id: string; cwd: string; query_id: string; configuration_id: string };

export type InstructionPromptLink = Context & {
  version: 1; at: string; prompt_id: string; tool_use_id: string; tool: string; agent_id?: string;
};

const identifier = /^[a-f\d]{8}-[a-f\d]{4}-[a-f\d]{4}-[a-f\d]{4}-[a-f\d]{12}$/i;
function string(value: unknown, max: number): string {
  if (typeof value !== 'string' || !value || value.length > max || value.includes('\0')) throw Error('invalid instruction metadata');
  return value;
}
function path(value: unknown): string {
  const result = string(value, 4096);
  if (!isAbsolute(result)) throw Error('invalid instruction path');
  return result;
}

// This callback observes a native notification. It does not read file bytes,
// alter the loaded instructions, or decide whether a tool may execute.
export function instructionLoadHooks(context: Context, append: (load: InstructionLoad) => void, failed: () => void): Partial<Record<HookEvent, HookCallbackMatcher[]>> {
  const callback: HookCallback = async input => {
    try {
      const raw = record(input);
      if (raw.hook_event_name !== 'InstructionsLoaded' || raw.session_id !== context.native_id) throw Error('instruction identity mismatch');
      const load: InstructionLoad = { version: 1, id: randomUUID(), ...context, at: new Date().toISOString(),
        native_cwd: path(raw.cwd), path: path(raw.file_path), source: string(raw.memory_type, 100), reason: string(raw.load_reason, 100) };
      if (raw.prompt_id !== undefined) {
        load.prompt_id = string(raw.prompt_id, 36);
        if (!identifier.test(load.prompt_id)) throw Error('invalid instruction prompt');
      }
      if (raw.agent_id !== undefined) load.agent_id = string(raw.agent_id, 200);
      if (raw.agent_type !== undefined) load.agent_type = string(raw.agent_type, 200);
      if (raw.parent_file_path !== undefined) load.parent_path = path(raw.parent_file_path);
      if (raw.trigger_file_path !== undefined) load.trigger_path = path(raw.trigger_file_path);
      if (raw.globs !== undefined) {
        if (!Array.isArray(raw.globs) || raw.globs.length > 32) throw Error('invalid instruction patterns');
        load.globs = raw.globs.map(value => string(value, 512));
      }
      append(load);
    } catch {
      try { failed(); } catch { /* A diagnostic failure does not change native execution. */ }
    }
    return {};
  };
  return { InstructionsLoaded: [{ hooks: [callback] }] };
}

// Native prompt IDs are independent of SDK input UUIDs. The tool ID connects
// this native prompt to an explicitly identified SDK assistant message.
export function instructionPromptHooks(context: Context, append: (link: InstructionPromptLink) => void, failed: () => void): Partial<Record<HookEvent, HookCallbackMatcher[]>> {
  const callback: HookCallback = async (input, toolUseID) => {
    try {
      const raw = record(input);
      if (raw.hook_event_name !== 'PreToolUse' || raw.session_id !== context.native_id) throw Error('instruction prompt identity mismatch');
      if (raw.prompt_id === undefined) return {};
      const promptID = string(raw.prompt_id, 36), toolID = string(raw.tool_use_id, 300);
      if (!identifier.test(promptID) || toolUseID !== undefined && toolUseID !== toolID) throw Error('invalid instruction prompt link');
      const link: InstructionPromptLink = {version: 1, ...context, at: new Date().toISOString(), prompt_id: promptID,
        tool_use_id: toolID, tool: string(raw.tool_name, 200)};
      if (raw.agent_id !== undefined) link.agent_id = string(raw.agent_id, 200);
      append(link);
    } catch { try { failed(); } catch { /* Metadata collection has no permission decision. */ } }
    return {};
  };
  return {PreToolUse: [{hooks: [callback]}]};
}
