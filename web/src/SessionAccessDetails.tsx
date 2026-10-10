import { useState } from 'react';
import { SessionContextDetails, type SessionContext } from './SessionContext';

export type SessionAccess = {
  source: string; source_label: string; connection_method: string; detail: string;
  observed_at?: string; preview_available: boolean;
  context?: SessionContext;
  capabilities: Record<string, { status: string; detail: string }>;
};
type Preview = {
  session_id: string; generation: number; checked_at: string; read_only: boolean; access: SessionAccess;
  evidence: { source: string; at: string; complete: boolean; detail: string }; native_status?: string; error?: string;
};
const names: Record<string, string> = { history: '已保存历史', live_observe: '原执行实时观察', resume: '恢复工作台控制', live_input: '发送输入' };
const statuses: Record<string, string> = { supported: '可查看', conditional: '有前提', unsupported: '不支持', unverified: '未验证', unavailable: '待核对 / 不可用' };

export function SessionAccessDetails({ sessionId, access, connected, read }: {
  sessionId: string; access?: SessionAccess; connected: boolean;
  read: <T>(path: string, body?: unknown) => Promise<T>;
}) {
  const [preview, setPreview] = useState<Preview>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  if (!access) return <p className="hint">此记录尚无接续来源说明，请重新读取会话。</p>;
  const shown = preview?.access ?? access;
  async function inspect() {
    setLoading(true); setError(''); setPreview(undefined);
    try { setPreview(await read<Preview>(`/api/sessions/${encodeURIComponent(sessionId)}/access/preview`, {})); }
    catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }
  return <details className="session-access"><summary>接续来源与操作前提 · {access.source_label}</summary>
    <p>{access.connection_method}</p><p className="hint">{access.detail}</p>
    <SessionContextDetails context={shown.context} />
    <dl>{Object.entries(shown.capabilities).map(([kind, capability]) => <div className="access-capability" key={kind}>
      <dt>{names[kind] ?? kind} · {statuses[capability.status] ?? '未验证'}</dt><dd>{capability.detail}</dd>
    </div>)}</dl>
    <p className="hint">新建会话使用单独入口；此处只核对当前记录。预览不会发送输入、恢复自动化或另建执行。</p>
    <button disabled={!connected || loading || !access.preview_available} onClick={() => void inspect()}>{loading ? '正在只读核对…' : '只读预览原会话'}</button>
    {shown.observed_at ? <p>最近证据：<time dateTime={shown.observed_at}>{new Date(shown.observed_at).toLocaleString()}</time></p> : <p className="hint">尚无原生观察时间。</p>}
    {preview ? <div role="status"><p>本次预览：{new Date(preview.checked_at).toLocaleString()} · 输入权未改变</p>
      {preview.native_status ? <p>原执行：{({ notLoaded: '原线程未加载', sdk_offline: 'SDK 查询已退出', sdk_live: 'SDK 查询存在' } as Record<string, string>)[preview.native_status] ?? preview.native_status}</p> : null}
      <p className="hint">这是该时刻的观察，实际操作仍会重新核对；预览通过不等于持续空闲或没有终端草稿。</p>
      {preview.error ? <p className="error">{preview.error}</p> : <p>{preview.evidence.detail}</p>}
      {preview.evidence.source ? <small>证据来源：{preview.evidence.source} · {preview.evidence.complete ? '本次观察完整' : '仍待核对'}</small> : null}
    </div> : null}
    {error ? <p role="alert" className="error">{error}</p> : null}
  </details>;
}
