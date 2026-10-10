import { useState } from 'react';

type SourceCheck = { group?: string; name: string; path: string; status: string; detail?: string };
type ExecutionRead = {
  snapshot?: { id: string };
  checked_at: string; status: string; binding_error?: string; sources: SourceCheck[];
  source_coverage?: { scope: string; areas: { id: string; name: string; status: string; detail: string }[] };
  native_evidence?: { source: string; at: string; complete: boolean; detail: string };
};
const coverageLabels: Record<string, string> = {
  recorded: '已记录', unrecorded: '快照未记录', not_selected: '未显式选择', not_covered: '尚未覆盖',
};
const checkLabels: Record<string, string> = { unchanged: '未发现变化', changed: '已变化', added: '新增未记录来源', unavailable: '无法读取' };

function CheckedSources({ sources }: { sources: SourceCheck[] }) {
  return <>{sources.map((source, index) => <div key={`${source.path}/${index}`} className="snapshot-asset">
    <p>{source.name} · {checkLabels[source.status] ?? '状态待核对'}</p>
    <p className="mono">{source.path}</p>
    {source.detail ? <p>{source.detail}</p> : null}
  </div>)}</>;
}

export function ExecutionSourceCheck({ sessionId, snapshotId, connected, read }: {
  sessionId: string; snapshotId: string; connected: boolean; read: <T>(path: string, body?: unknown) => Promise<T>;
}) {
  const [result, setResult] = useState<ExecutionRead>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  async function check() {
    setResult(undefined); setLoading(true); setError('');
    try {
      const value = await read<ExecutionRead>(`/api/sessions/${encodeURIComponent(sessionId)}/execution`);
      if (value.snapshot?.id !== snapshotId) throw new Error('执行版本已变化，请刷新后重新核对来源。');
      setResult(value);
    } catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }
  const areas = result?.source_coverage?.scope === 'recorded_sources_only' ? result.source_coverage.areas : undefined;
  return <div className="execution-source-check">
    <button onClick={() => void check()} disabled={!connected || loading}>{loading ? '正在核对…' : '核对当前来源'}</button>
    {result ? <div role="status" className={result.status === 'drifted' ? 'error' : 'hint'}>
      <p>{result.status === 'drifted' ? '来源或绑定已变化，继续前需核对。' : result.status === 'unchanged' ? '已记录的来源与绑定未发现变化。' : '来源检查尚未确认，不能判断是否变化。'}</p>
      <small>文件核对时间：{new Date(result.checked_at).toLocaleString()}；不代表全部原生配置已验证。</small>
      {result.binding_error ? <p>{result.binding_error}</p> : null}
      <section aria-label="本次来源检查覆盖">
        <p className="snapshot-subtitle">本次来源检查覆盖</p>
        {areas ? areas.map(area => <details key={area.id} open={area.status === 'unrecorded' || result.sources.some(source => source.group === area.id && source.status !== 'unchanged')}>
          <summary>{area.name} · {coverageLabels[area.status] ?? '覆盖待核对'}</summary>
          <p>{area.detail}</p>
          <CheckedSources sources={result.sources.filter(source => source.group === area.id)} />
        </details>) : <p>此响应未提供可识别的覆盖范围；以下仅列出返回的来源检查。</p>}
        <CheckedSources sources={result.sources.filter(source => !areas?.some(area => area.id === source.group))} />
      </section>
      <section aria-label="最近保存的原生观察">
        <p className="snapshot-subtitle">最近保存的原生观察</p>
        {result.native_evidence?.at ? <>
          <p>观察时间：{new Date(result.native_evidence.at).toLocaleString()} · 来源：{result.native_evidence.source || '未报告'}</p>
          <p>{result.native_evidence.complete ? '该次会话观察报告完整' : '该次会话观察不完整'}；不代表完整有效配置。</p>
          {result.native_evidence.detail ? <p>{result.native_evidence.detail}</p> : null}
        </> : <p>尚无带时间的原生观察。</p>}
        <p>本次文件检查不会刷新原生观察，也不确认当前加载、输入权、方法使用或 Gate 结果。</p>
      </section>
    </div> : null}
    {error ? <p role="alert" className="error">{error}</p> : null}
  </div>;
}
