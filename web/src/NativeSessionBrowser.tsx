import { useRef, useState, type FormEvent } from 'react';
import type { InstanceStatus } from './InstanceDetails';
import { NativeHistory } from './NativeHistory';
import { SessionContextDetails, type SessionContext } from './SessionContext';

type Record = { native_id: string; title: string; cwd: string; source: string; native_status: string; provider?: string; updated_at?: string; known_sessions: string[]; detail: string };
type Result = { read_only: boolean; instance_id: string; kind: string; cwd: string; state_namespace: string; at: string; sessions: Record[]; context?: SessionContext; binding_token?: string; next_cursor?: string; skipped: number; detail: string };
const sources: { [key: string]: string } = { cli: '原生分类 · cli', appServer: '原生分类 · appServer', exec: '原生分类 · exec', vscode: '原生分类 · vscode', subAgent: '原生子会话', custom: '自定义原生来源', unknown: '原生来源未知', native_history: '原生历史记录（创建入口未核对）' };
const statuses: { [key: string]: string } = { notLoaded: '未加载', idle: '原生报告空闲', active: '原生报告活动中', unknown: '当前执行未知' };

export function NativeSessionBrowser({ instances, projects, connected, read, openSession }: {
  instances: InstanceStatus[]; projects: { id: string; name: string; cwd: string }[]; connected: boolean;
  read: <T>(path: string, body?: unknown) => Promise<T>; openSession: (id: string) => void;
}) {
  const [instance, setInstance] = useState('');
  const [project, setProject] = useState('');
  const [archived, setArchived] = useState(false);
  const [id, setId] = useState('');
  const [result, setResult] = useState<Result>();
  const [preview, setPreview] = useState<Record>();
  const [previewContext, setPreviewContext] = useState<SessionContext>();
  const [binding, setBinding] = useState<{ token: string; commandId: string }>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [historyId, setHistoryId] = useState('');
  const generation = useRef(0);
  const options = instances.filter(i => i.kind === 'codex' || i.kind === 'claude');
  const instanceId = instance || options[0]?.id || '';
  const projectId = project || projects[0]?.id || '';
  const kind = options.find(i => i.id === instanceId)?.kind;
  const disabled = !connected || loading || !instanceId || !projectId;
  function reset() { generation.current++; setResult(undefined); setPreview(undefined); setPreviewContext(undefined); setBinding(undefined); setHistoryId(''); setError(''); setLoading(false); }
  async function discover(cursor = '') {
    const current = ++generation.current;
    setLoading(true); setError(''); setPreview(undefined); setBinding(undefined); setHistoryId('');
    if (!cursor) setResult(undefined);
    try {
      const value = await read<Result>('/api/native-sessions/discover', { instance_id: instanceId, project_id: projectId, archived: kind === 'codex' && archived, cursor });
      if (current === generation.current) setResult(value);
    } catch (e) { if (current === generation.current) setError((e as Error).message); }
    finally { if (current === generation.current) setLoading(false); }
  }
  async function inspect(nativeId: string) {
    const current = ++generation.current;
    setLoading(true); setError(''); setPreview(undefined); setBinding(undefined); setHistoryId('');
    try {
      const value = await read<Result>('/api/native-sessions/preview', { instance_id: instanceId, project_id: projectId, native_id: nativeId });
      if (current === generation.current) {
        setPreview(value.sessions[0]);
        setPreviewContext(value.context);
        if (value.binding_token) setBinding({ token: value.binding_token, commandId: crypto.randomUUID() });
      }
    } catch (e) { if (current === generation.current) setError((e as Error).message); }
    finally { if (current === generation.current) setLoading(false); }
  }
  async function bind() {
    if (!preview || !binding) return;
    const current = ++generation.current;
    setLoading(true); setError('');
    try {
      const receipt = await read<{ session_id: string }>('/api/native-sessions/bind', { command_id: binding.commandId, instance_id: instanceId, project_id: projectId, native_id: preview.native_id, preview_token: binding.token });
      if (current === generation.current) openSession(receipt.session_id);
    } catch (e) { if (current === generation.current) setError((e as Error).message); }
    finally { if (current === generation.current) setLoading(false); }
  }
  function submit(event: FormEvent) { event.preventDefault(); void discover(); }
  function detail(record: Record) {
    return <><p>{sources[record.source] ?? '未识别的原生来源'} · {statuses[record.native_status] ?? record.native_status}</p>
      <p className="mono">{record.native_id}</p><p className="mono">{record.cwd}</p>
      {record.provider ? <p>原生供应商标识：{record.provider}</p> : <p className="hint">此元数据未提供供应商绑定。</p>}
      {record.updated_at ? <p>记录更新时间：{new Date(record.updated_at).toLocaleString()}</p> : null}
      <p className="hint">{record.detail}</p>
      <button disabled={disabled} onClick={() => setHistoryId(record.native_id)}>读取原生历史</button>
      {record.known_sessions.map(known => <button key={known} onClick={() => openSession(known)}>打开已有工作台记录</button>)}
      {record.known_sessions.length === 0 ? <p className="hint">尚未接入工作台。实时观察、恢复与输入控制仍待相应入口验证。</p> : null}
    </>;
  }
  return <section className="native-browser"><h1>发现原生会话</h1>
    <p className="hint">查询所选执行配置和项目的原生记录，可按需只读查看历史。创建来源与当前执行方式分开核对；这里不提供实时接管。</p>
    <form onSubmit={submit}><label>执行配置<select value={instanceId} onChange={event => { setInstance(event.target.value); setArchived(false); reset(); }}>
      {options.map(i => <option key={i.id} value={i.id}>{i.name || i.id} · {i.kind}</option>)}
    </select></label><label>项目<select value={projectId} onChange={event => { setProject(event.target.value); reset(); }}>
      {projects.map(p => <option key={p.id} value={p.id}>{p.name || p.id} · {p.cwd}</option>)}
    </select></label>{kind === 'codex' ? <label className="native-archive-filter"><input type="checkbox" checked={archived} onChange={event => { setArchived(event.target.checked); reset(); }} /> 仅查询原生归档记录</label> : null}
      <button disabled={disabled}>{loading ? '正在读取…' : '查询原生记录'}</button>
    </form>
    <form onSubmit={event => { event.preventDefault(); void inspect(id); }}><label>原生会话 ID<input value={id} onChange={event => setId(event.target.value)} placeholder="粘贴 UUID，仅预览元数据" /></label><button disabled={disabled || !id.trim()}>按 ID 只读预览</button></form>
    {error ? <p className="error" role="alert">{error}</p> : null}
    {historyId ? <NativeHistory key={JSON.stringify([instanceId, projectId, historyId])} instanceId={instanceId} projectId={projectId} nativeId={historyId} connected={connected} read={read} close={() => setHistoryId('')} /> : null}
    {preview ? <section className="native-record" aria-label="原生记录预览"><h2>{preview.title || preview.native_id}</h2><SessionContextDetails context={previewContext} />{detail(preview)}
      {binding && preview.known_sessions.length === 0 ? <><p className="hint">可将这份原生来源登记到会话列表，以后按原 ID 读取历史。登记不创建执行、不复制历史，也不授予发送或恢复权限。</p><button disabled={disabled} onClick={() => void bind()}>登记为只读原生会话</button></> : null}
    </section> : null}
    {result ? <><p className="hint">{result.detail}</p><SessionContextDetails context={result.context} /><p className="mono">原生状态目录：{result.state_namespace}</p>
      {result.skipped ? <p className="hint">{result.skipped} 条记录的身份或项目归属无法匹配，未显示；不据此宣称完整发现。</p> : null}
      {result.sessions.length ? result.sessions.map(record => <article className="native-record" key={record.native_id}><h2>{record.title || record.native_id}</h2>{detail(record)}<button disabled={disabled} onClick={() => void inspect(record.native_id)}>重新只读预览此记录</button></article>) : <p>本页没有匹配记录。</p>}
      {result.next_cursor ? <button disabled={disabled} onClick={() => void discover(result.next_cursor)}>下一页原生记录</button> : null}
    </> : null}
  </section>;
}
