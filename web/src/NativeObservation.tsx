import { useEffect, useState } from 'react';
import { ExecutorEvidenceDetails, type ExecutorEvidence } from './SessionContext';

type Observation = {
  session_id: string; instance_id: string; state_namespace: string; cwd: string;
  checked_at: string; read_only: boolean; status: string; detail: string; error?: string;
  view: {
    agent_roster?: { kind: string; job_id?: string; pid?: number; state?: string; status?: string; waiting_for?: string; started_at: number };
    executor?: ExecutorEvidence;
    native_id: string; native_status?: string; status: string;
    evidence: { source: string; at: string; complete: boolean; detail: string };
    executions?: { id: string; kind: string; status: string }[];
    turns?: { id: string; status: string; failure?: { message?: string } }[];
    tools?: { id: string; turn_id: string; command: string; output: string; complete: boolean; exit_code?: number }[];
    pending?: { id: string; title: string; status: string }[];
    messages?: { id: string; turn_id: string; role: string; text: string; status: string }[];
  };
};
const states: { [key: string]: string } = { running: '执行中', idle: '本次查询为空闲', waiting_approval: '等待审批', waiting_input: '等待输入', unknown: '待核对' };
const kinds: { [key: string]: string } = { terminal: '后台终端', queue: '原生队列', native_goal: '原生 Goal', child_session: '子会话' };

export function NativeObservation({ sessionId, connected, read, kind = 'codex' }: {
  sessionId: string; connected: boolean; read: <T>(path: string, body?: unknown) => Promise<T>; kind?: string;
}) {
  const [watching, setWatching] = useState(false);
  const [sample, setSample] = useState<Observation>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    if (!watching) return;
    if (!connected) { setWatching(false); setLoading(false); return; }
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      if (!active) return;
      setLoading(true);
      try {
        const result = await read<Observation>(`/api/sessions/${encodeURIComponent(sessionId)}/native/observe`, {});
        if (!active) return;
        setSample(result);
        setError(result.error ?? '');
        if (result.status !== 'observed' && result.status !== 'sampled') { setWatching(false); return; }
      } catch (e) {
        if (!active) return;
        setError((e as Error).message); setWatching(false); return;
      } finally { if (active) setLoading(false); }
      if (active) timer = setTimeout(() => void refresh(), 3000);
    };
    void refresh();
    return () => { active = false; clearTimeout(timer); };
  }, [watching, connected, sessionId, read]);
  const stop = () => { setWatching(false); setLoading(false); };
  const current = watching && connected && !error && !loading;
  return <section className="native-history" aria-label="原执行只读观察">
    <h2>{kind === 'claude' ? '原生会话 · 状态采样' : '原执行 · 只读观察'}</h2>
    <p className="hint">{kind === 'claude' ? '查询固定 Claude CLI 的会话清单，CLI 可能维护自身元数据。每次结束后等待 3 秒再采样，不发送输入或恢复会话；清单不能证明工具状态完整或已取得控制。' : '显式查询原后台的轮次、工具与占用；每次查询结束后等待 3 秒再刷新。停止观察不影响原执行。权限、原生审批完整性和终端输入归属仍需独立核对。'}</p>
    <div className="native-history-actions">{watching ? <button onClick={stop}>停止观察</button> : <button disabled={!connected} onClick={() => { setError(''); setLoading(true); setWatching(true); }}>开始只读观察</button>}</div>
    {!connected ? <p className="warning" role="status">控制台连接断开，观察已中断；保留的采样已过期。</p> : loading && watching ? <p role="status">正在核对原执行…</p> : null}
    {error ? <p className="error" role="alert">{error}</p> : null}
    {sample ? <>
      <p role="status">{current ? sample.status === 'sampled' ? '原生清单已采样 · 控制待核对' : states[sample.view.status] ?? '待核对' : loading && watching && connected ? '上次采样 · 正在重新核对' : '采样已停止或不可用 · 以下为上次记录'} · {new Date(sample.checked_at).toLocaleString()}</p>
      <p className="hint">{sample.detail}</p>
      <ExecutorEvidenceDetails evidence={sample.view.executor} />
      <p className="mono">原生 ID：{sample.view.native_id}<br />项目：{sample.cwd}<br />原生状态目录：{sample.state_namespace}</p>
      <p>{sample.view.evidence.complete ? '本次执行范围查询完成' : '本次执行范围查询不完整'}：{sample.view.evidence.detail}</p>
      {sample.view.agent_roster ? <section aria-label="Claude 原生清单声明">
        <h3>原生清单声明</h3>
        <dl><dt>原生分类</dt><dd>{sample.view.agent_roster.kind === 'background' ? '原生后台' : sample.view.agent_roster.kind === 'interactive' ? '交互前台' : '未知'}（不推断创建来源）</dd>
          <dt>完整会话 ID</dt><dd className="mono">{sample.view.native_id}</dd>
          <dt>短作业 ID</dt><dd className="mono">{sample.view.agent_roster.job_id || '未返回'}</dd>
          <dt>清单中的 PID</dt><dd>{sample.view.agent_roster.pid ?? '未返回进程；不证明工作已结束'}</dd>
          <dt>任务状态 state</dt><dd>{sample.view.agent_roster.state || '未返回'}</dd>
          <dt>进程状态 status</dt><dd>{sample.view.agent_roster.status || '未返回'}</dd>
          <dt>等待原因 waitingFor</dt><dd>{sample.view.agent_roster.waiting_for || '未返回'}</dd></dl>
        <p className="hint">短 ID 不能替代完整会话身份。PID 未核对操作系统身份；idle/done 不排除原工具仍在运行或结果丢失。</p>
      </section> : null}
      <h3>执行占用（{sample.view.executions?.length ?? 0} 条已返回记录）</h3>
      {sample.view.executions?.map(item => <p key={`${item.kind}/${item.id}`}>{kinds[item.kind] ?? item.kind} · {item.status} · <span className="mono">{item.id}</span></p>)}
      <p className="hint">没有返回占用记录不授予输入权，也不能排除尚未收到的审批或终端草稿。</p>
      {sample.view.pending?.map(item => <p key={item.id}>原生请求 · {item.status} · {item.title}</p>)}
      <details><summary>最近轮次（最多 8 条）</summary>{sample.view.turns?.slice(-8).map(turn => <p key={turn.id}><span className="mono">{turn.id}</span> · {turn.status}{turn.failure?.message ? ` · ${turn.failure.message}` : ''}</p>)}</details>
      <details><summary>本次返回的最近消息（最多 20 条）</summary>{sample.view.messages?.slice(-20).map(message => <article className="native-history-entry" key={`${message.turn_id}/${message.id}`}><h3>{message.role} · {message.status}</h3><pre>{message.text}</pre></article>)}</details>
      {sample.view.tools?.slice(-10).map(tool => <details key={`${tool.turn_id}/${tool.id}`}><summary>工具 · {tool.complete ? `已结束（${tool.exit_code ?? '状态待核对'}）` : '执行中或结果未完整'} · {tool.command}</summary><pre>{tool.output}</pre></details>)}
    </> : <p className="hint">尚未查询原执行。只读来源仍保持暂停，没有新增模型输入。</p>}
  </section>;
}
