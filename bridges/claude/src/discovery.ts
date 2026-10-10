import { getSessionInfo, listSessions, type SDKSessionInfo } from '@anthropic-ai/claude-agent-sdk';
import { isAbsolute } from 'node:path';
import { NotSent, text, uuid } from './protocol.js';

function directory(params: Record<string, unknown>): string {
  const cwd = text(params.cwd, 'discovery project');
  if (!isAbsolute(cwd)) throw new NotSent('discovery requires an absolute project directory');
  return cwd;
}

export function metadata(session: SDKSessionInfo) {
  return { native_id: session.sessionId, title: session.summary.slice(0, 240), cwd: session.cwd ?? '',
    source: 'native_history', native_status: 'unknown', updated_at: new Date(session.lastModified).toISOString() };
}

export async function discover(params: Record<string, unknown>) {
  const cwd = directory(params);
  const cursor = params.cursor ?? '';
  if (typeof cursor !== 'string' || (cursor !== '' && !/^(0|[1-9][0-9]{0,5})$/.test(cursor))) throw new NotSent('invalid history cursor');
  const offset = Number(cursor);
  const limit = params.limit;
  if (!Number.isInteger(limit) || typeof limit !== 'number' || limit < 1 || limit > 50) throw new NotSent('invalid discovery page size');
  const sessions = await listSessions({ dir: cwd, includeWorktrees: false, includeProgrammatic: true, limit: limit + 1, offset });
  return { sessions: sessions.slice(0, limit).map(metadata), ...(sessions.length > limit ? { next_cursor: String(offset + limit) } : {}) };
}

export async function inspectNativeInfo(params: Record<string, unknown>) {
  const session = await getSessionInfo(uuid(params.native_id), { dir: directory(params) });
  if (!session) throw new NotSent('native session metadata not found in the selected project');
  return session;
}

export async function inspectNative(params: Record<string, unknown>) {
  return metadata(await inspectNativeInfo(params));
}
