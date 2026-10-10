import { importSessionToStore, type SessionStore } from '@anthropic-ai/claude-agent-sdk';
import { realpath } from 'node:fs/promises';
import { inspectNativeInfo, metadata } from './discovery.js';
import { NotSent, text } from './protocol.js';

// Capture the main file's parsed entries in an ephemeral, bounded sink. The
// SDK's getSessionMessages projection loses top-level system payloads and
// pre-compaction entries; this public SDK reader preserves the source objects.
// No store is persisted, no query is created, and no subagent files are read.
export async function readNativeHistory(params: Record<string, unknown>) {
  const info = await inspectNativeInfo(params);
  const session = metadata(info);
  const cwd = text(params.cwd, 'history project');
  if (!session.cwd || await realpath(session.cwd) !== await realpath(cwd)) throw new NotSent('native history project mismatch');
  if (info.fileSize === undefined || info.fileSize > 8 * 1024 * 1024) throw new NotSent('native history file size unknown or exceeds 8 MiB; no partial history returned');
  const entries: { id: string; kind: string; data: unknown }[] = [];
  let bytes = 0;
  let projectKey: string | undefined;
  const sink: SessionStore = {
    async append(key, batch) {
      if (key.sessionId !== session.native_id || key.subpath !== undefined || projectKey !== undefined && key.projectKey !== projectKey) throw new NotSent('native history key mismatch');
      projectKey = key.projectKey;
      for (const entry of batch) {
        if (!entry || typeof entry.type !== 'string' || !entry.type || entry.sessionId !== undefined && entry.sessionId !== session.native_id) throw new NotSent('native history entry identity or type mismatch');
        const size = Buffer.byteLength(JSON.stringify(entry), 'utf8');
        bytes += size;
        if (entries.length >= 5000 || size > 1024 * 1024 || bytes > 8 * 1024 * 1024) throw new NotSent('native history exceeds 5000 entries, 8 MiB, or 1 MiB per entry; no partial history returned');
        // A file can contain multiple updates with the same UUID and metadata
        // without a UUID. Position identifies each record within this baseline.
        entries.push({ id: `entry-${entries.length}`, kind: entry.type, data: entry });
      }
    },
    async load() { throw new NotSent('history capture is not a resumable session store'); },
  };
  await importSessionToStore(session.native_id, sink, { dir: cwd, includeSubagents: false, batchSize: 50 });
  return { session, entries };
}
