import { useState } from 'react';

type ContextReport = {
  native_id: string; cwd: string; source: string; at: string; complete: boolean; rollout_sha256: string;
  frames: { turn_id: string; recorded_at: string; descriptions_reported: boolean;
    input_evidence?: { operation_id: string; snapshot_id: string; items: { asset_id: string; asset_version: string; name: string; path: string; expected_sha256: string; status: string; record_ids: string[] }[] };
    descriptions: { name: string; description: string; path: string }[];
    loads: { id: string; name: string; path: string; content_sha256: string; body_sha256?: string }[];
  }[];
};

export function SkillContext({ sessionId, connected, read }: {
  sessionId: string; connected: boolean; read: <T>(path: string, body?: unknown) => Promise<T>;
}) {
  const [report, setReport] = useState<ContextReport>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  async function sample() {
    setReport(undefined); setError(''); setLoading(true);
    try { setReport(await read<ContextReport>(`/api/sessions/${encodeURIComponent(sessionId)}/skills/context`, {})); }
    catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }
  return <details className="skill-discovery skill-context"><summary>Skill 上下文记录</summary>
    <p className="hint">按原生轮次查看准备过的 Skill 描述和正文载入记录。不证明模型已收到、方法已使用或 Gate 通过。</p>
    <p className="hint">这些是历史片段。改配后尚无新轮次时，最近记录仍可能属于旧配置；禁用不会删除此前已载入的正文，也不改变文件权限。</p>
    <button disabled={!connected || loading} onClick={() => void sample()}>{loading ? '正在读取上下文…' : '读取 Skill 上下文'}</button>
    {!connected ? <p className="hint">工作台已断开，连接后可重新读取。</p> : null}
    {error ? <p role="alert" className="error">{error}</p> : null}
    {report ? <>
      <p role="status">最近记录的上下文 · {report.frames.length} 个轮次片段</p>
      <p className="mono">原生会话：{report.native_id}</p>
      <small>读取时间：{new Date(report.at).toLocaleString()}</small>
      {!report.frames.length ? <p className="hint">原生日志尚无可展示的轮次上下文；不表示所有 Skill 已排除。</p> : null}
      {report.frames.slice().reverse().map((frame, index) => <details className={`snapshot-asset skill-context-frame${index === 0 ? ' latest-skill-frame' : ''}`} key={`${frame.turn_id}/${index}`} open={index === 0}>
        <summary>{index === 0 ? '最近记录轮次' : '历史轮次'} · {frame.descriptions_reported ? `${frame.descriptions.length} 项描述` : '描述清单未报告'} · {frame.loads.length} 项正文载入</summary>
        <p className="mono">轮次：{frame.turn_id}</p>
        {frame.recorded_at ? <small>原生记录时间：{new Date(frame.recorded_at).toLocaleString()}</small> : null}
        {frame.input_evidence ? <section className="skill-input-evidence" aria-label="本轮显式 Skill 请求">
          <p className="snapshot-subtitle">本轮显式 Skill 请求</p>
          <p className="mono">原操作：{frame.input_evidence.operation_id}</p><p className="mono">执行快照：{frame.input_evidence.snapshot_id}</p>
          {frame.input_evidence.items.map(item => <div key={`${item.asset_id}/${item.name}`}>
            <p>{item.name} · {item.asset_id} · 请求资产版本 {item.asset_version}</p>
            <p>{({ loaded_exact: '请求版本正文已载入', content_mismatch: '载入正文与请求版本不一致', unverified_content: '载入正文摘要未报告', not_observed: '未观察到对应正文载入' } as Record<string, string>)[item.status] ?? '正文载入待核对'}</p>
            <details><summary>请求正文摘要</summary><p className="mono">{item.expected_sha256}</p></details>
          </div>)}
          <p className="hint">关联来自原操作与保存快照；正文摘要一致仅证明载入了请求版本，不代表模型已使用该方法。</p>
        </section> : null}
        {frame.descriptions_reported ? <>
          <p className="snapshot-subtitle">此片段的可用描述</p>
          {frame.descriptions.map(item => <div key={item.path}>
            <p>{item.name}</p><small>{item.description}</small><p className="mono">{item.path}</p>
          </div>)}
          {!frame.descriptions.length ? <p>原生在此片段报告了空描述清单。</p> : null}
        </> : <p>此片段未报告可解析的描述清单，不能据此确认排除。</p>}
        <p className="snapshot-subtitle">正文载入记录</p>
        {frame.loads.map((load, loadIndex) => <div key={`${load.id}/${loadIndex}`}>
          <p>{load.name} · 正文已载入上下文</p>
          <p className="mono">原生记录：{load.id}</p><p className="mono">来源路径：{load.path}</p>
          <details><summary>载入片段摘要</summary><p className="mono">{load.content_sha256}</p>{load.body_sha256 ? <><small>载入正文摘要</small><p className="mono">{load.body_sha256}</p></> : null}</details>
        </div>)}
        {!frame.loads.length ? <p className="hint">此片段没有新增正文载入记录；较早片段中的正文仍可能保留在历史里。</p> : null}
      </details>)}
      <p className="hint">来源路径按原生日志展示，不据名称或目录猜测插件资产和版本；正文内容不在此传出。</p>
    </> : null}
  </details>;
}
