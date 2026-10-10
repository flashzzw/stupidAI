import { useCallback, useEffect, useRef, useState } from 'react';

type Entry = { id: string; kind: string; turn_id?: string; data: { [key: string]: unknown }; raw_json: string };
type History = {
  read_only: boolean; instance_id: string; kind: string; state_namespace: string;
  session: { native_id: string; title: string; cwd: string };
  entries: Entry[]; baseline: string; offset: number; total: number; next_cursor?: string; at: string; coverage: string; detail: string;
};
const names: { [key: string]: string } = { turn: '轮次', user: '用户消息', assistant: '助手消息', system: '系统消息', userMessage: '用户消息', agentMessage: '助手消息', commandExecution: '命令执行', mcpToolCall: 'MCP 工具', fileChange: '文件变更', reasoning: '推理摘要', contextCompaction: '上下文压缩' };

function displayText(value: unknown): string | undefined {
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) {
    const blocks = value.map(displayText);
    return blocks.every(block => block !== undefined) ? blocks.join('\n\n') : undefined;
  }
  if (value && typeof value === 'object') {
    const block = value as { type?: string; text?: string };
    if (block.type === 'text' && typeof block.text === 'string') return block.text;
  }
  return undefined;
}

function entryText(entry: Entry): string {
  const data = entry.data;
  if (typeof data.text === 'string') return data.text;
  if (data.message && typeof data.message === 'object') {
    const message = data.message as { content?: unknown };
    if (message.content !== undefined) return displayText(message.content) ?? entry.raw_json;
  }
  if (data.content !== undefined) return displayText(data.content) ?? entry.raw_json;
  return entry.raw_json;
}

export function NativeHistory({ instanceId, projectId, nativeId, boundSessionId, connected, read, close }: {
  instanceId: string; projectId: string; nativeId: string; boundSessionId?: string; connected: boolean;
  read: <T>(path: string, body?: unknown) => Promise<T>; close?: () => void;
}) {
  const [page, setPage] = useState<History>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const generation = useRef(0);
  const load = useCallback(async (cursor = '') => {
    const current = ++generation.current;
    setLoading(true); setError(''); setPage(undefined);
    try {
      const result = boundSessionId ? await read<History>(`/api/sessions/${encodeURIComponent(boundSessionId)}/native/history`, { cursor }) : await read<History>('/api/native-sessions/history', { instance_id: instanceId, project_id: projectId, native_id: nativeId, cursor });
      if (current === generation.current) setPage(result);
    } catch (e) { if (current === generation.current) setError((e as Error).message); }
    finally { if (current === generation.current) setLoading(false); }
  }, [instanceId, projectId, nativeId, boundSessionId, read]);
  useEffect(() => { void load(); return () => { generation.current++; }; }, [load]);
  return <section className="native-history" aria-label="原生历史只读查看">
    <h2>原生历史 · 只读</h2><p className="mono">{nativeId}</p>
    <div className="native-history-actions"><button disabled={!connected || loading} onClick={() => void load()}>从头重新读取历史</button>{close ? <button onClick={close}>关闭历史</button> : null}</div>
    {loading ? <p role="status">正在读取并核对历史…</p> : null}
    {error ? <p className="error" role="alert">{error}</p> : null}
    {page ? <>
      <p className="hint">{page.detail}</p>
      <p>{page.session.title || '原生会话'} · {page.total ? `第 ${page.offset + 1}–${page.offset + page.entries.length} 条，共 ${page.total} 条接口可读记录` : '接口未返回可读条目'}</p>
      <p className="mono">项目：{page.session.cwd}<br />原生状态目录：{page.state_namespace}</p>
      <details><summary>读取范围与内容基线</summary><p>{page.coverage === 'claude_main_transcript_entries' ? 'Claude 主会话文件记录' : 'Codex 持久轮次与条目'} · {new Date(page.at).toLocaleString()}</p><p className="mono">{page.baseline}</p></details>
      {page.entries.map(entry => <article className="native-history-entry" key={JSON.stringify([entry.turn_id, entry.kind === 'turn', entry.id])}>
        <h3>{names[entry.kind] ?? `原生条目：${entry.kind}`}</h3>
        <p className="mono">{entry.id}{entry.turn_id && entry.kind !== 'turn' ? ` · 轮次 ${entry.turn_id}` : ''}</p>
        <pre>{entryText(entry)}</pre>
        <details><summary>查看原始字段</summary><pre>{entry.raw_json}</pre></details>
      </article>)}
      {page.next_cursor ? <button disabled={!connected || loading} onClick={() => void load(page.next_cursor)}>下一页历史</button> : page.total ? <p className="hint">已到本次接口可读记录末尾；读取范围见上方说明。</p> : null}
    </> : null}
  </section>;
}
